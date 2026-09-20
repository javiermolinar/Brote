package broker

import (
	"debug-handover/internal/session"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDurableHandbackAndDeliveryTransitions(t *testing.T) {
	b := &broker{s: session.Descriptor{ID: "test", Dir: t.TempDir(), Binding: &session.Binding{ID: "client", Revision: 1, Name: "Pi"}}, owner: "agent"}
	if err := b.emit("control_returned", "inspect total"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(b.s.Dir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved session.Descriptor
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Events) != 1 || saved.Events[0].Note != "inspect total" || saved.Notification.Status != "pending" {
		t.Fatalf("%+v", saved)
	}
	set := func(binding, status string) error {
		_, err := b.eventStatus(obj{"event": "1", "binding": binding, "revision": 1, "status": status})
		return err
	}
	if err = set("other", "sending"); err == nil {
		t.Fatal("wrong binding accepted")
	}
	if err = set("client", "sending"); err != nil {
		t.Fatal(err)
	}
	if err = set("client", "sending"); err == nil {
		t.Fatal("duplicate claim accepted")
	}
	if err = set("client", "queued"); err != nil {
		t.Fatal(err)
	}
	if err = set("client", "acknowledged"); err != nil {
		t.Fatal(err)
	}
	b.owner = "browser"
	if err = set("client", "acknowledged"); err == nil {
		t.Fatal("stale ownership accepted")
	}
}
func TestBoundedJournal(t *testing.T) {
	b := &broker{s: session.Descriptor{Dir: t.TempDir()}, owner: "agent"}
	for i := 0; i < 260; i++ {
		if err := b.emit("ownership_changed", ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.s.Events) != 256 || b.s.Events[0].ID != 5 || b.s.Cursor != 260 {
		t.Fatal("journal bound")
	}
}
