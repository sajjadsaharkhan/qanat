package antigravity

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
)

// Gemini response chunk, the fields that carry over. Cloud Code wraps each
// in {"response": …}.
type chunk struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text             string `json:"text"`
				Thought          bool   `json:"thought"`
				ThoughtSignature string `json:"thoughtSignature"`
				FunctionCall     *struct {
					ID   string          `json:"id"`
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata"`
	ResponseID    string         `json:"responseId"`
}

type usageMetadata struct {
	Prompt     int64 `json:"promptTokenCount"`
	Candidates int64 `json:"candidatesTokenCount"`
	Thoughts   int64 `json:"thoughtsTokenCount"`
	Cached     int64 `json:"cachedContentTokenCount"`
	ToolPrompt int64 `json:"toolUsePromptTokenCount"`
}

type anthropicUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	CacheReadTokens int64 `json:"cache_read_input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
}

func (u *usageMetadata) anthropic() anthropicUsage {
	if u == nil {
		return anthropicUsage{}
	}
	return anthropicUsage{
		InputTokens:     u.Prompt + u.ToolPrompt - u.Cached,
		CacheReadTokens: u.Cached,
		OutputTokens:    u.Candidates + u.Thoughts,
	}
}

// Stream reads Antigravity's SSE and yields the Anthropic Messages SSE for
// it, one upstream line at a time: nothing is buffered beyond the current
// line, so the client sees each token as it arrives. A read error mid-stream
// is returned as is; the gateway reports it to the client.
type Stream struct {
	src   *bufio.Reader
	body  io.Closer
	out   bytes.Buffer
	model string            // the name the client asked for
	tools map[string]string // Gemini-safe name → client's

	started, done, failed bool
	open                  string // "", "text" or "thinking": the block being written
	index                 int    // next content block index
	toolUse               bool
	finish                string
	usage                 *usageMetadata
}

// NewStream translates body; model is echoed in message_start and tools maps
// called names back to the client's.
func NewStream(body io.ReadCloser, model string, tools map[string]string) *Stream {
	return &Stream{src: bufio.NewReaderSize(body, 64<<10), body: body, model: model, tools: tools}
}

func (s *Stream) Read(p []byte) (int, error) {
	for s.out.Len() == 0 {
		if s.done {
			return 0, io.EOF
		}
		line, err := s.src.ReadBytes('\n')
		if len(line) > 0 {
			s.line(line)
		}
		switch {
		case err == io.EOF:
			s.end()
			s.done = true
		case err != nil:
			return 0, err
		}
	}
	return s.out.Read(p)
}

func (s *Stream) Close() error { return s.body.Close() }

func (s *Stream) line(line []byte) {
	data, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
	if !ok || s.failed {
		return
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "[DONE]" {
		return
	}
	var wrap struct {
		Response json.RawMessage `json:"response"`
		Error    json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &wrap) != nil {
		return
	}
	if len(wrap.Error) > 0 {
		typ, msg := errorInfo(0, data)
		s.emit("error", map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": msg}})
		s.failed, s.done = true, true
		return
	}
	raw := wrap.Response
	if len(raw) == 0 {
		raw = data
	}
	var c chunk
	if json.Unmarshal(raw, &c) != nil {
		return
	}
	if c.UsageMetadata != nil {
		s.usage = c.UsageMetadata
	}
	if !s.started {
		s.start(c.ResponseID)
	}
	if len(c.Candidates) == 0 {
		return
	}
	cand := c.Candidates[0]
	for _, pt := range cand.Content.Parts {
		switch {
		case pt.Thought:
			if pt.Text != "" {
				s.openBlock("thinking")
				s.delta(map[string]any{"type": "thinking_delta", "thinking": pt.Text})
			}
			if pt.ThoughtSignature != "" {
				s.sign(pt.ThoughtSignature)
			}
			continue
		case pt.ThoughtSignature != "":
			// The signature belongs to this part: it goes out in a thinking
			// block just before it, where the request side looks for it.
			s.sign(pt.ThoughtSignature)
		}
		if pt.Text != "" {
			s.openBlock("text")
			s.delta(map[string]any{"type": "text_delta", "text": pt.Text})
		}
		if fc := pt.FunctionCall; fc != nil {
			s.closeBlock()
			name := fc.Name
			if orig, ok := s.tools[name]; ok {
				name = orig
			}
			id := fc.ID
			if id == "" {
				id = "toolu_" + randomHex(12)
			}
			args := fc.Args
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			s.emit("content_block_start", map[string]any{"type": "content_block_start", "index": s.index,
				"content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}})
			s.delta(map[string]any{"type": "input_json_delta", "partial_json": string(args)})
			s.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": s.index})
			s.index++
			s.toolUse = true
		}
	}
	if cand.FinishReason != "" {
		s.finish = cand.FinishReason
	}
}

func (s *Stream) start(id string) {
	s.started = true
	if id == "" {
		id = randomHex(12)
	}
	s.emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_" + id, "type": "message", "role": "assistant", "model": s.model,
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": s.usage.anthropic(),
	}})
}

// sign writes signature into the open thinking block, or into an empty one
// of its own, and closes it.
func (s *Stream) sign(signature string) {
	if s.open != "thinking" {
		s.openBlock("thinking")
	}
	s.delta(map[string]any{"type": "signature_delta", "signature": signature})
	s.closeBlock()
}

func (s *Stream) openBlock(kind string) {
	if s.open == kind {
		return
	}
	s.closeBlock()
	cb := map[string]any{"type": "text", "text": ""}
	if kind == "thinking" {
		cb = map[string]any{"type": "thinking", "thinking": "", "signature": ""}
	}
	s.emit("content_block_start", map[string]any{"type": "content_block_start", "index": s.index, "content_block": cb})
	s.open = kind
}

func (s *Stream) closeBlock() {
	if s.open == "" {
		return
	}
	s.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": s.index})
	s.index++
	s.open = ""
}

func (s *Stream) delta(d map[string]any) {
	s.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": s.index, "delta": d})
}

// end closes the message once the upstream finished.
func (s *Stream) end() {
	if s.failed {
		return
	}
	if !s.started {
		s.emit("error", map[string]any{"type": "error", "error": map[string]string{
			"type": "api_error", "message": "Antigravity ended the response without any content. Retry the request.",
		}})
		return
	}
	s.closeBlock()
	stop := "end_turn"
	switch {
	case s.toolUse:
		stop = "tool_use"
	case s.finish == "MAX_TOKENS":
		stop = "max_tokens"
	case s.finish == "SAFETY" || s.finish == "RECITATION" || s.finish == "PROHIBITED_CONTENT" ||
		s.finish == "BLOCKLIST" || s.finish == "SPII":
		stop = "refusal"
	}
	s.emit("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": s.usage.anthropic()})
	s.emit("message_stop", map[string]any{"type": "message_stop"})
}

func (s *Stream) emit(event string, v any) {
	b, _ := json.Marshal(v)
	s.out.WriteString("event: ")
	s.out.WriteString(event)
	s.out.WriteString("\ndata: ")
	s.out.Write(b)
	s.out.WriteString("\n\n")
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
