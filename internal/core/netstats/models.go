package netstats

import (
	"strconv"
	"strings"
	"time"
)

// Per-model health over the last 24 hours, for the Network page's Model
// Status section. Gateways add each attempt to a counter for its (provider,
// model, 30-minute bucket) and flush the counters to one Redis hash per
// bucket with HINCRBY, so several gateways sum without coordinating. The
// hashes expire after ModelTTL; nothing else is stored.
const (
	ModelBucket  = 30 * time.Minute
	ModelBuckets = 48 // 24 hours
	ModelTTL     = 25 * time.Hour
)

// ModelKey is the Redis hash holding the bucket that starts at start.
func ModelKey(start time.Time) string {
	return keyPrefix + "models:" + strconv.FormatInt(start.Unix(), 10)
}

// ModelBucketStart is the start of the bucket containing t.
func ModelBucketStart(t time.Time) time.Time { return t.Truncate(ModelBucket) }

// ttftBounds are the upper edges, in ms, of the time-to-first-byte
// histogram; a last bin takes everything slower. P90 is read from it, so it
// is exact to the bin and interpolated within it.
var ttftBounds = [...]int64{100, 250, 500, 1000, 2000, 4000, 8000, 16000, 32000, 64000, 128000}

const ttftBins = len(ttftBounds) + 1

// ModelAgg is the attempts of one model in one bucket. Only attempts the
// provider or the path decided count: a success, or an upstream fault
// (connect, mid-stream, 5xx). Rate limits, revoked sign-ins, client aborts
// and pool rejections say nothing about the model's health.
type ModelAgg struct {
	OK, Faults int64
	DurSumMS   int64 // over OK attempts
	TTFTN      int64
	TTFT       [ttftBins]int64
}

// Add counts one attempt and reports whether it counted.
func (a *ModelAgg) Add(e Event) bool {
	switch {
	case e.Outcome == OK:
		a.OK++
		a.DurSumMS += e.DurMS
		if e.TTFTMS > 0 {
			a.TTFTN++
			a.TTFT[ttftBin(e.TTFTMS)]++
		}
	case e.upstreamFault():
		a.Faults++
	default:
		return false
	}
	return true
}

func ttftBin(ms int64) int {
	for i, b := range ttftBounds {
		if ms <= b {
			return i
		}
	}
	return len(ttftBounds)
}

// Merge adds b into a.
func (a *ModelAgg) Merge(b ModelAgg) {
	a.OK += b.OK
	a.Faults += b.Faults
	a.DurSumMS += b.DurSumMS
	a.TTFTN += b.TTFTN
	for i := range a.TTFT {
		a.TTFT[i] += b.TTFT[i]
	}
}

// Attempts is every counted attempt.
func (a ModelAgg) Attempts() int64 { return a.OK + a.Faults }

// Availability is the share of counted attempts that did not fault; nil with
// no attempts.
func (a ModelAgg) Availability() *float64 {
	n := a.Attempts()
	if n == 0 {
		return nil
	}
	v := float64(a.OK) / float64(n)
	return &v
}

// AvgLatencyMS is the mean duration of successful attempts; nil with none.
func (a ModelAgg) AvgLatencyMS() *int64 {
	if a.OK == 0 {
		return nil
	}
	v := a.DurSumMS / a.OK
	return &v
}

// TTFTP90MS is the 90th percentile time to first byte, interpolated inside
// its histogram bin; nil with no samples. The open last bin reports its lower edge.
func (a ModelAgg) TTFTP90MS() *int64 {
	if a.TTFTN == 0 {
		return nil
	}
	rank := float64(a.TTFTN) * 0.9
	var seen float64
	for i, n := range a.TTFT {
		if n == 0 {
			continue
		}
		if seen+float64(n) < rank {
			seen += float64(n)
			continue
		}
		var lo, hi int64
		if i > 0 {
			lo = ttftBounds[i-1]
		}
		if i == len(ttftBounds) {
			return &lo
		}
		hi = ttftBounds[i]
		v := lo + int64(float64(hi-lo)*(rank-seen)/float64(n))
		return &v
	}
	return nil
}

// Hash fields are "provider|model|counter". Models don't contain "|".
const (
	fOK, fFaults, fDur, fTTFTN = "ok", "fault", "dur", "tn"
	fTTFT                      = "t" // + bin index
)

// ModelKeyID names one model of one provider.
type ModelKeyID struct{ Provider, Model string }

// Fields is the agg as HINCRBY increments, zero counters left out.
func (a ModelAgg) Fields(id ModelKeyID) map[string]int64 {
	out := map[string]int64{}
	put := func(name string, v int64) {
		if v != 0 {
			out[id.Provider+"|"+id.Model+"|"+name] = v
		}
	}
	put(fOK, a.OK)
	put(fFaults, a.Faults)
	put(fDur, a.DurSumMS)
	put(fTTFTN, a.TTFTN)
	for i, n := range a.TTFT {
		put(fTTFT+strconv.Itoa(i), n)
	}
	return out
}

// ParseModelHash reads one bucket's hash back into per-model aggs.
func ParseModelHash(h map[string]string) map[ModelKeyID]*ModelAgg {
	out := map[ModelKeyID]*ModelAgg{}
	for field, raw := range h {
		first := strings.IndexByte(field, '|')
		last := strings.LastIndexByte(field, '|')
		v, err := strconv.ParseInt(raw, 10, 64)
		if first < 0 || last == first || err != nil {
			continue
		}
		id := ModelKeyID{field[:first], field[first+1 : last]}
		a := out[id]
		if a == nil {
			a = &ModelAgg{}
			out[id] = a
		}
		switch name := field[last+1:]; {
		case name == fOK:
			a.OK = v
		case name == fFaults:
			a.Faults = v
		case name == fDur:
			a.DurSumMS = v
		case name == fTTFTN:
			a.TTFTN = v
		case strings.HasPrefix(name, fTTFT):
			if i, err := strconv.Atoi(name[len(fTTFT):]); err == nil && i >= 0 && i < ttftBins {
				a.TTFT[i] = v
			}
		}
	}
	return out
}
