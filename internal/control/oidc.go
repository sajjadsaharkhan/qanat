package control

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OIDCClient discovers OIDC endpoints, exchanges authorization codes,
// and verifies tokens against JWKS keys.
type OIDCClient struct {
	issuer      string
	internalURL string
	clientID    string
	clientSec   string
	httpClient  *http.Client

	mu        sync.RWMutex
	authURL   string
	tokenURL  string
	jwksURL   string
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

func newOIDCClient(issuer, internalURL, clientID, clientSec string) *OIDCClient {
	return &OIDCClient{
		issuer:      strings.TrimRight(issuer, "/"),
		internalURL: strings.TrimRight(internalURL, "/"),
		clientID:    clientID,
		clientSec:   clientSec,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		keys:        make(map[string]*rsa.PublicKey),
	}
}

func (c *OIDCClient) internalOrIssuer() string {
	if c.internalURL != "" {
		return c.internalURL
	}
	return c.issuer
}

type oidcDiscovery struct {
	Issuer   string `json:"issuer"`
	AuthURL  string `json:"authorization_endpoint"`
	TokenURL string `json:"token_endpoint"`
	JwksURL  string `json:"jwks_uri"`
}

func (c *OIDCClient) discover(ctx context.Context) error {
	c.mu.RLock()
	ready := c.authURL != "" && c.tokenURL != "" && c.jwksURL != ""
	c.mu.RUnlock()
	if ready {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.authURL != "" && c.tokenURL != "" && c.jwksURL != "" {
		return nil
	}

	discoURL := c.internalOrIssuer() + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("oidc discovery request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc discovery status %d", resp.StatusCode)
	}

	var d oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return fmt.Errorf("decode oidc discovery: %w", err)
	}

	c.authURL = d.AuthURL
	c.tokenURL = d.TokenURL
	c.jwksURL = d.JwksURL

	// If internalURL was used for discovery, adjust internal endpoints (token and jwks)
	// so the control plane connects via the internal network, while the browser uses AuthURL.
	if c.internalURL != "" {
		c.tokenURL = c.remapToInternal(c.tokenURL)
		c.jwksURL = c.remapToInternal(c.jwksURL)
	}
	return nil
}

func (c *OIDCClient) remapToInternal(rawURL string) string {
	if c.internalURL == "" {
		return rawURL
	}
	if strings.HasPrefix(rawURL, c.issuer) {
		return c.internalURL + strings.TrimPrefix(rawURL, c.issuer)
	}
	return rawURL
}

type jwksResponse struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Alg string `json:"alg"`
		Use string `json:"use"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (c *OIDCClient) fetchKeys(ctx context.Context) error {
	if err := c.discover(ctx); err != nil {
		return err
	}

	c.mu.RLock()
	jwksURL := c.jwksURL
	c.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks status %d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey)
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		n := new(big.Int).SetBytes(nBytes)
		var e int
		for _, b := range eBytes {
			e = (e << 8) | int(b)
		}
		newKeys[k.Kid] = &rsa.PublicKey{N: n, E: e}
	}

	c.mu.Lock()
	c.keys = newKeys
	c.fetchedAt = time.Now()
	c.mu.Unlock()
	return nil
}

func (c *OIDCClient) getKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	key, ok := c.keys[kid]
	c.mu.RUnlock()
	if ok {
		return key, nil
	}

	if err := c.fetchKeys(ctx); err != nil {
		return nil, err
	}

	c.mu.RLock()
	key, ok = c.keys[kid]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("kid %q not found in jwks", kid)
	}
	return key, nil
}

// TokenClaims contains verified claims from the ID token.
type TokenClaims struct {
	Subject string   `json:"sub"`
	Email   string   `json:"email"`
	Name    string   `json:"name"`
	Groups  []string `json:"groups"`
}

func (c *OIDCClient) verifyToken(ctx context.Context, tokenStr string) (*TokenClaims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed jwt")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("unmarshal header: %w", err)
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported jwt alg %q", header.Alg)
	}

	key, err := c.getKey(ctx, header.Kid)
	if err != nil {
		return nil, fmt.Errorf("lookup signing key: %w", err)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	signedContent := parts[0] + "." + parts[1]
	sum := sha256.Sum256([]byte(signedContent))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return nil, fmt.Errorf("signature verification failed: %w", err)
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	var rawClaims struct {
		Iss               string   `json:"iss"`
		Sub               string   `json:"sub"`
		Aud               any      `json:"aud"`
		Exp               int64    `json:"exp"`
		Email             string   `json:"email"`
		Name              string   `json:"name"`
		PreferredUsername string   `json:"preferred_username"`
		Groups            []string `json:"groups"`
	}
	if err := json.Unmarshal(payloadBytes, &rawClaims); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}

	if rawClaims.Iss != c.issuer {
		return nil, fmt.Errorf("issuer mismatch: got %q, want %q", rawClaims.Iss, c.issuer)
	}
	if rawClaims.Exp < time.Now().Unix() {
		return nil, errors.New("token expired")
	}

	// Verify audience: can be a string or array of strings.
	audOK := false
	switch aud := rawClaims.Aud.(type) {
	case string:
		audOK = aud == c.clientID
	case []any:
		for _, a := range aud {
			if str, ok := a.(string); ok && str == c.clientID {
				audOK = true
				break
			}
		}
	}
	if !audOK {
		return nil, fmt.Errorf("audience mismatch: %v does not contain %q", rawClaims.Aud, c.clientID)
	}

	email := rawClaims.Email
	if email == "" {
		email = rawClaims.PreferredUsername
	}
	name := rawClaims.Name
	if name == "" {
		name = email
	}

	return &TokenClaims{
		Subject: rawClaims.Sub,
		Email:   email,
		Name:    name,
		Groups:  rawClaims.Groups,
	}, nil
}
