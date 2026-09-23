package cli

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
)

func serviceAPI(s session.Descriptor, operation string, payload obj) (obj, error) {
	if s.ServiceVersion != protocol.Version {
		return nil, &protocol.Error{Code: "unsupported_operation", Message: "this operation requires a shared session; start with --service"}
	}
	state, err := api(s, "GET", "/api/state?brief=1", nil)
	if err != nil {
		return nil, err
	}
	request := obj{"version": protocol.Version, "session": s.ID, "run": s.RunID, "generation": state["generation"], "pauseEpoch": state["pauseEpoch"], "client": "cli", "commandId": session.NewID(16), "operation": operation}
	for key, value := range payload {
		request[key] = value
	}
	result, err := api(s, "POST", "/api/v1", request)
	if err != nil {
		return result, err
	}
	identity, _ := result["identity"].(map[string]any)
	if result["version"] != float64(protocol.Version) || identity["session"] != s.ID || identity["run"] != s.RunID {
		return nil, fmt.Errorf("service response identity mismatch")
	}
	return result, nil
}
func definitionCommand(kind string, args []string) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("usage: %s add|update|remove|list SESSION [options]", kind)
	}
	operation, id := args[0], args[1]
	if operation != "add" && operation != "update" && operation != "remove" && operation != "list" {
		return nil, fmt.Errorf("unknown definition operation")
	}
	s, err := session.Read(id)
	if err != nil {
		return nil, err
	}
	f := flag.NewFlagSet(kind, flag.ContinueOnError)
	client := f.String("client", "cli", "stable configuration owner")
	definitionID := f.String("id", "", "stable definition ID")
	revision := f.Uint64("revision", 0, "expected definition revision for update/remove")
	file := f.String("file", "", "source path")
	line := f.Int("line", 0, "source line")
	function := f.String("function", "", "function breakpoint expression")
	condition := f.String("condition", "", "read-only condition")
	hit := f.String("hit-condition", "", "Delve hit condition")
	name := f.String("name", "", "capture label")
	values := f.String("values", "{}", "JSON map of aliases to read-only Go expressions")
	limit := f.Int("capture-limit", 100, "maximum capture attempts per run")
	enabled := f.Bool("enabled", true, "enable this definition")
	scope := f.String("scope", "session", "session or current run")
	if err = f.Parse(args[2:]); err != nil {
		return nil, err
	}
	if f.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments")
	}
	payload := obj{"client": *client}
	if operation == "list" {
		return serviceAPI(s, "definition.list", payload)
	}
	if (operation == "update" || operation == "remove") && (*definitionID == "" || *revision == 0) {
		return nil, fmt.Errorf("--id and --revision are required; list definitions first")
	}
	payload["revision"] = *revision
	if operation == "remove" {
		payload["id"] = *definitionID
		return serviceAPI(s, "definition.delete", payload)
	}
	d := protocol.Definition{ID: *definitionID, Kind: kind, Owner: *client, Enabled: *enabled, Scope: protocol.Scope{Workspace: s.Project, Session: s.ID}}
	if operation == "update" {
		result, err := serviceAPI(s, "definition.list", payload)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(result["definitions"])
		var store struct {
			Items []protocol.Definition `json:"items"`
		}
		if err = json.Unmarshal(data, &store); err != nil {
			return nil, err
		}
		found := false
		for _, old := range store.Items {
			if old.ID == *definitionID {
				d = old
				found = true
				break
			}
		}
		if !found || d.Kind != kind || d.Owner != *client {
			return nil, fmt.Errorf("definition not found for this kind/client")
		}
	}
	seen := map[string]bool{}
	f.Visit(func(flag *flag.Flag) { seen[flag.Name] = true })
	update := func(key string) bool { return operation == "add" || seen[key] }
	if update("file") {
		*file = filepath.Clean(*file)
		if *file != "." {
			if !filepath.IsAbs(*file) {
				*file = filepath.Join(s.Project, *file)
			}
			d.Location.File = *file
		}
	}
	if update("line") {
		d.Location.Line = *line
	}
	if update("function") {
		d.Location.Function = *function
		if *function != "" {
			d.Location.File = ""
			d.Location.Line = 0
		}
	}
	if update("condition") {
		d.Condition = *condition
	}
	if update("hit-condition") {
		d.HitCondition = *hit
	}
	if update("name") {
		d.Name = *name
	}
	if update("enabled") {
		d.Enabled = *enabled
	}
	if update("scope") {
		switch *scope {
		case "session":
			d.Scope.Run = ""
		case "run":
			d.Scope.Run = s.RunID
		default:
			return nil, fmt.Errorf("scope must be session or run")
		}
	}
	if kind == "tracepoint" {
		if update("capture-limit") {
			d.CaptureLimit = *limit
		}
		if update("values") {
			if err = json.Unmarshal([]byte(*values), &d.Values); err != nil {
				return nil, fmt.Errorf("values must be a JSON map of aliases to expressions")
			}
		}
	}
	payload["definition"] = d
	return serviceAPI(s, "definition.put", payload)
}
func serviceReadCommand(verb string, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("session ID required")
	}
	s, err := session.Read(args[0])
	if err != nil {
		return nil, err
	}
	f := flag.NewFlagSet(verb, flag.ContinueOnError)
	gid := f.Int("goroutine", 0, "goroutine ID")
	start := f.Int("start", 0, "page offset")
	count := f.Int("count", 64, "page size")
	if err = f.Parse(args[1:]); err != nil {
		return nil, err
	}
	if f.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments")
	}
	if verb == "capabilities" {
		state, err := api(s, "GET", "/api/state?brief=1", nil)
		if err != nil {
			return nil, err
		}
		return obj{"id": s.ID, "run": s.RunID, "serviceVersion": s.ServiceVersion, "capabilities": state["capabilities"], "traceIds": state["traceIds"]}, nil
	}
	operation := map[string]string{"captures": "capture.list", "goroutines": "inspection.goroutines", "stack": "inspection.stack"}[verb]
	payload := obj{}
	if verb != "captures" {
		payload["goroutine"], payload["start"], payload["count"] = *gid, *start, *count
	}
	return serviceAPI(s, operation, payload)
}
