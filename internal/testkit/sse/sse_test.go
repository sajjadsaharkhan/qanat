package sse

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReaderParsesEventsAndKeepsRaw(t *testing.T) {
	stream := "event: message_start\ndata: {\"a\":1}\n\n" +
		": keepalive comment\n\n" +
		"event: ping\ndata: {}\n\n" +
		"data: line1\ndata: line2\n\n"
	evs, err := ReadAll(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3 {
		t.Fatalf("want 3 events, got %d", len(evs))
	}
	if evs[0].Name != "message_start" || string(evs[0].Data) != `{"a":1}` {
		t.Fatalf("bad first event: %+v", evs[0])
	}
	if string(evs[2].Data) != "line1\nline2" {
		t.Fatalf("multi-line data not joined: %q", evs[2].Data)
	}
	var joined bytes.Buffer
	for _, e := range evs {
		joined.Write(e.Raw)
	}
	// Concatenated Raw reconstructs the input byte for byte, comments included.
	if joined.String() != stream {
		t.Fatalf("raw bytes not preserved:\n%q\nvs\n%q", joined.String(), stream)
	}
}

func TestReaderIncompleteEvent(t *testing.T) {
	_, err := ReadAll(strings.NewReader("event: x\ndata: partial"))
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("want ErrIncomplete, got %v", err)
	}
}

func TestEventBytesRoundTrip(t *testing.T) {
	in := Event{Name: "content_block_delta", Data: []byte("{\"x\":\"y\"}")}
	evs, err := ReadAll(bytes.NewReader(in.Bytes()))
	if err != nil || len(evs) != 1 {
		t.Fatalf("round trip failed: %v %d", err, len(evs))
	}
	if evs[0].Name != in.Name || !bytes.Equal(evs[0].Data, in.Data) {
		t.Fatalf("mismatch: %+v", evs[0])
	}
}
