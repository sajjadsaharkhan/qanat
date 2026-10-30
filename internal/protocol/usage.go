package protocol

import (
	"bytes"

	"github.com/bytedance/sonic"
)

// Usage is provider-reported token usage for one request (PRD §10.1).
type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_input_tokens"`
	CacheWriteTokens int64 `json:"cache_creation_input_tokens"`
}

// merge takes every non-zero field of u2: message_delta usage is cumulative.
func (u *Usage) merge(u2 Usage) {
	if u2.InputTokens > 0 {
		u.InputTokens = u2.InputTokens
	}
	if u2.OutputTokens > 0 {
		u.OutputTokens = u2.OutputTokens
	}
	if u2.CacheReadTokens > 0 {
		u.CacheReadTokens = u2.CacheReadTokens
	}
	if u2.CacheWriteTokens > 0 {
		u.CacheWriteTokens = u2.CacheWriteTokens
	}
}

// maxLine bounds one buffered line of an event we parse. message_start and
// message_delta are small; anything bigger is not something we need.
const maxLine = 1 << 20

// UsageScanner reads usage from an Anthropic SSE stream as bytes pass
// through. It never delays or copies the stream: Write only observes. It
// keeps at most one incomplete line, and only for events it parses
// (message_start, message_delta, error); content deltas are skipped without
// being stored.
type UsageScanner struct {
	Usage      Usage
	SawStart   bool // message_start seen
	SawStop    bool // message_stop seen
	StopReason string
	// OnStart, if set, is called once with the input-side usage as soon as
	// message_start arrives (PRD §10.4 step 2).
	OnStart func(Usage)

	line      []byte
	skipping  bool // inside a data line of an event we don't parse
	event     []byte
	wantEvent bool
}

// Write implements io.Writer and never fails.
func (s *UsageScanner) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		nl := bytes.IndexByte(p, '\n')
		chunk := p
		if nl >= 0 {
			chunk = p[:nl]
		}
		if !s.skipping {
			if len(s.line)+len(chunk) > maxLine {
				s.line, s.skipping = s.line[:0], true
			} else {
				s.line = append(s.line, chunk...)
				// A data line of an event we don't parse: stop buffering.
				if !s.wantEvent && len(s.line) >= 5 && bytes.HasPrefix(s.line, []byte("data:")) {
					s.line, s.skipping = s.line[:0], true
				}
			}
		}
		if nl < 0 {
			break
		}
		if !s.skipping {
			s.handleLine(bytes.TrimSuffix(s.line, []byte("\r")))
		}
		s.line, s.skipping = s.line[:0], false
		p = p[nl+1:]
	}
	return n, nil
}

func (s *UsageScanner) handleLine(line []byte) {
	switch {
	case len(line) == 0:
		s.event, s.wantEvent = s.event[:0], false
	case bytes.HasPrefix(line, []byte("event:")):
		s.event = append(s.event[:0], bytes.TrimSpace(line[6:])...)
		switch string(s.event) {
		case "message_start", "message_delta", "message_stop":
			s.wantEvent = true
		default:
			s.wantEvent = false
		}
	case bytes.HasPrefix(line, []byte("data:")) && s.wantEvent:
		s.parse(bytes.TrimSpace(line[5:]))
	}
}

func (s *UsageScanner) parse(data []byte) {
	switch string(s.event) {
	case "message_start":
		var v struct {
			Message struct {
				Usage Usage `json:"usage"`
			} `json:"message"`
		}
		if sonic.Unmarshal(data, &v) != nil {
			return
		}
		s.Usage.merge(v.Message.Usage)
		if !s.SawStart {
			s.SawStart = true
			if s.OnStart != nil {
				s.OnStart(s.Usage)
			}
		}
	case "message_delta":
		var v struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage Usage `json:"usage"`
		}
		if sonic.Unmarshal(data, &v) != nil {
			return
		}
		s.Usage.merge(v.Usage)
		if v.Delta.StopReason != "" {
			s.StopReason = v.Delta.StopReason
		}
	case "message_stop":
		s.SawStop = true
	}
}

// MessageUsage reads usage from a non-streaming Messages response body.
func MessageUsage(body []byte) (Usage, bool) {
	var v struct {
		Usage *Usage `json:"usage"`
	}
	if sonic.Unmarshal(body, &v) != nil || v.Usage == nil {
		return Usage{}, false
	}
	return *v.Usage, true
}
