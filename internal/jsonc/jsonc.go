// Package jsonc normalizes editor configuration files without changing byte offsets.
package jsonc

import (
	"fmt"
	"strings"
)

// Replace comments with spaces, keeping string contents and byte offsets intact.
func StripComments(data []byte) ([]byte, error) {
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
				return nil, fmt.Errorf("unclosed JSONC comment")
			}
			out[i] = ' '
			out[i+1] = ' '
			i++
		}
	}
	return out, nil
}

func StripTrailingCommas(clean []byte) []byte {
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
