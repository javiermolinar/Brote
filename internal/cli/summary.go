package cli

// summarizeState preserves routing and inspection errors while omitting the
// source listing, unrelated goroutines, and Delve function metadata.
func summarizeState(v obj) obj {
	out := obj{}
	for _, key := range []string{"id", "owner", "binding", "notification", "generation", "status", "project", "binary", "panel", "goroutine", "frame", "error", "inspectionError", "sourceIdentity", "sourceNewerThanBinary", "breakpoints", "watches"} {
		if value, ok := v[key]; ok {
			out[key] = value
		}
	}
	frames := []any{}
	if list, ok := v["frames"].([]any); ok {
		for _, raw := range list {
			f, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			frame := obj{"file": f["file"], "line": f["line"]}
			if fn, ok := f["function"].(map[string]any); ok {
				frame["function"] = fn["name"]
			}
			for _, key := range []string{"Arguments", "Locals", "Err"} {
				if value, ok := f[key]; ok {
					frame[key] = value
				}
			}
			frames = append(frames, frame)
		}
	}
	out["frames"] = frames
	if state, ok := v["state"].(map[string]any); ok {
		info := obj{}
		for _, key := range []string{"Pid", "stopReason", "exited", "exitStatus"} {
			if value, ok := state[key]; ok {
				info[key] = value
			}
		}
		out["state"] = info
	}
	return out
}
