// Command benchseed prepares the test stack for the latency gate (make bench)
// and prints a fresh API key on stdout. It uses the control plane's test-only
// endpoints, so it works only against a stack running with QANAT_TEST_MODE=1.
//
// It seeds: a bench user on a generous Plan, an account pointing at the
// compose mockupstream, a claude-* mapping and a price for the bench model.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	control  = flag.String("control", "http://localhost:58081", "control plane base URL")
	gateway  = flag.String("gateway", "http://localhost:58080", "gateway base URL (to wait for the config to apply)")
	upstream = flag.String("upstream", "http://mockupstream:9090", "mock upstream URL as the gateway sees it")
	client   = &http.Client{Timeout: 10 * time.Second}
)

func call(method, url, cookie string, in, out any) {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Qanat-Request", "1")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		fail("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fail("%s %s: %d %s", method, url, resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			fail("%s %s: %v", method, url, err)
		}
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "benchseed: "+format+"\n", a...)
	os.Exit(1)
}

func user(email, role string) (id, cookie string) {
	var u struct{ ID string }
	call("POST", *control+"/test/users", "", map[string]string{"email": email, "role": role}, &u)
	var s struct{ Cookie string }
	call("POST", *control+"/test/session", "", map[string]string{"user_id": u.ID}, &s)
	cookie, _, _ = strings.Cut(s.Cookie, ";")
	return u.ID, cookie
}

func main() {
	flag.Parse()
	suffix := time.Now().Format("150405.000")
	_, admin := user("bench-admin-"+suffix+"@example.com", "admin")
	benchID, bench := user("bench-"+suffix+"@example.com", "user")

	call("POST", *control+"/api/admin/accounts", admin, map[string]any{"provider": "claude", "label": "bench mock", "base_url": *upstream}, nil)
	call("PUT", *control+"/api/admin/mappings", admin, []map[string]any{{"pattern": "claude-*", "provider": "claude", "upstream_model": nil, "priority": 0}}, nil)
	call("POST", *control+"/api/admin/prices", admin, map[string]any{"upstream_model": "claude-sonnet-4-6", "p_in": 3, "p_out": 15, "p_cache_read": 0.3, "p_cache_write": 3.75}, nil)
	var plan struct{ ID string }
	call("POST", *control+"/api/admin/plans", admin, map[string]any{
		"name": "bench-" + suffix, "timezone": "UTC", "daily_anchor": "00:00", "weekly_anchor": 6, "monthly_anchor": 1,
		"quotas": []map[string]any{{"window": "daily", "model_scope": nil, "limit_usd": 1_000_000}},
		"limits": map[string]int{"rpm": 0, "max_concurrent": 0}, "models": []string{},
	}, &plan)
	call("PATCH", *control+"/api/admin/users/"+benchID, admin, map[string]string{"plan_id": plan.ID}, nil)
	var key struct{ Key string }
	call("POST", *control+"/api/me/keys", bench, map[string]string{"name": "bench"}, &key)

	// Wait for the gateway to apply the config before handing out the key.
	var want, got struct{ Generation int64 }
	call("GET", *control+"/test/generation", "", nil, &want)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		call("GET", *gateway+"/test/generation", "", nil, &got)
		if got.Generation >= want.Generation {
			break
		}
		if time.Now().After(deadline) {
			fail("gateway did not apply the bench config")
		}
	}
	fmt.Println(key.Key)
}
