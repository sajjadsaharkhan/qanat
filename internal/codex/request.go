// Package codex speaks the ChatGPT Codex backend (the OpenAI Responses API
// the Codex CLI uses) for the gateway: it turns an Anthropic Messages request
// into a /responses body and the Responses SSE back into Anthropic SSE, after
// 9router's open-sse codex executor and responses translators (MIT).
package codex

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Options are what a request needs besides its body.
type Options struct {
	Model   string // upstream model as routed, e.g. "gpt-6-sol" or "gpt-6-sol-high"
	Session string // stable per conversation: the backend's prompt cache key
}

// Request is a translated request.
type Request struct {
	Body   []byte
	Stream bool              // the client asked for SSE
	Tools  map[string]string // Codex tool name → the client's (names over 128 chars are cut)
}

// signaturePrefix marks thinking signatures that carry a Codex reasoning
// item's encrypted_content, so only those go back upstream (REQ-STREAM-04);
// a Claude or Gemini signature would fail the backend's decryption.
const signaturePrefix = "codex:"

// efforts are the reasoning levels a model name may end in.
var efforts = []string{"none", "minimal", "low", "medium", "high", "xhigh"}

// Upstream splits "gpt-6-sol-high" into the model and the effort its name
// implies ("" when none).
func Upstream(model string) (id, effort string) {
	for _, e := range efforts {
		if s, ok := strings.CutSuffix(model, "-"+e); ok {
			return s, e
		}
	}
	return model, ""
}

// Anthropic request, the fields that carry over. max_tokens and the sampling
// knobs have no Codex form: the backend rejects them.
type msgRequest struct {
	Messages   []message       `json:"messages"`
	System     json.RawMessage `json:"system"`
	Stream     bool            `json:"stream"`
	Tools      []tool          `json:"tools"`
	ToolChoice *struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"tool_choice"`
	Thinking *struct {
		Type         string `json:"type"`
		BudgetTokens int    `json:"budget_tokens"`
	} `json:"thinking"`
	OutputConfig *struct {
		Effort string `json:"effort"`
	} `json:"output_config"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Title     string          `json:"title"`
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	} `json:"source"`
}

type tool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Responses request. Field order and the allowlist follow the Codex CLI.
type responsesRequest struct {
	Model          string         `json:"model"`
	Instructions   string         `json:"instructions"`
	Input          []item         `json:"input"`
	Tools          []functionTool `json:"tools,omitempty"`
	ToolChoice     any            `json:"tool_choice,omitempty"`
	Reasoning      *reasoning     `json:"reasoning,omitempty"`
	Store          bool           `json:"store"`
	Stream         bool           `json:"stream"`
	Include        []string       `json:"include,omitempty"`
	PromptCacheKey string         `json:"prompt_cache_key,omitempty"`
}

type reasoning struct {
	Effort  string `json:"effort"`
	Summary string `json:"summary,omitempty"`
}

type functionTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

// item is one Responses input item; only the fields of its type are set.
type item struct {
	Type             string        `json:"type"`
	Role             string        `json:"role,omitempty"`
	Content          []contentPart `json:"content,omitempty"`
	CallID           string        `json:"call_id,omitempty"`
	Name             string        `json:"name,omitempty"`
	Arguments        string        `json:"arguments,omitempty"`
	Output           *string       `json:"output,omitempty"`
	Summary          *[]summary    `json:"summary,omitempty"`
	EncryptedContent string        `json:"encrypted_content,omitempty"`
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
}

type summary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

const (
	maxToolName = 128
	maxCallID   = 64
	// defaultInstructions stands in for a missing system prompt: the backend
	// wants instructions on every request.
	defaultInstructions = "You are a helpful coding assistant."
)

