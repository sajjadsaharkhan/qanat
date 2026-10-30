// Package mockupstream provides a scripted fake LLM provider for tests.
//
// A Script describes exactly what the upstream sends and when: status,
// headers, and a timed sequence of raw byte chunks. The server records every
// request it receives (headers and body, byte for byte) and the instant each
// chunk was written, so tests can assert passthrough fidelity and measure the
// gateway's added latency against a known upstream timeline.
package mockupstream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Step is one chunk written by the upstream.
type Step struct {
	// Delay is waited before writing Data (relative to the previous step,
	// or to request arrival for the first step).
	Delay time.Duration
	// Data is written verbatim and flushed immediately.
	Data []byte
}

// Script is a full upstream response.
type Script struct {
	Status  int
	Headers map[string]string
	Steps   []Step
	// AbortAfter, if >= 0, closes the TCP connection abruptly after the step
	// with that index (simulates an upstream dying mid-stream).
	AbortAfter int
	// HoldOpen keeps the response open after the last step until the client
	// goes away (simulates a long reasoning gap or a hung upstream).
	HoldOpen bool
}

// NewScript returns a 200 text/event-stream script with no abort.
func NewScript(steps ...Step) Script {
	return Script{
		Status:     200,
		Headers:    map[string]string{"Content-Type": "text/event-stream"},
		Steps:      steps,
		AbortAfter: -1,
	}
}

// JSONResponse returns a non-streaming script with a single JSON body.
func JSONResponse(status int, body string) Script {
	return Script{
		Status:     status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Steps:      []Step{{Data: []byte(body)}},
		AbortAfter: -1,
	}
}

// Every returns steps that write each chunk `gap` apart.
func Every(gap time.Duration, chunks ...string) []Step {
	out := make([]Step, len(chunks))
	for i, c := range chunks {
		out[i] = Step{Delay: gap, Data: []byte(c)}
	}
	return out
}

// fixtureLine is one line of a recorded-stream fixture (JSON Lines).
//
//	{"type":"meta","status":200,"headers":{"Content-Type":"text/event-stream"}}
//	{"type":"chunk","at_ms":412,"data":"event: message_start\ndata: {...}\n\n"}
//
// at_ms is the offset from request arrival at which the chunk was received
// when the fixture was recorded. Fixtures must be scrubbed of tokens, account
// identifiers and prompt content before being committed.
type fixtureLine struct {
	Type    string            `json:"type"`
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	AtMS    float64           `json:"at_ms,omitempty"`
	Data    string            `json:"data,omitempty"`
}

// LoadFixture reads a recorded-stream fixture into a Script that replays it
// with the original timing. speed > 1 replays faster (2 = twice as fast);
// speed <= 0 replays with no delays.
func LoadFixture(path string, speed float64) (Script, error) {
	f, err := os.Open(path)
	if err != nil {
		return Script{}, err
	}
	defer f.Close()

	sc := NewScript()
	var prev float64
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for n := 1; s.Scan(); n++ {
		if len(s.Bytes()) == 0 {
			continue
		}
		var l fixtureLine
		if err := json.Unmarshal(s.Bytes(), &l); err != nil {
			return Script{}, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		switch l.Type {
		case "meta":
			if l.Status != 0 {
				sc.Status = l.Status
			}
			for k, v := range l.Headers {
				sc.Headers[k] = v
			}
		case "chunk":
			d := time.Duration(0)
			if speed > 0 {
				d = time.Duration((l.AtMS - prev) / speed * float64(time.Millisecond))
			}
			prev = l.AtMS
			sc.Steps = append(sc.Steps, Step{Delay: d, Data: []byte(l.Data)})
		default:
			return Script{}, fmt.Errorf("%s:%d: unknown line type %q", path, n, l.Type)
		}
	}
	return sc, s.Err()
}
