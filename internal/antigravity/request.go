package antigravity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// skipSignature is Google's documented stand-in for a Gemini thought
// signature the client didn't keep: without one, Gemini 3 rejects a
// history's function call with 400.
const skipSignature = "skip_thought_signature_validator"

// Options are what a request needs besides its body.
type Options struct {
	Model   string // upstream model as routed, e.g. "gemini-3.8-flash-high"
	Project string // the account's Cloud Code project, from sign-in
	// Session is stable for one conversation (Claude Code's session ID, else
	// the account): Antigravity's session and request IDs derive from it.
	Session string
}

// Request is a translated request.
type Request struct {
	Body   []byte
	Stream bool              // the client asked for SSE
	Tools  map[string]string // Gemini-safe tool name → the client's name
}

// Anthropic request, the fields that carry over.
type msgRequest struct {
	Messages      []message       `json:"messages"`
	System        json.RawMessage `json:"system"`
	MaxTokens     int             `json:"max_tokens"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	TopK          *int            `json:"top_k"`
	StopSequences []string        `json:"stop_sequences"`
	Stream        bool            `json:"stream"`
	Tools         []tool          `json:"tools"`
	ToolChoice    *struct {
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
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	} `json:"source"`
}

type tool struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// Gemini request.
type content struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             *string           `json:"text,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type functionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type functionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type generationConfig struct {
	MaxOutputTokens int             `json:"maxOutputTokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"topP,omitempty"`
	TopK            *int            `json:"topK,omitempty"`
	StopSequences   []string        `json:"stopSequences,omitempty"`
	ThinkingConfig  *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	ThinkingBudget  int  `json:"thinkingBudget"`
	IncludeThoughts bool `json:"includeThoughts"`
}

type declaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiRequest struct {
	Contents          []content        `json:"contents"`
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	GenerationConfig  generationConfig `json:"generationConfig"`
	Tools             []map[string]any `json:"tools,omitempty"`
	ToolConfig        map[string]any   `json:"toolConfig,omitempty"`
	SessionID         string           `json:"sessionId"`
}

// envelope is the Cloud Code wrapper. The IDE omits requestType on its chat
// path; sending one buckets the request into a detail-free 429 (9router).
type envelope struct {
	Project   string        `json:"project"`
	Model     string        `json:"model"`
	UserAgent string        `json:"userAgent"`
	RequestID string        `json:"requestId"`
	Request   geminiRequest `json:"request"`
}

// Translate turns an Anthropic Messages body into a streamGenerateContent
// envelope.
func Translate(body []byte, o Options) (*Request, error) {
	var in msgRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	model, level := Upstream(o.Model)
	out := &Request{Stream: in.Stream, Tools: map[string]string{}}
	g := geminiRequest{SessionID: numericID(o.Session)}

	if sys := texts(in.System); sys != "" {
		g.SystemInstruction = &content{Role: "user", Parts: []part{{Text: &sys}}}
	}

	var decls []declaration
	for _, t := range in.Tools {
		if t.Type != "" && t.Type != "custom" { // server tools (web search…) have no Gemini form
			continue
		}
		name := toolName(t.Name)
		if _, dup := out.Tools[name]; dup {
			continue
		}
		out.Tools[name] = t.Name
		decls = append(decls, declaration{Name: name, Description: t.Description, Parameters: cleanSchema(t.InputSchema)})
	}
	if len(decls) > 0 {
		g.Tools = []map[string]any{{"functionDeclarations": decls}}
		fc := map[string]any{"mode": "VALIDATED"}
		if tc := in.ToolChoice; tc != nil {
			switch tc.Type {
			case "any":
				fc["mode"] = "ANY"
			case "tool":
				fc["mode"], fc["allowedFunctionNames"] = "ANY", []string{toolName(tc.Name)}
			case "none":
				fc["mode"] = "NONE"
			}
		}
		g.ToolConfig = map[string]any{"functionCallingConfig": fc}
	}

	contents, err := convert(in.Messages, !isClaude(model))
	if err != nil {
		return nil, err
	}
	g.Contents = contents

	gc := &g.GenerationConfig
	gc.MaxOutputTokens = min(in.MaxTokens, maxOutput)
	if !isGemini4(model) { // Gemini 4 dropped the sampling knobs
		gc.Temperature, gc.TopP, gc.TopK = in.Temperature, in.TopP, in.TopK
	}
	gc.StopSequences = in.StopSequences
	if budget, ok := thinking(in, level, model); ok {
		gc.ThinkingConfig = &thinkingConfig{ThinkingBudget: budget, IncludeThoughts: true}
		if f := outputFloor(budget); gc.MaxOutputTokens < f {
			gc.MaxOutputTokens = f
		}
	}

	step := max(1, len(contents)*2-1)
	out.Body, err = json.Marshal(envelope{
		Project:   project(o.Project),
		Model:     model,
		UserAgent: "antigravity",
		RequestID: fmt.Sprintf("agent/%s/%d/%s/%d", seededUUID("conversation:"+o.Session), time.Now().UnixMilli(), seededUUID("trajectory:"+o.Session+":"+model), step),
		Request:   g,
	})
	return out, err
}

