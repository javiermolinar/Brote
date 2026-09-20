package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func zedLabel(id string) string { return "Debug Handover · " + id }

// Replace comments with spaces, keeping string contents and byte offsets intact.
func stripComments(data []byte) ([]byte, error) {
	out := append([]byte(nil), data...)
	inString := false
	for i := 0; i < len(data); i++ {
		if inString {
			if data[i] == '\\' {
				i++
				continue
			}
			if data[i] == '"' {
				inString = false
			}
			continue
		}
		if data[i] == '"' {
			inString = true
			continue
		}
		if data[i] != '/' || i+1 >= len(data) {
			continue
		}
		if data[i+1] == '/' {
			for i < len(data) && data[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		if data[i+1] == '*' {
			out[i] = ' '
			out[i+1] = ' '
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				if data[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			if i+1 >= len(data) {
				return nil, fail("unclosed JSONC comment")
			}
			out[i] = ' '
			out[i+1] = ' '
			i++
		}
	}
	return out, nil
}

func stripTrailingCommas(clean []byte) []byte {
	// Strip trailing commas only outside strings. JSON decoding validates the rest.
	valid := append([]byte(nil), clean...)
	inString := false
	for i := 0; i < len(valid); i++ {
		if inString {
			if valid[i] == '\\' {
				i++
				continue
			}
			if valid[i] == '"' {
				inString = false
			}
			continue
		}
		if valid[i] == '"' {
			inString = true
			continue
		}
		if valid[i] == ',' {
			j := i + 1
			for j < len(valid) && strings.ContainsRune(" \r\n\t", rune(valid[j])) {
				j++
			}
			if j < len(valid) && (valid[j] == ']' || valid[j] == '}') {
				valid[i] = ' '
			}
		}
	}
	return valid
}

func appendZedProfile(data []byte, profile obj) ([]byte, error) {
	clean, e := stripComments(data)
	if e != nil {
		return nil, e
	}
	valid := stripTrailingCommas(clean)
	var entries []obj
	if e = json.Unmarshal(valid, &entries); e != nil {
		return nil, fail("existing .zed/debug.json is not a JSONC array: %w", e)
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
		return nil, fail("expected JSONC array")
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
func writeZedConfig(s Session) (string, error) {
	host, port, e := net.SplitHostPort(s.DAP)
	if e != nil {
		return "", e
	}
	n, e := strconv.Atoi(port)
	if e != nil {
		return "", e
	}
	profile := obj{"label": zedLabel(s.ID), "adapter": "Delve", "request": "attach", "mode": "remote", "stopOnEntry": true, "tcp_connection": obj{"host": host, "port": n}}
	path := filepath.Join(s.Project, ".zed", "debug.json")
	data, e := os.ReadFile(path)
	mode := os.FileMode(0644)
	if os.IsNotExist(e) {
		data = []byte("[\n]\n")
	} else if e != nil {
		return "", e
	} else if st, e := os.Stat(path); e == nil {
		mode = st.Mode().Perm()
	}
	updated, e := appendZedProfile(data, profile)
	if e != nil {
		return "", e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return "", e
	}
	// Use a unique adjacent temporary file so a failed write cannot truncate config.
	f, e := os.CreateTemp(filepath.Dir(path), ".debug-handover-*")
	if e != nil {
		return "", e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(updated)
	}
	closeErr := f.Close()
	if e != nil {
		return "", e
	}
	if closeErr != nil {
		return "", closeErr
	}
	if current, e := os.ReadFile(path); e == nil && string(current) != string(data) {
		return "", fail("Zed config changed while preparing handover; retry")
	}
	return path, os.Rename(tmp, path)
}

// Locate top-level profile objects without reformatting the surrounding JSONC.
func zedProfileRange(data []byte, label string) (int, int, error) {
	clean, e := stripComments(data)
	if e != nil {
		return 0, 0, e
	}
	valid := stripTrailingCommas(clean)
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
		clean, _ := stripComments(data)
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
func hasZedProfile(s Session) bool {
	data, e := os.ReadFile(filepath.Join(s.Project, ".zed", "debug.json"))
	if e != nil {
		return false
	}
	start, _, e := zedProfileRange(data, zedLabel(s.ID))
	return e == nil && start >= 0
}
func removeZedConfig(s Session) error {
	path := filepath.Join(s.Project, ".zed", "debug.json")
	data, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	updated, e := removeZedProfile(data, zedLabel(s.ID))
	if e != nil {
		return e
	}
	if string(updated) == string(data) {
		return nil
	}
	st, e := os.Stat(path)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".debug-handover-*")
	if e != nil {
		return e
	}
	temp := f.Name()
	defer os.Remove(temp)
	if e = f.Chmod(st.Mode().Perm()); e == nil {
		_, e = f.Write(updated)
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	current, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	if string(current) != string(data) {
		return fail("Zed config changed during cleanup; retry")
	}
	return os.Rename(temp, path)
}
