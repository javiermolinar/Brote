package delve

import (
	"net"
	"net/rpc/jsonrpc"
	"regexp"
	"strconv"
	"time"
)

func Call(addr, method string, arg any, timeout time.Duration) (map[string]any, error) {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if timeout > 0 {
		_ = c.SetDeadline(time.Now().Add(timeout))
	}
	client := jsonrpc.NewClient(c)
	defer client.Close()
	out := map[string]any{}
	err = client.Call("RPCServer."+method, arg, &out)
	return out, err
}

var exitedProcess = regexp.MustCompile(`^Process (\d+) has exited with status (-?\d+)$`)

// Delve's JSON-RPC protocol serializes ProcessExitedError as an error string,
// including on State after a normal exit. Recover the lifecycle state it carries.
func ExitState(err error) (map[string]any, bool) {
	if err == nil {
		return nil, false
	}
	parts := exitedProcess.FindStringSubmatch(err.Error())
	if parts == nil {
		return nil, false
	}
	pid, _ := strconv.Atoi(parts[1])
	code, _ := strconv.Atoi(parts[2])
	return map[string]any{"Pid": pid, "Running": false, "exited": true, "exitStatus": code, "stopReason": "exited"}, true
}

// Begin writes a command before returning; wait consumes its eventual response.
func Begin(addr, method string, arg any) (func() (map[string]any, error), error) {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	client := jsonrpc.NewClient(c)
	out := map[string]any{}
	_ = c.SetWriteDeadline(time.Now().Add(3 * time.Second))
	call := client.Go("RPCServer."+method, arg, &out, nil)
	_ = c.SetWriteDeadline(time.Time{})
	return func() (map[string]any, error) { defer client.Close(); result := <-call.Done; return out, result.Error }, nil
}
