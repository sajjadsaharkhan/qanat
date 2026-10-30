package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"qanat/internal/testkit/proxytest"
)

// REQ-PROXY-03: a pair turns unhealthy after FailAfter failed rounds in a
// row and healthy again after one success; unchecked pairs and direct count
// as healthy.
func TestHealth_ThresholdAndRecovery(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer up.Close()
	tp := proxytest.New(t, "u", "pw")
	good := spec(tp, "pw")
	bad := &Spec{ID: "bad", Type: "http", Host: "127.0.0.1", Port: 1} // nothing listens
	origin := Origin(up.URL)

	h := NewHealth()
	var last []CheckResult
	h.OnRound = func(rs []CheckResult) { last = rs }
	checks := []Check{{good, origin}, {bad, origin}}

	if !h.Healthy(bad, origin) || !h.Healthy(nil, origin) {
		t.Fatal("unchecked and direct must count as healthy")
	}
	h.Round(context.Background(), checks)
	if !h.Healthy(bad, origin) || last[1].OK || !last[1].Healthy {
		t.Fatalf("one failure must not flip health: %+v", last[1])
	}
	h.Round(context.Background(), checks)
	if h.Healthy(bad, origin) || !h.Healthy(good, origin) || last[1].Healthy || last[1].Error == "" {
		t.Fatalf("two failures must: %+v", last)
	}
	if tp.Hits() != 2 {
		t.Fatalf("checks must go through the proxy: %d hits", tp.Hits())
	}

	// The proxy comes back: one success restores it.
	h.Round(context.Background(), []Check{{good, origin}, {&Spec{ID: "bad", Type: "http", Host: good.Host, Port: good.Port, Username: "u", Password: "pw"}, origin}})
	if !h.Healthy(bad, origin) {
		t.Fatal("one success must restore health")
	}

	// Wrong password: the proxy answers 407, which is a failure.
	h.FailAfter = 1
	h.Round(context.Background(), []Check{{spec(tp, "wrong"), origin}})
	if h.Healthy(good, origin) || last[0].Error == "" {
		t.Fatalf("407 must be unhealthy: %+v", last[0])
	}
}

// §8.1 sticky: the same account always lands on the same member, accounts
// spread over members, and removing a member moves only its accounts.
func TestPick_StableAndSpread(t *testing.T) {
	a, b, c := &Spec{ID: "a"}, &Spec{ID: "b"}, &Spec{ID: "c"}
	all := []*Spec{a, b, c}
	count := map[string]int{}
	moved := 0
	for i := range 300 {
		acct := "acct-" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		p := Pick(acct, all)
		if Pick(acct, []*Spec{c, b, a}) != p {
			t.Fatal("member order must not matter")
		}
		count[p.ID]++
		if q := Pick(acct, []*Spec{a, b}); p != c && q != p {
			moved++
		}
	}
	if moved != 0 {
		t.Fatalf("%d accounts moved though their proxy stayed", moved)
	}
	for id, n := range count {
		if n < 60 {
			t.Fatalf("poor spread: %v (%s)", count, id)
		}
	}
}

func TestOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.anthropic.com": "https://api.anthropic.com",
		"http://mock:9090/base/":    "http://mock:9090",
		"not a url":                 "",
	} {
		if got := Origin(in); got != want {
			t.Errorf("Origin(%q) = %q, want %q", in, got, want)
		}
	}
}
