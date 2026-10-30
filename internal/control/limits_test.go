package control

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"qanat/internal/core/secretbox"
)

// Provider limits: a read is cached, Refresh skips the cache, a rate-limited
// usage API keeps the last good read and is left alone for a while, and API
// keys report no limits.
func TestAccountLimits_CacheForceCooldown(t *testing.T) {
	h := newHarness(t)
	box, err := secretbox.New(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	h.s.opts.Box = box
	c := h.signInRoot()

	var hits atomic.Int32
	status := atomic.Int32{}
	status.Store(http.StatusOK)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/usage" || r.Header.Get("Authorization") != "Bearer tok-1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		hits.Add(1)
		w.WriteHeader(int(status.Load()))
		w.Write([]byte(`{"five_hour":{"utilization":25,"resets_at":"2026-11-01T00:00:00Z"},"seven_day":{"utilization":60}}`))
	}))
	defer up.Close()

	var acct, keyAcct struct {
		ID string `json:"id"`
	}
	if st := h.do(c, "POST", "/api/admin/accounts", `{"provider":"claude","label":"cc","access_token":"tok-1","base_url":"`+up.URL+`","proxy":"direct"}`, &acct); st != 201 {
		t.Fatalf("create: %d", st)
	}
	if st := h.do(c, "POST", "/api/admin/accounts", `{"provider":"claude","label":"key","auth_kind":"api_key","access_token":"sk-1","proxy":"direct"}`, &keyAcct); st != 201 {
		t.Fatalf("create key: %d", st)
	}
	t.Cleanup(func() {
		h.s.rdb.Del(context.Background(), limitsKey(acct.ID), limitsCooldownKey(acct.ID))
		h.s.rdb.HDel(context.Background(), HeadroomKey, acct.ID)
	})

	path := "/api/admin/accounts/" + acct.ID + "/limits"
	var res accountLimitsResponse
	if st := h.do(c, "GET", path, "", &res); st != 200 || !res.Supported || res.Stale || res.FetchedAt == nil || len(res.Windows) != 2 || res.Windows[0].UsedPercent != 25 {
		t.Fatalf("first read: %d %+v", st, res)
	}
	// The read is published for the gateway: the tightest window (weekly, 60% used).
	if v := h.s.rdb.HGet(context.Background(), HeadroomKey, acct.ID).Val(); !strings.HasPrefix(v, "40.00|") {
		t.Fatalf("published headroom: %q", v)
	}
	h.do(c, "GET", path, "", &res)
	if hits.Load() != 1 {
		t.Fatalf("second read must come from the cache: %d upstream hits", hits.Load())
	}
	h.do(c, "GET", path+"?force=1", "", &res)
	if hits.Load() != 2 {
		t.Fatalf("force must ask the provider: %d upstream hits", hits.Load())
	}

	status.Store(http.StatusTooManyRequests)
	res = accountLimitsResponse{}
	if st := h.do(c, "GET", path+"?force=1", "", &res); st != 200 || !res.Stale || res.Error == "" || len(res.Windows) != 2 {
		t.Fatalf("429 keeps the last good read: %d %+v", st, res)
	}
	h.do(c, "GET", path+"?force=1", "", &res)
	if hits.Load() != 3 || !res.Stale {
		t.Fatalf("cooldown must skip the provider: %d upstream hits, %+v", hits.Load(), res)
	}

	res = accountLimitsResponse{}
	if st := h.do(c, "GET", "/api/admin/accounts/"+keyAcct.ID+"/limits", "", &res); st != 200 || res.Supported || res.Windows == nil {
		t.Fatalf("api key: %d %+v", st, res)
	}
	if st := h.do(c, "GET", "/api/admin/accounts/00000000-0000-0000-0000-000000000000/limits", "", nil); st != 404 {
		t.Fatalf("unknown account: %d", st)
	}
}

// Provider routing: defaults without a row, validation, and a saved setting.
func TestProviderRouting(t *testing.T) {
	h := newHarness(t)
	c := h.signInRoot()
	h.s.pg.Exec(context.Background(), `DELETE FROM provider_routing`)
	t.Cleanup(func() { h.s.pg.Exec(context.Background(), `DELETE FROM provider_routing`) })

	var list []providerRoutingResponse
	if st := h.do(c, "GET", "/api/admin/provider-routing", "", &list); st != 200 || len(list) != 3 {
		t.Fatalf("list: %d %+v", st, list)
	}
	for _, r := range list {
		if r.Strategy != "sticky-balanced" || r.StickyLimit != 3 || r.MinHeadroomPct != 5 {
			t.Fatalf("defaults: %+v", r)
		}
	}
	for _, bad := range []string{
		`{"strategy":"random","sticky_limit":3,"min_headroom_pct":5}`,
		`{"strategy":"round-robin","sticky_limit":0,"min_headroom_pct":5}`,
		`{"strategy":"round-robin","sticky_limit":3,"min_headroom_pct":101}`,
	} {
		if st := h.do(c, "PUT", "/api/admin/provider-routing/claude", bad, nil); st != 400 {
			t.Fatalf("%s: %d", bad, st)
		}
	}
	if st := h.do(c, "PUT", "/api/admin/provider-routing/nope", `{"strategy":"fill-first","sticky_limit":1,"min_headroom_pct":0}`, nil); st != 404 {
		t.Fatalf("unknown provider: %d", st)
	}
	if st := h.do(c, "PUT", "/api/admin/provider-routing/claude", `{"strategy":"round-robin","sticky_limit":3,"min_headroom_pct":5}`, nil); st != 200 {
		t.Fatalf("round-robin: %d", st)
	}
	var saved providerRoutingResponse
	if st := h.do(c, "PUT", "/api/admin/provider-routing/codex", `{"strategy":"fill-first","sticky_limit":10,"min_headroom_pct":15}`, &saved); st != 200 || saved.Strategy != "fill-first" {
		t.Fatalf("save: %d %+v", st, saved)
	}
	list = nil
	h.do(c, "GET", "/api/admin/provider-routing", "", &list)
	for _, r := range list {
		if r.Provider == "codex" && (r.Strategy != "fill-first" || r.StickyLimit != 10 || r.MinHeadroomPct != 15) {
			t.Fatalf("after save: %+v", r)
		}
	}
}
