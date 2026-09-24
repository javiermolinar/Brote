package session

import (
	"agentdebugger/internal/delivery"
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/traceinfo"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// Evidence provenance is independent of the recipient and current execution scope.
type EvidenceIdentity struct {
	ID           string `json:"id"`
	Session      string `json:"session,omitempty"`
	ExecutionRun string `json:"executionRun,omitempty"`
	PauseEpoch   int    `json:"pauseEpoch,omitempty"`
}
type CommentMessage struct {
	Trace     *traceinfo.Record   `json:"trace,omitempty"`
	Evidence  *EvidenceIdentity   `json:"evidence,omitempty"`
	Recipient *protocol.Recipient `json:"recipient,omitempty"`
	Context   map[string]any      `json:"context,omitempty"`
	Run       string              `json:"run,omitempty"`
	ID        string              `json:"id"`
	Author    string              `json:"author"`
	Body      string              `json:"body"`
	Created   string              `json:"created"`
	Question  string              `json:"question,omitempty"`
}
type CommentDelivery struct {
	AttemptRequired bool                `json:"attemptRequired,omitempty"`
	Attempt         *delivery.Attempt   `json:"attempt,omitempty"`
	Recipient       *protocol.Recipient `json:"recipient,omitempty"`
	Question        string              `json:"question"`
	Status          string              `json:"status"`
	Error           string              `json:"error,omitempty"`
	Binding         *Binding            `json:"binding,omitempty"`
}
type CommentThread struct {
	Legacy     json.RawMessage  `json:"legacy,omitempty"`
	ID         string           `json:"id"`
	File       string           `json:"file"`
	Line       int              `json:"line"`
	Expression string           `json:"expression,omitempty"`
	Created    string           `json:"created"`
	Resolved   bool             `json:"resolved"`
	Context    map[string]any   `json:"context"`
	Messages   []CommentMessage `json:"messages"`
	Delivery   CommentDelivery  `json:"delivery"`
}
type Discussion struct {
	TraceOwner    *TraceOwner     `json:"traceOwner,omitempty"`
	Version       int             `json:"version,omitempty"`
	Revision      uint64          `json:"revision,omitempty"`
	Investigation string          `json:"investigation,omitempty"`
	Session       string          `json:"session"`
	Binary        string          `json:"binary"`
	Project       string          `json:"project"`
	Threads       []CommentThread `json:"threads"`
}

var discussionID = regexp.MustCompile(`^[a-f0-9]{10}$`)

func discussionPath(id string) (string, error) {
	if !discussionID.MatchString(id) {
		return "", fmt.Errorf("invalid session ID")
	}
	if dir, err := FindHistory(id); err == nil {
		path := filepath.Join(dir, "discussion.json")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	root := os.Getenv("DEBUG_HANDOVER_HOME")
	if root != "" {
		root = filepath.Join(root, "discussions")
	} else {
		config, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(config, "delve-llm-adapter", "discussions")
	}
	return filepath.Join(root, id+".json"), nil
}
func ReadDiscussion(id string) (Discussion, error) {
	d := Discussion{Session: id, Threads: []CommentThread{}}
	path, err := discussionPath(id)
	if err != nil {
		return d, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxDiscussionBytes+1))
	if len(data) > MaxDiscussionBytes {
		return d, fmt.Errorf("discussion exceeds 32 MiB")
	}
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err = decoder.Decode(&d)
	return d, err
}

const MaxDiscussionBytes = 32 * 1024 * 1024
const MaxEvidenceBytes = 1024 * 1024
const MaxAnswerBytes = 128 * 1024

var ErrDiscussionConflict = errors.New("discussion changed; reread before writing")

// A stable per-session lock covers legacy-to-archive promotion and all writers.
// Broker locks are never acquired while holding this lock.
func lockDiscussion(id string) (*os.File, error) {
	if !discussionID.MatchString(id) {
		return nil, fmt.Errorf("invalid session ID")
	}
	root := os.Getenv("DEBUG_HANDOVER_HOME")
	if root == "" {
		var err error
		root, err = os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(root, "delve-llm-adapter")
	}
	dir := filepath.Join(root, "discussion-locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// CommitDiscussion compares the revision while holding the cross-process lock.
// A stale snapshot can fail but can never erase a concurrently posted answer.
func CommitDiscussion(d *Discussion) error {
	lock, err := lockDiscussion(d.Session)
	if err != nil {
		return err
	}
	defer Unlock(lock)
	current, err := ReadDiscussion(d.Session)
	if err != nil {
		return err
	}
	if current.Revision != d.Revision {
		return ErrDiscussionConflict
	}
	next := *d
	next.Version = 1
	next.Revision++
	if err := normalizeEvidence(&next); err != nil {
		return err
	}
	if err := prepareConversationTraces(current, &next); err != nil {
		return err
	}
	if err := normalizeEvidence(&next); err != nil {
		return err
	}
	if next.TraceOwner != nil && next.TraceOwner.Valid() {
		if err := registerTraceSource(next.Session); err != nil {
			return err
		}
	}
	path, err := discussionPath(d.Session)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err = Write(path, next); err != nil {
		return err
	}
	*d = next
	return nil
}
func WriteDiscussion(d Discussion) error { return CommitDiscussion(&d) }

// ArchiveDiscussion promotes the latest committed document, never a caller's
// stale snapshot. Future writes discover the archive under the same lock.
func ArchiveDiscussion(id, dir string) error {
	lock, err := lockDiscussion(id)
	if err != nil {
		return err
	}
	defer Unlock(lock)
	d, err := ReadDiscussion(id)
	if err != nil {
		return err
	}
	return Write(filepath.Join(dir, "discussion.json"), d)
}

func normalizeEvidence(d *Discussion) error {
	if d.Investigation == "" {
		if id, err := InvestigationFor(d.Session); err == nil {
			d.Investigation = id
		}
	}
	for ti := range d.Threads {
		t := &d.Threads[ti]
		for mi := range t.Messages {
			m := &t.Messages[mi]
			if len(m.Body) > MaxAnswerBytes {
				return fmt.Errorf("message exceeds 128 KiB")
			}
			if m.Author == "human" && m.Context == nil {
				m.Context = t.Context
				if m.Run == "" {
					m.Run = d.Session
				}
			}
			if m.Context == nil {
				continue
			}
			data, err := json.Marshal(m.Context)
			if err != nil {
				return err
			}
			if len(data) > MaxEvidenceBytes {
				return fmt.Errorf("evidence exceeds 1 MiB")
			}
			// Normalize typed nested structs to JSON maps so field order and Go
			// concrete types cannot change the hash after a disk round trip.
			var canonical any
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			if err := decoder.Decode(&canonical); err != nil {
				return err
			}
			data, err = json.Marshal(canonical)
			if err != nil {
				return err
			}
			id := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
			if m.Evidence != nil && m.Evidence.ID != id {
				return fmt.Errorf("immutable evidence changed")
			}
			if m.Evidence == nil {
				sessionID := m.Run // Legacy run identifies a session, never an execution run.
				if sessionID == "" {
					sessionID = d.Session
				}
				if captured, ok := m.Context["session"].(string); ok && captured != "" {
					sessionID = captured
				}
				run, _ := m.Context["run"].(string)
				epoch := 0
				switch n := m.Context["pauseEpoch"].(type) {
				case int:
					epoch = n
				case float64:
					epoch = int(n)
				case json.Number:
					value, _ := n.Int64()
					epoch = int(value)
				}
				m.Evidence = &EvidenceIdentity{ID: id, Session: sessionID, ExecutionRun: run, PauseEpoch: epoch}
			}
		}
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > MaxDiscussionBytes {
		return fmt.Errorf("discussion exceeds 32 MiB")
	}
	return nil
}
