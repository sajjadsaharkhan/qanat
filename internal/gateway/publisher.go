package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"qanat/internal/core/usage"
)

// bufferSize bounds usage events held in memory while Redis is unreachable
// (§15.3). Beyond it events are dropped and counted, never blocking a stream.
const bufferSize = 10_000

// Publisher writes usage events to Redis Streams and the recent-request ring
// buffer, off the hot path.
type Publisher struct {
	rdb  *redis.Client
	log  *slog.Logger
	ch   chan usage.Event
	done chan struct{}
}

// NewPublisher starts the background writer.
func NewPublisher(rdb *redis.Client, log *slog.Logger) *Publisher {
	p := &Publisher{rdb: rdb, log: log, ch: make(chan usage.Event, bufferSize), done: make(chan struct{})}
	go p.run()
	return p
}

// Publish queues ev without blocking.
func (p *Publisher) Publish(ev usage.Event) {
	select {
	case p.ch <- ev:
		usageBuffered.Set(float64(len(p.ch)))
	default:
		usageDropped.Inc()
		p.log.Error("usage buffer full; event dropped", "request_id", ev.RequestID)
	}
}

func (p *Publisher) run() {
	defer close(p.done)
	for ev := range p.ch {
		for delay := 100 * time.Millisecond; ; delay = min(delay*2, 5*time.Second) {
			if err := p.write(ev); err == nil {
				break
			} else {
				redisErrors.Inc()
				p.log.Warn("usage write failed; retrying", "err", err, "retry_in", delay.String())
			}
			time.Sleep(delay)
		}
		usageBuffered.Set(float64(len(p.ch)))
	}
}

func (p *Publisher) write(ev usage.Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	js, err := json.Marshal(ev)
	if err != nil {
		return nil // unencodable: nothing to retry
	}
	pipe := p.rdb.TxPipeline()
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: usage.Stream, Values: map[string]any{"e": js}})
	key := usage.RecentKey(ev.UserID)
	pipe.LPush(ctx, key, js)
	pipe.LTrim(ctx, key, 0, usage.RecentLen-1)
	pipe.Expire(ctx, key, usage.RecentTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// Close stops accepting events and waits for the buffer to drain, up to
// ctx's deadline (graceful shutdown, §15.3).
func (p *Publisher) Close(ctx context.Context) {
	close(p.ch)
	select {
	case <-p.done:
	case <-ctx.Done():
		p.log.Error("shutdown before the usage buffer drained", "remaining", len(p.ch))
	}
}
