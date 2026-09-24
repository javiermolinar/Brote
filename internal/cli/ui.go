package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agentdebugger/internal/session"
	"agentdebugger/internal/tracing"
	inspector "agentdebugger/packages/web"
)

// The workspace UI outlives individual brokers. Ending the last run must not
// remove access to its evidence or the controls for launching another one.
func uiPath() (string, error) {
	root, e := session.DataRoot()
	return filepath.Join(root, "workspace"), e
}
func ensureUI() (string, error) {
	dir, e := uiPath()
	if e != nil {
		return "", e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	probe := func() string {
		data, e := os.ReadFile(filepath.Join(dir, "endpoint.json"))
		if e != nil {
			return ""
		}
		var endpoint string
		if json.Unmarshal(data, &endpoint) != nil {
			return ""
		}
		u, e := url.Parse(endpoint)
		if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return ""
		}
		c := http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		r, e := c.Get(endpoint + "/health")
		if e != nil {
			return ""
		}
		defer r.Body.Close()
		var status struct {
			Service string `json:"service"`
		}
		if json.NewDecoder(r.Body).Decode(&status) != nil || status.Service != "agentdebugger-workspace" {
			return ""
		}
		return endpoint
	}
	if endpoint := probe(); endpoint != "" {
		return endpoint, nil
	}
	exe, e := os.Executable()
	if e != nil {
		return "", e
	}
	log, e := os.OpenFile(filepath.Join(dir, "ui.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	defer log.Close()
	cmd := exec.Command(exe, "ui-serve")
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = cmd.Start(); e != nil {
		return "", e
	}
	go func() { _ = cmd.Wait() }()
	for i := 0; i < 50; i++ {
		if endpoint := probe(); endpoint != "" {
			return endpoint, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", fmt.Errorf("workspace startup timed out; see %s", log.Name())
}
func serveUI() error {
	dir, e := uiPath()
	if e != nil {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	lock, e := session.Lock(dir)
	if e != nil {
		return e
	}
	defer session.Unlock(lock)
	var ln net.Listener
	var previous string
	if data, err := os.ReadFile(filepath.Join(dir, "endpoint.json")); err == nil && json.Unmarshal(data, &previous) == nil {
		if u, err := url.Parse(previous); err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.User == nil {
			ln, _ = net.Listen("tcp", u.Host)
		}
	}
	if ln == nil {
		ln, e = net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return e
		}
	}
	origin := "http://" + ln.Addr().String()
	if e = session.Write(filepath.Join(dir, "endpoint.json"), origin); e != nil {
		ln.Close()
		return e
	}
	server := http.Server{Handler: workspaceHandler(origin), ReadHeaderTimeout: 5 * time.Second}
	return server.Serve(ln)
}
func workspaceHandler(origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(code int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}
		if "http://"+r.Host != origin || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) {
			write(403, obj{"error": "foreign origin rejected"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == "POST" && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			write(415, obj{"error": "JSON required"})
			return
		}
		if r.URL.Path == "/health" {
			write(200, obj{"service": "agentdebugger-workspace"})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/workspace" {
			v, e := session.Workspace(r.Context())
			if e != nil {
				write(500, obj{"error": e.Error()})
			} else {
				for i := range v {
					for j := range v[i].Runs {
						if v[i].Runs[j].Panel != "" {
							v[i].Runs[j].Panel = origin + "/?session=" + v[i].Runs[j].ID
						}
					}
				}
				write(200, obj{"investigations": v})
			}
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/saved-run" {
			v, e := session.SavedRun(r.URL.Query().Get("id"))
			if e != nil {
				write(409, obj{"error": e.Error()})
			} else {
				write(200, v)
			}
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/runs/start" {
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
				args = []string{"start", "--binary", input.Binary, "--project", input.Project, "--title", input.Title, "--thread", ""}
			}
			result, e := Run(args)
			if e != nil {
				write(409, obj{"error": e.Error()})
			} else {
				write(200, result)
			}
			return
		}
		if r.Method == "POST" && (r.URL.Path == "/api/runs/delete" || r.URL.Path == "/api/investigations/delete") {
			var input struct {
				ID        string `json:"id"`
				Confirmed bool   `json:"confirmed"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil || !input.Confirmed {
				write(400, obj{"error": "explicit confirmation required"})
				return
			}
			var e error
			if r.URL.Path == "/api/investigations/delete" {
				e = session.DeleteInvestigation(r.Context(), input.ID)
			} else {
				e = session.DeleteRun(input.ID)
			}
			if e != nil {
				write(409, obj{"error": e.Error()})
			} else {
				write(200, obj{"deleted": input.ID})
			}
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/sessions/stop" {
			var input struct {
				ID        string `json:"id"`
				Confirmed bool   `json:"confirmed"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil || !input.Confirmed {
				write(400, obj{"error": "explicit confirmation required"})
				return
			}
			if descriptor, err := session.Read(input.ID); err == nil && descriptor.ServiceVersion > 0 &&
				!session.ValidToken(descriptor.Token, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
				write(401, obj{"error": "session credential required"})
				return
			}
			v, e := session.End(r.Context(), input.ID)
			if e != nil {
				write(409, obj{"error": e.Error()})
			} else {
				write(200, v)
			}
			return
		}

		if (r.URL.Path == "/api/annotations" || r.URL.Path == "/api/annotation-evidence") && r.URL.Query().Get("history") != "" {
			id := r.URL.Query().Get("history")
			if r.URL.Path == "/api/annotation-evidence" && r.Method != "GET" {
				write(405, obj{"error": "method not allowed"})
				return
			}
			if _, err := session.FindHistory(id); err != nil {
				write(404, obj{"error": "saved run unavailable"})
				return
			}
			if descriptor, err := session.Read(id); err == nil && !descriptor.Stopped && descriptor.ServiceVersion > 0 && !session.ValidToken(descriptor.Token, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
				write(401, obj{"error": "live session credential required"})
				return
			}
			var result any
			var err error
			if r.URL.Path == "/api/annotation-evidence" && r.Method == "GET" {
				result, err = tracing.EvidenceFor(r.Context(), id)
			} else {
				owner := r.URL.Query().Get("traceSession")
				var in tracing.AnnotationRequest
				if r.Method == "POST" {
					decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
					decoder.DisallowUnknownFields()
					if decoder.Decode(&in) != nil {
						write(400, obj{"error": "invalid annotation request"})
						return
					}
					owner = in.Session
				} else if r.Method != "GET" {
					write(405, obj{"error": "method not allowed"})
					return
				}
				if owner != id && !strings.HasPrefix(owner, id+":") {
					write(409, obj{"error": "annotation session identity mismatch"})
					return
				}
				if r.Method == "POST" {
					result, err = tracing.Annotate(r.Context(), in)
				} else {
					result, err = tracing.Annotations(r.Context(), owner)
				}
			}
			if err != nil {
				write(409, obj{"error": err.Error()})
			} else {
				write(200, result)
			}
			return
		}
		if r.URL.Path == "/api/comments" && r.URL.Query().Get("history") != "" {
			id := r.URL.Query().Get("history")
			if descriptor, err := session.Read(id); err == nil && !descriptor.Stopped && descriptor.ServiceVersion > 0 && !session.ValidToken(descriptor.Token, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
				write(401, obj{"error": "live session credential required"})
				return
			}
			if r.Method == "GET" {
				d, err := session.ReadDiscussion(id)
				if err != nil {
					write(409, obj{"error": err.Error()})
				} else {
					write(200, obj{"discussion": d})
				}
				return
			}
			if r.Method != "POST" {
				write(405, obj{"error": "method not allowed"})
				return
			}
			var input struct {
				session.DiscussionRequest
				ContextMode string `json:"contextMode"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, session.MaxAnswerBytes+16384)).Decode(&input); err != nil {
				write(400, obj{"error": "invalid comment request"})
				return
			}
			if input.ContextMode == "current" {
				write(409, obj{"error": "current evidence requires a live session"})
				return
			}
			t, err := session.MutateDiscussion(id, input.DiscussionRequest)
			if err != nil {
				write(409, obj{"error": err.Error()})
			} else {
				result := session.DiscussionResult(id, t, input.Action)
				syncDiscussionTrace(id, result)
				write(200, result)
			}
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			id := r.URL.Query().Get("session")
			s, e := session.Read(id)
			if e != nil {
				write(404, obj{"error": "Select a live run"})
				return
			}
			if s.ServiceVersion > 0 && !session.ValidToken(s.Token, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
				write(401, obj{"error": "Open this service session using its authenticated launch URL"})
				return
			}
			target, e := url.Parse(s.HTTP)
			if e != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" || target.User != nil {
				write(400, obj{"error": "invalid broker endpoint"})
				return
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			director := proxy.Director
			proxy.Director = func(req *http.Request) {
				director(req)
				req.Host = target.Host
				req.Header.Set("Origin", s.HTTP)
				if s.Token != "" {
					req.Header.Set("Authorization", "Bearer "+s.Token)
				}
				q := req.URL.Query()
				q.Del("session")
				req.URL.RawQuery = q.Encode()
			}
			proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
				write(502, obj{"error": "Debugger connection lost; use recovery instructions"})
			}
			proxy.ServeHTTP(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		mime := map[string]string{"index.html": "text/html", "app.js": "text/javascript", "style.css": "text/css", "brote-plant.png": "image/png"}
		if r.Method != "GET" || mime[name] == "" {
			http.NotFound(w, r)
			return
		}
		data, e := inspector.Assets.ReadFile("public/" + name)
		if e != nil {
			write(500, obj{"error": e.Error()})
			return
		}
		if name == "index.html" {
			data = []byte(strings.Replace(string(data), "<body>", "<body data-workspace=\"true\">", 1))
		}
		w.Header().Set("Content-Type", mime[name])
		_, _ = w.Write(data)
	})
}
