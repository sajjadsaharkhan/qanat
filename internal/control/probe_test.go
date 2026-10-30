package control

import (
	"net/http"
	"testing"

	"qanat/internal/core/oauth"
)

// The Test button's probe must match the credential's own protocol: OAuth
// tokens on the CLI's usage endpoint (no model dependency), API keys on the
// models list.
func TestUpstreamProbeClaude(t *testing.T) {
	req, err := upstreamProbe(t.Context(), "claude", "oauth", "sk-ant-oat01-x", "https://api.anthropic.com/")
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodGet || req.URL.String() != "https://api.anthropic.com/api/oauth/usage" {
		t.Fatalf("oauth probe: %s %s", req.Method, req.URL)
	}
	if req.Header.Get("Authorization") != "Bearer sk-ant-oat01-x" {
		t.Fatalf("oauth probe auth: %v", req.Header)
	}
	if req.Header.Get("anthropic-beta") != oauth.OAuthBeta || req.Header.Get("anthropic-version") == "" {
		t.Fatalf("oauth probe headers: %v", req.Header)
	}
	if req.Header.Get("X-Api-Key") != "" {
		t.Fatalf("oauth probe must not send x-api-key: %v", req.Header)
	}

	req, err = upstreamProbe(t.Context(), "claude", "api_key", "sk-ant-api01-y", "https://api.anthropic.com")
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://api.anthropic.com/v1/models" {
		t.Fatalf("api_key probe: %s", req.URL)
	}
	if req.Header.Get("X-Api-Key") != "sk-ant-api01-y" || req.Header.Get("Authorization") != "" {
		t.Fatalf("api_key probe headers: %v", req.Header)
	}
}
