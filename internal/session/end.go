package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// End is an explicit human lifecycle action, independent of execution ownership.
// It uses the broker protocol rather than killing potentially reused OS PIDs.
func End(ctx context.Context, id string) (map[string]any, error) {
	s, err := Read(id)
	if err != nil {
		return nil, err
	}
	if s.Stopped {
		return map[string]any{"status": "ended"}, nil
	}
	u, err := url.Parse(s.HTTP)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil {
		return nil, fmt.Errorf("invalid local broker endpoint")
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path string, body any) (map[string]any, error) {
		var reader io.Reader
		if body != nil {
			data, e := json.Marshal(body)
			if e != nil {
				return nil, e
			}
			reader = bytes.NewReader(data)
		}
		req, e := http.NewRequestWithContext(ctx, method, s.HTTP+path, reader)
		if e != nil {
			return nil, e
		}
		req.Header.Set("Content-Type", "application/json")
		if s.Token != "" {
			req.Header.Set("Authorization", "Bearer "+s.Token)
		}
		res, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		defer res.Body.Close()
		var result map[string]any
		if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result); e != nil {
			return nil, e
		}
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("%v", result["error"])
		}
		return result, nil
	}
	state, err := call("GET", "/api/state?brief=1", nil)
	if err != nil {
		return nil, fmt.Errorf("broker unavailable; recover the session before ending it: %w", err)
	}
	if state["id"] != s.ID {
		return nil, fmt.Errorf("broker identity changed; refusing to end session")
	}
	owner, ok := state["owner"].(string)
	if !ok || owner == "" || state["generation"] == nil {
		return nil, fmt.Errorf("incomplete broker state")
	}
	action := map[string]any{"action": "stop", "actor": owner, "generation": state["generation"]}
	if binding, ok := state["binding"].(map[string]any); ok {
		action["binding"] = binding["id"]
	}
	return call("POST", "/api/action", action)
}
