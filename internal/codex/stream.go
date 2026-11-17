package codex

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
)

// Responses stream event, the fields that carry over.
type event struct {
	Type         string          `json:"type"`
	ItemID       string          `json:"item_id"`
	SummaryIndex int             `json:"summary_index"`
	Delta        string          `json:"delta"`
	Item         *outputItem     `json:"item"`
	Response     *response       `json:"response"`
	Code         string          `json:"code"`
	Message      string          `json:"message"`
	Error        json.RawMessage `json:"error"`
}

type outputItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	CallID           string `json:"call_id"`
	Name             string `json:"name"`
	Arguments        string `json:"arguments"`
	EncryptedContent string `json:"encrypted_content"`
}

type response struct {
	ID                string `json:"id"`
	Usage             *usage `json:"usage"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type usage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

type anthropicUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	CacheReadTokens int64 `json:"cache_read_input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
}

// anthropic: Responses input_tokens include the cached ones; Anthropic's
// don't. Output includes reasoning tokens, billed as output.
func (u *usage) anthropic() anthropicUsage {
	if u == nil {
		return anthropicUsage{}
	}
	return anthropicUsage{
		InputTokens:     u.InputTokens - u.InputTokensDetails.CachedTokens,
		CacheReadTokens: u.InputTokensDetails.CachedTokens,
		OutputTokens:    u.OutputTokens,
	}
}

// Stream reads the Responses SSE and yields the Anthropic Messages SSE for
// it, one upstream line at a time (REQ-STREAM-01): nothing is held beyond
// the current line. Tool arguments pass through fragment by fragment
// (REQ-STREAM-03). A read error mid-stream is returned as is.
type Stream struct {
	src   *bufio.Reader
	body  io.Closer
	out   bytes.Buffer
	model string            // the name the client asked for
	tools map[string]string // Codex tool name → client's

	started, done, failed bool
	open                  string // "", "text", "thinking" or "tool": the block being written
	index                 int    // next content block index
	toolArgs              bool   // the open tool block got argument deltas
	toolUse               bool
	stop                  string
	usage                 *usage
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
	if !ok || s.done {
		return
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "[DONE]" {
		return
	}
	var ev event
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	if !s.started { // REQ-STREAM-02: on the first upstream event
		id := ""
		if ev.Response != nil {
			id = ev.Response.ID
		}
		s.start(id)
	}
	switch ev.Type {
	case "response.output_text.delta":
		if ev.Delta != "" {
			s.openBlock("text")
			s.delta(map[string]any{"type": "text_delta", "text": ev.Delta})
		}
	case "response.reasoning_summary_part.added":
		if ev.SummaryIndex > 0 && s.open == "thinking" {
			s.delta(map[string]any{"type": "thinking_delta", "thinking": "\n\n"})
		}
	case "response.reasoning_summary_text.delta":
		if ev.Delta != "" {
			s.openBlock("thinking")
			s.delta(map[string]any{"type": "thinking_delta", "thinking": ev.Delta})
		}
	case "response.output_item.added":
		if it := ev.Item; it != nil && it.Type == "function_call" {
			s.closeBlock()
			name := it.Name
			if orig, ok := s.tools[name]; ok {
				name = orig
			}
			id := it.CallID
			if id == "" {
				id = "call_" + randomHex(12)
			}
			s.emit("content_block_start", map[string]any{"type": "content_block_start", "index": s.index,
				"content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}})
			s.open, s.toolArgs, s.toolUse = "tool", false, true
		}
	case "response.function_call_arguments.delta":
		if s.open == "tool" && ev.Delta != "" {
			s.delta(map[string]any{"type": "input_json_delta", "partial_json": ev.Delta})
			s.toolArgs = true
		}
	case "response.output_item.done":
		it := ev.Item
		if it == nil {
			return
		}
		switch it.Type {
		case "function_call":
			if s.open == "tool" && !s.toolArgs && it.Arguments != "" {
				s.delta(map[string]any{"type": "input_json_delta", "partial_json": it.Arguments})
			}
			s.closeBlock()
		case "reasoning":
			if it.EncryptedContent != "" { // kept for the next turn (REQ-STREAM-04)
				if s.open != "thinking" {
					s.openBlock("thinking")
				}
				s.delta(map[string]any{"type": "signature_delta", "signature": signaturePrefix + it.EncryptedContent})
			}
			if s.open == "thinking" {
				s.closeBlock()
			}
		case "message":
			if s.open == "text" {
				s.closeBlock()
			}
		}
	case "response.completed", "response.incomplete", "response.done":
		if r := ev.Response; r != nil {
			s.usage = r.Usage
			if d := r.IncompleteDetails; d != nil {
				switch d.Reason {
				case "max_output_tokens":
					s.stop = "max_tokens"
				case "content_filter":
					s.stop = "refusal"
				}
			}
		}
	case "response.failed", "error":
		code, msg := ev.Code, ev.Message
		if r := ev.Response; r != nil && r.Error != nil {
			code, msg = r.Error.Code, r.Error.Message
		}
		if len(ev.Error) > 0 && msg == "" {
			var e struct{ Code, Message, Type string }
			_ = json.Unmarshal(ev.Error, &e)
			code, msg = e.Code+e.Type, e.Message
		}
		typ, text := errorType(code, 0), "Codex: "+msg
		if msg == "" {
			text = "Codex: the response failed (" + code + ")"
		}
		s.emit("error", map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": text}})
		s.failed, s.done = true, true
	}
}

func (s *Stream) start(id string) {
	s.started = true
	if id == "" {
		id = randomHex(12)
	}
	s.emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_" + id, "type": "message", "role": "assistant", "model": s.model,
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": anthropicUsage{},
	}})
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

// end closes the message once the upstream finished (REQ-STREAM-06).
func (s *Stream) end() {
	if s.failed {
		return
	}
	if !s.started {
		s.emit("error", map[string]any{"type": "error", "error": map[string]string{
			"type": "api_error", "message": "Codex ended the response without any content. Retry the request.",
		}})
		return
	}
	s.closeBlock()
	stop := s.stop
	switch {
	case stop != "":
	case s.toolUse:
		stop = "tool_use"
	default:
		stop = "end_turn"
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
