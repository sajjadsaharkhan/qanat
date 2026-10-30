package control

import (
	"net/http"
	"strings"
)

// Test-only endpoints (docs/testing.md "The qanat driver"). Registered only
// with QANAT_TEST_MODE=1, which config.Load refuses next to a non-local
// identity provider.
func (s *Server) registerTestMode(mux *http.ServeMux) {
	mux.HandleFunc("POST /test/reset", s.testReset)
	mux.HandleFunc("POST /test/users", s.testCreateUser)
	mux.HandleFunc("POST /test/session", s.testSession)
	mux.HandleFunc("POST /test/flush-usage", func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Worker != nil {
			if err := s.opts.Worker.Flush(r.Context()); err != nil {
				s.internal(w, r, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /test/generation", func(w http.ResponseWriter, r *http.Request) {
		gen, err := s.q.SnapshotGeneration(r.Context())
		if err != nil {
			s.internal(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"generation": gen})
	})
}

// resetTables is every table holding state; schema_migrations and
// config_generation (monotonic on purpose) are kept.
var resetTables = []string{
	"audit_log", "usage_5m", "usage_hourly", "usage_daily", "sticky_bindings", "provider_accounts",
	"model_prices", "model_mappings", "combo_members", "combos", "quota_grants", "api_keys",
	"users", "plan_models", "plan_limits", "plan_quotas", "plans",
}

// testReset truncates all tables and flushes Redis, then restores the seed
// data a fresh install has (default Plan, root admin).
func (s *Server) testReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, err := s.pg.Exec(ctx, "TRUNCATE "+strings.Join(resetTables, ", ")+" CASCADE")
	if err == nil {
		_, err = s.pg.Exec(ctx, "INSERT INTO plans (name, is_default) VALUES ('Default', true)")
	}
	if err == nil {
		err = s.rdb.FlushDB(ctx).Err()
	}
	if err == nil {
		err = s.Bootstrap(ctx)
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type testUserRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (s *Server) testCreateUser(w http.ResponseWriter, r *http.Request) {
	var req testUserRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}
	var id string
	err := s.pg.QueryRow(r.Context(), `INSERT INTO users (email, role, plan_id)
		SELECT $1, $2, id FROM plans WHERE is_default RETURNING id`, req.Email, req.Role).Scan(&id)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// testSession returns a session token for any user, so the driver can call
// the Admin API (as an admin) and create keys (as the user).
func (s *Server) testSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	rec := &cookieRecorder{h: http.Header{}}
	if err := s.startSession(r.Context(), rec, req.UserID); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"cookie": rec.h.Get("Set-Cookie")})
}

// cookieRecorder captures Set-Cookie from startSession.
type cookieRecorder struct{ h http.Header }

func (c *cookieRecorder) Header() http.Header         { return c.h }
func (c *cookieRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (c *cookieRecorder) WriteHeader(int)             {}