// thinking picks the thinking budget: the level a model's name implies, else
// Claude Code's effort, else its thinking budget; adaptive thinking is
// Gemini's dynamic budget (-1).
func thinking(in msgRequest, level, model string) (int, bool) {
	if level == "" && in.OutputConfig != nil {
		level = strings.ToLower(in.OutputConfig.Effort)
	}
	if b, ok := levelBudget[level]; ok {
		return clampBudget(b, model), true
	}
	t := in.Thinking
	switch {
	case t == nil || t.Type == "disabled":
		return 0, false
	case t.BudgetTokens > 0:
		return clampBudget(t.BudgetTokens, model), true
	}
	return -1, true
}

// clampBudget keeps a budget inside the model's range: Gemini Flash stops at
// 24576 thinking tokens.
func clampBudget(b int, model string) int {
	if strings.Contains(model, "flash") {
		return min(b, 24576)
	}
	return min(b, 32768)
}

// convert maps the conversation. A thinking block carries no content across
// (Gemini doesn't want thoughts back) but its signature does: it belongs to
// the part that followed it in the answer, which is how the response side
// emitted it.
func convert(msgs []message, gemini bool) ([]content, error) {
	names := map[string]string{} // tool_use id → Gemini-safe name
	var out []content
	for _, m := range msgs {
		blocks, err := parseBlocks(m.Content)
		if err != nil {
			return nil, err
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		var parts, responses []part
		sig, signedCall := "", false
		add := func(p part) {
			if role == "model" && sig != "" {
				p.ThoughtSignature, sig = sig, ""
			}
			parts = append(parts, p)
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					add(textPart(b.Text))
				}
			case "thinking":
				if b.Signature != "" {
					sig = b.Signature
				}
			case "image", "document":
				if p, ok := media(b); ok {
					add(p)
				}
			case "tool_use":
				name := toolName(b.Name)
				names[b.ID] = name
				args := b.Input
				if len(args) == 0 || string(args) == "null" {
					args = json.RawMessage("{}")
				}
				p := part{FunctionCall: &functionCall{ID: b.ID, Name: name, Args: args}}
				// Gemini checks the first call of a step for a signature;
				// parallel siblings go unsigned.
				if sig == "" && !signedCall && gemini {
					p.ThoughtSignature = skipSignature
				}
				signedCall = true
				add(p)
			case "tool_result":
				name := names[b.ToolUseID]
				if name == "" {
					name = "tool"
				}
				text, images, err := resultContent(b.Content)
				if err != nil {
					return nil, err
				}
				key := "result"
				if b.IsError {
					key = "error"
				}
				responses = append(responses, part{FunctionResponse: &functionResponse{ID: b.ToolUseID, Name: name, Response: map[string]any{key: text}}})
				parts = append(parts, images...)
			}
		}
		if sig != "" { // a signature that closed the answer stands alone, as Gemini sent it
			empty := ""
			parts = append(parts, part{Text: &empty, ThoughtSignature: sig})
		}
		// A turn answering calls ends with the responses (Gemini 4 requires it).
		parts = append(parts, responses...)
		if len(parts) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Parts = append(out[n-1].Parts, parts...)
			if role == "user" {
				out[n-1].Parts = responsesLast(out[n-1].Parts)
			}
			continue
		}
		out = append(out, content{Role: role, Parts: parts})
	}
	// Gemini wants the conversation to open and close on a user turn.
	if len(out) > 0 && out[0].Role != "user" {
		out = append([]content{{Role: "user", Parts: []part{textPart("...")}}}, out...)
	}
	if n := len(out); n > 0 && out[n-1].Role == "model" {
		var pending []part
		for _, p := range out[n-1].Parts {
			if fc := p.FunctionCall; fc != nil {
				pending = append(pending, part{FunctionResponse: &functionResponse{ID: fc.ID, Name: fc.Name, Response: map[string]any{"result": "Continue."}}})
			}
		}
		if len(pending) == 0 {
			pending = []part{textPart("Continue.")}
		}
		out = append(out, content{Role: "user", Parts: pending})
	}
	return out, nil
}

