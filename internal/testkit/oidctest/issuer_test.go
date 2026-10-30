package oidctest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestDiscoveryAndTokenRoundTrip(t *testing.T) {
	iss := New(t)

	resp, err := http.Get(iss.IssuerURL() + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatal(err)
	}
	var disc map[string]any
	json.NewDecoder(resp.Body).Decode(&disc)
	resp.Body.Close()
	if disc["issuer"] != iss.IssuerURL() {
		t.Fatalf("bad issuer: %v", disc["issuer"])
	}

	tok := iss.Token(Claims{Subject: "u1", Email: "a@example.com", Groups: []string{"qanat-users"}})
	claims, err := iss.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims["email"] != "a@example.com" {
		t.Fatalf("bad claims: %v", claims)
	}
	if _, err := iss.Verify(tok[:len(tok)-4] + "AAAA"); err == nil {
		t.Fatal("tampered token verified")
	}
}

func TestAutoLoginCodeFlow(t *testing.T) {
	iss := New(t)
	iss.LoginAs(Claims{Subject: "u2", Email: "b@example.com", Groups: []string{"qanat-users"}})

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	authURL := iss.IssuerURL() + "/protocol/openid-connect/auth?redirect_uri=" +
		url.QueryEscape("http://app.test/callback") + "&state=xyz&code_challenge=abc&code_challenge_method=S256"
	resp, err := noRedirect.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("state") != "xyz" || loc.Query().Get("code") == "" {
		t.Fatalf("bad redirect: %v", loc)
	}

	form := url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}}
	tr, err := http.Post(iss.IssuerURL()+"/protocol/openid-connect/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.NewDecoder(tr.Body).Decode(&body)
	claims, err := iss.Verify(body["access_token"].(string))
	if err != nil || claims["sub"] != "u2" {
		t.Fatalf("token exchange failed: %v %v", err, claims)
	}

	// Codes are single-use.
	tr2, _ := http.Post(iss.IssuerURL()+"/protocol/openid-connect/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if tr2.StatusCode != http.StatusBadRequest {
		t.Fatalf("code reuse allowed: %d", tr2.StatusCode)
	}
}
