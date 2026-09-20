package dap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func Read(r *bufio.Reader) (map[string]any, error) {
	length := -1
	headers := 0
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			return nil, e
		}
		headers += len(line)
		if headers > 8192 {
			return nil, fmt.Errorf("DAP header too large")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		key, val, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(key, "Content-Length") {
			length, e = strconv.Atoi(strings.TrimSpace(val))
			if e != nil {
				return nil, e
			}
		}
	}
	if length < 0 || length > 8<<20 {
		return nil, fmt.Errorf("invalid DAP content length")
	}
	data := make([]byte, length)
	if _, e := io.ReadFull(r, data); e != nil {
		return nil, e
	}
	var v map[string]any
	e := json.Unmarshal(data, &v)
	return v, e
}

func Write(w io.Writer, v map[string]any) error {
	data, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return e
}
