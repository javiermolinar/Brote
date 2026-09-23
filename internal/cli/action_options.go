package cli

import (
	"flag"
	"fmt"
	"strings"
	"time"
)

type actionFlag struct {
	name, key string
	value     any
	usage     string
}

var actionFlags = []actionFlag{
	{"command-id", "commandId", "", "Stable execution request identity for retry detection"},
	{"consumer", "consumer", "", "Managed host consumer"}, {"instance", "instance", "", "Managed host instance"}, {"turn", "turn", "", "Active host turn"},
	{"task", "task", "", "Current debugging task ID"}, {"human", "", false, "Direct human action or authorization"}, {"instruction", "instruction", "", "User-requested investigation scope"},
	{"file", "file", "", "Source path"}, {"line", "line", 0, "Source line"}, {"function", "function", "", "Function name"}, {"condition", "condition", "", "Read-only condition"}, {"hit-condition", "hitCondition", "", "Hit condition"}, {"breakpoint", "breakpoint", 0, "Breakpoint ID"},
	{"goroutine", "goroutine", 0, "Goroutine ID"}, {"frame", "frame", 0, "Frame index"}, {"brief", "", false, "State metadata without inspection"}, {"summary", "", false, "Compact stack and selected-frame values"}, {"wait", "", time.Duration(0), "Wait for a pause (for example 20s)"},
	{"no-open", "open", false, "Do not open the editor"}, {"editor", "editor", "", "browser, zed or vscode"},
	{"expression", "expression", "", "Read-only Go expression"}, {"depth", "depth", 3, "Variable depth (0–6)"}, {"count", "count", 64, "Maximum entries (1–128)"},
	{"thread", "thread", "", "Codex task UUID; empty disables wakeups"}, {"binding", "binding", "", "Client binding ID"}, {"name", "name", "Agent", "Agent display name"}, {"note", "note", "", "Handover or delivery note"}, {"attempt", "attempt", "", "Current delivery attempt"}, {"event", "event", "", "Event ID"}, {"status", "status", "acknowledged", "Event delivery status"}, {"revision", "revision", uint64(0), "Binding revision"}, {"notify", "notify", false, "Legacy flag; handback always emits an event"},
}

func actionFlagNames(verb string) string {
	common := "binding human name"
	execution := common + " task command-id consumer instance turn wait summary note"
	switch verb {
	case "state":
		return "goroutine frame brief summary"
	case "eval":
		return common + " expression depth count goroutine frame"
	case "watch", "unwatch":
		return common + " expression"
	case "bind":
		return "binding name thread"
	case "continue", "next", "step", "stepout", "canonical-step", "pause":
		return execution
	case "restart", "detach", "stop":
		return common + " task"
	case "task-start":
		return common + " revision instruction task"
	case "task-heartbeat", "task-complete", "task-cancel":
		return common + " task consumer instance turn"
	case "event-status":
		return common + " event status revision attempt note"
	case "retry-notification":
		return common
	default:
		return ""
	}
}
func actionFlagSet(verb string, legacy bool) *flag.FlagSet {
	f := newFlagSet(verb)
	selected := " " + actionFlagNames(verb) + " "
	for _, spec := range actionFlags {
		if !legacy && !strings.Contains(selected, " "+spec.name+" ") {
			continue
		}
		switch v := spec.value.(type) {
		case string:
			f.String(spec.name, v, spec.usage)
		case int:
			f.Int(spec.name, v, spec.usage)
		case uint64:
			f.Uint64(spec.name, v, spec.usage)
		case bool:
			f.Bool(spec.name, v, spec.usage)
		case time.Duration:
			f.Duration(spec.name, v, spec.usage)
		}
	}
	if verb == "restart" && !legacy {
		f.Bool("new-session", false, "Launch a new session from saved settings")
	}
	if verb == "canonical-step" {
		for _, name := range []string{"over", "into", "out"} {
			f.Bool(name, false, "Step "+name)
		}
	}
	return f
}
func parseAction(verb string, args []string, legacy bool) (obj, obj, error) {
	f := actionFlagSet(verb, legacy)
	if err := f.Parse(args); err != nil {
		return nil, nil, err
	}
	if f.NArg() != 0 {
		return nil, nil, fmt.Errorf("unexpected arguments: %v", f.Args())
	}
	if v := f.Lookup("new-session"); v != nil && v.Value.String() == "true" {
		if err := validateFlags(f, "new-session"); err != nil {
			return nil, nil, fmt.Errorf("--new-session accepts only saved session ID: %w", err)
		}
	}
	values := obj{}
	f.VisitAll(func(v *flag.Flag) { values[v.Name] = v.Value.(flag.Getter).Get() })
	payload := obj{"action": verb, "actor": "agent"}
	if values["human"] == true {
		payload["actor"] = "human"
	}
	for _, spec := range actionFlags {
		if value, ok := values[spec.name]; ok && spec.key != "" {
			if spec.name == "no-open" {
				value = !value.(bool)
			}
			payload[spec.key] = value
		}
	}
	if note, ok := values["note"]; ok {
		payload["error"] = note
	}
	if verb == "canonical-step" {
		selected := 0
		operation := "next"
		for _, mode := range []struct{ name, operation string }{{"over", "next"}, {"into", "step"}, {"out", "stepout"}} {
			if values[mode.name] == true {
				selected++
				operation = mode.operation
			}
		}
		if selected > 1 {
			return nil, nil, fmt.Errorf("--over, --into and --out are mutually exclusive")
		}
		payload["action"] = operation
	}
	return values, payload, nil
}