// Translate turns an Anthropic Messages body into a /responses body.
func Translate(body []byte, o Options) (*Request, error) {
	var in msgRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	model, effort := Upstream(o.Model)
	out := &Request{Stream: in.Stream, Tools: map[string]string{}}
	r := responsesRequest{
		Model: model, Instructions: texts(in.System), Store: false, Stream: true,
		PromptCacheKey: o.Session,
	}
	if r.Instructions == "" {
		r.Instructions = defaultInstructions
	}

	names := map[string]string{} // client's tool name → Codex name
	for _, t := range in.Tools {
		if t.Type != "" && t.Type != "custom" { // server tools (web search…) have no Codex form here
			continue
		}
		name := toolName(t.Name)
		if _, dup := out.Tools[name]; dup {
			continue
		}
		out.Tools[name], names[t.Name] = t.Name, name
		r.Tools = append(r.Tools, functionTool{Type: "function", Name: name, Description: t.Description, Parameters: schema(t.InputSchema)})
	}
	if tc := in.ToolChoice; tc != nil && len(r.Tools) > 0 {
		switch tc.Type {
		case "any":
			r.ToolChoice = "required"
		case "tool":
			if n, ok := names[tc.Name]; ok {
				r.ToolChoice = map[string]string{"type": "function", "name": n}
			}
		case "none":
			r.ToolChoice = "none"
		}
	}

	input, err := convert(in.Messages, names)
	if err != nil {
		return nil, err
	}
	if len(input) == 0 {
		input = []item{userText("...")}
	}
	r.Input = input

	if e := pickEffort(in, effort); e != "none" {
		r.Reasoning = &reasoning{Effort: e, Summary: "auto"}
		r.Include = []string{"reasoning.encrypted_content"}
	} else {
		r.Reasoning = &reasoning{Effort: e}
	}
	out.Body, err = json.Marshal(r)
	return out, err
}

// pickEffort: the model name's level, else Claude Code's effort, else its
// thinking budget, else medium.
func pickEffort(in msgRequest, fromName string) string {
	if fromName != "" {
		return fromName
	}
	if oc := in.OutputConfig; oc != nil && oc.Effort != "" {
		switch e := strings.ToLower(oc.Effort); e {
		case "max":
			return "xhigh"
		case "minimal", "low", "medium", "high", "xhigh":
			return e
		}
	}
	if t := in.Thinking; t != nil {
		switch {
		case t.Type == "disabled":
			return "low"
		case t.BudgetTokens > 0 && t.BudgetTokens <= 2048:
			return "low"
		case t.BudgetTokens > 16384:
			return "high"
		}
	}
	return "medium"
}

// convert maps the conversation. Assistant thinking goes back as the
// reasoning item it came from, when its signature carries one.
func convert(msgs []message, names map[string]string) ([]item, error) {
	var out []item
	for _, m := range msgs {
		blocks, err := parseBlocks(m.Content)
		if err != nil {
			return nil, err
		}
		var parts []contentPart // the message being built, flushed around calls and results
		flush := func() {
			if len(parts) == 0 {
				return
			}
			role := "user"
			if m.Role == "assistant" {
				role = "assistant"
			}
			out = append(out, item{Type: "message", Role: role, Content: parts})
			parts = nil
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text == "" {
					continue
				}
				if m.Role == "assistant" {
					parts = append(parts, contentPart{Type: "output_text", Text: b.Text})
				} else {
					parts = append(parts, contentPart{Type: "input_text", Text: b.Text})
				}
			case "image", "document":
				if m.Role != "assistant" {
					if p, ok := media(b); ok {
						parts = append(parts, p)
					}
				}
			case "thinking":
				if enc, ok := strings.CutPrefix(b.Signature, signaturePrefix); ok && enc != "" {
					flush()
					s := []summary{}
					if b.Thinking != "" {
						s = append(s, summary{Type: "summary_text", Text: b.Thinking})
					}
					out = append(out, item{Type: "reasoning", Summary: &s, EncryptedContent: enc})
				}
			case "tool_use":
				flush()
				args := string(b.Input)
				if !json.Valid(b.Input) || args == "null" || args == "" {
					args = "{}"
				}
				name := names[b.Name]
				if name == "" {
					name = toolName(b.Name)
				}
				out = append(out, item{Type: "function_call", CallID: callID(b.ID), Name: name, Arguments: args})
			case "tool_result":
				flush()
				text, media, err := resultContent(b.Content)
				if err != nil {
					return nil, err
				}
				if b.IsError && text != "" {
					text = "Error: " + text
				}
				out = append(out, item{Type: "function_call_output", CallID: callID(b.ToolUseID), Output: &text})
				if len(media) > 0 { // tool output is text only: images follow as user input
					out = append(out, item{Type: "message", Role: "user", Content: media})
				}
			}
		}
		flush()
	}
	return out, nil
}

