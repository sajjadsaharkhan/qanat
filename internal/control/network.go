package control

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"qanat/internal/core/netstats"
	"qanat/internal/core/provider"
	"qanat/internal/store/db"
)

// --- network analytics (admin Network page) ---------------------------------
//
// Live per-provider network health merged from every gateway's report in
// Redis (internal/core/netstats). Nothing here is stored.

type networkResponse struct {
	GeneratedAt     time.Time         `json:"generated_at"`
	WindowSeconds   int               `json:"window_seconds"`
	IntervalSeconds int               `json:"interval_seconds"`
	Gateways        []networkGateway  `json:"gateways"`
	Internet        *netstats.Probe   `json:"internet"`
	InternetHistory []netstats.Point  `json:"internet_history"`
	Providers       []providerNetwork `json:"providers"`
}

type networkGateway struct {
	Instance   string    `json:"instance"`
	ReportedAt time.Time `json:"reported_at"`
	StartedAt  time.Time `json:"started_at"`
}

type providerNetwork struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Prefix            string            `json:"prefix"`
	Status            netstats.Status   `json:"status"`
	ActiveConnections int               `json:"active_connections"`
	Stats             netstats.Stats    `json:"stats"`
	Accounts          []accountNetwork  `json:"accounts"`
	Probes            []probeNetwork    `json:"probes"`
	History           []netstats.Bucket `json:"history"`
	Recent            []eventNetwork    `json:"recent"`
	RefreshedAt       *time.Time        `json:"refreshed_at"`
	LastProbeAt       *time.Time        `json:"last_probe_at"`
}

type accountNetwork struct {
	ID           string     `json:"id"`
	Label        string     `json:"label"`
	State        string     `json:"state"`
	Disabled     bool       `json:"disabled"`
	Usable       bool       `json:"usable"`
	Active       int        `json:"active"`
	CoolingUntil *time.Time `json:"cooling_until"`
	Attempts     int64      `json:"attempts"`
	Errors       int64      `json:"errors"`
	TTFTP50      *int64     `json:"ttft_p50_ms"`
	LastSuccess  *time.Time `json:"last_success_at"`
}

type probeNetwork struct {
	netstats.Probe
	RouteLabel string `json:"route_label"`
}

type eventNetwork struct {
	netstats.Event
	AccountLabel string `json:"account_label,omitempty"`
}

// recentLen is how many of the newest attempts the live traffic list shows.
const recentLen = 25

