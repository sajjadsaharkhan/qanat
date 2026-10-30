// Package config reads Qanat's runtime settings from the environment.
//
// Secrets may be given as NAME_FILE pointing at a file (Docker secrets); the
// file wins over NAME. Values are never logged.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config is shared by both binaries; each reads the fields it needs.
type Config struct {
	PostgresDSN string // QANAT_POSTGRES_DSN
	RedisAddr   string // QANAT_REDIS_ADDR
	GatewayAddr string // QANAT_GATEWAY_ADDR, default :8080
	ControlAddr string // QANAT_CONTROL_ADDR, default :8081
	// TestMode enables the test-only endpoints (QANAT_TEST_MODE=1). It is
	// refused when any production-looking setting is present.
	TestMode bool
}

// Load reads the environment and validates it.
func Load() (Config, error) {
	c := Config{
		PostgresDSN: secret("QANAT_POSTGRES_DSN"),
		RedisAddr:   os.Getenv("QANAT_REDIS_ADDR"),
		GatewayAddr: envOr("QANAT_GATEWAY_ADDR", ":8080"),
		ControlAddr: envOr("QANAT_CONTROL_ADDR", ":8081"),
		TestMode:    os.Getenv("QANAT_TEST_MODE") == "1",
	}
	if c.PostgresDSN == "" {
		return c, fmt.Errorf("QANAT_POSTGRES_DSN is required")
	}
	if c.RedisAddr == "" {
		return c, fmt.Errorf("QANAT_REDIS_ADDR is required")
	}
	if c.TestMode {
		if err := refuseProductionInTestMode(); err != nil {
			return c, err
		}
	}
	return c, nil
}

// refuseProductionInTestMode keeps QANAT_TEST_MODE from ever running against
// real identity: the OIDC issuer must be local (docs/testing.md).
func refuseProductionInTestMode() error {
	iss := os.Getenv("QANAT_OIDC_ISSUER")
	if iss == "" {
		return nil
	}
	u, err := url.Parse(iss)
	if err != nil {
		return fmt.Errorf("QANAT_OIDC_ISSUER: %w", err)
	}
	switch h := u.Hostname(); {
	case h == "localhost", h == "127.0.0.1", h == "::1", !strings.Contains(h, "."):
		return nil
	default:
		return fmt.Errorf("QANAT_TEST_MODE=1 refused: OIDC issuer %q is not local", h)
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func secret(name string) string {
	if f := os.Getenv(name + "_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return os.Getenv(name)
}
