// Package golden compares outputs against committed golden files.
//
// Translator tests (Seam B) feed a recorded upstream stream through a
// translator and compare the resulting Anthropic event sequence with a golden
// file. Run with QANAT_UPDATE_GOLDEN=1 to rewrite goldens after an intended
// change; review the diff like any other code change.
package golden

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qanat/internal/testkit/sse"
)

// Updating reports whether goldens should be rewritten.
func Updating() bool { return os.Getenv("QANAT_UPDATE_GOLDEN") == "1" }

// Path returns testdata/golden/<name>.
func Path(name string) string { return filepath.Join("testdata", "golden", name) }

// Assert compares got with the golden file name.
func Assert(t testing.TB, name string, got []byte) {
	t.Helper()
	p := Path(name)
	if Updating() {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("golden %s missing (run with QANAT_UPDATE_GOLDEN=1 to create): %v", p, err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("golden mismatch for %s\n--- want\n%s\n--- got\n%s", p, want, got)
	}
}

// DefaultScrub lists JSON keys whose string values vary between runs and are
// replaced with "<scrubbed>" before comparison.
var DefaultScrub = []string{"id", "signature"}

// AssertEvents normalizes an event sequence and compares it with a golden.
// Normalization: JSON data is re-encoded with sorted keys and volatile keys
// scrubbed; non-JSON data is kept verbatim. Event order is significant.
func AssertEvents(t testing.TB, name string, events []sse.Event, scrub ...string) {
	t.Helper()
	if scrub == nil {
		scrub = DefaultScrub
	}
	Assert(t, name, Normalize(events, scrub))
}

// Normalize renders events in a stable, diff-friendly form.
func Normalize(events []sse.Event, scrub []string) []byte {
	keys := map[string]bool{}
	for _, k := range scrub {
		keys[k] = true
	}
	var b bytes.Buffer
	for _, ev := range events {
		b.WriteString("event: ")
		b.WriteString(ev.Name)
		b.WriteString("\ndata: ")
		var v any
		if err := json.Unmarshal(ev.Data, &v); err == nil {
			var out bytes.Buffer
			enc := json.NewEncoder(&out) // map keys are sorted by encoding/json
			enc.SetEscapeHTML(false)
			enc.Encode(scrubValue(v, keys))
			b.Write(bytes.TrimRight(out.Bytes(), "\n"))
		} else {
			b.WriteString(strings.ReplaceAll(string(ev.Data), "\n", "\\n"))
		}
		b.WriteString("\n\n")
	}
	return b.Bytes()
}

func scrubValue(v any, keys map[string]bool) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if _, isStr := val.(string); isStr && keys[k] {
				x[k] = "<scrubbed>"
				continue
			}
			x[k] = scrubValue(val, keys)
		}
		return x
	case []any:
		for i := range x {
			x[i] = scrubValue(x[i], keys)
		}
		return x
	default:
		return v
	}
}
