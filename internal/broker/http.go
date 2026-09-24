package broker

import (
	"agentdebugger/internal/protocol"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"agentdebugger/internal/session"
	"agentdebugger/packages/web"
)

func (b *broker) handler() http.Handler {
	origin := b.s.HTTP
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		write := func(code int, v any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(v) }
		if requestOrigin := r.Header.Get("Origin"); requestOrigin != "" && requestOrigin != origin {
			write(403, obj{"error": "foreign origin rejected"})
			return
		}
		if !b.admitHTTP(w, r) {
			return
		}
		if r.URL.Path == "/api/health" && r.Method == "GET" {
			b.mu.Lock()
			result := obj{"id": b.s.ID, "run": b.s.RunID, "serviceVersion": b.s.ServiceVersion, "version": b.s.Version, "status": "connected"}
			b.mu.Unlock()
			write(200, result)
			return
		}
		if r.Method == "POST" {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/json" {
				write(415, obj{"error": "application/json required"})
				return
			}
		}
		if b.s.ServiceVersion > 0 {
			switch r.URL.Path {
			case "/api/workspace", "/api/sessions", "/api/runs/start":
				write(403, obj{"error": "workspace operations require the local CLI, not a session credential", "code": "unsupported_operation", "version": protocol.Version})
				return
			case "/api/saved-run":
				if r.URL.Query().Get("id") != b.s.ID {
					write(403, obj{"error": "session identity mismatch", "code": "identity_mismatch", "version": protocol.Version})
					return
				}
			}
		}
		if r.URL.Path == "/api/workspace" && r.Method == "GET" {
			v, e := session.Workspace(r.Context())
			if e != nil {
				write(500, obj{"error": e.Error()})
			} else {
				write(200, obj{"investigations": v})
			}
			return
		}
		if r.URL.Path == "/api/saved-run" && r.Method == "GET" {
			v, e := session.SavedRun(r.URL.Query().Get("id"))
			if e != nil {
				response := obj{"error": e.Error()}
				write(409, response)
			} else {
				write(200, v)
			}
			return
		}
		if r.URL.Path == "/api/runs/start" && r.Method == "POST" {
			var input struct {
				Run     string `json:"run"`
				Binary  string `json:"binary"`
				Project string `json:"project"`
				Title   string `json:"title"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input) != nil {
				write(400, obj{"error": "invalid launch request"})
				return
			}
			args := []string{"run-again", input.Run}
			if input.Run == "" {
				if input.Binary == "" || input.Project == "" {
					write(400, obj{"error": "binary and project required"})
					return
				}
				args = []string{"start", "--binary", input.Binary, "--project", input.Project, "--title", input.Title, "--thread", ""}
			}
			exe, e := os.Executable()
			if e != nil {
				write(500, obj{"error": e.Error()})
				return
			}
			data, e := exec.Command(exe, args...).CombinedOutput()
			if e != nil {
				write(409, obj{"error": "Launch failed: " + string(data) + " " + e.Error()})
				return
			}
			var result obj
			if json.Unmarshal(data, &result) != nil {
				write(500, obj{"error": "invalid launch response"})
				return
			}
			write(200, result)
			return
		}
		if r.URL.Path == "/api/events" && r.Method == "GET" {
			b.events(w, r)
			return
		}
		if r.URL.Path == "/api/sources" && r.Method == "GET" {
			b.mu.Lock()
			descriptor := b.s
			b.mu.Unlock()
			result, err := session.Sources(descriptor, r.URL.Query().Get("file"))
			if err != nil {
				write(409, obj{"error": err.Error()})
			} else {
				write(200, result)
			}
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
			b.mu.Lock()
			ownID, serviceVersion := b.s.ID, b.s.ServiceVersion
			b.mu.Unlock()
			if serviceVersion > 0 && input.ID != ownID {
				write(403, obj{"error": "session credential cannot end another session"})
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
		if r.URL.Path == "/api/annotations" || r.URL.Path == "/api/annotation-evidence" {
			result, err := b.annotationRequest(w, r)
			if err != nil {
				write(409, obj{"error": err.Error()})
			} else {
				write(200, result)
			}
			return
		}
		if r.URL.Path == "/api/comments" {
			var input obj
			if r.Method == "POST" {
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, session.MaxAnswerBytes+16384)).Decode(&input); err != nil || input == nil {
					write(400, obj{"error": "invalid comment request"})
					return
				}
			} else if r.Method != "GET" {
				write(405, obj{"error": "method not allowed"})
				return
			}
			result, err := b.comments(input)
			if err != nil {
				write(409, obj{"error": err.Error()})
			} else {
				write(200, result)
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
		case r.URL.Path == "/api/v1" && r.Method == "POST":
			var request serviceRequest
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
			dec.DisallowUnknownFields()
			if e = dec.Decode(&request); e == nil {
				v, e = b.service(request)
			}
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
			response := obj{"error": e.Error()}
			if b.s.ServiceVersion > 0 && (r.URL.Path == "/api/v1" || r.URL.Path == "/api/action") {
				response["version"] = 1
				response["code"] = "invalid_request"
				var structured *protocol.Error
				if errors.As(e, &structured) {
					response["code"] = structured.Code
				}
			}
			write(409, response)
			return
		}
		write(200, v)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if name != "index.html" && name != "app.js" && name != "style.css" && name != "brote-plant.png" {
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
		case ".png":
			w.Header().Set("Content-Type", "image/png")
		case ".css":
			w.Header().Set("Content-Type", "text/css")
		default:
			w.Header().Set("Content-Type", "text/html")
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "http://"+r.Host != origin {
			http.Error(w, "unexpected host", 403)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}
