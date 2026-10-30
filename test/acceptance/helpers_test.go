package acceptance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"qanat/internal/testkit/mockupstream"
)

// env is a ready-to-use scenario: one user with a generous Plan and a key,
// one Claude account backed by a mock upstream, and the default mapping.
type env struct {
	d    Driver
	up   *mockupstream.Server
	user string
	key  string
}

const msgPath = "/v1/messages"

func setup(t *testing.T) *env {
	t.Helper()
	d := needDriver(t)
	ctx := context.Background()
	must(t, d.Reset(ctx))

	up := mockupstream.New(t)
	_, err := d.AddUpstreamAccount(ctx, "claude", up.URL())
	must(t, err)
	must(t, d.SetMapping(ctx, []MappingRule{
		{Pattern: "sonnet", Provider: "claude", Model: "claude-sonnet-4-6"},
		{Pattern: "claude-*", Provider: "claude"},
	}))
	for _, m := range []string{"claude-sonnet-4-6", "claude-opus-4-7"} {
		must(t, d.SetPrice(ctx, m, Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}))
	}
	user, err := d.CreateUser(ctx, "dev@example.com")
	must(t, err)
	must(t, d.SetPlan(ctx, user, Plan{DailyUSD: 100}))
	key, err := d.CreateKey(ctx, user)
	must(t, err)
	return &env{d: d, up: up, user: user, key: key}
}

// requestTimeout bounds every request so a hanging or buffering gateway
// fails the test quickly instead of stalling the whole suite.
const requestTimeout = 5 * time.Second

// post sends a Messages request the way Claude Code does.
func (e *env) post(t *testing.T, ctx context.Context, body string, hdr map[string]string) *http.Response {
	t.Helper()
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		t.Cleanup(cancel)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.d.GatewayURL()+msgPath, strings.NewReader(body))
	must(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// anthropicError decodes and checks the Anthropic error envelope (REQ-ERR-01).
func anthropicError(t *testing.T, resp *http.Response) (errType, message string) {
	t.Helper()
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &body); err != nil || body.Type != "error" || body.Error.Type == "" {
		t.Fatalf("REQ-ERR-01: response is not an Anthropic error envelope: %s", raw)
	}
	if body.Error.Message == "" {
		t.Fatalf("REQ-ERR-01: error message must be human-readable, got empty: %s", raw)
	}
	return body.Error.Type, body.Error.Message
}

// Canned Anthropic stream pieces.
const (
	evMessageStart = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_01\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-6\",\"content\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":1}}}\n\n"
	evBlockStart   = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"
	evDelta        = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"
	evBlockStop    = "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
	evMessageDelta = "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":500}}\n\n"
	evMessageStop  = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
)

func fullStream(gap time.Duration) mockupstream.Script {
	return mockupstream.NewScript(mockupstream.Every(gap,
		evMessageStart, evBlockStart, evDelta, evDelta, evBlockStop, evMessageDelta, evMessageStop)...)
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// eventually polls cond until it returns true or the deadline passes.
func eventually(t *testing.T, within time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}
