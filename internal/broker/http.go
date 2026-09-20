package broker

import (
	"encoding/json"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"debug-handover/internal/session"
	"debug-handover/ui/inspector"
)

func (b *broker) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		write := func(code int, v any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(v) }
		if origin := r.Header.Get("Origin"); origin != "" && origin != b.s.HTTP {
			write(403, obj{"error": "foreign origin rejected"})
			return
		}
		if r.Method == "POST" {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/json" {
				write(415, obj{"error": "application/json required"})
				return
			}
		}
		if r.URL.Path == "/api/events" && r.Method == "GET" {
			b.events(w, r)
			return
		}
		if r.URL.Path == "/api/sessions/stop" && r.Method == "POST" {
			var input struct {
				ID        string `json:"id"`
				Confirmed bool   `json:"confirmed"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil || !input.Confirmed || input.ID == "" {
				write(400, obj{"error": "session ID and explicit confirmation required"})
				return
			}
			result, err := session.End(r.Context(), input.ID)
			if err != nil {
				write(409, obj{"error": err.Error()})
			} else {
				write(200, result)
			}
			return
		}
		if r.URL.Path == "/api/sessions" && r.Method == "GET" {
			list, err := session.List(r.Context())
			if err != nil {
				write(500, obj{"error": err.Error()})
			} else {
				write(200, obj{"sessions": list})
			}
			return
		}
		var v obj
		var e error
		switch {
		case r.URL.Path == "/api/state" && r.Method == "GET":
			gid, _ := strconv.Atoi(r.URL.Query().Get("goroutine"))
			frame, _ := strconv.Atoi(r.URL.Query().Get("frame"))
			v, e = b.snapshot(gid, frame, r.URL.Query().Get("brief") == "1")
		case r.URL.Path == "/api/action" && r.Method == "POST":
			var a obj
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
			if e = dec.Decode(&a); e == nil {
				v, e = b.action(a)
			}
		default:
			write(404, obj{"error": "unknown endpoint"})
			return
		}
		if e != nil {
			write(409, obj{"error": e.Error()})
			return
		}
		write(200, v)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if name != "index.html" && name != "app.js" && name != "style.css" {
			http.NotFound(w, r)
			return
		}
		data, e := inspector.Assets.ReadFile("public/" + name)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		switch filepath.Ext(name) {
		case ".js":
			w.Header().Set("Content-Type", "text/javascript")
		case ".css":
			w.Header().Set("Content-Type", "text/css")
		default:
			w.Header().Set("Content-Type", "text/html")
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "http://"+r.Host != b.s.HTTP {
			http.Error(w, "unexpected host", 403)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}
