package gateway

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"
)

// pingEvery is how long a translated stream may stay quiet before the client
// gets a ping (REQ-STREAM-07): long reasoning can leave Codex or Antigravity
// silent for minutes, and an idle proxy on the way may cut the connection. A
// var so tests can shorten it.
var pingEvery = 15 * time.Second

const pingEvent = "event: ping\ndata: {\"type\": \"ping\"}\n\n"

// sseWriter serializes writes to the client between the copy loop and the
// keep-alive pinger. A ping goes out only after the first byte and only when
// the last write ended an SSE event, so it never lands inside one.
type sseWriter struct {
	mu       sync.Mutex
	w        io.Writer
	rc       *http.ResponseController
	last     time.Time
	boundary bool
	stop     chan struct{}
	done     chan struct{}
}

func (s *sseWriter) write(b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write(b); err != nil {
		return err
	}
	s.rc.Flush()
	s.last, s.boundary = time.Now(), bytes.HasSuffix(b, []byte("\n\n"))
	return nil
}

// keepAlive starts the pinger; stopKeepAlive ends it and waits, so nothing
// writes to the client after the handler returns.
func (s *sseWriter) keepAlive() {
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(pingEvery / 5)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.mu.Lock()
				if s.boundary && time.Since(s.last) >= pingEvery {
					if _, err := io.WriteString(s.w, pingEvent); err == nil {
						s.rc.Flush()
						s.last = time.Now()
					}
				}
				s.mu.Unlock()
			}
		}
	}()
}

func (s *sseWriter) stopKeepAlive() {
	if s.stop != nil {
		close(s.stop)
		<-s.done
		s.stop = nil
	}
}
