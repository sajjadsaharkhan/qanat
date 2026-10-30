package acceptance

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"qanat/internal/testkit/latency"
	"qanat/internal/testkit/mockupstream"
)

// A body with unusual whitespace, key order and unicode: any re-encoding
// by the gateway would change at least one byte.
const quirkyBody = `{"max_tokens": 1024,  "model":"claude-sonnet-4-6","stream":true,` +
	`"messages":[{"role":"user","content":"سلام é <tag> & ok"}],` +
	`"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}]}`

// REQ-PASS-01, REQ-PASS-02, REQ-PASS-05
func TestPassthrough_RequestBodyAndHeadersUnchanged(t *testing.T) {
	e := setup(t)
	e.up.Enqueue(msgPath, fullStream(0))

	resp := e.post(t, context.Background(), quirkyBody, map[string]string{
		"anthropic-beta": "prompt-caching-2024-07-31,interleaved-thinking-2025-05-14",
	})
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	got := e.up.Last()
	if got == nil {
		t.Fatal("upstream never called")
	}
	if !bytes.Equal(got.Body, []byte(quirkyBody)) {
		t.Fatalf("REQ-PASS-02: body changed\nsent: %s\ngot:  %s", quirkyBody, got.Body)
	}
	if got.Header.Get("anthropic-beta") != "prompt-caching-2024-07-31,interleaved-thinking-2025-05-14" {
		t.Fatalf("REQ-PASS-05: anthropic-beta changed: %q", got.Header.Get("anthropic-beta"))
	}
	if got.Header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("REQ-PASS-05: anthropic-version changed: %q", got.Header.Get("anthropic-version"))
	}
	// The user's Qanat key must never reach the provider.
	for _, h := range []string{"Authorization", "X-Api-Key"} {
		if strings.Contains(got.Header.Get(h), e.key) {
			t.Fatalf("user key leaked upstream in %s", h)
		}
	}
}

// REQ-PASS-03
func TestPassthrough_ModelRewriteOnlyTouchesModelField(t *testing.T) {
	e := setup(t)
	e.up.Enqueue(msgPath, fullStream(0))

	sent := strings.Replace(quirkyBody, `"model":"claude-sonnet-4-6"`, `"model":"sonnet"`, 1)
	resp := e.post(t, context.Background(), sent, nil)
	io.Copy(io.Discard, resp.Body)

	want := strings.Replace(sent, `"model":"sonnet"`, `"model":"claude-sonnet-4-6"`, 1)
	if got := string(e.up.Last().Body); got != want {
		t.Fatalf("REQ-PASS-03: only the model value may change\nwant: %s\ngot:  %s", want, got)
	}
}

// REQ-PASS-04, REQ-PERF-01
func TestPassthrough_ResponseBytesUnchanged(t *testing.T) {
	e := setup(t)
	sc := fullStream(0)
	e.up.Enqueue(msgPath, sc)

	resp := e.post(t, context.Background(), quirkyBody, nil)
	got, _ := io.ReadAll(resp.Body)

	var want bytes.Buffer
	for _, st := range sc.Steps {
		want.Write(st.Data)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("REQ-PASS-04: response bytes changed\nwant: %q\ngot:  %q", want.Bytes(), got)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Fatalf("REQ-PERF-01: Cache-Control = %q", cc)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("REQ-PERF-01: X-Accel-Buffering = %q", resp.Header.Get("X-Accel-Buffering"))
	}
}

// REQ-STREAM-01 (structural): every chunk reaches the client before the
// upstream writes the next one. A buffering gateway fails this regardless of
// machine speed, so it is safe to run everywhere.
func TestStreaming_NoBuffering(t *testing.T) {
	e := setup(t)
	e.up.Enqueue(msgPath, fullStream(40*time.Millisecond))

	resp := e.post(t, context.Background(), quirkyBody, nil)
	arrivals, err := latency.Read(resp.Body)
	must(t, err)

	rec := e.up.Last()
	<-rec.Done
	for i := 0; i+1 < len(rec.WrittenAt); i++ {
		if i >= len(arrivals) {
			t.Fatalf("missing event %d", i)
		}
		if !arrivals[i].At.Before(rec.WrittenAt[i+1]) {
			t.Fatalf("REQ-STREAM-01: event %d arrived after upstream wrote chunk %d (buffered)", i, i+1)
		}
	}
}

// REQ-PERF-03
func TestStreaming_ClientDisconnectCancelsUpstream(t *testing.T) {
	e := setup(t)
	sc := mockupstream.NewScript(mockupstream.Step{Data: []byte(evMessageStart)})
	sc.HoldOpen = true
	e.up.Enqueue(msgPath, sc)

	ctx, cancel := context.WithCancel(context.Background())
	resp := e.post(t, ctx, quirkyBody, nil)
	buf := make([]byte, 1)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()

	rec := e.up.Last()
	select {
	case <-rec.Done:
	case <-time.After(time.Second):
		t.Fatal("REQ-PERF-03: upstream request still open 1s after client disconnect")
	}
	if !rec.ClientGone {
		t.Fatal("REQ-PERF-03: upstream did not observe cancellation")
	}
}

// REQ-POOL-06: after the first byte, an upstream failure becomes an
// Anthropic error event in the stream, not a silent truncation.
func TestStreaming_UpstreamDiesMidStreamYieldsErrorEvent(t *testing.T) {
	e := setup(t)
	sc := mockupstream.NewScript(mockupstream.Every(0, evMessageStart, evBlockStart, evDelta)...)
	sc.AbortAfter = 2
	e.up.Enqueue(msgPath, sc)

	resp := e.post(t, context.Background(), quirkyBody, nil)
	arrivals, _ := latency.Read(resp.Body)
	if len(arrivals) == 0 || arrivals[len(arrivals)-1].Event.Name != "error" {
		t.Fatalf("REQ-POOL-06: want final event \"error\", got %d events", len(arrivals))
	}
}

// REQ-POOL-06, REQ-POOL-04: before the first byte, a 429 from one account
// fails over to the next account transparently.
func TestRouting_FailoverBeforeFirstByte(t *testing.T) {
	e := setup(t)
	second := mockupstream.New(t)
	_, err := e.d.AddUpstreamAccount(context.Background(), "claude", second.URL())
	must(t, err)

	e.up.Enqueue(msgPath, mockupstream.JSONResponse(429,
		`{"type":"error","error":{"type":"rate_limit_error","message":"account limit"}}`))
	second.Enqueue(msgPath, fullStream(0))

	resp := e.post(t, context.Background(), quirkyBody, nil)
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want failover to second account, got status %d", resp.StatusCode)
	}
	if second.Last() == nil {
		t.Fatal("second account never called")
	}
}
