package store_test

import (
	"context"
	"testing"
	"time"

	"qanat/internal/store"
	"qanat/internal/store/db"
	"qanat/internal/store/storetest"
)

func TestMigrate_IdempotentAndSeedsZeroQuotaDefaultPlan(t *testing.T) {
	pool := storetest.DB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, pool); err != nil { // second run is a no-op
		t.Fatal(err)
	}

	// REQ-PLAN-03: exactly one default Plan, with no quota rows (zero quota)
	// and no allowlist rows (all models).
	var n, quotas, models int
	err := pool.QueryRow(ctx, `SELECT count(*),
		(SELECT count(*) FROM plan_quotas q JOIN plans p ON p.id = q.plan_id WHERE p.is_default),
		(SELECT count(*) FROM plan_models m JOIN plans p ON p.id = m.plan_id WHERE p.is_default)
		FROM plans WHERE is_default`).Scan(&n, &quotas, &models)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || quotas != 0 || models != 0 {
		t.Fatalf("default plans=%d quotas=%d models=%d", n, quotas, models)
	}

	// Generated snapshot queries run against the real schema.
	q := db.New(pool)
	if _, err := q.SnapshotPlans(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SnapshotPrices(ctx); err != nil {
		t.Fatal(err)
	}
}

// §11.6: config changes notify the gateway; usage bookkeeping on keys does not.
func TestMigrate_ConfigChangesNotify(t *testing.T) {
	pool := storetest.DB(t)
	ctx := context.Background()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN qanat_config"); err != nil {
		t.Fatal(err)
	}
	wait := func() *string {
		wctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		n, err := conn.Conn().WaitForNotification(wctx)
		if err != nil {
			return nil
		}
		return &n.Payload
	}

	var userID, keyID string
	must(t, pool.QueryRow(ctx, `INSERT INTO users (email, plan_id)
		SELECT 'a@example.com', id FROM plans WHERE is_default RETURNING id`).Scan(&userID))
	if p := wait(); p == nil || *p != "users" {
		t.Fatalf("users insert: payload %v", p)
	}
	must(t, pool.QueryRow(ctx, `INSERT INTO api_keys (user_id, name, hash, prefix)
		VALUES ($1, 'k', '\x01', 'qnt_live_') RETURNING id`, userID).Scan(&keyID))
	if p := wait(); p == nil || *p != "api_keys" {
		t.Fatalf("key insert: payload %v", p)
	}
	_, err = pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now(), last_ip = '10.0.0.1' WHERE id = $1`, keyID)
	must(t, err)
	if p := wait(); p != nil {
		t.Fatalf("last_used_at update must not notify, got %q", *p)
	}
	_, err = pool.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id = $1`, keyID)
	must(t, err)
	if p := wait(); p == nil || *p != "api_keys" {
		t.Fatalf("revoke: payload %v", p)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
