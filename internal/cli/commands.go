package cli

import (
	"agentdebugger/internal/session"
	"fmt"
	"time"
)

type obj = map[string]any

func actionCommand(verb string, args []string) (any, error)   { return runAction(verb, args, true) }
func canonicalAction(verb string, args []string) (any, error) { return runAction(verb, args, false) }
func runAction(verb string, args []string, legacy bool) (any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("session ID required")
	}
	options, body, err := parseAction(verb, args[1:], legacy)
	if err != nil {
		return nil, err
	}
	if options["new-session"] == true {
		return runAgainCommand(args[:1])
	}
	if verb == "task-start" && (options["human"] == true || str(body["binding"]) == "" || body["revision"] == uint64(0) || str(body["task"]) != "") {
		return nil, fmt.Errorf("--binding BINDING --revision N --instruction REQUEST required (record the debugging request as the agent)")
	}
	thread := str(options["thread"])
	if verb == "bind" && thread != "" && !validCodexThread(thread) {
		return nil, fmt.Errorf("invalid Codex thread UUID")
	}
	s, err := session.Read(args[0])
	if err != nil {
		return nil, err
	}
	if verb == "state" {
		brief := 0
		if options["brief"] == true {
			brief = 1
		}
		v, err := api(s, "GET", fmt.Sprintf("/api/state?goroutine=%d&frame=%d&brief=%d", options["goroutine"], options["frame"], brief), nil)
		if options["summary"] == true && err == nil {
			v = summarizeState(v)
		}
		return v, err
	}
	state, err := api(s, "GET", "/api/state?brief=1", nil)
	if err != nil {
		return nil, err
	}
	body["generation"] = state["generation"]
	if str(body["binding"]) == "" && s.Binding != nil {
		body["binding"] = s.Binding.ID
	}
	if verb == "bind" && thread != "" {
		if str(body["binding"]) == "" {
			body["binding"] = session.NewID(16)
		}
		body["name"] = "Codex"
	}
	result, err := api(s, "POST", "/api/action", body)
	if err != nil {
		return nil, err
	}
	if verb == "bind" && thread != "" {
		fresh, err := session.Read(s.ID)
		if err != nil {
			return nil, err
		}
		if err = configureBridge(fresh, thread); err != nil {
			return result, err
		}
	}
	if wait, ok := options["wait"].(time.Duration); ok && wait > 0 {
		return waitForPause(s, wait, options["summary"] == true)
	}
	return result, nil
}
func waitForPause(s session.Descriptor, wait time.Duration, summary bool) (obj, error) {
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		v, err := api(s, "GET", "/api/state?brief=1", nil)
		if err != nil {
			return nil, err
		}
		if str(v["status"]) != "running" {
			if pending, ok := v["capturePending"].(float64); ok && pending > 0 {
				continue
			}
			v, err = api(s, "GET", "/api/state", nil)
			if err != nil {
				return nil, err
			}
			if summary {
				v = summarizeState(v)
			}
			return v, nil
		}
	}
	return nil, fmt.Errorf("wait timed out; session remains alive (use debug state or debug pause)")
}
