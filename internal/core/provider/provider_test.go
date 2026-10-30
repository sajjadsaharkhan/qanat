package provider

import "testing"

func TestSplit(t *testing.T) {
	cases := []struct {
		in, id, model string
		ok            bool
	}{
		{"cc/claude-opus-4-7", "claude", "claude-opus-4-7", true},
		{"cx/gpt-5.5", "codex", "gpt-5.5", true},
		{"ag/gemini-3-pro", "antigravity", "gemini-3-pro", true},
		{"ag/models/gemini/x", "antigravity", "models/gemini/x", true}, // only the first slash splits
		{"cc/", "", "", false},
		{"zz/gpt-5.5", "", "", false},
		{"claude-opus-4-7", "", "", false},
	}
	for _, c := range cases {
		id, m, ok := Split(c.in)
		if id != c.id || m != c.model || ok != c.ok {
			t.Errorf("%s → %q %q %v", c.in, id, m, ok)
		}
	}
	if Bare("cc/opus") != "opus" || Bare("zz/opus") != "zz/opus" {
		t.Error("Bare")
	}
}

func TestServes(t *testing.T) {
	cl, _ := Get("claude")
	cx, _ := Get("codex")
	ag, _ := Get("antigravity")
	if !cl.Serves("claude-opus-4-7") || cl.Serves("gpt-5.5") || !cx.Serves("gpt-5.5") || !ag.Serves("gemini-3-pro") || !ag.Serves("claude-sonnet-4-6") {
		t.Fatal("families")
	}
}
