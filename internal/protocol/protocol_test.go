package protocol

import (
	"strings"
	"testing"
)

func TestModelName_TopLevelOnly(t *testing.T) {
	// A tool schema with a "model" property comes before the real field.
	body := `{"tools":[{"name":"x","input_schema":{"properties":{"model":{"type":"string"}},"model":"decoy"}}],` +
		` "system":"he said \"model\": \"nope\"", "max_tokens":10, "model" : "claude-sonnet-4-6", "stream":true}`
	name, s, e, err := ModelName([]byte(body))
	if err != nil || name != "claude-sonnet-4-6" {
		t.Fatalf("got %q %v", name, err)
	}
	if body[s-1] != '"' || body[e] != '"' {
		t.Fatalf("range [%d,%d) does not sit inside quotes", s, e)
	}
}

// REQ-PASS-03: only the model value changes, every other byte stays.
func TestReplaceRange_OnlyModelBytes(t *testing.T) {
	body := `{"max_tokens": 1024,  "model":"sonnet","messages":[{"role":"user","content":"سلام"}]}`
	_, s, e, err := ModelName([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	got := string(ReplaceRange([]byte(body), s, e, "claude-sonnet-4-6"))
	if want := strings.Replace(body, `"sonnet"`, `"claude-sonnet-4-6"`, 1); got != want {
		t.Fatalf("\nwant %s\ngot  %s", want, got)
	}
}

func TestModelName_Errors(t *testing.T) {
	for _, b := range []string{``, `[]`, `{"model":5}`, `{"messages":[]}`, `{"model":"x`} {
		if _, _, _, err := ModelName([]byte(b)); err == nil {
			t.Errorf("%q: want error", b)
		}
	}
	if name, _, _, err := ModelName([]byte(`{"model":"a/b"}`)); err != nil || name != "a/b" {
		t.Errorf("escaped: %q %v", name, err)
	}
}

const stream = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1000,\"cache_read_input_tokens\":200,\"cache_creation_input_tokens\":50,\"output_tokens\":1}}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"" + "xxxxxxxxxxxxxxxx" + "\"}}\n\n" +
	"event: message_delta\r\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":500}}\r\n\r\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func TestUsageScanner_AnySplit(t *testing.T) {
	want := Usage{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 200, CacheWriteTokens: 50}
	for size := 1; size <= len(stream); size++ {
		var started Usage
		s := &UsageScanner{OnStart: func(u Usage) { started = u }}
		for i := 0; i < len(stream); i += size {
			s.Write([]byte(stream[i:min(i+size, len(stream))]))
		}
		if s.Usage != want || !s.SawStart || !s.SawStop || s.StopReason != "end_turn" {
			t.Fatalf("chunk %d: %+v start=%v stop=%v reason=%q", size, s.Usage, s.SawStart, s.SawStop, s.StopReason)
		}
		if started.InputTokens != 1000 || started.OutputTokens != 1 {
			t.Fatalf("chunk %d: OnStart got %+v", size, started)
		}
	}
}

// Content deltas must not be buffered: the scanner holds at most a line of
// an event it parses (REQ-STREAM-01).
func TestUsageScanner_DoesNotBufferContent(t *testing.T) {
	s := &UsageScanner{}
	s.Write([]byte("event: content_block_delta\ndata: {\"delta\":\"" + strings.Repeat("x", 4<<20)))
	if cap(s.line) > 64 {
		t.Fatalf("buffered %d bytes of a content delta", cap(s.line))
	}
	s.Write([]byte("\"}\n\n" + stream))
	if s.Usage.OutputTokens != 500 {
		t.Fatalf("lost sync after a huge delta: %+v", s.Usage)
	}
}

func TestMessageUsage(t *testing.T) {
	u, ok := MessageUsage([]byte(`{"id":"m","usage":{"input_tokens":12,"output_tokens":34}}`))
	if !ok || u.InputTokens != 12 || u.OutputTokens != 34 {
		t.Fatalf("%+v %v", u, ok)
	}
}
