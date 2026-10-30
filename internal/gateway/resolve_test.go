package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Claude Code's "[1m]" marker selects a context window: the model is routed,
// priced and forwarded under its name without it.
func TestResolve_StripsContextSuffix(t *testing.T) {
	s := testSnapshot()
	c := &caller{snap: s, user: &User{ID: "u1"}, key: &Key{}, plan: &Plan{}}
	for _, name := range []string{"claude-sonnet-4-6[1m]", "cc/claude-sonnet-4-6[1M]"} {
		body := `{"model":"` + name + `","max_tokens":1}`
		w := httptest.NewRecorder()
		rt, ok := newTestGateway().resolve(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body)), c, "messages")
		if !ok {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
		want := strings.TrimSuffix(strings.TrimSuffix(name, "[1m]"), "[1M]")
		if rt.name != want || string(rt.body[rt.mStart:rt.mEnd]) != want {
			t.Fatalf("%s: name %q, body model %q", name, rt.name, rt.body[rt.mStart:rt.mEnd])
		}
		if !strings.Contains(string(rt.body), `"max_tokens":1`) {
			t.Fatalf("rest of the body changed: %s", rt.body)
		}
	}
	if _, ok := contextBase("[1m]"); ok {
		t.Fatal("a bare suffix is not a model name")
	}
}
