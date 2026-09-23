package cli

import (
	"agentdebugger/internal/session"
	"context"
	"fmt"
	"net/url"
)

func sessionList(args []string) (any, error) {
	f := newFlagSet("session list")
	all := f.Bool("all", false, "Include saved and offline sessions")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	if f.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments")
	}
	live, err := session.List(context.Background())
	if err != nil {
		return nil, err
	}
	result := []session.Summary{}
	seen := map[string]bool{}
	for _, s := range live {
		if *all || (s.Panel != "" && s.Status != "ended" && s.Status != "exited") {
			result = append(result, s)
			seen[s.ID] = true
		}
	}
	if *all {
		saved, err := session.ListHistory()
		if err != nil {
			return nil, err
		}
		for _, h := range saved {
			if !seen[h.ID] {
				result = append(result, session.Summary{ID: h.ID, Project: h.Project, Binary: h.Binary, Status: h.Status})
				seen[h.ID] = true
			}
		}
	}
	return result, nil
}
func sessionHistory(args []string) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("session ID required")
	}
	return session.ReadHistory(args[0])
}

func openSession(args []string) (any, error) {
	if len(args) == 0 {
		return openWorkspace(args)
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("expected optional session ID")
	}
	s, err := session.Read(args[0])
	if err == nil && !s.Stopped {
		if err = session.LocalEndpoint(s.HTTP); err != nil {
			return nil, err
		}
		panel := s.HTTP + "/"
		if s.ServiceVersion == 0 && s.Token != "" {
			panel += "#" + s.Token
		}
		return obj{"panel": panel}, nil
	}
	// Historical sessions use the workspace, which never starts a debug target.
	if _, err := session.ReadHistory(args[0]); err != nil {
		return nil, err
	}
	endpoint, err := ensureUI()
	if err != nil {
		return nil, err
	}
	return obj{"panel": endpoint + "/?session=" + url.QueryEscape(args[0])}, nil
}
func restartSession(args []string) (any, error) { return canonicalAction("restart", args) }
