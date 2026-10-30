// Package storetest gives integration tests an isolated, migrated database.
//
// Packages run in parallel against one PostgreSQL server, so every test gets
// its own database (created from the itenv DSN, dropped on cleanup).
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"qanat/internal/store"
	"qanat/internal/testkit/itenv"
)

// DB returns a pool on a fresh migrated database, or skips without Docker.
func DB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, _ := Fresh(t, true)
	return pool
}

// Fresh creates an empty database and returns a pool and its DSN. With
// migrate set it applies the schema first.
func Fresh(t testing.TB, migrate bool) (*pgxpool.Pool, string) {
	t.Helper()
	base := itenv.Postgres(t)
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect %s: %v", itenv.EnvPostgres, err)
	}
	defer admin.Close(ctx)

	b := make([]byte, 6)
	rand.Read(b)
	name := "qanat_t_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if migrate {
		if err := store.Migrate(ctx, pool); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return pool, dsn
}
