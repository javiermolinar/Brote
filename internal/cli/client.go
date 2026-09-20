package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"debug-handover/internal/session"
)

func api(s session.Descriptor, method, path string, body any) (obj, error) {
	var r io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		r = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, s.HTTP+path, r)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	v := obj{}
	if e = json.NewDecoder(resp.Body).Decode(&v); e != nil {
		return nil, e
	}
	if resp.StatusCode >= 400 {
		return v, fmt.Errorf("%s", str(v["error"]))
	}
	return v, nil
}

func str(v any) string { s, _ := v.(string); return s }

func errorString(e error) string {
	if e != nil {
		return e.Error()
	}
	return ""
}
