//go:build qanat

// Driver for the real Qanat stack (deploy/test/compose.test.yml, profile
// "stack"). It talks to the gateway through Nginx and seeds state through the
// control plane's Admin API plus its test-only endpoints, which exist only
// when QANAT_TEST_MODE=1.
//
// The gateway applies config asynchronously (LISTEN/NOTIFY), so after every
// change the driver waits until the gateway's snapshot has reached the
// database's config generation. Tests never race a reload.
//
// Mock upstreams run on the host; the gateway reaches them as
// host.docker.internal (make test-acceptance binds them to all interfaces).
package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func init() {
	RegisterDriver(func(t testing.TB) Driver {
		gw, ctl := os.Getenv("QANAT_ACCEPTANCE_GATEWAY"), os.Getenv("QANAT_ACCEPTANCE_CONTROL")
		if gw == "" || ctl == "" {
			t.Fatal("QANAT_ACCEPTANCE_GATEWAY and QANAT_ACCEPTANCE_CONTROL must be set (use `make test-acceptance`)")
		}
		return &qanatDriver{t: t, gateway: gw, control: ctl, http: &http.Client{Timeout: 10 * time.Second}}
	})
}

type qanatDriver struct {
	t                testing.TB
	gateway, control string
	http             *http.Client
	admin            string // session cookie of the driver's admin
	plans            int
}

func (d *qanatDriver) GatewayURL() string { return d.gateway }

// call sends a JSON request to the control plane and decodes the response.
func (d *qanatDriver) call(ctx context.Context, method, path, cookie string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.control+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Qanat-Request", "1")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (d *qanatDriver) generation(ctx context.Context, base string) (int64, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/test/generation", nil)
	resp, err := d.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var g struct{ Generation int64 }
	return g.Generation, json.NewDecoder(resp.Body).Decode(&g)
}

// synced blocks until the gateway has loaded the latest config.
func (d *qanatDriver) synced(ctx context.Context) error {
	want, err := d.generation(ctx, d.control)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := d.generation(ctx, d.gateway)
		if err == nil && got >= want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gateway config generation %d never reached %d (err %v)", got, want, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (d *qanatDriver) session(ctx context.Context, userID string) (string, error) {
	var s struct{ Cookie string }
	if err := d.call(ctx, http.MethodPost, "/test/session", "", map[string]string{"user_id": userID}, &s); err != nil {
		return "", err
	}
	cookie, _, _ := strings.Cut(s.Cookie, ";")
	return cookie, nil
}

func (d *qanatDriver) Reset(ctx context.Context) error {
	if err := d.call(ctx, http.MethodPost, "/test/reset", "", nil, nil); err != nil {
		return err
	}
	var u struct{ ID string }
	if err := d.call(ctx, http.MethodPost, "/test/users", "", map[string]string{"email": "driver-admin@example.com", "role": "admin"}, &u); err != nil {
		return err
	}
	var err error
	if d.admin, err = d.session(ctx, u.ID); err != nil {
		return err
	}
	return d.synced(ctx)
}

func (d *qanatDriver) CreateUser(ctx context.Context, email string) (string, error) {
	var u struct{ ID string }
	if err := d.call(ctx, http.MethodPost, "/test/users", "", map[string]string{"email": email}, &u); err != nil {
		return "", err
	}
	return u.ID, d.synced(ctx)
}

func (d *qanatDriver) SetPlan(ctx context.Context, userID string, p Plan) error {
	d.plans++
	quotas := []map[string]any{{"window": "daily", "model_scope": nil, "limit_usd": p.DailyUSD}}
	models := p.AllowedModels
	if models == nil {
		models = []string{}
	}
	body := map[string]any{
		"name": fmt.Sprintf("acceptance-%d", d.plans), "timezone": "Asia/Tehran", "daily_anchor": "00:00",
		"weekly_anchor": 6, "monthly_anchor": 1, "quotas": quotas,
		"limits": map[string]int{"rpm": p.RPM, "max_concurrent": p.MaxConcurrent}, "models": models,
	}
	var plan struct{ ID string }
	if err := d.call(ctx, http.MethodPost, "/api/admin/plans", d.admin, body, &plan); err != nil {
		return err
	}
	if err := d.call(ctx, http.MethodPatch, "/api/admin/users/"+userID, d.admin, map[string]string{"plan_id": plan.ID}, nil); err != nil {
		return err
	}
	return d.synced(ctx)
}

func (d *qanatDriver) CreateKey(ctx context.Context, userID string) (string, error) {
	cookie, err := d.session(ctx, userID)
	if err != nil {
		return "", err
	}
	var k struct{ Key string }
	if err := d.call(ctx, http.MethodPost, "/api/me/keys", cookie, map[string]string{"name": "acceptance"}, &k); err != nil {
		return "", err
	}
	return k.Key, d.synced(ctx)
}

// AddUpstreamAccount registers a mock upstream running on the host.
func (d *qanatDriver) AddUpstreamAccount(ctx context.Context, provider, baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	u.Host = "host.docker.internal:" + u.Port()
	var a struct{ ID string }
	body := map[string]any{"provider": provider, "label": "mock " + u.Port(), "base_url": u.String(), "access_token": "oauth-access-mock"}
	if err := d.call(ctx, http.MethodPost, "/api/admin/accounts", d.admin, body, &a); err != nil {
		return "", err
	}
	return a.ID, d.synced(ctx)
}

func (d *qanatDriver) SetMapping(ctx context.Context, rules []MappingRule) error {
	out := make([]map[string]any, 0, len(rules))
	for i, r := range rules {
		var model any
		if r.Model != "" {
			model = r.Model
		}
		out = append(out, map[string]any{"pattern": r.Pattern, "provider": r.Provider, "upstream_model": model, "priority": i})
	}
	if err := d.call(ctx, http.MethodPut, "/api/admin/mappings", d.admin, out, nil); err != nil {
		return err
	}
	return d.synced(ctx)
}

func (d *qanatDriver) SetPrice(ctx context.Context, model string, p Price) error {
	// Only models a provider's catalog lists can be priced.
	if err := d.call(ctx, http.MethodPost, "/test/provider-models", "", map[string]any{"provider": "claude", "model": model}, nil); err != nil {
		return err
	}
	body := map[string]any{"model_id": model, "p_in": p.Input, "p_out": p.Output, "p_cache_read": p.CacheRead, "p_cache_write": p.CacheWrite}
	if err := d.call(ctx, http.MethodPost, "/api/admin/prices", d.admin, body, nil); err != nil {
		return err
	}
	return d.synced(ctx)
}

func (d *qanatDriver) Usage(ctx context.Context, userID string) (Usage, error) {
	if err := d.call(ctx, http.MethodPost, "/test/flush-usage", "", nil, nil); err != nil {
		return Usage{}, err
	}
	var u struct {
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	}
	err := d.call(ctx, http.MethodGet, "/api/admin/users/"+userID+"/usage", d.admin, nil, &u)
	return Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CostUSD: u.CostUSD}, err
}
