package control

import (
	"net/http"

	"qanat/internal/core/provider"
	"qanat/internal/store/db"
)

// How the gateway picks among a provider's accounts (after 9router's
// fallbackStrategy, per provider). A provider with no row routes
// sticky-balanced with the defaults below.

const (
	defaultStrategy       = "sticky-balanced"
	defaultStickyLimit    = 3
	defaultMinHeadroomPct = 5
)

type providerRoutingResponse struct {
	Provider       string `json:"provider"`
	Strategy       string `json:"strategy"`
	StickyLimit    int32  `json:"sticky_limit"`
	MinHeadroomPct int32  `json:"min_headroom_pct"`
}

func (s *Server) listProviderRouting(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	rows, err := s.q.ListProviderRouting(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	set := map[string]db.ProviderRouting{}
	for _, p := range rows {
		set[p.Provider] = p
	}
	out := []providerRoutingResponse{}
	for _, p := range provider.All {
		v, ok := set[p.ID]
		if !ok {
			v = db.ProviderRouting{Provider: p.ID, Strategy: defaultStrategy, StickyLimit: defaultStickyLimit, MinHeadroomPct: defaultMinHeadroomPct}
		}
		out = append(out, providerRoutingResponse{v.Provider, v.Strategy, v.StickyLimit, v.MinHeadroomPct})
	}
	writeJSON(w, http.StatusOK, out)
}

type providerRoutingRequest struct {
	Strategy       string `json:"strategy"`
	StickyLimit    int32  `json:"sticky_limit"`
	MinHeadroomPct int32  `json:"min_headroom_pct"`
}

func (s *Server) setProviderRouting(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	name := r.PathValue("provider")
	if !provider.Known(name) {
		fail(w, http.StatusNotFound, "not_found", `Provider must be "claude", "codex" or "antigravity".`)
		return
	}
	var req providerRoutingRequest
	if !decode(w, r, &req) {
		return
	}
	switch {
	case req.Strategy != "sticky-balanced" && req.Strategy != "round-robin" && req.Strategy != "fill-first":
		fail(w, http.StatusBadRequest, "invalid_strategy", `Strategy must be "sticky-balanced", "round-robin" or "fill-first".`)
		return
	case req.StickyLimit < 1 || req.StickyLimit > 1000:
		fail(w, http.StatusBadRequest, "invalid_sticky_limit", "Requests per account must be between 1 and 1000.")
		return
	case req.MinHeadroomPct < 0 || req.MinHeadroomPct > 100:
		fail(w, http.StatusBadRequest, "invalid_min_headroom", "The low-quota threshold must be between 0 and 100 percent.")
		return
	}
	if err := s.q.SetProviderRouting(r.Context(), db.SetProviderRoutingParams{
		Provider: name, Strategy: req.Strategy, StickyLimit: req.StickyLimit, MinHeadroomPct: req.MinHeadroomPct,
	}); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "provider.routing", "provider", name, nil, req)
	writeJSON(w, http.StatusOK, providerRoutingResponse{name, req.Strategy, req.StickyLimit, req.MinHeadroomPct})
}
