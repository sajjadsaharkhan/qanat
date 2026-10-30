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

	"qanat/internal/proxy"
)

// §7: an ag/ request reaches Cloud Code as streamGenerateContent with the
// account's token, project and the IDE's UA; the client gets Anthropic SSE.
// A 429 account cools down for Google's retryDelay and the next one answers.
func TestForward_Antigravity(t *testing.T) {
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"code":429,"message":"exhausted","status":"RESOURCE_EXHAUSTED","details":[{"retryDelay":"42s"}]}}`)
	}))
	defer busy.Close()
	var got struct {
		path, auth, ua string
		body           map[string]any
	}
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth, got.ua = r.URL.String(), r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		json.NewDecoder(r.Body).Decode(&got.body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"response":{"candidates":[{"content":{"parts":[{"text":"Hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":2}}}`+"\n\n")
	}))
	defer ok.Close()
	s := &Snapshot{Gen: 1, accounts: map[string][]*Account{"antigravity": {
		{ID: "a1", Provider: "antigravity", State: "healthy", AuthKind: "oauth", Token: "t1", BaseURL: busy.URL, Origin: busy.URL, Proxies: []*proxy.Spec{nil}},
		{ID: "a2", Provider: "antigravity", State: "healthy", AuthKind: "oauth", Token: "t2", Project: "proj-2", BaseURL: ok.URL, Origin: ok.URL, Proxies: []*proxy.Spec{nil}},
	}}, sticky: map[string]string{}}
	g := newTestGateway()
	g.health = proxy.NewHealth()
	g.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	g.sticky["u1|antigravity"] = "a1"
	body := []byte(`{"model":"gemini-3.8-flash-high","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	rt := &route{caller: &caller{snap: s, user: &User{ID: "u1"}, key: &Key{}, plan: &Plan{}}, name: "ag/gemini-3.8-flash-high", body: body,
		mStart: 10, mEnd: 31, members: []Member{{Provider: "antigravity", Model: "gemini-3.8-flash-high"}}}

	w := httptest.NewRecorder()
	g.forward(w, httptest.NewRequest(http.MethodPost, "/v1/messages", nil), rt, forwardOpts{reqID: "r", start: time.Now(), remaining: -1})
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d %v: %s", w.Code, w.Header(), w.Body)
	}
	for _, want := range []string{"event: message_start", `"text":"Hello"`, `"stop_reason":"end_turn"`, `"output_tokens":2`, "event: message_stop"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %s in %s", want, w.Body)
		}
	}
	if got.path != "/v1internal:streamGenerateContent?alt=sse" || got.auth != "Bearer t2" || !strings.HasPrefix(got.ua, "antigravity/") {
		t.Fatalf("upstream request %+v", got)
	}
	if got.body["project"] != "proj-2" || got.body["model"] != "gemini-3.8-flash-high" {
		t.Fatalf("envelope %v", got.body)
	}
	if left := time.Until(g.cooldown["a1"]); left < 40*time.Second || left > 43*time.Second {
		t.Fatalf("a1 cooldown %v, want Google's 42s", left)
	}

	// A client that doesn't stream gets one Messages JSON.
	rt.body = []byte(`{"model":"gemini-3.8-flash-high","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	w = httptest.NewRecorder()
	g.forward(w, httptest.NewRequest(http.MethodPost, "/v1/messages", nil), rt, forwardOpts{reqID: "r2", start: time.Now(), remaining: -1})
	var msg struct {
		Content []struct{ Text string } `json:"content"`
		Usage   struct {
			InputTokens int64 `json:"input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &msg); err != nil || len(msg.Content) != 1 || msg.Content[0].Text != "Hello" || msg.Usage.InputTokens != 9 {
		t.Fatalf("message %d %s (%v)", w.Code, w.Body, err)
	}
}