func userText(s string) item {
	return item{Type: "message", Role: "user", Content: []contentPart{{Type: "input_text", Text: s}}}
}

// media is an image or document block as Responses input. The gateway has
// already fetched URL sources into base64 (inlineURLMedia); a URL left here
// passes through as is.
func media(b block) (contentPart, bool) {
	src := b.Source
	switch {
	case src == nil:
		return contentPart{}, false
	case src.Type == "text" && src.Data != "":
		return contentPart{Type: "input_text", Text: src.Data}, true
	case b.Type == "image" && src.Type == "base64" && src.Data != "":
		return contentPart{Type: "input_image", ImageURL: "data:" + src.MediaType + ";base64," + src.Data}, true
	case b.Type == "image" && src.URL != "":
		return contentPart{Type: "input_image", ImageURL: src.URL}, true
	case b.Type == "document" && src.Type == "base64" && src.Data != "":
		name := b.Title
		if name == "" {
			name = "document.pdf"
		}
		return contentPart{Type: "input_file", Filename: name, FileData: "data:" + src.MediaType + ";base64," + src.Data}, true
	case src.URL != "":
		return contentPart{Type: "input_text", Text: fmt.Sprintf("[%s: %s]", b.Type, src.URL)}, true
	}
	return contentPart{}, false
}

// resultContent is a tool result's text, and its images and documents.
func resultContent(raw json.RawMessage) (string, []contentPart, error) {
	blocks, err := parseBlocks(raw)
	if err != nil {
		return "", nil, err
	}
	var text []string
	var parts []contentPart
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text = append(text, b.Text)
		case "image", "document":
			if p, ok := media(b); ok {
				parts = append(parts, p)
			}
		}
	}
	return strings.Join(text, "\n"), parts, nil
}

// parseBlocks reads content given as a string or as a block list.
func parseBlocks(raw json.RawMessage) ([]block, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return []block{{Type: "text", Text: s}}, nil
	}
	var bs []block
	err := json.Unmarshal(raw, &bs)
	return bs, err
}

// texts joins a system prompt given as a string or as text blocks.
func texts(raw json.RawMessage) string {
	bs, err := parseBlocks(raw)
	if err != nil {
		return ""
	}
	var out []string
	for _, b := range bs {
		if b.Type == "text" && b.Text != "" {
			out = append(out, b.Text)
		}
	}
	return strings.Join(out, "\n\n")
}

// schema is a tool's input schema as Responses parameters: an object
// schema needs a properties map, even an empty one.
func schema(s map[string]any) map[string]any {
	if s == nil {
		s = map[string]any{"type": "object"}
	}
	if t, _ := s["type"].(string); t == "object" {
		if _, ok := s["properties"]; !ok {
			c := make(map[string]any, len(s)+1)
			for k, v := range s {
				c[k] = v
			}
			c["properties"] = map[string]any{}
			return c
		}
	}
	return s
}

func toolName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) > maxToolName {
		name = name[:maxToolName]
	}
	if name == "" {
		name = "_unknown"
	}
	return name
}

// callID fits the backend's 64-character call_id limit.
func callID(id string) string {
	if len(id) > maxCallID {
		return id[:maxCallID]
	}
	return id
}
