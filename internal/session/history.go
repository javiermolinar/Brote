package session

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// HistoryEvent is independent of the live protocol and debugger implementation.
type HistoryEvent struct {
	Version int             `json:"v"`
	Seq     uint64          `json:"seq"`
	At      string          `json:"at"`
	Type    string          `json:"type"`
	Actor   string          `json:"actor"`
	Data    json.RawMessage `json:"data"`
}
type HistoryMetadata struct {
	Version      int          `json:"v"`
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Project      string       `json:"project"`
	Directory    string       `json:"directory"`
	Binary       string       `json:"binary"`
	Args         []string     `json:"args"`
	Started      string       `json:"started"`
	LastActivity string       `json:"last_activity"`
	Status       string       `json:"status"`
	Fingerprint  *Fingerprint `json:"fingerprint,omitempty"`
}
type History struct {
	mu       sync.Mutex
	Dir      string
	file     *os.File
	lock     *os.File
	seq      uint64
	Metadata HistoryMetadata
	failed   error
}

func DataRoot() (string, error) {
	if p := os.Getenv("AGENTDEBUGGER_DATA_DIR"); p != "" {
		return filepath.Abs(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "AgentDebugger"), nil
	case "windows":
		if p := os.Getenv("LOCALAPPDATA"); p != "" {
			return filepath.Join(p, "AgentDebugger"), nil
		}
		return filepath.Join(home, "AppData", "Local", "AgentDebugger"), nil
	default:
		if p := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(p) {
			return filepath.Join(p, "agentdebugger"), nil
		}
		return filepath.Join(home, ".local", "share", "agentdebugger"), nil
	}
}

