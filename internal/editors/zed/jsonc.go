package zed

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentdebugger/internal/jsonc"
)

type obj = map[string]any

func appendZedProfile(data []byte, profile obj) ([]byte, error) {
	clean, e := jsonc.StripComments(data)
	if e != nil {
		return nil, e
	}
	valid := jsonc.StripTrailingCommas(clean)
	var entries []obj
	if e = json.Unmarshal(valid, &entries); e != nil {
		return nil, fmt.Errorf("existing .zed/debug.json is not a JSONC array: %w", e)
	}
	for _, entry := range entries {
		if str(entry["label"]) == str(profile["label"]) {
			if pretty(entry) == pretty(profile) {
				return data, nil
			}
			return replaceZedProfile(data, profile)
		}
	}
	end := strings.LastIndex(string(clean), "]")
	if end < 0 {
		return nil, fmt.Errorf("expected JSONC array")
	}
	before := strings.TrimSpace(string(clean[:end]))
	comma := ""
	if !strings.HasSuffix(before, "[") && !strings.HasSuffix(before, ",") {
		comma = ","
	}
	encoded, _ := json.MarshalIndent(profile, "  ", "  ")
	out := append([]byte(nil), data[:end]...)
	out = append(out, []byte(comma+"\n  "+string(encoded)+"\n")...)
	out = append(out, data[end:]...)
	return out, nil
}

// Locate top-level profile objects without reformatting the surrounding JSONC.
func zedProfileRange(data []byte, label string) (int, int, error) {
	clean, e := jsonc.StripComments(data)
	if e != nil {
		return 0, 0, e
	}
	valid := jsonc.StripTrailingCommas(clean)
	var entries []obj
	if e = json.Unmarshal(valid, &entries); e != nil {
		return 0, 0, e
	}
	quoted := false
	depth := 0
	start := -1
	for i := 0; i < len(valid); i++ {
		c := valid[i]
		if quoted {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		if c == '{' {
			if depth == 0 {
				start = i
			}
			depth++
		}
		if c == '}' {
			depth--
			if depth == 0 && start >= 0 {
				var entry obj
				if e = json.Unmarshal(valid[start:i+1], &entry); e != nil {
					return 0, 0, e
				}
				if str(entry["label"]) == label {
					return start, i + 1, nil
				}
			}
		}
	}
	return -1, -1, nil
}

func replaceZedProfile(data []byte, profile obj) ([]byte, error) {
	start, end, e := zedProfileRange(data, str(profile["label"]))
	if e != nil {
		return nil, e
	}
	if start < 0 {
		return appendZedProfile(data, profile)
	}
	encoded, e := json.MarshalIndent(profile, "  ", "  ")
	if e != nil {
		return nil, e
	}
	out := append([]byte(nil), data[:start]...)
	out = append(out, encoded...)
	return append(out, data[end:]...), nil
}

func removeZedProfile(data []byte, label string) ([]byte, error) {
	for {
		start, end, e := zedProfileRange(data, label)
		if e != nil {
			return nil, e
		}
		if start < 0 {
			return data, nil
		}
		clean, _ := jsonc.StripComments(data)
		out := append([]byte(nil), data...)
		for i := start; i < end; i++ {
			if out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
			}
		}
		after := end
		for after < len(clean) && strings.ContainsRune(" \t\r\n", rune(clean[after])) {
			after++
		}
		if after < len(clean) && clean[after] == ',' {
			out[after] = ' '
		} else {
			before := start - 1
			for before >= 0 && strings.ContainsRune(" \t\r\n", rune(clean[before])) {
				before--
			}
			if before >= 0 && clean[before] == ',' {
				out[before] = ' '
			}
		}
		data = out
	}
}

func str(v any) string { s, _ := v.(string); return s }

func pretty(v any) string { data, _ := json.MarshalIndent(v, "", "  "); return string(data) }
