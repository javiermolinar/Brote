package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

type CommentMessage struct {
	Context  map[string]any `json:"context,omitempty"`
	Run      string         `json:"run,omitempty"`
	ID       string         `json:"id"`
	Author   string         `json:"author"`
	Body     string         `json:"body"`
	Created  string         `json:"created"`
	Question string         `json:"question,omitempty"`
}
type CommentDelivery struct {
	Question string   `json:"question"`
	Status   string   `json:"status"`
	Error    string   `json:"error,omitempty"`
	Binding  *Binding `json:"binding,omitempty"`
}
type CommentThread struct {
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
	Session string          `json:"session"`
	Binary  string          `json:"binary"`
	Project string          `json:"project"`
	Threads []CommentThread `json:"threads"`
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
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	err = json.Unmarshal(data, &d)
	return d, err
}
func WriteDiscussion(d Discussion) error {
	path, err := discussionPath(d.Session)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return Write(path, d)
}
