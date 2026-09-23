package cli

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineProviderQuestionUsesSavedIdentity(t *testing.T) {
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	r := protocol.Recipient{Kind: "provider", ID: "model", Revision: 1}
	d := session.Discussion{Session: "0123456789", Threads: []session.CommentThread{{ID: "thread", Context: obj{"run": "original", "pauseEpoch": 2}, Messages: []session.CommentMessage{{ID: "q", Author: "human", Body: "same text"}}, Delivery: session.CommentDelivery{Question: "q", Status: "pending", Recipient: &r}}}}
	if err := session.CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	args := []string{"claim", d.Session, "thread", "--offline", "--question", "q", "--recipient-kind", "provider", "--recipient-id", "model"}
	claimed, err := commentCommand(args)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(claimed)
	var result struct {
		Delivery protocol.DeliveryEnvelope `json:"delivery"`
	}
	json.Unmarshal(data, &result)
	if result.Delivery.EvidenceRun != "original" || result.Delivery.Attempt == "" {
		t.Fatal(string(data))
	}
	if _, err := commentCommand(args); err == nil {
		t.Fatal("duplicate claim")
	}
	body := filepath.Join(t.TempDir(), "answer")
	os.WriteFile(body, []byte("saved answer"), 0600)
	reply := []string{"reply", d.Session, "thread", "--offline", "--question", "q", "--recipient-kind", "provider", "--recipient-id", "model", "--attempt", result.Delivery.Attempt, "--message-id", "answer", "--body-file", body}
	if _, err := commentCommand(reply); err != nil {
		t.Fatal(err)
	}
	if _, err := commentCommand(reply); err != nil {
		t.Fatal("idempotency", err)
	}
	got, _ := session.ReadDiscussion(d.Session)
	if got.Threads[0].Delivery.Status != "answered" {
		t.Fatal(got)
	}
	if _, err := session.Read(d.Session); err == nil {
		t.Fatal("offline command created runtime session")
	}
}

func TestHistoricalHTTPUsesSameQuestionReducerWithoutRuntime(t *testing.T) {
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	r := protocol.Recipient{Kind: "provider", ID: "model", Revision: 1}
	d := session.Discussion{Session: "0123456789", Threads: []session.CommentThread{{ID: "thread", Messages: []session.CommentMessage{{ID: "q", Author: "human", Body: "saved"}}, Delivery: session.CommentDelivery{Question: "q", Status: "pending", Recipient: &r}}}}
	if err := session.CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	handler := workspaceHandler("http://127.0.0.1:1234")
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://127.0.0.1:1234/api/comments?history="+d.Session, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	w := post(`{"action":"reply","thread":"thread","question":"q","messageId":"answer","body":"historical answer","recipient":{"kind":"provider","id":"model","revision":1}}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = post(`{"action":"ask","thread":"thread","body":"new","contextMode":"current"}`); w.Code != 409 {
		t.Fatal("current capture accepted offline")
	}
	if w = post(`{"action":"reply","thread":"thread","question":"q","messageId":"answer","body":"different","recipient":{"kind":"provider","id":"model","revision":1}}`); w.Code != 409 {
		t.Fatal("conflicting reply key")
	}
	if _, err := session.Read(d.Session); err == nil {
		t.Fatal("HTTP created target runtime")
	}
}
