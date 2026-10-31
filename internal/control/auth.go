package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"qanat/internal/store/db"
)

const (
	sessionCookie = "qanat_session"
	// csrfHeader must be present on every state-changing request. Browsers
	// can't add it cross-site without a CORS preflight, which this API never
	// grants, so it blocks CSRF on top of SameSite=Lax.
	csrfHeader = "X-Qanat-Request"

	// Root login throttling: after loginMaxFailures within loginWindow the
	// root login is refused until the window passes.
	loginMaxFailures = 10
	loginWindow      = 15 * time.Minute
)

func sessionKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return "qanat:sess:" + hex.EncodeToString(h[:])
}

func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) != "1" {
			fail(w, http.StatusForbidden, "csrf", "This request is missing the "+csrfHeader+" header. Use the dashboard or send the header from your client.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authed resolves the session to a live user; with admin set it also requires
// role admin (REQ-USER-02: roles live in Qanat).
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, *db.GetUserRow), admin bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.sessionUser(r)
		if err != nil {
			if !errors.Is(err, errNoSession) {
				s.internal(w, r, err)
				return
			}
			fail(w, http.StatusUnauthorized, "unauthenticated", "Your session has ended. Sign in again.")
			return
		}
		if admin && u.Role != "admin" {
			fail(w, http.StatusForbidden, "forbidden", "This page is for admins. Ask an admin if you need access.")
			return
		}
		h(w, r, u)
	})
}

var errNoSession = errors.New("no session")

