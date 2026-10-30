package mockupstream

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

// Request is everything the upstream observed about one call.
type Request struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
	// ArrivedAt is when the request headers reached the upstream.
	ArrivedAt time.Time
	// WrittenAt[i] is when step i was written and flushed.
	WrittenAt []time.Time
	// ClientGone is true if the caller (the gateway) cancelled before the
	// script finished. Used to assert REQ-PERF-03 (disconnect cancels upstream).
	ClientGone bool
	// Done is closed when the handler returns.
	Done chan struct{}
}

// Server is a scripted fake provider. Scripts are queued per path and served
// in FIFO order; when a path's queue is empty the Default script is served.
type Server struct {
	srv *httptest.Server

	// closing is closed at shutdown so held-open or delayed responses end even
	// if a misbehaving gateway never cancels them. Without this, a gateway bug
	// (e.g. not propagating client disconnects) would hang test cleanup
	// instead of just failing the test.
	closing chan struct{}

	mu       sync.Mutex
	queues   map[string][]Script
	defaults map[string]Script
	requests []*Request
}

// EnvBind, when set (e.g. "0.0.0.0:0"), makes New listen there instead of
// loopback, so a gateway running in a container can reach the mock
// (make test-acceptance sets it).
const EnvBind = "QANAT_MOCKUPSTREAM_BIND"

// New starts a server and registers cleanup on t.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{queues: map[string][]Script{}, defaults: map[string]Script{}, closing: make(chan struct{})}
	s.srv = httptest.NewUnstartedServer(http.HandlerFunc(s.handle))
	if addr := os.Getenv(EnvBind); addr != "" {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("%s=%s: %v", EnvBind, addr, err)
		}
		s.srv.Listener.Close()
		s.srv.Listener = ln
	}
	s.srv.Start()
	t.Cleanup(func() {
		close(s.closing)
		s.srv.Close()
	})
	return s
}

// URL is the base URL to register as the provider endpoint.
func (s *Server) URL() string { return s.srv.URL }

// Enqueue serves sc for the next request to path.
func (s *Server) Enqueue(path string, sc Script) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queues[path] = append(s.queues[path], sc)
}

// SetDefault serves sc for every request to path once its queue is empty.
func (s *Server) SetDefault(path string, sc Script) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaults[path] = sc
}

// Requests returns a snapshot of all recorded requests in arrival order.
func (s *Server) Requests() []*Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Request(nil), s.requests...)
}

// Last returns the most recent request, or nil.
func (s *Server) Last() *Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return nil
	}
	return s.requests[len(s.requests)-1]
}

func (s *Server) next(path string) (Script, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q := s.queues[path]; len(q) > 0 {
		s.queues[path] = q[1:]
		return q[0], true
	}
	sc, ok := s.defaults[path]
	return sc, ok
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	rec := &Request{
		Method:    r.Method,
		Path:      r.URL.Path,
		Header:    r.Header.Clone(),
		ArrivedAt: time.Now(),
		Done:      make(chan struct{}),
	}
	defer close(rec.Done)
	rec.Body, _ = io.ReadAll(r.Body)

	s.mu.Lock()
	s.requests = append(s.requests, rec)
	s.mu.Unlock()

	sc, ok := s.next(r.URL.Path)
	if !ok {
		http.Error(w, `{"type":"error","error":{"type":"not_found_error","message":"mockupstream: no script for path"}}`, http.StatusNotFound)
		return
	}

	for k, v := range sc.Headers {
		w.Header().Set(k, v)
	}
	status := sc.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	fl, _ := w.(http.Flusher)
	if fl != nil {
		fl.Flush()
	}

	ctx := r.Context()
	for i, st := range sc.Steps {
		if st.Delay > 0 {
			timer := time.NewTimer(st.Delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				s.markGone(rec)
				return
			case <-s.closing:
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		if _, err := w.Write(st.Data); err != nil {
			s.markGone(rec)
			return
		}
		if fl != nil {
			fl.Flush()
		}
		s.mu.Lock()
		rec.WrittenAt = append(rec.WrittenAt, time.Now())
		s.mu.Unlock()

		if sc.AbortAfter == i {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close()
				}
			}
			return
		}
	}
	if sc.HoldOpen {
		select {
		case <-ctx.Done():
			s.markGone(rec)
		case <-s.closing:
		}
	}
}

func (s *Server) markGone(rec *Request) {
	s.mu.Lock()
	rec.ClientGone = true
	s.mu.Unlock()
}
