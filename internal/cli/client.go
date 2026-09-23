package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
)

func api(s session.Descriptor, method, path string, body any) (obj, error) {
	if err := session.LocalEndpoint(s.HTTP); err != nil {
		return nil, err
	}
	var r io.Reader
	if a, ok := body.(map[string]any); ok && s.ServiceVersion > 0 {
		copy := obj{}
		for k, v := range a {
			copy[k] = v
		}
		if str(copy["commandId"]) == "" {
			copy["commandId"] = session.NewID(16)
		}
		if _, exists := copy["run"]; !exists {
			copy["run"] = s.RunID
		}
		body = copy
	}
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		r = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, s.HTTP+path, r)
	if e != nil {
		return nil, e
	}
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	timeout := 15 * time.Second
	if a, ok := body.(map[string]any); ok && a["action"] == "restart" {
		timeout = 45 * time.Second
	}
	resp, e := (&http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	v := obj{}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&v); e != nil {
		return nil, e
	}
	if resp.StatusCode >= 400 {
		if problem, ok := v["error"].(map[string]any); ok {
			return v, &protocol.Error{Code: str(problem["code"]), Message: str(problem["message"])}
		}
		if code := str(v["code"]); code != "" {
			return v, &protocol.Error{Code: code, Message: str(v["error"])}
		}
		return v, fmt.Errorf("%s", str(v["error"]))
	}
	if s.ServiceVersion > 0 && method == "GET" && (path == "/api/state" || strings.HasPrefix(path, "/api/state?")) && (v["id"] != s.ID || v["run"] != s.RunID) {
		return nil, fmt.Errorf("session identity changed; rediscover the session")
	}
	return v, nil
}

func str(v any) string { s, _ := v.(string); return s }

func errorString(e error) string {
	if e != nil {
		return e.Error()
	}
	return ""
}