func (s *Server) sessionUser(r *http.Request) (*db.GetUserRow, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, errNoSession
	}
	id, err := s.rdb.Get(r.Context(), sessionKey(c.Value)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, errNoSession
	}
	if err != nil {
		return nil, err
	}
	u, err := s.q.GetUser(r.Context(), id)
	if notFound(err) || (err == nil && u.DisabledAt != nil) {
		return nil, errNoSession
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Server) startSession(ctx context.Context, w http.ResponseWriter, userID string) error {
	b := make([]byte, 32)
	rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	if err := s.rdb.Set(ctx, sessionKey(token), userID, s.opts.SessionTTL).Err(); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		MaxAge: int(s.opts.SessionTTL.Seconds()), HttpOnly: true, Secure: s.opts.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

type authMethods struct {
	Password bool `json:"password"` // root login enabled (D23)
	OIDC     bool `json:"oidc"`     // Keycloak sign-in enabled
}

func (s *Server) authMethods(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, authMethods{
		Password: s.opts.RootPassword != "",
		OIDC:     s.opts.OIDCIssuer != "",
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// login is the root admin's password sign-in (D23). Only "root" exists.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.opts.RootPassword == "" {
		fail(w, http.StatusNotFound, "password_login_disabled", "Password sign-in is turned off. Sign in with Keycloak.")
		return
	}
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	failKey := "qanat:login:fail:root"
	if n, _ := s.rdb.Get(ctx, failKey).Int(); n >= loginMaxFailures {
		ttl, _ := s.rdb.TTL(ctx, failKey).Result()
		w.Header().Set("Retry-After", itoa(int(ttl.Seconds())+1))
		fail(w, http.StatusTooManyRequests, "too_many_attempts", "Too many failed sign-ins. Wait 15 minutes and try again.")
		return
	}
	if !(req.Username == "root" && equalSecret(req.Password, s.opts.RootPassword)) {
		pipe := s.rdb.TxPipeline()
		pipe.Incr(ctx, failKey)
		pipe.ExpireNX(ctx, failKey, loginWindow)
		pipe.Exec(ctx)
		s.log.WarnContext(ctx, "root login failed", "remote", r.RemoteAddr)
		fail(w, http.StatusUnauthorized, "bad_credentials", "Wrong username or password.")
		return
	}
	id, err := s.q.UpsertRootUser(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if err := s.startSession(ctx, w, id); err != nil {
		s.internal(w, r, err)
		return
	}
	s.rdb.Del(ctx, failKey)
	s.audit(ctx, &id, "auth.login", "user", id, nil, map[string]string{"method": "password"})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.rdb.Del(r.Context(), sessionKey(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.opts.SecureCookies, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

// equalSecret compares in constant time regardless of length.
func equalSecret(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

type oidcState struct {
	Verifier string `json:"verifier"`
	Next     string `json:"next"`
}

func (s *Server) oidcRedirectURI(r *http.Request) string {
	if s.opts.WebURL != "" {
		return strings.TrimRight(s.opts.WebURL, "/") + "/api/auth/oidc/callback"
	}
	proto := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		proto = "https"
	}
	host := r.Host
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
		host = xfh
	}
	return fmt.Sprintf("%s://%s/api/auth/oidc/callback", proto, host)
}

func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if s.opts.OIDCIssuer == "" || s.oidc == nil {
		fail(w, http.StatusNotFound, "oidc_disabled", "Keycloak sign-in is not configured on this server.")
		return
	}
	ctx := r.Context()
	if err := s.oidc.discover(ctx); err != nil {
		s.internal(w, r, fmt.Errorf("oidc discover: %w", err))
		return
	}

	stateBytes := make([]byte, 24)
	if _, err := rand.Read(stateBytes); err != nil {
		s.internal(w, r, err)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		s.internal(w, r, err)
		return
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challengeHash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])

	next := r.URL.Query().Get("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = ""
	}

	data, _ := json.Marshal(oidcState{Verifier: verifier, Next: next})
	if err := s.rdb.Set(ctx, "qanat:oidc:state:"+state, data, 10*time.Minute).Err(); err != nil {
		s.internal(w, r, err)
		return
	}

	redirectURI := s.oidcRedirectURI(r)

	authURL, err := url.Parse(s.oidc.authURL)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", s.opts.OIDCClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "openid profile email")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()

	http.Redirect(w, r, authURL.String(), http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.opts.OIDCIssuer == "" || s.oidc == nil {
		http.Redirect(w, r, "/sign-in?error=oidc_disabled", http.StatusFound)
		return
	}
	q := r.URL.Query()
	if errParam := q.Get("error"); errParam != "" {
		s.log.Warn("oidc auth error", "error", errParam, "desc", q.Get("error_description"))
		http.Redirect(w, r, "/sign-in?error="+url.QueryEscape(errParam), http.StatusFound)
		return
	}
	code := q.Get("code")
	state := q.Get("state")
	if code == "" || state == "" {
		http.Redirect(w, r, "/sign-in?error=missing_code_or_state", http.StatusFound)
		return
	}

	ctx := r.Context()
	stateKey := "qanat:oidc:state:" + state
	val, err := s.rdb.Get(ctx, stateKey).Result()
	if err != nil {
		http.Redirect(w, r, "/sign-in?error=state_expired", http.StatusFound)
		return
	}
	s.rdb.Del(ctx, stateKey)

	var st oidcState
	if err := json.Unmarshal([]byte(val), &st); err != nil {
		http.Redirect(w, r, "/sign-in?error=invalid_state", http.StatusFound)
		return
	}

	redirectURI := s.oidcRedirectURI(r)

	tokenURL := s.oidc.tokenURL
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {s.opts.OIDCClientID},
		"code_verifier": {st.Verifier},
	}
	if s.opts.OIDCClientSecret != "" {
		form.Set("client_secret", s.opts.OIDCClientSecret)
	}

	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResp, err := s.oidc.httpClient.Do(tokenReq)
	if err != nil {
		s.log.ErrorContext(ctx, "oidc token exchange error", "err", err)
		http.Redirect(w, r, "/sign-in?error=token_exchange_failed", http.StatusFound)
		return
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode != http.StatusOK {
		s.log.ErrorContext(ctx, "oidc token exchange status", "status", tokenResp.StatusCode)
		http.Redirect(w, r, "/sign-in?error=token_exchange_rejected", http.StatusFound)
		return
	}

	var tokRes struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokRes); err != nil {
		s.internal(w, r, err)
		return
	}

	rawJWT := tokRes.IDToken
	if rawJWT == "" {
		rawJWT = tokRes.AccessToken
	}
	claims, err := s.oidc.verifyToken(ctx, rawJWT)
	if err != nil {
		s.log.ErrorContext(ctx, "oidc token verification failed", "err", err)
		http.Redirect(w, r, "/sign-in?error=token_verification_failed", http.StatusFound)
		return
	}

	// Gate check (REQ-AUTH-02): must be in group "qanat-users".
	hasGroup := false
	for _, g := range claims.Groups {
		if g == "qanat-users" {
			hasGroup = true
			break
		}
	}
	if !hasGroup {
		s.log.WarnContext(ctx, "oidc user denied: not in qanat-users", "sub", claims.Subject, "groups", claims.Groups)
		http.Redirect(w, r, "/access-denied", http.StatusFound)
		return
	}

	// JIT provisioning / Profile sync (REQ-AUTH-03, REQ-AUTH-04)
	var userID string
	disabled := false
	u, err := s.q.GetUserByOIDCSubject(ctx, &claims.Subject)
	if err == nil {
		userID, disabled = u.ID, u.DisabledAt != nil
		_ = s.q.UpdateOIDCUser(ctx, db.UpdateOIDCUserParams{
			ID:          u.ID,
			Email:       claims.Email,
			Name:        claims.Name,
			OidcSubject: &claims.Subject,
		})
	} else if notFound(err) {
		uEmail, errEmail := s.q.GetUserByEmail(ctx, claims.Email)
		if errEmail == nil {
			userID, disabled = uEmail.ID, uEmail.DisabledAt != nil
			_ = s.q.UpdateOIDCUser(ctx, db.UpdateOIDCUserParams{
				ID:          uEmail.ID,
				Email:       claims.Email,
				Name:        claims.Name,
				OidcSubject: &claims.Subject,
			})
		} else if notFound(errEmail) {
			role := "user"
			if s.opts.BootstrapAdminEmail != "" && strings.EqualFold(claims.Email, s.opts.BootstrapAdminEmail) {
				role = "admin"
			}
			row, createErr := s.q.CreateOIDCUser(ctx, db.CreateOIDCUserParams{
				Email:       claims.Email,
				Name:        claims.Name,
				OidcSubject: &claims.Subject,
				Role:        role,
			})
			if createErr != nil {
				s.internal(w, r, createErr)
				return
			}
			userID = row.ID
		} else {
			s.internal(w, r, errEmail)
			return
		}
	} else {
		s.internal(w, r, err)
		return
	}

	// An admin disabled or banned this user; signing in again doesn't undo it.
	if disabled {
		s.log.WarnContext(ctx, "oidc user denied: disabled", "user", userID)
		http.Redirect(w, r, "/access-denied?reason=disabled", http.StatusFound)
		return
	}

	if err := s.startSession(ctx, w, userID); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(ctx, &userID, "auth.login", "user", userID, nil, map[string]string{"method": "oidc"})

	dest := "/"
	if st.Next != "" {
		dest = st.Next
	}
	http.Redirect(w, r, dest, http.StatusFound)
}
