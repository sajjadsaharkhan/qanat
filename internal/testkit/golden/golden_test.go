package golden

import (
	"strings"
	"testing"

	"qanat/internal/testkit/sse"
)

func TestNormalizeSortsKeysAndScrubs(t *testing.T) {
	evs := []sse.Event{
		{Name: "message_start", Data: []byte(`{"type":"message_start","message":{"model":"m","id":"msg_123"}}`)},
		{Name: "ping", Data: []byte(`not json`)},
	}
	got := string(Normalize(evs, DefaultScrub))
	want := "event: message_start\ndata: {\"message\":{\"id\":\"<scrubbed>\",\"model\":\"m\"},\"type\":\"message_start\"}\n\n" +
		"event: ping\ndata: not json\n\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestAssertEventsAgainstCommittedGolden(t *testing.T) {
	evs, err := sse.ReadAll(strings.NewReader(
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_9\"}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	AssertEvents(t, "example.events", evs)
}
