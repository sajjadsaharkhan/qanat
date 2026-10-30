package netstats

import (
	"slices"
	"time"
)

// Merged is one provider across every gateway's report.
type Merged struct {
	ID       string
	Active   int
	Accounts []Account // Active summed; usable on any gateway
	Probes   []Probe   // latest per (route, origin)
	Events   []Event   // oldest first
	History  []Bucket
	Status   Status
	// RefreshedAt is the newest report that mentioned the provider.
	RefreshedAt time.Time
}

// View is every report combined.
type View struct {
	Internet        *Probe
	InternetHistory []Point
	Providers       map[string]*Merged
}

// Merge combines reports from several gateways. Counts add up; history
// percentiles are averaged weighted by requests (exact percentiles come
// from Summarize over the merged events).
func Merge(reports []Report, now time.Time) View {
	v := View{Providers: map[string]*Merged{}}
	inet := map[time.Time]*int64{}
	statuses := map[string][]Status{}
	for _, r := range reports {
		if r.Internet != nil && (v.Internet == nil || r.Internet.Fails > v.Internet.Fails ||
			r.Internet.Fails == v.Internet.Fails && r.Internet.TotalMS > v.Internet.TotalMS) {
			p := *r.Internet
			v.Internet = &p
		}
		for _, pt := range r.InternetHistory {
			if cur, seen := inet[pt.T]; !seen || pt.MS == nil || cur != nil && *pt.MS > *cur {
				inet[pt.T] = pt.MS
			}
		}
		for _, p := range r.Providers {
			m := v.Providers[p.ID]
			if m == nil {
				m = &Merged{ID: p.ID}
				v.Providers[p.ID] = m
			}
			if r.At.After(m.RefreshedAt) {
				m.RefreshedAt = r.At
			}
			m.Active += p.Active
			m.Accounts = mergeAccounts(m.Accounts, p.Accounts)
			m.Probes = mergeProbes(m.Probes, p.Probes)
			m.Events = append(m.Events, p.Events...)
			m.History = mergeHistory(m.History, p.History)
			statuses[p.ID] = append(statuses[p.ID], p.Status)
		}
	}
	for t, ms := range inet {
		v.InternetHistory = append(v.InternetHistory, Point{T: t, MS: ms})
	}
	slices.SortFunc(v.InternetHistory, func(a, b Point) int { return a.T.Compare(b.T) })

	for id, m := range v.Providers {
		slices.SortStableFunc(m.Events, func(a, b Event) int { return a.At.Compare(b.At) })
		state, reason := Assess(m.Accounts, m.Probes, v.Internet, m.Events, now)
		m.Status = Status{State: state, Reason: reason, Since: m.RefreshedAt}
		for _, s := range statuses[id] {
			if s.State == state && !s.Since.IsZero() && s.Since.Before(m.Status.Since) {
				m.Status.Since = s.Since
			}
		}
	}
	return v
}

func mergeAccounts(into, from []Account) []Account {
	for _, a := range from {
		i := slices.IndexFunc(into, func(b Account) bool { return b.ID == a.ID })
		if i < 0 {
			into = append(into, a)
			continue
		}
		b := &into[i]
		b.Active += a.Active
		b.Usable = b.Usable || a.Usable
		if a.CoolingUntil != nil && (b.CoolingUntil == nil || a.CoolingUntil.After(*b.CoolingUntil)) {
			b.CoolingUntil = a.CoolingUntil
		}
	}
	return into
}

func mergeProbes(into, from []Probe) []Probe {
	for _, p := range from {
		i := slices.IndexFunc(into, func(q Probe) bool { return q.Route == p.Route && q.Origin == p.Origin })
		switch {
		case i < 0:
			into = append(into, p)
		case p.At.After(into[i].At):
			into[i] = p
		}
	}
	return into
}

func mergeHistory(into, from []Bucket) []Bucket {
	for _, b := range from {
		i := slices.IndexFunc(into, func(c Bucket) bool { return c.T.Equal(b.T) })
		if i < 0 {
			into = append(into, b)
			continue
		}
		c := &into[i]
		c.TTFTP50 = weighted(c.TTFTP50, c.Requests, b.TTFTP50, b.Requests)
		c.TTFTP95 = weighted(c.TTFTP95, c.Requests, b.TTFTP95, b.Requests)
		if b.HandshakeMS != nil && (c.HandshakeMS == nil || *b.HandshakeMS < *c.HandshakeMS) {
			c.HandshakeMS = b.HandshakeMS
		}
		c.Requests += b.Requests
		c.Errors += b.Errors
		c.Rejected += b.Rejected
		c.Active += b.Active
	}
	slices.SortFunc(into, func(a, b Bucket) int { return a.T.Compare(b.T) })
	return into
}

func weighted(a *int64, na int64, b *int64, nb int64) *int64 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	v := (*a*na + *b*nb) / max(na+nb, 1)
	return &v
}
