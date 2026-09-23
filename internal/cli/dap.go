package cli

import (
	"agentdebugger/internal/dap"
	"agentdebugger/internal/session"
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// dapTransport is the editor transport, not an agent-facing arbitrary-DAP API.
// Authentication is consumed locally; the editor sees ordinary DAP framing.
func dapTransport(id string, input io.Reader, output io.Writer) error {
	s, err := session.Read(id)
	if err != nil {
		return err
	}
	if err = session.LocalEndpoint(s.HTTP); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(s.DAP)
	if err != nil || host != "127.0.0.1" {
		return fmt.Errorf("expected local DAP endpoint")
	}
	conn, err := net.DialTimeout("tcp", s.DAP, 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if s.ServiceVersion > 0 {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		err = dap.Write(conn, obj{"seq": 0, "type": "request", "command": "brote/authenticate", "arguments": obj{"session": s.ID, "run": s.RunID, "token": s.Token}})
		if err != nil {
			return err
		}
		reply, e := dap.Read(reader)
		if e != nil {
			return e
		}
		if reply["success"] != true || reply["command"] != "brote/authenticate" {
			return fmt.Errorf("DAP session admission failed")
		}
		_ = conn.SetDeadline(time.Time{})
	}
	go func() {
		_, _ = io.Copy(conn, input)
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	_, err = io.Copy(output, reader)
	// Closing the socket wakes writes; stdin can remain blocked until this CLI exits.
	return err
}

func runDAP(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: brote dap SESSION")
	}
	return dapTransport(args[0], os.Stdin, os.Stdout)
}
