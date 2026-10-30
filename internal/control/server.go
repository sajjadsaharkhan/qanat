// Package control is the Admin API behind the dashboard (spec.md "API
// contracts"). The dashboard reaches it through its own server, so the session
// cookie is first-party and the browser never holds an identity-provider token.
//
// The contract is openapi.yaml in this package; the dashboard's typed client
// is generated from it. Keep the two in step.
package control

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"qanat/internal/store/db"
)

//go:embed openapi.yaml
var openapiSpec []byte

// Options configure the Admin API.
type Options struct {
	// RootPassword enables the break-glass root login (D23). Empty disables it.
	RootPassword string
	// GatewayURL is what users put in ANTHROPIC_BASE_URL (Setup page).
	GatewayURL string
	// SessionTTL bounds a dashboard session. Default 12 h.
	SessionTTL time.Duration
	// SecureCookies sets the Secure flag; on everywhere except plain-HTTP local runs.
	SecureCookies bool
}

// Server is the Admin API.
type Server struct {
	pg   *pgxpool.Pool
	q    *db.Queries
	rdb  *redis.Client
	log  *slog.Logger
	opts Options
	now  func() time.Time
}

// New builds the Admin API.
func New(pg *pgxpool.Pool, rdb *redis.Client, log *slog.Logger, opts Options) *Server {
	if opts.SessionTTL == 0 {
		opts.SessionTTL = 12 * time.Hour
	}
	return &Server{pg: pg, q: db.New(pg), rdb: rdb, log: log, opts: opts, now: time.Now}
}

// Bootstrap prepares state the API relies on: the root user when enabled.
func (s *Server) Bootstrap(ctx context.Context) error {
	if s.opts.RootPassword == "" {
		return nil
	}
	_, err := s.q.UpsertRootUser(ctx)
	return err
}

// Register mounts the API on mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write(openapiSpec)
	})

	mux.HandleFunc("GET /api/auth/methods", s.authMethods)
	mux.Handle("POST /api/auth/login", s.csrf(http.HandlerFunc(s.login)))
	mux.Handle("POST /api/auth/logout", s.csrf(http.HandlerFunc(s.logout)))

	user := func(h func(http.ResponseWriter, *http.Request, *db.GetUserRow)) http.Handler {
		return s.csrf(s.authed(h, false))
	}
	admin := func(h func(http.ResponseWriter, *http.Request, *db.GetUserRow)) http.Handler {
		return s.csrf(s.authed(h, true))
	}

	mux.Handle("GET /api/me", user(s.me))
	mux.Handle("GET /api/me/quotas", user(s.myQuotas))
	mux.Handle("GET /api/me/keys", user(s.listKeys))
	mux.Handle("POST /api/me/keys", user(s.createKey))
	mux.Handle("DELETE /api/me/keys/{id}", user(s.revokeKey))
	mux.Handle("GET /api/me/setup", user(s.setup))

	mux.Handle("GET /api/admin/users", admin(s.listUsers))
	mux.Handle("PATCH /api/admin/users/{id}", admin(s.updateUser))
	mux.Handle("GET /api/admin/plans", admin(s.listPlans))
	mux.Handle("POST /api/admin/plans", admin(s.createPlan))
	mux.Handle("GET /api/admin/plans/{id}", admin(s.getPlan))
	mux.Handle("PUT /api/admin/plans/{id}", admin(s.updatePlan))
	mux.Handle("DELETE /api/admin/plans/{id}", admin(s.deletePlan))
	mux.Handle("GET /api/admin/audit", admin(s.listAudit))
}

// --- responses ---------------------------------------------------------------

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// fail writes an error. Messages say what happened and how to fix it
// (design.md §10.3 rule 6), because the dashboard shows them verbatim.
func fail(w http.ResponseWriter, status int, code, msg string) {
	var e apiError
	e.Error.Code, e.Error.Message = code, msg
	writeJSON(w, status, e)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// decode reads a JSON body of at most 64 KiB, rejecting unknown fields.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "invalid_body", "The request body is not valid JSON for this endpoint: "+err.Error())
		return false
	}
	return true
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.ErrorContext(r.Context(), "admin api", "path", r.URL.Path, "err", err)
	fail(w, http.StatusInternalServerError, "internal", "Something went wrong on the server. Try again; if it keeps failing, check the control logs.")
}

func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
