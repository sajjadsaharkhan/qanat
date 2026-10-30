package antigravity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Collect reads a translated stream to its end and returns the Anthropic
// Messages response, for clients that didn't ask to stream.
func Collect(r io.Reader) ([]byte, error) {
	var msg map[string]any
	var blocks []map[string]any
	partial := map[int]*bytes.Buffer{} // tool_use input as it arrives
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		data, ok := bytes.CutPrefix(sc.Bytes(), []byte("data: "))
		if !ok {
			continue
		}
		var ev struct {
			Type         string          `json:"type"`
			Index        int             `json:"index"`
			Message      map[string]any  `json:"message"`
			ContentBlock map[string]any  `json:"content_block"`
			Delta        map[string]any  `json:"delta"`
			Usage        json.RawMessage `json:"usage"`
			Error        struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil, err
		}
		switch ev.Type {
		case "error":
			return nil, errors.New(ev.Error.Message)
		case "message_start":
			msg = ev.Message
		case "content_block_start":
			blocks = append(blocks, ev.ContentBlock)
		case "content_block_delta":
			if ev.Index >= len(blocks) {
				continue
			}
			b := blocks[ev.Index]
			switch ev.Delta["type"] {
			case "text_delta":
				b["text"] = str(b["text"]) + str(ev.Delta["text"])
			case "thinking_delta":
				b["thinking"] = str(b["thinking"]) + str(ev.Delta["thinking"])
			case "signature_delta":
				b["signature"] = ev.Delta["signature"]
			case "input_json_delta":
				if partial[ev.Index] == nil {
					partial[ev.Index] = &bytes.Buffer{}
				}
				partial[ev.Index].WriteString(str(ev.Delta["partial_json"]))
			}
		case "content_block_stop":
			if buf := partial[ev.Index]; buf != nil && ev.Index < len(blocks) {
				blocks[ev.Index]["input"] = json.RawMessage(buf.Bytes())
			}
		case "message_delta":
			if msg != nil {
				for k, v := range ev.Delta {
					msg[k] = v
				}
				msg["usage"] = ev.Usage
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, errors.New("Antigravity ended the response without any content")
	}
	if blocks == nil {
		blocks = []map[string]any{}
	}
	msg["content"] = blocks
	return json.Marshal(msg)
}

func str(v any) string { s, _ := v.(string); return s }
