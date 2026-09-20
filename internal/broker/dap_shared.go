package broker

import (
	"agentdebugger/internal/dap"
	"agentdebugger/internal/editors"
	"bufio"
	"fmt"
	"net"
)

// Editors are clients of the broker, never independent clients of Delve.
func (b *broker) connectSharedDAP(front net.Conn) {
	b.mu.Lock()
	if !editors.IsOwner(b.owner) || b.peer != nil {
		b.mu.Unlock()
		front.Close()
		return
	}
	p := &dapPeer{front: front, pending: map[int]string{}}
	b.peer = p
	b.record("editor.connected", "human", obj{"editor": b.owner})
	b.generation++
	b.mu.Unlock()
	defer func() {
		p.close()
		b.mu.Lock()
		b.record("editor.disconnected", "human", obj{})
		if b.peer == p {
			b.peer = nil
			b.generation++
		}
		b.mu.Unlock()
	}()
	r := bufio.NewReader(front)
	for {
		v, e := dap.Read(r)
		if e != nil {
			return
		}
		if str(v["type"]) != "request" {
			continue
		}
		command := str(v["command"])
		args := asObj(v["arguments"])
		if args == nil {
			args = obj{}
		}
		b.mu.Lock()
		var body obj
		var err error
		startedExecution := false
		if b.peer != p || !editors.IsOwner(b.owner) {
			err = fmt.Errorf("editor no longer controls execution")
		} else {
			switch command {
			case "initialize":
				body = b.backend.Capabilities()
			case "attach":
				body = obj{}
			case "configurationDone":
				p.ready = true
				body = obj{}
			case "disconnect":
				body = obj{}
			case "launch", "restart", "terminate":
				err = fmt.Errorf("end or restart through AgentDebugger session controls")
			case "setBreakpoints":
				body, err = b.backend.ReplaceBreakpoints("editor", str(asObj(args["source"])["path"]), asList(args["breakpoints"]), false)
			case "setFunctionBreakpoints":
				body, err = b.backend.ReplaceBreakpoints("editor", "", asList(args["breakpoints"]), true)
			default:
				err = p.translateArguments(args, b.generation)
				if err == nil && isExecution(command) {
					if b.moving || truth(b.backend.State()["Running"]) {
						err = fmt.Errorf("execution already in progress")
					} else {
						b.moving = true
						startedExecution = true
						b.generation++
					}
				}
				if err == nil {
					body, err = b.backend.Request(command, args)
				}
				if err != nil && startedExecution {
					b.moving = false
				}
				if err == nil {
					p.exportHandles(body, command, b.generation)
				}
			}
		}
		if err == nil && (command == "setBreakpoints" || command == "setFunctionBreakpoints") {
			b.generation++
			err = b.persist()
		}
		b.record("editor."+command, "human", obj{"stop_id": b.stopID, "arguments": args, "result": body, "error": errorString(err)})
		b.mu.Unlock()
		response := obj{"seq": v["seq"], "type": "response", "request_seq": v["seq"], "command": command, "success": err == nil, "body": body}
		if err != nil {
			response["message"] = err.Error()
		}
		if p.send(response) != nil {
			return
		}
		if err == nil && command == "attach" {
			_ = p.send(obj{"seq": 0, "type": "event", "event": "initialized"})
		}
		if err == nil && command == "configurationDone" {
			state := b.backend.State()
			if !truth(state["Running"]) {
				_ = p.send(obj{"seq": 0, "type": "event", "event": "stopped", "body": obj{"reason": "entry", "threadId": asObj(state["currentGoroutine"])["id"], "allThreadsStopped": true}})
			}
		}
		if command == "disconnect" {
			return
		}
	}
}
