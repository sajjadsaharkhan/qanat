package codex

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"qanat/internal/protocol"
)

func translate(t *testing.T, body, model string) (responsesRequest, *Request) {
	t.Helper()
	r, err := Translate([]byte(body), Options{Model: model, Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	var out responsesRequest
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatal(err)
	}
	return out, r
}

func TestTranslate_ConversationToolsAndReasoning(t *testing.T) {
	body := `{"model":"gpt-6-sol","max_tokens":32000,"temperature":1,"stream":true,
	 "system":[{"type":"text","text":"You are Claude Code."},{"type":"text","text":"Be brief."}],
	 "tools":[{"name":"Read","description":"read a file","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search"}],
	 "tool_choice":{"type":"tool","name":"Read"},
	 "messages":[
	  {"role":"user","content":"read a.go"},
	  {"role":"assistant","content":[
	    {"type":"thinking","thinking":"plan","signature":"codex:ENC1"},
	    {"type":"thinking","thinking":"from claude","signature":"EqQBCkYI"},
	    {"type":"text","text":"Reading."},
	    {"type":"tool_use","id":"call_1","name":"Read","input":{"path":"a.go"}}]},
	  {"role":"user","content":[
	    {"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"package a"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAA"}}]},
	    {"type":"text","text":"now explain"}]}]}`
	r, tr := translate(t, body, "gpt-6-sol-high")

	if r.Model != "gpt-6-sol" || r.Reasoning == nil || r.Reasoning.Effort != "high" || r.Reasoning.Summary != "auto" {
		t.Fatalf("model/effort from the name suffix: %s %+v", r.Model, r.Reasoning)
	}
	if r.Instructions != "You are Claude Code.\n\nBe brief." || !r.Stream || r.Store || r.PromptCacheKey != "s1" {
		t.Fatalf("instructions/stream/store/cache key: %+v", r)
	}
	if len(r.Include) != 1 || r.Include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %v", r.Include)
	}
	if !tr.Stream || len(r.Tools) != 1 || r.Tools[0].Name != "Read" || r.Tools[0].Parameters["properties"] == nil {
		t.Fatalf("server tools dropped, object schema gets properties: %+v", r.Tools)
	}
	if tc, _ := r.ToolChoice.(map[string]any); tc["type"] != "function" || tc["name"] != "Read" {
		t.Fatalf("tool_choice = %v", r.ToolChoice)
	}
	for _, banned := range []string{"max_output_tokens", "max_tokens", "temperature", "parallel_tool_calls"} {
		if strings.Contains(string(tr.Body), `"`+banned+`"`) {
			t.Fatalf("the backend rejects %s: %s", banned, tr.Body)
		}
	}

	types := []string{}
	for _, it := range r.Input {
		types = append(types, it.Type+":"+it.Role)
	}
	want := "message:user reasoning: message:assistant function_call: function_call_output: message:user message:user"
	if got := strings.Join(types, " "); got != want {
		t.Fatalf("input items\n got %s\nwant %s", got, want)
	}
	rs := r.Input[1]
	if rs.EncryptedContent != "ENC1" || rs.Summary == nil || (*rs.Summary)[0].Text != "plan" {
		t.Fatalf("only the codex-signed thinking goes back, as its reasoning item: %+v", rs)
	}
	fc := r.Input[3]
	if fc.CallID != "call_1" || fc.Name != "Read" || fc.Arguments != `{"path":"a.go"}` {
		t.Fatalf("function_call = %+v", fc)
	}
	if out := r.Input[4]; out.CallID != "call_1" || out.Output == nil || *out.Output != "package a" {
		t.Fatalf("function_call_output = %+v", out)
	}
	if img := r.Input[5].Content[0]; img.Type != "input_image" || img.ImageURL != "data:image/png;base64,AAA" {
		t.Fatalf("result image follows as user input: %+v", img)
	}
}

func TestTranslate_EffortFallbacks(t *testing.T) {
	cases := map[string]string{
		`{"messages":[]}`: "medium",
		`{"output_config":{"effort":"max"},"messages":[]}`:                    "xhigh",
		`{"output_config":{"effort":"low"},"messages":[]}`:                    "low",
		`{"thinking":{"type":"enabled","budget_tokens":32000},"messages":[]}`: "high",
		`{"thinking":{"type":"disabled"},"messages":[]}`:                      "low",
	}
	for body, want := range cases {
		r, _ := translate(t, body, "gpt-6-sol")
		if r.Reasoning.Effort != want {
			t.Errorf("%s: effort %q, want %q", body, r.Reasoning.Effort, want)
		}
		if len(r.Input) != 1 || r.Instructions == "" {
			t.Errorf("%s: empty input and instructions get stand-ins: %+v", body, r)
		}
	}
	if r, _ := translate(t, `{"messages":[]}`, "gpt-6-sol-none"); r.Include != nil || r.Reasoning.Summary != "" {
		t.Errorf("effort none asks for no reasoning output: %+v", r)
	}
}

