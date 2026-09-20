package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/rpc/jsonrpc"
	"regexp"
	"strconv"
	"time"
)

type obj = map[string]any

func rpcCall(addr, method string, arg any, timeout time.Duration) (obj, error) {
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
	out := obj{}
	err = client.Call("RPCServer."+method, arg, &out)
	return out, err
}

func asObj(v any) obj    { o, _ := v.(map[string]any); return o }
func asList(v any) []any { a, _ := v.([]any); return a }
func str(v any) string   { s, _ := v.(string); return s }
func num(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
func truth(v any) bool                      { b, _ := v.(bool); return b }
func pretty(v any) string                   { data, _ := json.MarshalIndent(v, "", "  "); return string(data) }
func fail(format string, args ...any) error { return fmt.Errorf(format, args...) }

var loadConfig = obj{"FollowPointers": true, "MaxVariableRecurse": 1, "MaxStringLen": 256, "MaxArrayValues": 20, "MaxStructFields": 30}

func pick(v obj, keys ...string) obj {
	out := obj{}
	for _, k := range keys {
		if value, ok := v[k]; ok {
			out[k] = value
		}
	}
	return out
}
func compactVariables(input any, depth int) []any {
	return compactVariableDepth(input, depth, 2)
}
func compactVariableDepth(input any, depth, limit int) []any {
	out := []any{}
	for _, raw := range asList(input) {
		v := asObj(raw)
		item := pick(v, "name", "type", "value", "unreadable", "len", "cap", "kind")
		if depth < limit {
			if children := asList(v["children"]); len(children) > 0 {
				item["children"] = compactVariableDepth(children, depth+1, limit)
			}
		}
		out = append(out, item)
	}
	return out
}

var exitedProcess = regexp.MustCompile(`^Process (\d+) has exited with status (-?\d+)$`)

// Delve's JSON-RPC protocol serializes ProcessExitedError as an error string,
// including on State after a normal exit. Recover the lifecycle state it carries.
func exitState(err error) (obj, bool) {
	if err == nil {
		return nil, false
	}
	parts := exitedProcess.FindStringSubmatch(err.Error())
	if parts == nil {
		return nil, false
	}
	pid, _ := strconv.Atoi(parts[1])
	code, _ := strconv.Atoi(parts[2])
	return obj{"Pid": pid, "Running": false, "exited": true, "exitStatus": code, "stopReason": "exited"}, true
}
