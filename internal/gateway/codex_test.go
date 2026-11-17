package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"qanat/internal/protocol"
	"qanat/internal/proxy"
)

// A cx/ request reaches the Codex backend as a Responses call with the
// account's binding, and the client gets Anthropic SSE back.
func TestForward_Codex(t *testing.T) {
	var got struct {
		path, auth, account, session string
		body                         map[string]any
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth, got.account, got.session = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-ID"), r.Header.Get("session_id")
		json.NewDecoder(r.Body).Decode(&got.body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n"+
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n")
	}))
	defer up.Close()
	s := &Snapshot{Gen: 1, accounts: map[string][]*Account{"codex": {
		{ID: "cx1", Provider: "codex", State: "healthy", AuthKind: "oauth", Token: "tok", ChatGPTAccount: "ws-1", BaseURL: up.URL, Origin: up.URL, Proxies: []*proxy.Spec{nil}},
	}}, sticky: map[string]string{}}
	g := newTestGateway()
	g.health = proxy.NewHealth()
	g.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	body := []byte(`{"model":"gpt-6-sol-high","stream":true,"max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`)
	_, ms, me, _ := protocol.ModelName(body)
	rt := &route{caller: &caller{snap: s, user: &User{ID: "u1"}, key: &Key{}, plan: &Plan{}}, name: "cx/gpt-6-sol-high", body: body,
		mStart: ms, mEnd: me, members: []Member{{Provider: "codex", Model: "gpt-6-sol-high"}}}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("X-Claude-Code-Session-Id", "sess-1")
	g.forward(w, r, rt, forwardOpts{reqID: "r", start: time.Now(), remaining: -1})

	if w.Code != 200 || !strings.Contains(w.Body.String(), `"text":"hi"`) || !strings.Contains(w.Body.String(), "message_stop") {
		t.Fatalf("status %d:\n%s", w.Code, w.Body)
	}
	if got.path != "/responses" || got.auth != "Bearer tok" || got.account != "ws-1" || got.session != "sess-1" {
		t.Fatalf("upstream request: %+v", got)
	}
	if got.body["model"] != "gpt-6-sol" || got.body["stream"] != true || got.body["store"] != false {
		t.Fatalf("upstream body: %v", got.body)
	}
	if n := counterValue(upstreamResponses.WithLabelValues("codex", "cx1", "gpt-6-sol-high", "200")); n != 1 {
		t.Fatalf("codex response counted %v times", n)
	}
}
