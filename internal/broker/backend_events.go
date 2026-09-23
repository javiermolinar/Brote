package broker

import "agentdebugger/internal/backend"

func (b *broker) watchBackend(source *backend.Delve) {
	if source != nil {
		go func() {
			for event := range source.Events {
				b.mu.Lock()
				if b.backend != source {
					b.mu.Unlock()
					continue
				}
				switch str(event["event"]) {
				case "breakpoint":
					point := asObj(asObj(event["body"])["breakpoint"])
					for i := range b.resolutions {
						r := &b.resolutions[i]
						if r.AdapterID == num(point["id"]) && r.Run == b.s.RunID {
							r.Verified = truth(point["verified"]) && str(asObj(event["body"])["reason"]) != "removed"
							r.Message = str(point["message"])
							if line := num(point["line"]); line > 0 {
								r.Resolved.Line = line
							}
							if file := str(asObj(point["source"])["path"]); file != "" {
								r.Resolved.File = file
							}
						}
					}
				case "continued":
					b.moving = true
					b.generation++
					b.handleEpoch++
				case "stopped":
					if b.s.ServiceVersion > 0 {
						points, _ := source.Call("ListBreakpoints", obj{})
						b.currentStop = attributeStop(asObj(event["body"]), b.s.RunID, b.s.Definitions, b.resolutions, asList(points["Breakpoints"]))
					}
					b.moving = false
					b.generation++
					b.handleEpoch++
					_ = b.emit("stopped", str(asObj(event["body"])["reason"]))
					b.captureStop()
				case "exited":
					b.moving = false
					b.generation++
					b.handleEpoch++
					_ = b.emit("target_exited", "")
				}
				if str(event["event"]) != "initialized" && b.peer != nil && b.peer.back == nil && (b.peer.ready || (str(event["event"]) != "stopped" && str(event["event"]) != "continued")) {
					_ = b.peer.send(event)
				}
				b.mu.Unlock()
			}
		}()
	}
}
