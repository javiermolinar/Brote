package session

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

type Summary struct {
	RunID          string   `json:"run,omitempty"`
	ServiceVersion int      `json:"serviceVersion,omitempty"`
	ID             string   `json:"id"`
	Project        string   `json:"project"`
	Binary         string   `json:"binary"`
	Status         string   `json:"status"`
	Owner          string   `json:"owner,omitempty"`
	Panel          string   `json:"panel,omitempty"`
	Binding        *Binding `json:"binding,omitempty"`
	Recoverable    bool     `json:"recoverable,omitempty"`
}

// List probes only recorded loopback brokers. Reading sessions never acquires
// execution control, reconnects an editor, or changes the conversation binding.
func List(ctx context.Context) ([]Summary, error) {
	entries, err := os.ReadDir(Root())
	if os.IsNotExist(err) {
		return []Summary{}, nil
	}
	if err != nil {
		return nil, err
	}
	list := []Summary{}
	descriptors := []Descriptor{}
	for _, ent := range entries {
		s, err := Read(ent.Name())
		if err == nil {
			descriptors = append(descriptors, s)
			list = append(list, Summary{ID: s.ID, Project: s.Project, Binary: s.Binary, Status: "offline", Recoverable: s.RPC != "" && !s.Stopped})
		}
	}
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i, s := range descriptors {
		wg.Add(1)
		go func(i int, s Descriptor) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if s.ServiceVersion > 0 {
				list[i].RunID, list[i].ServiceVersion = s.RunID, s.ServiceVersion
			}
			if s.Stopped {
				list[i].Status = "ended"
				return
			}
			u, err := url.Parse(s.HTTP)
			if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil {
				return
			}
			route := "/api/state?brief=1"
			if s.ServiceVersion > 0 {
				route = "/api/health"
			}
			req, err := http.NewRequestWithContext(ctx, "GET", s.HTTP+route, nil)
			if err != nil {
				return
			}
			if s.Token != "" {
				req.Header.Set("Authorization", "Bearer "+s.Token)
			}
			res, err := client.Do(req)
			if err != nil {
				return
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				return
			}
			var state struct {
				ID             string   `json:"id"`
				RunID          string   `json:"run"`
				ServiceVersion int      `json:"serviceVersion"`
				Status         string   `json:"status"`
				Owner          string   `json:"owner"`
				Binding        *Binding `json:"binding"`
			}
			if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&state) != nil || state.ID != s.ID {
				return
			}
			if s.ServiceVersion > 0 && (state.RunID != s.RunID || state.ServiceVersion != s.ServiceVersion) {
				return
			}
			list[i].Status = state.Status
			list[i].Owner = state.Owner
			list[i].Binding = state.Binding
			list[i].Panel = s.HTTP + "/"
			list[i].Recoverable = false
			if s.Token != "" && s.ServiceVersion == 0 {
				list[i].Panel += "#" + s.Token
			}
		}(i, s)
	}
	wg.Wait()
	return list, nil
}
