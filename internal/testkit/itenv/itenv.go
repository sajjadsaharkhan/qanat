// Package itenv resolves integration-test dependencies from the environment.
//
// Integration and acceptance tests need real Redis and PostgreSQL. Locally and
// in CI they come from deploy/test/compose.test.yml; `make test-integration`
// exports the variables below. When a variable is missing the test is skipped,
// never failed, so `go test ./...` stays green on a laptop without Docker.
package itenv

import (
	"os"
	"testing"
)

const (
	EnvRedis    = "QANAT_TEST_REDIS_ADDR"   // e.g. localhost:56379
	EnvPostgres = "QANAT_TEST_POSTGRES_DSN" // e.g. postgres://qanat:qanat@localhost:55432/qanat_test?sslmode=disable
)

// Redis returns the test Redis address or skips the test.
func Redis(t testing.TB) string { return need(t, EnvRedis) }

// Postgres returns the test PostgreSQL DSN or skips the test.
func Postgres(t testing.TB) string { return need(t, EnvPostgres) }

func need(t testing.TB, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s not set; run `make test-integration` to start test dependencies", key)
	}
	return v
}
