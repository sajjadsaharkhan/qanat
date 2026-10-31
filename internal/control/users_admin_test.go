package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"qanat/internal/core/quota"
)

// signInAs starts a dashboard session for an existing user id.
func (h *harness) signInAs(id string) *http.Client {
	h.t.Helper()
	rec := httptest.NewRecorder()
	if err := h.s.startSession(context.Background(), rec, id); err != nil {
		h.t.Fatal(err)
	}
	c := h.client()
	u, _ := url.Parse(h.srv.URL)
	c.Jar.SetCookies(u, rec.Result().Cookies())
	return c
}

// Disable is reversible and keeps keys; a ban revokes every key and needs a
// reason. Either way the user's session stops working.
func TestAdmin_DisableAndBan(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	root := h.signInRoot()
	var me meResponse
	h.do(root, "GET", "/api/me", "", &me)

	var id string
	if err := h.s.pg.QueryRow(ctx, `INSERT INTO users (email, plan_id) SELECT 'dev@example.com', id FROM plans WHERE is_default RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"laptop", "ci"} {
		if _, err := h.s.pg.Exec(ctx, `INSERT INTO api_keys (user_id, name, hash, prefix) VALUES ($1, $2, $3, 'qk_')`, id, name, []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	dev := h.signInAs(id)
	if st := h.do(dev, "GET", "/api/me", "", nil); st != 200 {
		t.Fatalf("dev session before disable: %d", st)
	}

	if st := h.do(root, "POST", "/api/admin/users/"+me.ID+"/disable", `{}`, nil); st != 400 {
		t.Fatalf("disabling yourself (root): %d", st)
	}
	if st := h.do(root, "POST", "/api/admin/users/"+id+"/disable", `{"reason":"on leave"}`, nil); st != 204 {
		t.Fatalf("disable: %d", st)
	}
	if st := h.do(dev, "GET", "/api/me", "", nil); st != 401 {
		t.Fatalf("dev session after disable: %d", st)
	}
	user := func() userResponse {
		var users []userResponse
		h.do(root, "GET", "/api/admin/users", "", &users)
		for _, u := range users {
			if u.ID == id {
				return u
			}
		}
		t.Fatal("user missing")
		return userResponse{}
	}
	if u := user(); u.DisabledAt == nil || u.BannedAt != nil || u.DisabledReason != "on leave" || u.ActiveKeys != 2 {
		t.Fatalf("disabled user %+v", u)
	}

	if st := h.do(root, "POST", "/api/admin/users/"+id+"/enable", "", nil); st != 204 {
		t.Fatalf("enable: %d", st)
	}
	if u := user(); u.DisabledAt != nil || u.ActiveKeys != 2 {
		t.Fatalf("enabled user %+v", u)
	}

	if st := h.do(root, "POST", "/api/admin/users/"+id+"/disable", `{"ban":true,"reason":"  "}`, nil); st != 400 {
		t.Fatalf("ban without reason: %d", st)
	}
	if st := h.do(root, "POST", "/api/admin/users/"+id+"/disable", `{"ban":true,"reason":"shared a key"}`, nil); st != 204 {
		t.Fatalf("ban: %d", st)
	}
	if u := user(); u.DisabledAt == nil || u.BannedAt == nil || u.ActiveKeys != 0 {
		t.Fatalf("banned user %+v", u)
	}
	h.do(root, "POST", "/api/admin/users/"+id+"/enable", "", nil)
	if u := user(); u.DisabledAt != nil || u.BannedAt != nil || u.ActiveKeys != 0 {
		t.Fatalf("unbanned user keeps keys revoked: %+v", u)
	}

	var audit []auditEntry
	h.do(root, "GET", "/api/admin/audit", "", &audit)
	var actions []string
	for _, e := range audit {
		actions = append(actions, e.Action)
	}
	for _, want := range []string{"user.disable", "user.enable", "user.ban"} {
		if !strings.Contains(strings.Join(actions, ","), want) {
			t.Fatalf("audit %v lacks %s", actions, want)
		}
	}
}

// A reset clears only the chosen windows' counters.
func TestAdmin_ResetQuota(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	c := h.signInRoot()
	var team planResponse
	body := `{"name":"Team","timezone":"Asia/Tehran","daily_anchor":"00:00","weekly_anchor":6,"monthly_anchor":1,
		"quotas":[{"window":"daily","model_scope":null,"limit_usd":10},{"window":"rolling_5h","model_scope":null,"limit_usd":4},
		          {"window":"daily","model_scope":"opus","limit_usd":5}],
		"limits":{"rpm":0,"max_concurrent":0},"models":[]}`
	if st := h.do(c, "POST", "/api/admin/plans", body, &team); st != 201 {
		t.Fatalf("create: %d", st)
	}
	var id string
	if err := h.s.pg.QueryRow(ctx, `INSERT INTO users (email, plan_id) VALUES ('dev@example.com', $1) RETURNING id`, team.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	a := anchorsOf("Asia/Tehran", 0, 6, 1)
	start, _ := quota.Bounds(quota.Daily, a, now)
	h.s.rdb.Set(ctx, quota.FixedKey(id, quota.Daily, "", start), quota.ToMicro(3), time.Minute)
	h.s.rdb.Set(ctx, quota.FixedKey(id, quota.Daily, "opus", start), quota.ToMicro(2), time.Minute)
	h.s.rdb.Set(ctx, quota.BucketKeys(id, "", now)[0], quota.ToMicro(1), time.Minute)

	used := func() map[string]float64 {
		var q quotasResponse
		if st := h.do(c, "GET", "/api/admin/users/"+id+"/quotas", "", &q); st != 200 {
			t.Fatalf("quotas: %d", st)
		}
		out := map[string]float64{}
		for _, w := range q.Windows {
			out[string(w.Window)+"/"+deref(w.ModelScope)] = w.UsedUSD
		}
		return out
	}
	if u := used(); u["daily/"] != 3 || u["daily/opus"] != 2 || u["rolling_5h/"] != 1 {
		t.Fatalf("before reset %v", u)
	}

	if st := h.do(c, "POST", "/api/admin/users/"+id+"/quota/reset", `{"windows":[]}`, nil); st != 400 {
		t.Fatalf("empty reset: %d", st)
	}
	if st := h.do(c, "POST", "/api/admin/users/"+id+"/quota/reset", `{"windows":[{"window":"weekly","model_scope":null}]}`, nil); st != 400 {
		t.Fatalf("window not on Plan: %d", st)
	}
	reset := `{"windows":[{"window":"daily","model_scope":null},{"window":"rolling_5h","model_scope":null}]}`
	if st := h.do(c, "POST", "/api/admin/users/"+id+"/quota/reset", reset, nil); st != 204 {
		t.Fatalf("reset: %d", st)
	}
	if u := used(); u["daily/"] != 0 || u["daily/opus"] != 2 || u["rolling_5h/"] != 0 {
		t.Fatalf("after reset %v", u)
	}
}
