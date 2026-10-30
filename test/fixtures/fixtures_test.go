// Package fixtures validates every recorded upstream stream in this tree.
// A malformed or unscrubbed fixture fails CI before it can mislead a
// translator golden test.
package fixtures

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"qanat/internal/testkit/mockupstream"
	"qanat/internal/testkit/sse"
)

// Patterns that must never appear in a committed fixture.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{10,}`),                 // Anthropic keys / OAuth tokens
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), // JWTs
	regexp.MustCompile(`ya29\.[A-Za-z0-9_-]{10,}`),                  // Google OAuth access tokens
	regexp.MustCompile(`qnt_live_[A-Za-z0-9]{8,}`),                  // Qanat keys
	regexp.MustCompile(`(?i)"authorization"\s*:`),                   // recorded auth headers
}

func TestFixturesLoadParseAndAreScrubbed(t *testing.T) {
	var n int
	err := filepath.WalkDir("upstream", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return err
		}
		n++
		t.Run(p, func(t *testing.T) {
			sc, err := mockupstream.LoadFixture(p, 0)
			if err != nil {
				t.Fatal(err)
			}
			if sc.Status != 200 && sc.Status != 429 && sc.Status < 500 {
				t.Fatalf("unexpected status %d", sc.Status)
			}
			var all bytes.Buffer
			for _, st := range sc.Steps {
				all.Write(st.Data)
			}
			evs, err := sse.ReadAll(bytes.NewReader(all.Bytes()))
			if err != nil {
				t.Fatalf("stream does not parse as SSE: %v", err)
			}
			if len(evs) == 0 {
				t.Fatal("no events")
			}
			for _, re := range secretPatterns {
				if re.Match(all.Bytes()) {
					t.Fatalf("fixture contains a secret-like value matching %s", re)
				}
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no fixtures found")
	}
}
