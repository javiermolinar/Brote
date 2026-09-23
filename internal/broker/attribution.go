package broker

import (
	"agentdebugger/internal/definitions"
	"agentdebugger/internal/protocol"
)

type stopAttribution struct {
	Eligible    bool                  `json:"eligible"`
	Reason      string                `json:"reason"`
	Goroutine   int                   `json:"goroutine"`
	Definitions []protocol.Definition `json:"definitions,omitempty"`
}

// Missing IDs, ambiguous threads and mixed ordinary/tracepoint hits all preserve
// the stop. A relocated point is matched by verified adapter identity, not by a
// guess based on the requested source line.
func attributeStop(body obj, run string, store definitions.Store, resolutions []definitionResolution, adapterPoints []any) stopAttribution {
	result := stopAttribution{Reason: "stop is not an exclusive verified tracepoint hit", Goroutine: num(body["threadId"])}
	if str(body["reason"]) != "breakpoint" || result.Goroutine <= 0 || !truth(body["allThreadsStopped"]) {
		return result
	}
	hits := asList(body["hitBreakpointIds"])
	if len(hits) == 0 || len(hits) > 64 {
		result.Reason = "missing or ambiguous breakpoint IDs"
		return result
	}
	selected := map[string]bool{}
	for _, raw := range hits {
		id := num(raw)
		found := false
		if id <= 0 {
			return result
		}
		for _, r := range resolutions {
			if r.AdapterID != id {
				continue
			}
			if !r.Verified || r.Run != run {
				result.Reason = "obsolete or unverified breakpoint"
				return result
			}
			// A legacy point outside the definition store may share the same adapter ID
			// or address. It still owns a user-visible stop.
			for _, rawPoint := range adapterPoints {
				bp := asObj(rawPoint)
				if str(bp["client"]) == "brote-definitions" {
					continue
				}
				if num(bp["id"]) == id || (str(bp["file"]) == r.Resolved.File && num(bp["line"]) == r.Resolved.Line) {
					result.Reason = "ordinary breakpoint overlaps tracepoint"
					return result
				}
			}
			matched := false
			for _, d := range store.Items {
				if d.ID == r.DefinitionID {
					matched = true
					if !d.Enabled || d.Kind != "tracepoint" || (d.Scope.Run != "" && d.Scope.Run != run) {
						result.Reason = "ordinary or disabled definition also owns this stop"
						return result
					}
					if !selected[d.ID] {
						result.Definitions = append(result.Definitions, d)
						selected[d.ID] = true
					}
				}
			}
			if !matched {
				result.Reason = "configuration changed"
				return result
			}
			found = true
		}
		if !found {
			result.Reason = "unknown breakpoint ID"
			return result
		}
	}
	if len(result.Definitions) > 16 {
		result.Reason = "too many tracepoints share this stop"
		return result
	}
	result.Eligible = len(result.Definitions) > 0
	if result.Eligible {
		result.Reason = "exclusive tracepoint hit"
	}
	return result
}
