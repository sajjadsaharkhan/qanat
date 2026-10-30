// Command mockupstream serves recorded provider streams over HTTP so the full
// docker compose test stack (deploy/test/compose.test.yml) can run the gateway
// against deterministic "providers" with realistic timing.
//
// Fixture selection, in order:
//  1. header X-Mock-Fixture: <provider>/<name>     e.g. claude/text_simple
//  2. -route flag defaults per path               e.g. /v1/messages=claude/text_simple
//
// Header X-Mock-Status forces an error status with an Anthropic-style body
// (useful for failover tests). /healthz returns 200.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"qanat/internal/testkit/mockupstream"
)

type routes map[string]string

func (r routes) String() string { return fmt.Sprint(map[string]string(r)) }
func (r routes) Set(v string) error {
	path, fixture, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("want PATH=provider/name, got %q", v)
	}
	r[path] = fixture
	return nil
}

func main() {
	addr := flag.String("addr", ":9090", "listen address")
	dir := flag.String("fixtures", "test/fixtures/upstream", "fixture root directory")
	speed := flag.Float64("speed", 1, "replay speed (2 = twice as fast, 0 = no delays)")
	rt := routes{
		"/v1/messages": "claude/text_simple",
	}
	flag.Var(rt, "route", "default fixture per path, PATH=provider/name (repeatable)")
	flag.Parse()

	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if s := r.Header.Get("X-Mock-Status"); s != "" {
			code, _ := strconv.Atoi(s)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("retry-after", "30")
			w.WriteHeader(code)
			fmt.Fprintf(w, `{"type":"error","error":{"type":"rate_limit_error","message":"mockupstream forced %d"}}`, code)
			return
		}
		name := r.Header.Get("X-Mock-Fixture")
		if name == "" {
			name = rt[r.URL.Path]
		}
		if name == "" || strings.Contains(name, "..") {
			http.Error(w, "no fixture for "+r.URL.Path, http.StatusNotFound)
			return
		}
		sc, err := mockupstream.LoadFixture(filepath.Join(*dir, name+".jsonl"), *speed)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		serve(w, r, sc)
	})
	log.Printf("mockupstream on %s (fixtures=%s speed=%v)", *addr, *dir, *speed)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func serve(w http.ResponseWriter, r *http.Request, sc mockupstream.Script) {
	for k, v := range sc.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(sc.Status)
	fl, _ := w.(http.Flusher)
	for _, st := range sc.Steps {
		if st.Delay > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(st.Delay):
			}
		}
		if _, err := w.Write(st.Data); err != nil {
			return
		}
		if fl != nil {
			fl.Flush()
		}
	}
}