var unsafeProject = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func projectIdentity(path string) (string, string) {
	path, _ = filepath.Abs(path)
	if p, e := filepath.EvalSymlinks(path); e == nil {
		path = p
	}
	out, err := exec.Command("git", "-C", path, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	identity := path
	if err == nil {
		identity = strings.TrimSpace(string(out))
		if p, e := filepath.EvalSymlinks(identity); e == nil {
			identity = p
		}
		path = filepath.Dir(identity)
	}
	name := strings.Trim(unsafeProject.ReplaceAllString(filepath.Base(path), "-"), ".-")
	if name == "" {
		name = "workspace"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	return name, identity
}

func historyProject(root, path string) (string, error) {
	projects := filepath.Join(root, "projects")
	if err := os.MkdirAll(projects, 0700); err != nil {
		return "", err
	}
	// Serialize project name allocation across brokers, including first creation.
	lock, err := os.OpenFile(filepath.Join(projects, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return "", err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	name, identity := projectIdentity(path)
	entries, err := os.ReadDir(projects)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var m struct {
			Identity string `json:"identity"`
		}
		data, e := os.ReadFile(filepath.Join(projects, entry.Name(), "project.json"))
		if e == nil && json.Unmarshal(data, &m) == nil && m.Identity == identity {
			return filepath.Join(projects, entry.Name()), nil
		}
	}
	dir := filepath.Join(projects, name)
	if _, err = os.Stat(dir); err == nil {
		hash := sha256.Sum256([]byte(identity))
		dir += "-" + hex.EncodeToString(hash[:8])
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err = writeHistoryFile(filepath.Join(dir, "project.json"), map[string]any{"v": 1, "name": name, "identity": identity}); err != nil {
		return "", err
	}
	return dir, nil
}

func OpenHistory(s Descriptor, args []string) (*History, error) {
	if !discussionID.MatchString(s.ID) {
		return nil, fmt.Errorf("invalid history session ID")
	}
	root, err := DataRoot()
	if err != nil {
		return nil, err
	}
	// Reuse the original archive even if the workspace has moved since launch.
	dir, err := FindHistory(s.ID)
	if os.IsNotExist(err) {
		project, e := historyProject(root, s.Project)
		if e != nil {
			return nil, e
		}
		started, e := time.Parse(time.RFC3339Nano, s.Created)
		if e != nil {
			return nil, fmt.Errorf("invalid session start time: %w", e)
		}
		dir = filepath.Join(project, started.UTC().Format("2006-01-02"), s.ID)
	} else if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(dir, "snapshots"), 0700); err != nil {
		return nil, err
	}
	lock, err := Lock(dir)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		Unlock(lock)
		return nil, err
	}
	h := &History{Dir: dir, file: f, lock: lock}
	events, end, err := readHistory(f)
	if err == nil {
		err = f.Truncate(end)
	} // Discard only an unterminated final record after a crash.
	if err != nil {
		h.Close()
		return nil, err
	}
	if len(events) > 0 {
		h.seq = events[len(events)-1].Seq
	}
	h.Metadata = HistoryMetadata{Version: 1, ID: s.ID, Title: filepath.Base(s.Binary), Project: s.Project, Directory: dir, Binary: s.Binary, Args: args, Started: s.Created, LastActivity: s.Created, Status: "active", Fingerprint: s.Fingerprint}
	data, e := os.ReadFile(filepath.Join(dir, "session.json"))
	if e == nil {
		err = json.Unmarshal(data, &h.Metadata)
	} else if !os.IsNotExist(e) {
		err = e
	}
	if err != nil {
		h.Close()
		return nil, err
	}
	// Metadata is a projection; replay lifecycle changes if its last write was interrupted.
	for _, event := range events {
		h.project(event)
	}
	if err = writeHistoryFile(filepath.Join(dir, "session.json"), h.Metadata); err != nil {
		h.Close()
		return nil, err
	}
	if h.seq == 0 {
		err = h.Append("session.started", "core", h.Metadata)
	}
	if err != nil {
		h.Close()
		return nil, err
	}
	return h, nil
}

// readHistory accepts an incomplete final line, but never skips corrupt complete records.
func readHistory(r io.Reader) ([]HistoryEvent, int64, error) {
	reader := bufio.NewReader(r)
	events := []HistoryEvent{}
	var offset int64
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return events, offset, nil
		}
		if err != nil {
			return nil, offset, err
		}
		var event HistoryEvent
		if err = json.Unmarshal(line, &event); err != nil {
			return nil, offset, fmt.Errorf("corrupt history at byte %d: %w", offset, err)
		}
		if event.Version != 1 || event.Seq != uint64(len(events)+1) {
			return nil, offset, fmt.Errorf("unsupported history version or invalid sequence at byte %d", offset)
		}
		events = append(events, event)
		offset += int64(len(line))
	}
}
func ReadHistory(id string) ([]HistoryEvent, error) {
	dir, err := FindHistory(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	events, _, err := readHistory(f)
	return events, err
}
func FindHistory(id string) (string, error) {
	if !discussionID.MatchString(id) {
		return "", fmt.Errorf("invalid history session ID")
	}
	root, err := DataRoot()
	if err != nil {
		return "", err
	}
	matches, err := filepath.Glob(filepath.Join(root, "projects", "*", "*", id, "session.json"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", os.ErrNotExist
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("ambiguous history session ID %s", id)
	}
	return filepath.Dir(matches[0]), nil
}
func ListHistory() ([]HistoryMetadata, error) {
	root, err := DataRoot()
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(root, "projects", "*", "*", "*", "session.json"))
	if err != nil {
		return nil, err
	}
	list := []HistoryMetadata{}
	for _, path := range paths {
		data, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		var m HistoryMetadata
		if e = json.Unmarshal(data, &m); e != nil {
			return nil, e
		}
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Started > list[j].Started })
	return list, nil
}
func (h *History) project(e HistoryEvent) {
	h.Metadata.LastActivity = e.At
	switch e.Type {
	case "session.ended":
		h.Metadata.Status = "ended"
	case "broker.disconnected":
		if h.Metadata.Status != "ended" {
			h.Metadata.Status = "detached"
		}
	case "session.started", "broker.connected":
		h.Metadata.Status = "active"
	}
}
func (h *History) Append(kind, actor string, data any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failed != nil {
		return h.failed
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	event := HistoryEvent{1, h.seq + 1, time.Now().UTC().Format(time.RFC3339Nano), kind, actor, payload}
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	n, err := h.file.Write(line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = h.file.Sync()
	}
	if err != nil {
		h.failed = err
		return err
	}
	h.seq = event.Seq
	h.project(event)
	return writeHistoryFile(filepath.Join(h.Dir, "session.json"), h.Metadata)
}

// Snapshot uses content addressing: immutable files and no rewrite on repeated inspection.
func (h *History) Snapshot(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	name := filepath.Join("snapshots", hex.EncodeToString(hash[:])+".json")
	path := filepath.Join(h.Dir, name)
	if _, err = os.Stat(path); err == nil {
		return filepath.ToSlash(name), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err = writeHistoryFile(path, value); err != nil {
		return "", err
	}
	return filepath.ToSlash(name), nil
}
func (h *History) Close() {
	if h.file != nil {
		_ = h.file.Close()
	}
	if h.lock != nil {
		Unlock(h.lock)
	}
}

// Sync the containing directory as well as the file before journaling a reference.
func writeHistoryFile(path string, value any) error {
	if err := Write(path, value); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