func responsesLast(ps []part) []part {
	var rest, resp []part
	for _, p := range ps {
		if p.FunctionResponse != nil {
			resp = append(resp, p)
		} else {
			rest = append(rest, p)
		}
	}
	return append(rest, resp...)
}

func textPart(s string) part { return part{Text: &s} }

// media is an image or document block as inline data; a URL source, which
// Gemini can't fetch, is named in text instead.
func media(b block) (part, bool) {
	src := b.Source
	switch {
	case src == nil:
		return part{}, false
	case src.Type == "base64" && src.Data != "":
		return part{InlineData: &inlineData{MimeType: src.MediaType, Data: src.Data}}, true
	case src.Type == "text" && src.Data != "":
		return textPart(src.Data), true
	case src.URL != "":
		return textPart(fmt.Sprintf("[%s: %s]", b.Type, src.URL)), true
	}
	return part{}, false
}

// resultContent is a tool result's text, and its images as parts of their own.
func resultContent(raw json.RawMessage) (string, []part, error) {
	blocks, err := parseBlocks(raw)
	if err != nil {
		return "", nil, err
	}
	var text []string
	var images []part
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text = append(text, b.Text)
		case "image", "document":
			if p, ok := media(b); ok {
				images = append(images, p)
			}
		}
	}
	return strings.Join(text, "\n"), images, nil
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

var badName = regexp.MustCompile(`[^a-zA-Z0-9_.:\-]`)

// toolName fits Gemini's function-name rule: [a-zA-Z_][a-zA-Z0-9_.:-]{0,63}.
func toolName(name string) string {
	if name == "" {
		return "_unknown"
	}
	s := badName.ReplaceAllString(name, "_")
	if c := s[0]; !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		s = "_" + s
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// numericID is the IDE's session ID form: a negative int63 from a hash.
func numericID(seed string) string {
	h := sha256.Sum256([]byte("antigravity:session:" + seed))
	return "-" + strconv.FormatUint(binary.BigEndian.Uint64(h[:8])&(1<<63-1), 10)
}

// seededUUID is a v5-shaped UUID from a hash, stable for one seed.
func seededUUID(seed string) string {
	h := sha256.Sum256([]byte("antigravity:" + seed))
	b := h[:16]
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	x := hex.EncodeToString(b)
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:]
}

// project falls back to an IDE-style generated name when sign-in found no
// Cloud Code project, as 9router does.
func project(p string) string {
	if p != "" {
		return p
	}
	var r [3]byte
	rand.Read(r[:])
	return "useful-flow-" + hex.EncodeToString(r[:])[:5]
}
