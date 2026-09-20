package dap

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

func TestClientCorrelatesOutOfOrderResponsesAndEvents(t *testing.T) {
	front, back := net.Pipe()
	defer back.Close()
	c := &Client{conn: front, pending: map[int]chan map[string]any{}, done: make(chan struct{}), Events: make(chan map[string]any, 8)}
	defer c.Close()
	go c.read()
	results := make(chan string, 2)
	for _, command := range []string{"threads", "stackTrace"} {
		go func(command string) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			v, e := c.Request(ctx, command, nil)
			if e != nil {
				results <- e.Error()
				return
			}
			results <- v["result"].(string)
		}(command)
	}
	r := bufio.NewReader(back)
	first, e := Read(r)
	if e != nil {
		t.Fatal(e)
	}
	second, e := Read(r)
	if e != nil {
		t.Fatal(e)
	}
	Write(back, map[string]any{"type": "event", "event": "stopped"})
	for _, v := range []map[string]any{second, first} {
		Write(back, map[string]any{"type": "response", "request_seq": v["seq"], "success": true, "body": map[string]any{"result": v["command"]}})
	}
	seen := map[string]bool{<-results: true}
	seen[<-results] = true
	if !seen["threads"] || !seen["stackTrace"] {
		t.Fatal(seen)
	}
	if (<-c.Events)["event"] != "stopped" {
		t.Fatal("event lost")
	}
}
