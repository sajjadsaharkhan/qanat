package antigravity

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func translate(t *testing.T, body string, model string) envelope {
	t.Helper()
	r, err := Translate([]byte(body), Options{Model: model, Project: "proj-1", Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	if err := json.Unmarshal(r.Body, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestUpstream(t *testing.T) {
	cases := map[string][2]string{
		"gemini-3.8-flash-high":    {"gemini-3.8-flash-high", "high"},
		"gemini-3.8-flash":         {"gemini-3.8-flash-medium", "medium"},
		"gemini-3.7-flash-low":     {"gemini-3.7-flash-tiered", "low"},
		"gemini-3.8-flash-tiered":  {"gemini-3.8-flash-tiered", ""},
		"claude-opus-4-6-thinking": {"claude-opus-4-6-thinking", ""},
	}
	for in, want := range cases {
		if id, l := Upstream(in); id != want[0] || l != want[1] {
			t.Errorf("%s: %s %s", in, id, l)
		}
	}
}

// The envelope matches the IDE's: project, model, no requestType, an
// agent/… request ID, the system prompt, and the name's thinking level.
func TestTranslate_Envelope(t *testing.T) {
	r, err := Translate([]byte(`{"model":"x","max_tokens":1000,"stream":true,"system":[{"type":"text","text":"be brief","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":"hi"}]}`), Options{Model: "gemini-3.8-flash-high", Project: "proj-1", Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stream || strings.Contains(string(r.Body), "requestType") {
		t.Fatalf("stream %v, body %s", r.Stream, r.Body)
	}
	var e envelope
	json.Unmarshal(r.Body, &e)
	if e.Project != "proj-1" || e.Model != "gemini-3.8-flash-high" || e.UserAgent != "antigravity" || !strings.HasPrefix(e.RequestID, "agent/") {
		t.Fatalf("envelope %+v", e)
	}
	g := e.Request
	if *g.SystemInstruction.Parts[0].Text != "be brief" || len(g.Contents) != 1 || *g.Contents[0].Parts[0].Text != "hi" {
		t.Fatalf("contents %s", r.Body)
	}
	if tc := g.GenerationConfig.ThinkingConfig; tc == nil || tc.ThinkingBudget != 24576 || !tc.IncludeThoughts {
		t.Fatalf("thinking %+v", tc)
	}
	if g.GenerationConfig.MaxOutputTokens != 32768 { // raised to leave room after thinking
		t.Fatalf("max output %d", g.GenerationConfig.MaxOutputTokens)
	}
	if g.SessionID == "" || g.SessionID[0] != '-' {
		t.Fatalf("session %q", g.SessionID)
	}
}

func TestTranslate_ThinkingFromClient(t *testing.T) {
	for body, want := range map[string]int{
		`{"max_tokens":64000,"messages":[{"role":"user","content":"x"}],"thinking":{"type":"enabled","budget_tokens":4000}}`:              4000,
		`{"max_tokens":64000,"messages":[{"role":"user","content":"x"}],"thinking":{"type":"adaptive"}}`:                                  -1,
		`{"max_tokens":64000,"messages":[{"role":"user","content":"x"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`: 1024,
	} {
		tc := translate(t, body, "gemini-3.8-flash-tiered").Request.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingBudget != want {
			t.Errorf("%s: %+v", body, tc)
		}
	}
	if tc := translate(t, `{"max_tokens":10,"messages":[{"role":"user","content":"x"}]}`, "gemini-3.8-flash-tiered").Request.GenerationConfig.ThinkingConfig; tc != nil {
		t.Errorf("no thinking asked: %+v", tc)
	}
}

// Tools: names made Gemini-safe and mapped back, schemas cleaned, tool
// results paired with their call's name, server tools dropped.
func TestTranslate_Tools(t *testing.T) {
	long := "mcp__" + strings.Repeat("x", 70)
	body := `{"max_tokens":100,"tools":[
		{"name":"Bash","description":"run","input_schema":{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","additionalProperties":false,
			"properties":{"command":{"type":"string","minLength":1},"format":{"type":["string","null"],"title":"Format"},
			"mode":{"anyOf":[{"type":"null"},{"enum":[1,2]}]},"opts":{"type":"object"}},"required":["command","missing"]}},
		{"name":"` + long + `","input_schema":{"type":"object","properties":{}}},
		{"type":"web_search_20250305","name":"web_search"}],
		"messages":[{"role":"user","content":"ls"},
		{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"a.txt"}]},{"type":"text","text":"and?"}]}]}`
	r, err := Translate([]byte(body), Options{Model: "gemini-3.8-flash-high"})
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	json.Unmarshal(r.Body, &e)
	decls := e.Request.Tools[0]["functionDeclarations"].([]any)
	if len(decls) != 2 {
		t.Fatalf("declarations: %v", decls)
	}
	safe := decls[1].(map[string]any)["name"].(string)
	if len(safe) != 64 || r.Tools[safe] != long || r.Tools["Bash"] != "Bash" {
		t.Fatalf("names %q %v", safe, r.Tools)
	}
	params, _ := json.Marshal(decls[0].(map[string]any)["parameters"])
	for _, bad := range []string{"$schema", "additionalProperties", "minLength", "title", "anyOf", `"missing"`} {
		if strings.Contains(string(params), bad) {
			t.Errorf("%s left in %s", bad, params)
		}
	}
	for _, good := range []string{`"format":{"type":"string"}`, `"enum":["1","2"]`, `"reason"`} {
		if !strings.Contains(string(params), good) {
			t.Errorf("%s missing from %s", good, params)
		}
	}
	c := e.Request.Contents
	if len(c) != 3 || c[1].Role != "model" || c[1].Parts[0].FunctionCall.Name != "Bash" || c[1].Parts[0].ThoughtSignature != skipSignature {
		t.Fatalf("call turn: %+v", c)
	}
	last := c[2].Parts
	if *last[0].Text != "and?" || last[1].FunctionResponse == nil || last[1].FunctionResponse.Name != "Bash" ||
		last[1].FunctionResponse.Response["result"] != "a.txt" {
		t.Fatalf("result turn ends with the response: %+v", last)
	}
	if e.Request.ToolConfig["functionCallingConfig"].(map[string]any)["mode"] != "VALIDATED" {
		t.Fatalf("tool config %v", e.Request.ToolConfig)
	}
}

const upstream = `data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"Let me look","thought":true}]}}],"usageMetadata":{"promptTokenCount":120,"cachedContentTokenCount":20},"responseId":"r1"}}

data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"Checking."}]}}]}}

data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"mcp_x","args":{"q":1}},"thoughtSignature":"SIG1"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":120,"cachedContentTokenCount":20,"candidatesTokenCount":30,"thoughtsTokenCount":12}}}

`

func TestStream(t *testing.T) {
	out, err := io.ReadAll(NewStream(io.NopCloser(strings.NewReader(upstream)), "ag/gemini-3.8-flash-high", map[string]string{"mcp_x": "mcp/x"}))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`"model":"ag/gemini-3.8-flash-high"`, `"input_tokens":100`, `"cache_read_input_tokens":20`,
		`{"thinking":"Let me look","type":"thinking_delta"}`, `{"text":"Checking.","type":"text_delta"}`,
		`{"signature":"SIG1","type":"signature_delta"}`, `"name":"mcp/x"`, `"partial_json":"{\"q\":1}"`,
		`"stop_reason":"tool_use"`, `"output_tokens":42`, "event: message_stop",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in\n%s", want, s)
		}
	}
	// The signature is announced before the call it belongs to.
	if strings.Index(s, "SIG1") > strings.Index(s, `"tool_use"`) {
		t.Errorf("signature after the call:\n%s", s)
	}
}

// What the client keeps from an answer and sends back reproduces the
// signature on the same function call.
func TestSignatureRoundTrip(t *testing.T) {
	msg, err := Collect(NewStream(io.NopCloser(strings.NewReader(upstream)), "m", nil))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
	}
	json.Unmarshal(msg, &m)
	if m.StopReason != "tool_use" {
		t.Fatalf("message %s", msg)
	}
	var blocks []map[string]any
	json.Unmarshal(m.Content, &blocks)
	id := blocks[len(blocks)-1]["id"].(string)
	body := `{"max_tokens":100,"messages":[{"role":"user","content":"go"},{"role":"assistant","content":` + string(m.Content) +
		`},{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]}]}`
	parts := translate(t, body, "gemini-3.8-flash-high").Request.Contents[1].Parts
	if len(parts) != 2 || *parts[0].Text != "Checking." || parts[0].ThoughtSignature != "" ||
		parts[1].FunctionCall == nil || parts[1].ThoughtSignature != "SIG1" {
		b, _ := json.Marshal(parts)
		t.Fatalf("model turn: %s", b)
	}
}

func TestStream_UpstreamErrorAndEmpty(t *testing.T) {
	out, _ := io.ReadAll(NewStream(io.NopCloser(strings.NewReader(`data: {"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`+"\n\n")), "m", nil))
	if !strings.Contains(string(out), `"message":"Antigravity: quota","type":"rate_limit_error"`) {
		t.Fatalf("%s", out)
	}
	out, _ = io.ReadAll(NewStream(io.NopCloser(strings.NewReader("")), "m", nil))
	if !strings.Contains(string(out), "event: error") {
		t.Fatalf("%s", out)
	}
	if _, err := Collect(strings.NewReader(string(out))); err == nil {
		t.Fatal("Collect hides the error")
	}
}

func TestErrors(t *testing.T) {
	body := []byte(`{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"12.5s"}]}}`)
	if d := RetryAfter(body, time.Now()); d != 12500*time.Millisecond {
		t.Fatalf("retry %v", d)
	}
	if s := string(ErrorBody(404, []byte(`{"error":{"code":404,"message":"model not found"}}`))); s != `{"error":{"message":"Antigravity: model not found","type":"not_found_error"},"type":"error"}` {
		t.Fatalf("%s", s)
	}
}
