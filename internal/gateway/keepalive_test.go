package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"qanat/internal/proxy"
)

// REQ-STREAM-07: a translated stream that goes quiet gets pings between
// events; Claude's passthrough stream is never touched.
func TestStream_KeepAlivePings(t *testing.T) {
	defer func(d time.Duration) { pingEvery = d }(pingEvery)
	pingEvery = 30 * time.Millisecond

	slow := func(first, rest string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, first)
			w.(http.Flusher).Flush()
			time.Sleep(200 * time.Millisecond) // long reasoning
			io.WriteString(w, rest)
		}))
	}
	run := func(provider string, up *httptest.Server) string {
		s := &Snapshot{Gen: 1, accounts: map[string][]*Account{provider: {
			{ID: "a1", Provider: provider, State: "healthy", Token: "t", BaseURL: up.URL, Origin: up.URL, Proxies: []*proxy.Spec{nil}},
		}}, sticky: map[string]string{}}
		g := newTestGateway()
		g.health = proxy.NewHealth()
		g.log = slog.New(slog.NewTextHandler(io.Discard, nil))
		rt := &route{caller: &caller{snap: s, user: &User{ID: "u1"}, key: &Key{}, plan: &Plan{}}, name: "m",
			body: []byte(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`), members: []Member{{Provider: provider, Model: "m"}}}
		w := httptest.NewRecorder()
		g.forward(w, httptest.NewRequest(http.MethodPost, "/v1/messages", nil), rt, forwardOpts{reqID: "r", start: time.Now(), remaining: -1})
		return w.Body.String()
	}

	cx := slow("data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	defer cx.Close()
	out := run("codex", cx)
	pings := strings.Count(out, "event: ping")
	start, ping, text, stop := strings.Index(out, "message_start"), strings.Index(out, "event: ping"), strings.Index(out, `"text":"hi"`), strings.Index(out, "event: message_stop")
	if pings < 2 || !(start < ping && ping < text) || strings.Contains(out[stop:], "ping") {
		t.Fatalf("want pings between message_start and the text, none after message_stop (got %d):\n%s", pings, out)
	}

	cc := slow("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{}}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	defer cc.Close()
	if out := run("claude", cc); strings.Contains(out, "ping") {
		t.Fatalf("Claude passthrough must stay byte-for-byte:\n%s", out)
	}
}