func (s *Server) adminNetwork(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	ctx := r.Context()
	now := time.Now()
	reports := s.netReports(ctx)

	accounts, err := s.q.ListAccounts(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	proxies, err := s.q.ListProxies(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	acctLabel := map[string]string{}
	dbAccounts := map[string][]db.ListAccountsRow{}
	for _, a := range accounts {
		acctLabel[a.ID] = a.Label
		dbAccounts[a.Provider] = append(dbAccounts[a.Provider], a)
	}
	routeLabel := map[string]string{"direct": "Direct"}
	for _, p := range proxies {
		routeLabel[p.ID] = p.Label
	}

	view := netstats.Merge(reports, now)
	out := networkResponse{
		GeneratedAt: now, WindowSeconds: int(netstats.Window.Seconds()), IntervalSeconds: int(netstats.Interval.Seconds()),
		Gateways: []networkGateway{}, Internet: view.Internet, InternetHistory: orEmptyPoints(view.InternetHistory),
		Providers: []providerNetwork{},
	}
	for _, rep := range reports {
		out.Gateways = append(out.Gateways, networkGateway{rep.Instance, rep.At, rep.StartedAt})
	}

	for _, pv := range provider.All {
		pn := providerNetwork{ID: pv.ID, Name: pv.Name, Prefix: pv.Prefix, Accounts: []accountNetwork{}, Probes: []probeNetwork{},
			History: []netstats.Bucket{}, Recent: []eventNetwork{}}
		m := view.Providers[pv.ID]
		if m == nil {
			// No gateway is reporting: say so rather than guess.
			pn.Status = netstats.Status{State: "checking", Reason: "no_gateway", Since: now}
			if len(dbAccounts[pv.ID]) == 0 {
				pn.Status = netstats.Status{State: "no_accounts", Since: now}
			}
			pn.Stats = netstats.Summarize(nil, now, netstats.Window)
			for _, a := range dbAccounts[pv.ID] {
				pn.Accounts = append(pn.Accounts, accountNetwork{ID: a.ID, Label: a.Label, State: a.State, Disabled: a.DisabledAt != nil})
			}
			out.Providers = append(out.Providers, pn)
			continue
		}
		pn.Status = m.Status
		pn.ActiveConnections = m.Active
		pn.Stats = netstats.Summarize(m.Events, now, netstats.Window)
		pn.History = m.History
		refreshed := m.RefreshedAt
		pn.RefreshedAt = &refreshed

		for _, a := range m.Accounts {
			var evs []netstats.Event
			for _, e := range m.Events {
				if e.Account == a.ID {
					evs = append(evs, e)
				}
			}
			st := netstats.Summarize(evs, now, netstats.Window)
			label := acctLabel[a.ID]
			if label == "" {
				label = a.ID
			}
			pn.Accounts = append(pn.Accounts, accountNetwork{ID: a.ID, Label: label, State: a.State, Disabled: a.Disabled, Usable: a.Usable,
				Active: a.Active, CoolingUntil: a.CoolingUntil, Attempts: st.Attempts, Errors: st.Errors, TTFTP50: st.TTFT.P50, LastSuccess: st.LastSuccess})
		}
		for _, p := range m.Probes {
			label := routeLabel[p.Route]
			if label == "" {
				label = p.Route
			}
			pn.Probes = append(pn.Probes, probeNetwork{p, label})
			if pn.LastProbeAt == nil || p.At.After(*pn.LastProbeAt) {
				at := p.At
				pn.LastProbeAt = &at
			}
		}
		slices.SortFunc(pn.Probes, func(a, b probeNetwork) int {
			if c := strings.Compare(a.RouteLabel, b.RouteLabel); c != 0 {
				return c
			}
			return strings.Compare(a.Origin, b.Origin)
		})
		for i := len(m.Events) - 1; i >= 0 && len(pn.Recent) < recentLen; i-- {
			e := m.Events[i]
			pn.Recent = append(pn.Recent, eventNetwork{e, acctLabel[e.Account]})
		}
		out.Providers = append(out.Providers, pn)
	}
	writeJSON(w, http.StatusOK, out)
}

// netReports reads every live gateway report. Analytics are informational:
// a Redis error leaves the page empty, it doesn't fail it.
func (s *Server) netReports(ctx context.Context) []netstats.Report {
	if s.rdb == nil {
		return nil
	}
	// Forget gateways that stopped reporting; their reports have expired.
	cutoff := strconv.FormatInt(time.Now().Add(-netstats.ReportTTL).Unix(), 10)
	s.rdb.ZRemRangeByScore(ctx, netstats.Gateways, "-inf", "("+cutoff)
	ids, err := s.rdb.ZRange(ctx, netstats.Gateways, 0, -1).Result()
	if err != nil || len(ids) == 0 {
		if err != nil {
			s.log.Warn("network reports unavailable", "err", err)
		}
		return nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = netstats.Key(id)
	}
	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		s.log.Warn("network reports unavailable", "err", err)
		return nil
	}
	var out []netstats.Report
	for _, v := range vals {
		str, ok := v.(string)
		if !ok {
			continue // expired since its last ZADD
		}
		var rep netstats.Report
		if json.Unmarshal([]byte(str), &rep) == nil {
			out = append(out, rep)
		}
	}
	return out
}

func orEmptyPoints(p []netstats.Point) []netstats.Point {
	if p == nil {
		return []netstats.Point{}
	}
	return p
}
