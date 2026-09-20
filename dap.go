package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

func readDAP(r *bufio.Reader) (obj, error) {
	length := -1
	headers := 0
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			return nil, e
		}
		headers += len(line)
		if headers > 8192 {
			return nil, fail("DAP header too large")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		key, val, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(key, "Content-Length") {
			length, e = strconv.Atoi(strings.TrimSpace(val))
			if e != nil {
				return nil, e
			}
		}
	}
	if length < 0 || length > 8<<20 {
		return nil, fail("invalid DAP content length")
	}
	data := make([]byte, length)
	if _, e := io.ReadFull(r, data); e != nil {
		return nil, e
	}
	var v obj
	e := json.Unmarshal(data, &v)
	return v, e
}
func writeDAP(w io.Writer, v obj) error {
	data, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return e
}

type dapPeer struct {
	front, back net.Conn
	wmu         sync.Mutex
	mu          sync.Mutex
	pending     map[int]string
	once        sync.Once
	ready       bool // protected by the broker mutex
}

func (p *dapPeer) send(v obj) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_ = p.front.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return writeDAP(p.front, v)
}
func (p *dapPeer) pendingCount() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.pending) }
func (p *dapPeer) close() {
	p.once.Do(func() {
		_ = p.send(obj{"seq": 1000000000, "type": "event", "event": "terminated", "body": obj{}})
		_ = p.front.Close()
		_ = p.back.Close()
	})
}
func isExecution(command string) bool {
	switch command {
	case "continue", "next", "stepIn", "stepOut", "stepBack", "reverseContinue":
		return true
	}
	return false
}
func (b *Broker) acceptDAP(ln net.Listener) {
	for {
		c, e := ln.Accept()
		if e != nil {
			return
		}
		go b.connectDAP(c)
	}
}
func (b *Broker) connectDAP(front net.Conn) {
	b.mu.Lock()
	if !editorOwner(b.owner) || b.peer != nil {
		b.mu.Unlock()
		_ = front.Close()
		return
	}
	back, e := net.DialTimeout("tcp", b.rpcAddr, 2*time.Second)
	if e != nil {
		b.mu.Unlock()
		_ = front.Close()
		return
	}
	p := &dapPeer{front: front, back: back, pending: map[int]string{}}
	b.peer = p
	b.generation++
	b.mu.Unlock()
	defer func() {
		p.close()
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.peer == p {
			b.peer = nil
			b.generation++
		}
	}()
	go func() {
		defer p.close()
		r := bufio.NewReader(back)
		for {
			v, e := readDAP(r)
			if e != nil {
				return
			}
			b.mu.Lock()
			if b.peer != p {
				b.mu.Unlock()
				return
			}
			if str(v["type"]) == "event" {
				switch str(v["event"]) {
				case "continued":
					b.moving = true
					b.generation++
				case "stopped":
					// Delve's stopOnEntry event hardcodes threadId=1, even when
					// attaching to an existing breakpoint in another goroutine.
					// DAP thread IDs are Go goroutine IDs. Preserve the real stop.
					body := asObj(v["body"])
					if str(body["reason"]) == "entry" {
						if state, err := b.state(); err == nil {
							if gid := num(asObj(state["currentGoroutine"])["id"]); gid > 0 {
								body["threadId"] = gid
								body["preserveFocusHint"] = false
							}
						}
					}
					b.moving = false
					b.generation++
				case "exited", "terminated":
					b.moving = false
					b.generation++
				}
			}
			if str(v["type"]) == "response" {
				p.mu.Lock()
				command := p.pending[num(v["request_seq"])]
				delete(p.pending, num(v["request_seq"]))
				p.mu.Unlock()
				if command == "configurationDone" && truth(v["success"]) {
					p.ready = true
					b.lastError = ""
				}
				if !truth(v["success"]) && isExecution(command) {
					b.moving = false
					b.generation++
				}
			}
			b.mu.Unlock()
			if e = p.send(v); e != nil {
				return
			}
		}
	}()
	r := bufio.NewReader(front)
	for {
		v, e := readDAP(r)
		if e != nil {
			return
		}
		if str(v["type"]) != "request" {
			continue
		}
		command := str(v["command"])
		b.mu.Lock()
		message := ""
		if !editorOwner(b.owner) || b.peer != p {
			message = "Codex owns this session; hand over before attaching an editor"
		}
		if command == "launch" || command == "restart" || command == "terminate" {
			message = "This is a persistent attach session. End it using debug-handover stop or the inspector."
		}
		if command == "attach" {
			a := asObj(v["arguments"])
			if a == nil {
				a = obj{}
			}
			a["mode"] = "remote"
			a["stopOnEntry"] = true
			v["arguments"] = a
		}
		if command == "disconnect" {
			a := asObj(v["arguments"])
			if a == nil {
				a = obj{}
			}
			a["terminateDebuggee"] = false
			v["arguments"] = a
		}
		if message != "" {
			b.mu.Unlock()
			_ = p.send(obj{"seq": num(v["seq"]), "type": "response", "request_seq": v["seq"], "command": command, "success": false, "message": message})
			continue
		}
		if isExecution(command) {
			b.moving = true
			b.generation++
		}
		if strings.HasPrefix(command, "set") || command == "evaluate" {
			b.generation++
		}
		p.mu.Lock()
		p.pending[num(v["seq"])] = command
		p.mu.Unlock()
		_ = back.SetWriteDeadline(time.Now().Add(3 * time.Second))
		e = writeDAP(back, v)
		b.mu.Unlock()
		if e != nil {
			return
		}
	}
}
