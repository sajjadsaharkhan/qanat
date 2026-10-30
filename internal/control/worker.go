package control

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"qanat/internal/core/quota"
	"qanat/internal/core/usage"
	"qanat/internal/store/db"
)

// Worker is the single consumer of the usage stream (PRD §10.6, D17). It
// upserts 5-minute, hourly and daily aggregates; being the only writer means
// aggregate rows never race.
type Worker struct {
	pg       *pgxpool.Pool
	rdb      *redis.Client
	log      *slog.Logger
	mu       sync.Mutex // one batch at a time (Run and Flush)
	consumer string
}

// NewWorker creates the aggregation worker.
func NewWorker(pg *pgxpool.Pool, rdb *redis.Client, log *slog.Logger) *Worker {
	return &Worker{pg: pg, rdb: rdb, log: log, consumer: "control-1"}
}

const batchSize = 500

// Run consumes until ctx ends. It first re-reads its own pending entries, so
// events read but not acknowledged before a crash are not lost.
func (w *Worker) Run(ctx context.Context) {
	w.ensureGroup(ctx)
	for ctx.Err() == nil {
		n, err := w.batch(ctx, "0", 0) // pending first (crash recovery)
		if err == nil && n == 0 {
			n, err = w.batch(ctx, ">", time.Second)
		}
		if err != nil && ctx.Err() == nil {
			if strings.Contains(err.Error(), "NOGROUP") {
				w.ensureGroup(ctx) // Redis lost the stream (restart without AOF, or a test reset)
				continue
			}
			w.log.Warn("usage worker", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

// Flush drains everything currently in the stream (tests, shutdown).
func (w *Worker) Flush(ctx context.Context) error {
	w.ensureGroup(ctx)
	for {
		n, err := w.batch(ctx, ">", -1)
		if err != nil || n == 0 {
			return err
		}
	}
}

func (w *Worker) ensureGroup(ctx context.Context) {
	err := w.rdb.XGroupCreateMkStream(ctx, usage.Stream, usage.Group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		w.log.Warn("usage group", "err", err)
	}
}

// batch reads up to batchSize entries starting at id ("0" = own pending,
// ">" = new) and commits them. block <= 0 returns at once.
func (w *Worker) batch(ctx context.Context, id string, block time.Duration) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if block <= 0 || id == "0" {
		block = -1 // go-redis: negative = no BLOCK argument, return at once
	}
	args := &redis.XReadGroupArgs{Group: usage.Group, Consumer: w.consumer, Streams: []string{usage.Stream, id}, Count: batchSize, Block: block}
	res, err := w.rdb.XReadGroup(ctx, args).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil || len(res) == 0 || len(res[0].Messages) == 0 {
		return 0, err
	}
	msgs := res[0].Messages
	events := make([]usage.Event, 0, len(msgs))
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
		raw, _ := m.Values["e"].(string)
		var ev usage.Event
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			w.log.Error("usage event unreadable; skipped", "id", m.ID, "err", err)
			continue
		}
		events = append(events, ev)
	}
	if err := w.commit(ctx, events); err != nil {
		return 0, err
	}
	pipe := w.rdb.Pipeline()
	pipe.XAck(ctx, usage.Stream, usage.Group, ids...)
	pipe.XDel(ctx, usage.Stream, ids...)
	_, err = pipe.Exec(ctx)
	return len(msgs), err
}

func (w *Worker) commit(ctx context.Context, events []usage.Event) error {
	return pgx.BeginFunc(ctx, w.pg, func(tx pgx.Tx) error {
		q := db.New(tx)
		for _, ev := range events {
			if ev.UserID == "" || ev.KeyID == "" {
				continue
			}
			at := ev.At.UTC()
			cost := quota.FromMicro(ev.CostMicro)
			params := func(bucket time.Time) db.UpsertUsage5mParams {
				return db.UpsertUsage5mParams{
					BucketStart: bucket, UserID: ev.UserID, KeyID: ev.KeyID, Provider: ev.Provider, UpstreamModel: ev.UpstreamModel, AccountID: ev.AccountID,
					Aborted: ev.Aborted, PriceVersion: ev.PriceVersion, InputTokens: ev.InputTokens, OutputTokens: ev.OutputTokens,
					CacheReadTokens: ev.CacheReadTokens, CacheWriteTokens: ev.CacheWriteTokens, CostUsd: cost,
				}
			}
			if err := q.UpsertUsage5m(ctx, params(at.Truncate(quota.BucketSpan))); err != nil {
				return err
			}
			if err := q.UpsertUsageHourly(ctx, db.UpsertUsageHourlyParams(params(at.Truncate(time.Hour)))); err != nil {
				return err
			}
			day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
			if err := q.UpsertUsageDaily(ctx, db.UpsertUsageDailyParams(params(day))); err != nil {
				return err
			}
			var ip *netip.Addr
			if a, err := netip.ParseAddr(ev.ClientIP); err == nil {
				ip = &a
			}
			if err := q.TouchKey(ctx, db.TouchKeyParams{ID: ev.KeyID, LastUsedAt: &at, LastIp: ip}); err != nil {
				return err
			}
		}
		return nil
	})
}
