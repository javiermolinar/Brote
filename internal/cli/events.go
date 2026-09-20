package cli

import (
	"bufio"
	"context"
	"debug-handover/internal/session"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// stream follows a broker stream once. Callers reconnect with the last cursor.
func stream(ctx context.Context, s session.Descriptor, cursor uint64, binding string, receive func(session.Event) error) error {
	if !strings.HasPrefix(s.HTTP, "http://127.0.0.1:") {
		return fmt.Errorf("expected loopback broker")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.HTTP+"/api/events?cursor="+strconv.FormatUint(cursor, 10)+"&binding="+url.QueryEscape(binding), nil)
	if err != nil {
		return err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("event stream %d: %s", resp.StatusCode, data)
	}
	scan := bufio.NewScanner(resp.Body)
	scan.Buffer(make([]byte, 4096), 1<<20)
	for scan.Scan() {
		line := scan.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if strings.Contains(line, `"reset":true`) {
			return fmt.Errorf("event stream 409: cursor expired; read state and reconnect")
		}
		var event session.Event
		if err := json.Unmarshal([]byte(line[6:]), &event); err != nil {
			return err
		}
		if err := receive(event); err != nil {
			return err
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	return io.EOF
}

var eventDone = errors.New("event received")

func eventsCommand(args []string, wait bool) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("session ID required")
	}
	f := flag.NewFlagSet("events", flag.ContinueOnError)
	cursor := f.Uint64("cursor", 0, "last event ID")
	binding := f.String("binding", "", "bound client ID")
	timeout := f.Duration("timeout", 20*time.Second, "bounded wait timeout")
	if err := f.Parse(args[1:]); err != nil {
		return nil, err
	}
	s, err := session.Read(args[0])
	if err != nil {
		return nil, err
	}
	if *binding == "" && s.Binding != nil {
		*binding = s.Binding.ID
	}
	ctx := context.Background()
	cancel := func() {}
	if wait {
		if *timeout <= 0 || *timeout > time.Minute {
			return nil, fmt.Errorf("timeout must be in (0,1m]")
		}
		ctx, cancel = context.WithTimeout(ctx, *timeout)
	}
	defer cancel()
	var result any
	for {
		err = stream(ctx, s, *cursor, *binding, func(event session.Event) error {
			*cursor = event.ID
			if !wait {
				return json.NewEncoder(os.Stdout).Encode(event)
			}
			if event.Kind == "control_returned" || event.Kind == "terminated" || event.Kind == "target_exited" || event.Kind == "binding_changed" {
				fresh, e := api(s, "GET", "/api/state?brief=1", nil)
				if e != nil && event.Kind != "terminated" {
					return e
				}
				if event.Kind == "control_returned" {
					currentBinding, _ := fresh["binding"].(map[string]any)
					notification, _ := fresh["notification"].(map[string]any)
					if str(fresh["owner"]) != "agent" || event.Binding == nil || str(currentBinding["id"]) != event.Binding.ID || currentBinding["revision"] != float64(event.Binding.Revision) || str(notification["id"]) != strconv.FormatUint(event.ID, 10) {
						return nil
					}
				}
				result = obj{"event": event, "cursor": *cursor, "state": fresh}
				return eventDone
			}
			return nil
		})
		if errors.Is(err, eventDone) {
			return result, nil
		}
		if ctx.Err() != nil {
			return obj{"status": "timeout", "cursor": *cursor}, nil
		}
		// A changed binding or expired cursor requires an explicit state reconciliation.
		if strings.Contains(err.Error(), "event stream 4") {
			return nil, err
		}
		s, err = session.Read(s.ID)
		if err != nil {
			return nil, err
		}
		if s.Stopped {
			if !wait {
				return nil, nil
			}
			return obj{"status": "terminated", "cursor": *cursor}, nil
		}
		select {
		case <-ctx.Done():
			return obj{"status": "timeout", "cursor": *cursor}, nil
		case <-time.After(time.Second):
		}
	}
}
