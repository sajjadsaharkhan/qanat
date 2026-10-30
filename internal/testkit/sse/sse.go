// Package sse is a minimal Server-Sent Events codec used by Qanat tests.
//
// It keeps the raw bytes of every event so tests can assert byte-exact
// passthrough (REQ-PASS-04) as well as parsed fields.
package sse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// Event is one dispatched SSE event.
type Event struct {
	// Name is the value of the last "event:" field ("" if absent).
	Name string
	// Data is the joined value of all "data:" fields, separated by '\n'.
	Data []byte
	// Raw is every byte read for this event, including the blank-line terminator.
	Raw []byte
}

// Bytes encodes the event in canonical form: "event: <name>\ndata: <data>\n\n".
// Multi-line data is split into one "data:" line per line.
func (e Event) Bytes() []byte {
	var b bytes.Buffer
	if e.Name != "" {
		b.WriteString("event: ")
		b.WriteString(e.Name)
		b.WriteByte('\n')
	}
	for _, line := range bytes.Split(e.Data, []byte{'\n'}) {
		b.WriteString("data: ")
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// Reader parses an SSE stream incrementally. It never reads past the end of
// the current event, so callers can timestamp each event as it arrives.
type Reader struct {
	br *bufio.Reader
}

// NewReader wraps r.
func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReaderSize(r, 64*1024)}
}

// ErrIncomplete is returned when the stream ends in the middle of an event.
var ErrIncomplete = errors.New("sse: stream ended mid-event")

// Next returns the next dispatched event. It returns io.EOF at a clean end of
// stream and ErrIncomplete if the stream ends with a partial event.
// Comment lines (starting with ':') are kept in Raw but otherwise ignored.
func (r *Reader) Next() (Event, error) {
	var (
		ev      Event
		raw     bytes.Buffer
		data    bytes.Buffer
		hasData bool
	)
	for {
		line, err := r.br.ReadBytes('\n')
		raw.Write(line)
		if err != nil {
			if errors.Is(err, io.EOF) {
				pending := hasData || ev.Name != "" ||
					len(bytes.TrimRight(line, "\r\n")) > 0 && line[0] != ':'
				if !pending {
					return Event{}, io.EOF
				}
				return Event{}, ErrIncomplete
			}
			return Event{}, err
		}
		trimmed := bytes.TrimRight(line, "\r\n")
		if len(trimmed) == 0 {
			if !hasData && ev.Name == "" {
				// Blank line with nothing pending (a comment-only block or a
				// stray newline). Its bytes stay in Raw of the next event so
				// that concatenating Raw reconstructs the stream.
				continue
			}
			ev.Data = append([]byte(nil), data.Bytes()...)
			ev.Raw = append([]byte(nil), raw.Bytes()...)
			return ev, nil
		}
		if trimmed[0] == ':' {
			continue
		}
		field, value := trimmed, []byte(nil)
		if i := bytes.IndexByte(trimmed, ':'); i >= 0 {
			field = trimmed[:i]
			value = trimmed[i+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}
		switch string(field) {
		case "event":
			ev.Name = string(value)
		case "data":
			if hasData {
				data.WriteByte('\n')
			}
			data.Write(value)
			hasData = true
		}
	}
}

// ReadAll reads every event until EOF.
func ReadAll(r io.Reader) ([]Event, error) {
	rd := NewReader(r)
	var out []Event
	for {
		ev, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
}
