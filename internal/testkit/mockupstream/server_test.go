package mockupstream

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qanat/internal/testkit/sse"
)

func TestReplaysChunksWithTiming(t *testing.T) {
	s := New(t)
	s.Enqueue("/v1/messages", NewScript(Every(30*time.Millisecond,
		"event: a\ndata: 1\n\n",
		"event: b\ndata: 2\n\n",
	)...))

	start := time.Now()
	resp, err := http.Post(s.URL()+"/v1/messages", "application/json", strings.NewReader(`{"model":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	rd := sse.NewReader(resp.Body)
	first, err := rd.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "a" {
		t.Fatalf("got %q", first.Name)
	}
	if el := time.Since(start); el < 25*time.Millisecond {
		t.Fatalf("first chunk arrived too early: %v", el)
	}
	if _, err := rd.Next(); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 55*time.Millisecond {
		t.Fatalf("second chunk arrived too early: %v", el)
	}

	req := s.Last()
	<-req.Done
	if string(req.Body) != `{"model":"x"}` || len(req.WrittenAt) != 2 {
		t.Fatalf("bad recording: body=%q writes=%d", req.Body, len(req.WrittenAt))
	}
}

func TestDetectsClientCancellation(t *testing.T) {
	s := New(t)
	sc := NewScript(Step{Data: []byte("event: a\ndata: 1\n\n")})
	sc.HoldOpen = true
	s.Enqueue("/v1/messages", sc)

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.URL()+"/v1/messages", strings.NewReader("{}"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sse.NewReader(resp.Body).Next(); err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()

	rec := s.Last()
	select {
	case <-rec.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not observe cancellation")
	}
	if !rec.ClientGone {
		t.Fatal("ClientGone not set")
	}
}

func TestAbortMidStream(t *testing.T) {
	s := New(t)
	sc := NewScript(Every(0, "event: a\ndata: 1\n\n", "event: b\ndata: 2\n\n")...)
	sc.AbortAfter = 0
	s.Enqueue("/v1/messages", sc)

	resp, err := http.Post(s.URL()+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	if err == nil {
		t.Fatal("expected a read error after abrupt close")
	}
}

func TestLoadFixture(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.jsonl")
	os.WriteFile(p, []byte(`{"type":"meta","status":200,"headers":{"anthropic-ratelimit-unified-5h-utilization":"0.4"}}
{"type":"chunk","at_ms":100,"data":"event: a\ndata: 1\n\n"}
{"type":"chunk","at_ms":150,"data":"event: b\ndata: 2\n\n"}
`), 0o600)

	sc, err := LoadFixture(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Steps) != 2 || sc.Steps[0].Delay != 100*time.Millisecond || sc.Steps[1].Delay != 50*time.Millisecond {
		t.Fatalf("bad timing: %+v", sc.Steps)
	}
	if sc.Headers["anthropic-ratelimit-unified-5h-utilization"] != "0.4" {
		t.Fatal("meta headers not loaded")
	}
	fast, _ := LoadFixture(p, 0)
	if fast.Steps[0].Delay != 0 {
		t.Fatal("speed 0 must remove delays")
	}
}
