package acceptance

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"sort"
	"testing"
	"time"
)

// REQ-ERR-01
func TestAuth_UnknownKeyIsRejected(t *testing.T) {
	e := setup(t)
	e.key = "qnt_live_not-a-real-key"
	resp := e.post(t, context.Background(), quirkyBody, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if typ, _ := anthropicError(t, resp); typ != "authentication_error" {
		t.Fatalf("error type %q", typ)
	}
	if e.up.Last() != nil {
		t.Fatal("upstream called for an unauthenticated request")
	}
}

// REQ-ERR-03, REQ-ACCESS-01
func TestAccess_DisallowedModelIs403WithoutFallback(t *testing.T) {
	e := setup(t)
	must(t, e.d.SetPlan(context.Background(), e.user, Plan{DailyUSD: 100, AllowedModels: []string{"claude-sonnet-4-6"}}))

	body := `{"model":"claude-opus-4-7","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"x"}]}`
	resp := e.post(t, context.Background(), body, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if typ, _ := anthropicError(t, resp); typ != "permission_error" {
		t.Fatalf("error type %q", typ)
	}
	if e.up.Last() != nil {
		t.Fatal("REQ-ERR-03: request was forwarded instead of rejected")
	}
}

// REQ-ACCESS-02
func TestAccess_ModelsListIsFilteredByPlan(t *testing.T) {
	e := setup(t)
	must(t, e.d.SetPlan(context.Background(), e.user, Plan{DailyUSD: 100, AllowedModels: []string{"sonnet", "claude-sonnet-4-6"}}))

	req, _ := http.NewRequest(http.MethodGet, e.d.GatewayURL()+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	defer resp.Body.Close()

	var list struct {
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"data"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("bad /v1/models body: %s", raw)
	}
	var ids []string
	for _, m := range list.Data {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "claude-sonnet-4-6" || ids[1] != "sonnet" {
		t.Fatalf("REQ-ACCESS-02: got %v", ids)
	}
}

// REQ-PLAN-03, REQ-ERR-02
func TestQuota_ZeroQuotaIs429WithRetryAfter(t *testing.T) {
	e := setup(t)
	must(t, e.d.SetPlan(context.Background(), e.user, Plan{DailyUSD: 0}))

	resp := e.post(t, context.Background(), quirkyBody, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp.Header.Get("retry-after") == "" {
		t.Fatal("REQ-ERR-02: missing retry-after")
	}
	if typ, _ := anthropicError(t, resp); typ != "rate_limit_error" {
		t.Fatalf("error type %q", typ)
	}
	if e.up.Last() != nil {
		t.Fatal("upstream called despite zero quota")
	}
}

// PRD §10.1, REQ-QUOTA-01: usage is recorded from provider-reported usage and
// priced with the model price table. The pipeline is async, so poll.
func TestUsage_RecordedAndPriced(t *testing.T) {
	e := setup(t)
	e.up.Enqueue(msgPath, fullStream(0)) // 1000 input tokens, 500 output tokens

	resp := e.post(t, context.Background(), quirkyBody, nil)
	io.Copy(io.Discard, resp.Body)

	wantCost := (1000*3.0 + 500*15.0) / 1e6
	var u Usage
	ok := eventually(t, 5*time.Second, func() bool {
		var err error
		u, err = e.d.Usage(context.Background(), e.user)
		return err == nil && u.InputTokens == 1000 && u.OutputTokens == 500
	})
	if !ok {
		t.Fatalf("usage not recorded within 5s: %+v", u)
	}
	if math.Abs(u.CostUSD-wantCost) > 1e-9 {
		t.Fatalf("cost = %v, want %v", u.CostUSD, wantCost)
	}
}