// A Responses stream as the Codex backend sends it: reasoning, text, then a
// tool call whose arguments arrive in fragments.
const upstream = `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

data: {"type":"response.output_item.added","item":{"id":"rs_1","type":"reasoning"}}

data: {"type":"response.reasoning_summary_part.added","item_id":"rs_1","summary_index":0}

data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_1","summary_index":0,"delta":"Think"}

data: {"type":"response.reasoning_summary_part.added","item_id":"rs_1","summary_index":1}

data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_1","summary_index":1,"delta":"more"}

data: {"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","encrypted_content":"ENC"}}

data: {"type":"response.output_item.added","item":{"id":"msg_1","type":"message"}}

data: {"type":"response.output_text.delta","item_id":"msg_1","delta":"Hel"}

data: {"type":"response.output_text.delta","item_id":"msg_1","delta":"lo"}

data: {"type":"response.output_item.done","item":{"id":"msg_1","type":"message"}}

data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_9","name":"Read","arguments":""}}

data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\":"}

data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"\"a.go\"}"}

data: {"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_9","name":"Read","arguments":"{\"path\":\"a.go\"}"}}

data: {"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":800},"output_tokens":50}}}

`

func TestStream_TranslatesEventByEvent(t *testing.T) {
	st := NewStream(io.NopCloser(strings.NewReader(upstream)), "gpt-6-sol", map[string]string{"Read": "Read"})
	out, err := io.ReadAll(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`"type":"message_start"`, `"model":"gpt-6-sol"`,
		`"thinking":"Think"`, `"thinking":"\n\n"`, `"signature":"codex:ENC"`,
		`"text":"Hel"`, `"text":"lo"`,
		`"id":"call_9","input":{},"name":"Read","type":"tool_use"`,
		`"partial_json":"{\"path\":"`, `"partial_json":"\"a.go\"}"`,
		`"stop_reason":"tool_use"`,
		`"usage":{"input_tokens":200,"cache_read_input_tokens":800,"output_tokens":50}`,
		`"type":"message_stop"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in\n%s", want, s)
		}
	}
	if strings.Count(s, `"partial_json"`) != 2 {
		t.Fatalf("arguments pass through as fragments, never re-sent whole:\n%s", s)
	}
	// Blocks are thinking 0, text 1, tool 2, each opened and closed once.
	if strings.Count(s, "event: content_block_start") != 3 || strings.Count(s, "event: content_block_stop") != 3 ||
		!strings.Contains(s, `{"index":2,"type":"content_block_stop"}`) {
		t.Fatalf("block indexes:\n%s", s)
	}

	msg, err := protocol.Collect(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Content []struct {
			Type, Text, Thinking, Signature string
			Input                           map[string]string
		}
		StopReason string `json:"stop_reason"`
	}
	json.Unmarshal(msg, &m)
	if len(m.Content) != 3 || m.Content[1].Text != "Hello" || m.Content[2].Input["path"] != "a.go" || m.StopReason != "tool_use" {
		t.Fatalf("collected message: %s", msg)
	}
}

func TestStream_IncompleteAndFailed(t *testing.T) {
	inc := `data: {"type":"response.output_text.delta","delta":"x"}
data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":5,"output_tokens":9}}}
`
	out, _ := io.ReadAll(NewStream(io.NopCloser(strings.NewReader(inc)), "m", nil))
	if !strings.Contains(string(out), `"stop_reason":"max_tokens"`) {
		t.Fatalf("incomplete → max_tokens:\n%s", out)
	}

	failed := `data: {"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"try later"}}}
`
	out, _ = io.ReadAll(NewStream(io.NopCloser(strings.NewReader(failed)), "m", nil))
	s := string(out)
	if !strings.Contains(s, `"type":"overloaded_error"`) || !strings.Contains(s, "Codex: try later") || strings.Contains(s, "message_stop") {
		t.Fatalf("failure → error event, no message_stop:\n%s", s)
	}
	if _, err := protocol.Collect(strings.NewReader(s)); err == nil {
		t.Fatal("a failed stream must not collect into a message")
	}

	out, _ = io.ReadAll(NewStream(io.NopCloser(strings.NewReader("")), "m", nil))
	if !strings.Contains(string(out), `"type":"error"`) {
		t.Fatalf("an empty upstream is an error:\n%s", out)
	}
}

func TestErrors(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	limit := []byte(`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","resets_in_seconds":3600}}`)
	if b := string(ErrorBody(429, limit)); !strings.Contains(b, `"rate_limit_error"`) || !strings.Contains(b, "Codex: The usage limit has been reached") {
		t.Fatalf("ErrorBody = %s", b)
	}
	if d := RetryAfter(limit, now); d != time.Hour {
		t.Fatalf("RetryAfter = %v", d)
	}
	if d := RetryAfter([]byte(`{"error":{"resets_at":1000600}}`), now); d != 10*time.Minute {
		t.Fatalf("RetryAfter from resets_at = %v", d)
	}
	if b := string(ErrorBody(400, []byte(`{"detail":"Unsupported model"}`))); !strings.Contains(b, `"invalid_request_error"`) || !strings.Contains(b, "Codex: Unsupported model") {
		t.Fatalf("detail body = %s", b)
	}
}
