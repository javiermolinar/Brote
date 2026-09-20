package dap

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// Client multiplexes requests over one backend connection. Events are delivered
// independently of responses so an execution request never blocks a pause.
type Client struct {
	conn    net.Conn
	write   sync.Mutex
	mu      sync.Mutex
	seq     int
	pending map[int]chan map[string]any
	done    chan struct{}
	once    sync.Once
	Events  chan map[string]any
}

func Dial(address string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, pending: map[int]chan map[string]any{}, done: make(chan struct{}), Events: make(chan map[string]any, 256)}
	go c.read()
	return c, nil
}
func (c *Client) Close() { c.once.Do(func() { close(c.done); c.conn.Close() }) }
func (c *Client) read() {
	defer c.Close()
	defer close(c.Events)
	r := bufio.NewReader(c.conn)
	for {
		v, e := Read(r)
		if e != nil {
			return
		}
		if v["type"] == "response" {
			seq, _ := v["request_seq"].(float64)
			c.mu.Lock()
			ch := c.pending[int(seq)]
			delete(c.pending, int(seq))
			c.mu.Unlock()
			if ch != nil {
				ch <- v
			}
		} else if v["type"] == "event" {
			select {
			case c.Events <- v:
			case <-c.done:
				return
			}
		}
	}
}
func (c *Client) Request(ctx context.Context, command string, args map[string]any) (map[string]any, error) {
	c.mu.Lock()
	c.seq++
	seq := c.seq
	ch := make(chan map[string]any, 1)
	c.pending[seq] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, seq); c.mu.Unlock() }()
	c.write.Lock()
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := Write(c.conn, map[string]any{"seq": seq, "type": "request", "command": command, "arguments": args})
	c.write.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case v := <-ch:
		if v["success"] != true {
			return nil, fmt.Errorf("DAP %s: %v", command, v["message"])
		}
		body, _ := v["body"].(map[string]any)
		if body == nil {
			body = map[string]any{}
		}
		return body, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("DAP connection closed")
	}
}
