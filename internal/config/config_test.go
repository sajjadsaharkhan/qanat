package config

import (
	"os"
	"testing"
)

func TestLoad_TestModeRefusesNonLocalIssuer(t *testing.T) {
	t.Setenv("QANAT_POSTGRES_DSN", "postgres://x")
	t.Setenv("QANAT_REDIS_ADDR", "localhost:6379")
	t.Setenv("QANAT_TEST_MODE", "1")

	for iss, ok := range map[string]bool{
		"http://localhost:59180/realms/qanat":     true,
		"http://oidcmock:9180/realms/qanat":       true, // compose service name
		"https://sso.example.com/realms/company":  false,
		"https://keycloak.internal.corp/realms/x": false,
	} {
		t.Setenv("QANAT_OIDC_ISSUER", iss)
		_, err := Load()
		if (err == nil) != ok {
			t.Errorf("issuer %s: err = %v, want ok=%v", iss, err, ok)
		}
	}
}

func TestLoad_SecretFileWins(t *testing.T) {
	f := t.TempDir() + "/dsn"
	if err := os.WriteFile(f, []byte("postgres://from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QANAT_POSTGRES_DSN", "postgres://from-env")
	t.Setenv("QANAT_POSTGRES_DSN_FILE", f)
	t.Setenv("QANAT_REDIS_ADDR", "localhost:6379")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PostgresDSN != "postgres://from-file" {
		t.Fatalf("got %q", c.PostgresDSN)
	}
}
