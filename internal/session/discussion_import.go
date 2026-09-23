package session

import (
	"agentdebugger/internal/protocol"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type NativeImport struct {
	Workspace            string            `json:"workspace"`
	Discussions          []json.RawMessage `json:"discussions"`
	Tracepoints          json.RawMessage   `json:"tracepoints,omitempty"`
	TracepointServiceIDs map[string]string `json:"tracepointServiceIDs,omitempty"`
}
type ImportedDiscussion struct {
	Session string `json:"session"`
	Thread  string `json:"thread"`
}
type ImportIndex struct {
	Version              int                  `json:"version"`
	Workspace            string               `json:"workspace"`
	Discussions          []ImportedDiscussion `json:"discussions"`
	TracepointServiceIDs map[string]string    `json:"tracepointServiceIDs,omitempty"`
}

func importPath(workspace string) (string, error) {
	if workspace == "" || len(workspace) > 4096 {
		return "", fmt.Errorf("workspace identity required")
	}
	root := os.Getenv("DEBUG_HANDOVER_HOME")
	if root == "" {
		var err error
		root, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(root, "delve-llm-adapter")
	}
	return filepath.Join(root, "discussion-imports", fmt.Sprintf("%x", sha256.Sum256([]byte(workspace)))), nil
}
func ReadImportIndex(workspace string) (ImportIndex, error) {
	index := ImportIndex{Version: 1, Workspace: workspace, Discussions: []ImportedDiscussion{}}
	dir, err := importPath(workspace)
	if err != nil {
		return index, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if os.IsNotExist(err) {
		return index, nil
	}
	if err != nil {
		return index, err
	}
	err = json.Unmarshal(data, &index)
	return index, err
}
func ImportNative(in NativeImport) (ImportIndex, error) {
	index := ImportIndex{Version: 1, Workspace: in.Workspace, Discussions: []ImportedDiscussion{}, TracepointServiceIDs: in.TracepointServiceIDs}
	raw, err := json.Marshal(in)
	if err != nil {
		return index, err
	}
	if len(raw) > MaxDiscussionBytes {
		return index, fmt.Errorf("import exceeds 32 MiB")
	}
	dir, err := importPath(in.Workspace)
	if err != nil {
		return index, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return index, err
	}
	lock, err := Lock(dir)
	if err != nil {
		return index, err
	}
	defer Unlock(lock)
	previous, err := ReadImportIndex(in.Workspace)
	if err != nil {
		return index, err
	}
	index.Discussions = previous.Discussions
	if index.TracepointServiceIDs == nil {
		index.TracepointServiceIDs = map[string]string{}
	}
	for key, id := range previous.TracepointServiceIDs {
		if current := index.TracepointServiceIDs[key]; current != "" && current != id {
			return index, fmt.Errorf("tracepoint mapping changed")
		}
		index.TracepointServiceIDs[key] = id
	}
	// Retain the exact source before any destination writes. Content-addressed
	// backups are never replaced, including after a partial migration retry.
	backup := filepath.Join(dir, fmt.Sprintf("source-%x.json", sha256.Sum256(raw)))
	if _, err = os.Stat(backup); os.IsNotExist(err) {
		if err = Write(backup, in); err != nil {
			return index, err
		}
	}
	if index.TracepointServiceIDs == nil {
		index.TracepointServiceIDs = map[string]string{}
	}
	var points []struct {
		ID string `json:"id"`
	}
	if len(in.Tracepoints) > 0 {
		if err = json.Unmarshal(in.Tracepoints, &points); err != nil {
			return index, err
		}
	}
	for _, point := range points {
		if index.TracepointServiceIDs[point.ID] == "" {
			index.TracepointServiceIDs[point.ID] = fmt.Sprintf("vscode-%x", sha256.Sum256([]byte(in.Workspace+"\x00"+point.ID)))[:39]
		}
	}
	type turn struct {
		Question    string         `json:"question"`
		Answer      string         `json:"answer"`
		Evidence    map[string]any `json:"evidence"`
		ContextNote string         `json:"contextNote"`
	}
	type record struct {
		turn
		ID       string `json:"id"`
		File     string `json:"file"`
		Line     int    `json:"line"`
		Source   string `json:"source"`
		Status   string `json:"status"`
		Resolved bool   `json:"resolved"`
		Turns    []turn `json:"turns"`
	}
	for _, rawRecord := range in.Discussions {
		var native record
		decoder := json.NewDecoder(bytes.NewReader(rawRecord))
		decoder.UseNumber()
		if err = decoder.Decode(&native); err != nil {
			return index, err
		}
		if native.ID == "" || len(native.ID) > 128 {
			return index, fmt.Errorf("legacy discussion ID required")
		}
		service, _ := native.Evidence["serviceSession"].(string)
		if !discussionID.MatchString(service) {
			legacy, _ := native.Evidence["session"].(string)
			service = fmt.Sprintf("%x", sha256.Sum256([]byte(in.Workspace+"\x00"+legacy)))[:10]
		}
		imported := CommentThread{ID: native.ID, File: native.File, Line: native.Line, Resolved: native.Resolved, Messages: []CommentMessage{}, Legacy: append(json.RawMessage(nil), rawRecord...)}
		turns := append(native.Turns, native.turn)
		r := protocol.Recipient{Kind: "provider", ID: "vscode-legacy", Revision: 1, Name: "Brote"}
		for i, t := range turns {
			context := map[string]any{"nativeEvidence": t.Evidence, "contextNote": t.ContextNote, "sourceText": native.Source}
			if run, ok := t.Evidence["run"].(string); ok {
				context["run"] = run
			}
			captured, _ := t.Evidence["capturedAt"].(string)
			if imported.Created == "" {
				imported.Created = captured
			}
			question := fmt.Sprintf("%s-q-%d", native.ID, i)
			imported.Messages = append(imported.Messages, CommentMessage{ID: question, Author: "human", Body: t.Question, Created: captured, Run: service, Context: context})
			status := "unknown"
			if t.Answer != "" {
				imported.Messages = append(imported.Messages, CommentMessage{ID: fmt.Sprintf("%s-a-%d", native.ID, i), Author: "Brote", Body: t.Answer, Created: captured, Question: question, Recipient: &r})
				status = "answered"
			}
			imported.Context = context
			imported.Delivery = CommentDelivery{Question: question, Status: status, Recipient: &r}
		}
		for retries := 0; retries < 8; retries++ {
			d, e := ReadDiscussion(service)
			if e != nil {
				return index, e
			}
			found := false
			for _, t := range d.Threads {
				if t.ID == native.ID {
					var existing, incoming any
					decode := func(raw []byte, target *any) error {
						decoder := json.NewDecoder(bytes.NewReader(raw))
						decoder.UseNumber()
						return decoder.Decode(target)
					}
					if err := decode(t.Legacy, &existing); err != nil {
						return index, fmt.Errorf("import ID conflicts with existing thread")
					}
					if err := decode(rawRecord, &incoming); err != nil {
						return index, err
					}
					left, _ := json.Marshal(existing)
					right, _ := json.Marshal(incoming)
					if !bytes.Equal(left, right) {
						return index, fmt.Errorf("legacy source changed for an already imported ID; retained source backup for reconciliation")
					}
					if len(t.Legacy) == 0 {
						return index, fmt.Errorf("import ID conflicts with existing thread")
					}
					found = true
					break
				}
			}
			if found {
				break
			}
			if len(d.Threads) >= 200 {
				return index, fmt.Errorf("discussion thread limit reached")
			}
			d.Threads = append(d.Threads, imported)
			if e = CommitDiscussion(&d); e == ErrDiscussionConflict {
				if retries == 7 {
					return index, e
				}
				continue
			} else if e != nil {
				return index, e
			}
			break
		}
		linked := false
		for _, item := range index.Discussions {
			if item.Session == service && item.Thread == native.ID {
				linked = true
				break
			}
		}
		if !linked {
			index.Discussions = append(index.Discussions, ImportedDiscussion{Session: service, Thread: native.ID})
		}
	}
	if err = Write(filepath.Join(dir, "index.json"), index); err != nil {
		return index, err
	}
	return index, nil
}

func LinkDiscussion(workspace, id, thread string) error {
	if !discussionID.MatchString(id) || thread == "" {
		return fmt.Errorf("session and thread required")
	}
	dir, err := importPath(workspace)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := Lock(dir)
	if err != nil {
		return err
	}
	defer Unlock(lock)
	index, err := ReadImportIndex(workspace)
	if err != nil {
		return err
	}
	for _, item := range index.Discussions {
		if item.Session == id && item.Thread == thread {
			return nil
		}
	}
	index.Discussions = append(index.Discussions, ImportedDiscussion{Session: id, Thread: thread})
	return Write(filepath.Join(dir, "index.json"), index)
}
