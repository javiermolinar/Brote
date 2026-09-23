package broker

import (
	"agentdebugger/internal/dap"
	"agentdebugger/internal/session"
	"bufio"
	"net"
	"net/http"
	"strings"
	"time"
)

func (b *broker) admitHTTP(w http.ResponseWriter, r *http.Request) bool {
	b.mu.Lock()
	descriptor := b.s
	b.mu.Unlock()
	if descriptor.ServiceVersion == 0 {
		return true
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !session.ValidToken(descriptor.Token, token) {
		http.Error(w, `{"error":"session credential required","code":"unauthenticated"}`, http.StatusUnauthorized)
		return false
	}
	if id := r.URL.Query().Get("session"); id != "" && id != descriptor.ID {
		http.Error(w, `{"error":"session identity mismatch","code":"identity_mismatch"}`, http.StatusConflict)
		return false
	}
	return true
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
	run    string
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// A Brote transport authenticates before DAP initialization, peer reservation,
// history writes or any connection to Delve. The token is never forwarded.
func (b *broker) admitDAP(front net.Conn) (net.Conn, bool) {
	b.mu.Lock()
	descriptor := b.s
	b.mu.Unlock()
	if descriptor.ServiceVersion == 0 {
		return front, true
	}
	_ = front.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(front)
	v, err := dap.Read(reader)
	a := asObj(v["arguments"])
	ok := err == nil && str(v["type"]) == "request" && str(v["command"]) == "brote/authenticate" &&
		str(a["session"]) == descriptor.ID && str(a["run"]) == descriptor.RunID && session.ValidToken(descriptor.Token, str(a["token"]))
	_ = front.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err == nil {
		_ = dap.Write(front, obj{"seq": 0, "type": "response", "request_seq": v["seq"], "command": "brote/authenticate", "success": ok})
	}
	if !ok {
		_ = front.Close()
		return nil, false
	}
	_ = front.SetDeadline(time.Time{})
	return &bufferedConn{Conn: front, reader: reader, run: descriptor.RunID}, true
}
