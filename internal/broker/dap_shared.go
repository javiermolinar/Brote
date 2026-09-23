package broker

import (
	"agentdebugger/internal/dap"
	"agentdebugger/internal/protocol"
	"bufio"
	"fmt"
	"net"
)

// Editors are clients of the broker, never independent clients of Delve.
func (b *broker) connectSharedDAP(front net.Conn) {
	b.mu.Lock()
	if admitted, ok := front.(*bufferedConn); (ok && admitted.run != b.s.RunID) || b.backend == nil || b.closing || b.s.Stopped {
		b.mu.Unlock()
		front.Close()
		return
	}
	if b.peer != nil {
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
	lastSequence := 0
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
		deferredResponse := false
		if num(v["seq"]) <= lastSequence {
			err = fmt.Errorf("duplicate or out-of-order DAP request sequence")
		} else if b.peer != p {
			err = fmt.Errorf("editor connection changed")
		} else if !protocol.DAPRequestSupported(command) {
			err = &protocol.Error{Code: "unsupported_operation", Message: "Brote does not support DAP request: " + command}
		} else {
			switch command {
			case "initialize":
				body = protocol.DAPCapabilities(b.backend.Capabilities())
			case "attach", "launch":
				if p.setup {
					err = fmt.Errorf("editor already configured")
					break
				}
				p.setup = true
				if command == "launch" {
					p.launched = true
					p.stopOnEntry = truth(args["stopOnEntry"])
				}
				body = obj{}
			case "configurationDone":
				if p.ready || !p.setup {
					err = fmt.Errorf("editor already configured")
					break
				}
				p.ready = true
				b.editorConfigured = true
				p.configuredIntent = b.executionIntent
				body = obj{}
			case "disconnect":
				// Legacy attach sessions retain their target even when an editor
				// requests termination on disconnect, matching the old proxy.
				if b.s.ServiceVersion > 0 && truth(args["terminateDebuggee"]) {
					err = b.endTargetLocked()
				}
				body = obj{}
			case "pause":
				_ = b.humanIntent("pause")
				err = b.interruptExecution()
			case "restart":
				if !p.ready {
					err = fmt.Errorf("editor is not configured")
					break
				}
				err = b.humanIntent("restart")
				if err == nil {
					p.ready = false
					_, err = b.restartLocked(true)
					if err != nil {
						p.ready = true
					}
				}
			case "terminate":
				err = b.endTargetLocked()
			case "setBreakpoints":
				if b.s.ServiceVersion > 0 {
					body, err = b.replaceEditorDefinitions(str(asObj(args["source"])["path"]), asList(args["breakpoints"]), false)
				} else {
					body, err = b.backend.ReplaceBreakpoints("editor", str(asObj(args["source"])["path"]), asList(args["breakpoints"]), false)
				}
			case "setFunctionBreakpoints":
				if b.s.ServiceVersion > 0 {
					body, err = b.replaceEditorDefinitions("", asList(args["breakpoints"]), true)
				} else {
					body, err = b.backend.ReplaceBreakpoints("editor", "", asList(args["breakpoints"]), true)
				}
			case "continue", "next", "stepIn", "stepOut":
				if !p.ready {
					err = fmt.Errorf("configurationDone is required before execution")
					break
				}
				err = b.humanIntent(command)
				request := v
				if err == nil {
					b.setExecutionIntent(command, "vscode")
					err = b.dispatchDAP(command, args, func(result obj, e error) {
						response := obj{"type": "response", "request_seq": request["seq"], "command": command, "success": e == nil, "body": result}
						if e != nil {
							response["message"] = e.Error()
						}
						_ = p.send(response)
					})
				}
				deferredResponse = err == nil
			default:
				err = b.inspectEditor(p, v, command, args)
				deferredResponse = err == nil

			}
		}
		lastSequence = max(lastSequence, num(v["seq"]))
		if err == nil && (command == "setBreakpoints" || command == "setFunctionBreakpoints") {
			b.generation++
			err = b.persist()
		}
		outcome := "accepted"
		if err != nil {
			outcome = "failed"
		}
		b.trace.Action(command, outcome)
		b.record("editor."+command, "human", obj{"stop_id": b.stopID, "arguments": args, "result": body, "error": errorString(err)})
		b.mu.Unlock()
		if deferredResponse {
			continue
		}
		response := obj{"seq": v["seq"], "type": "response", "request_seq": v["seq"], "command": command, "success": err == nil, "body": body}
		if err != nil {
			response["message"] = err.Error()
		}
		if p.send(response) != nil {
			return
		}
		if err == nil && (command == "attach" || command == "launch" || command == "restart") {
			_ = p.send(obj{"seq": 0, "type": "event", "event": "initialized"})
		}
		if err == nil && command == "configurationDone" {
			b.mu.Lock()
			if b.peer == p && !b.s.Stopped && b.backend != nil {
				if p.launched && !p.stopOnEntry && p.configuredIntent == b.executionIntent {
					if e := b.humanIntent("continue"); e == nil {
						b.setExecutionIntent("continue", "vscode")
						if e = b.dispatchDAP("continue", obj{}, nil); e != nil {
							b.lastError = e.Error()
						}
					} else {
						b.lastError = e.Error()
					}
				} else {
					state := b.backend.State()
					if !truth(state["Running"]) {
						_ = p.send(obj{"type": "event", "event": "stopped", "body": obj{"reason": "entry", "threadId": asObj(state["currentGoroutine"])["id"], "allThreadsStopped": true}})
					}
				}
			}
			b.mu.Unlock()
		}
		if command == "disconnect" {
			return
		}
	}
}
