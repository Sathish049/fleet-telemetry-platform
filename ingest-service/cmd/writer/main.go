// Command writer reads telemetry from the Redis stream, stores it in MySQL,
// updates each vehicle's latest state, and runs the alert rules.
//
// It uses a Redis consumer group, which gives "at-least-once" delivery:
//   - XREADGROUP hands each message to one consumer.
//   - The message stays "pending" until we XACK it.
//   - We only XACK after MySQL has committed the batch.
//   - If the writer crashes mid-batch, the pending messages are re-read on
//     restart. Duplicates are harmless because telemetry has a primary key
//     on (vehicle_id, ts) and we use INSERT IGNORE (idempotent writes).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/alerts"
	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/config"
	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/metrics"
	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/store"
	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/telemetry"
)

type writer struct {
	log      *slog.Logger
	rdb      *redis.Client
	db       *store.Store
	engine   *alerts.Engine
	stream   string
	group    string
	consumer string
	batch    int64

	rowsWritten *metrics.Counter
	duplicates  *metrics.Counter
	badMessages *metrics.Counter
	alertsFired *metrics.Counter
	dbErrors    *metrics.Counter
	batchTime   *metrics.Histogram
	e2eLatency  *metrics.Histogram
	streamLag   *metrics.Gauge
	pending     *metrics.Gauge
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	host, _ := os.Hostname()
	reg := metrics.NewRegistry()
	w := &writer{
		log:      log,
		rdb:      redis.NewClient(&redis.Options{Addr: config.String("REDIS_ADDR", "localhost:6379")}),
		engine:   alerts.NewEngine(alerts.Defaults()),
		stream:   config.String("STREAM", "telemetry"),
		group:    config.String("CONSUMER_GROUP", "writers"),
		consumer: config.String("CONSUMER_NAME", host),
		batch:    int64(config.Int("WRITE_BATCH", 1000)),

		rowsWritten: reg.NewCounter("writer_rows_written_total", "Telemetry rows inserted into MySQL"),
		duplicates:  reg.NewCounter("writer_duplicates_ignored_total", "Messages skipped because the row already existed"),
		badMessages: reg.NewCounter("writer_bad_messages_total", "Stream entries that could not be decoded"),
		alertsFired: reg.NewCounter("writer_alerts_total", "Alerts created by the rules engine"),
		dbErrors:    reg.NewCounter("writer_db_errors_total", "Failed MySQL batch writes (retried)"),
		batchTime:   reg.NewHistogram("writer_batch_seconds", "Time to store one batch in MySQL", metrics.LatencyBuckets),
		e2eLatency:  reg.NewHistogram("writer_end_to_end_seconds", "Time from vehicle timestamp to row stored", metrics.LatencyBuckets),
		streamLag:   reg.NewGauge("writer_stream_lag", "Stream entries not yet delivered to the consumer group"),
		pending:     reg.NewGauge("writer_pending_messages", "Entries delivered but not yet acknowledged"),
	}

	// Connect to MySQL with retries (the database may still be starting).
	dsn := config.String("MYSQL_DSN", "fleet:fleet@tcp(localhost:3306)/fleet?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27")
	var err error
	for i := 0; ; i++ {
		if w.db, err = store.Open(ctx, dsn); err == nil {
			break
		}
		if i == 60 || ctx.Err() != nil {
			log.Error("mysql not reachable", "err", err)
			os.Exit(1)
		}
		time.Sleep(2 * time.Second)
	}
	defer w.db.Close()

	// Create the consumer group once. "0" means start from the beginning of
	// the stream, so messages published before the writer started are kept.
	err = w.rdb.XGroupCreateMkStream(ctx, w.stream, w.group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		log.Error("create consumer group", "err", err)
		os.Exit(1)
	}

	go w.partitionLoop(ctx)
	go w.lagLoop(ctx)

	metricsAddr := config.String("METRICS_ADDR", ":9101")
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", reg.Handler())
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, _ *http.Request) { rw.Write([]byte("ok")) })
	go func() { _ = http.ListenAndServe(metricsAddr, mux) }()

	log.Info("writer started", "stream", w.stream, "group", w.group, "consumer", w.consumer, "metrics", metricsAddr)
	w.run(ctx)
	log.Info("writer stopped", "rows", w.rowsWritten.Value())
}

// run first re-processes anything left pending by a previous crash
// (ID "0"), then switches to new messages (ID ">").
func (w *writer) run(ctx context.Context) {
	startID := "0"
	for ctx.Err() == nil {
		res, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    w.group,
			Consumer: w.consumer,
			Streams:  []string{w.stream, startID},
			Count:    w.batch,
			Block:    2 * time.Second,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue // no new messages within the block timeout
		}
		if err != nil {
			if ctx.Err() == nil {
				w.log.Warn("xreadgroup failed", "err", err)
				time.Sleep(time.Second)
			}
			continue
		}
		msgs := res[0].Messages
		if startID == "0" && len(msgs) == 0 {
			startID = ">" // pending backlog is empty, move on to new messages
			w.log.Info("pending backlog drained, reading new messages")
			continue
		}
		w.handleBatch(ctx, msgs)
	}
}

func (w *writer) handleBatch(ctx context.Context, msgs []redis.XMessage) {
	readings := make([]telemetry.Reading, 0, len(msgs))
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
		raw, _ := m.Values["d"].(string)
		var r telemetry.Reading
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			// A "poison" message would block the stream forever if we
			// retried it, so count it, log it and acknowledge it.
			w.badMessages.Inc()
			w.log.Warn("dropping undecodable message", "id", m.ID, "err", err)
			continue
		}
		readings = append(readings, r)
	}

	var fired []alerts.Alert
	for _, r := range readings {
		fired = append(fired, w.engine.Evaluate(r)...)
	}

	// Retry until MySQL accepts the batch. Nothing is acknowledged until
	// then, so a failure never loses data.
	start := time.Now()
	for attempt := 1; ; attempt++ {
		inserted, err := w.store(ctx, readings, fired)
		if err == nil {
			w.rowsWritten.Add(uint64(inserted))
			w.duplicates.Add(uint64(int64(len(readings)) - inserted))
			break
		}
		w.dbErrors.Inc()
		w.log.Warn("store batch failed, retrying", "attempt", attempt, "err", err)
		if ctx.Err() != nil {
			return // shutting down; messages stay pending for next start
		}
		time.Sleep(time.Duration(min(attempt, 10)) * 500 * time.Millisecond)
	}
	w.batchTime.Observe(time.Since(start).Seconds())
	w.alertsFired.Add(uint64(len(fired)))

	now := time.Now()
	for _, r := range readings {
		w.e2eLatency.Observe(now.Sub(r.Timestamp).Seconds())
	}

	if err := w.rdb.XAck(context.Background(), w.stream, w.group, ids...).Err(); err != nil {
		w.log.Warn("xack failed (messages will be re-delivered, inserts are idempotent)", "err", err)
	}
}

func (w *writer) store(ctx context.Context, rs []telemetry.Reading, as []alerts.Alert) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	n, err := w.db.InsertTelemetry(ctx, rs)
	if err != nil {
		return n, err
	}
	if err := w.db.UpsertLatest(ctx, rs); err != nil {
		return n, err
	}
	return n, w.db.InsertAlerts(ctx, as)
}

// partitionLoop keeps daily partitions ready and drops expired ones.
func (w *writer) partitionLoop(ctx context.Context) {
	ahead := config.Int("PARTITION_DAYS_AHEAD", 3)
	retention := config.Int("RETENTION_DAYS", 7)
	for {
		plan, err := w.db.MaintainPartitions(ctx, time.Now(), ahead, retention)
		if err != nil {
			w.log.Warn("partition maintenance failed", "err", err)
		} else if len(plan.Add)+len(plan.Drop) > 0 {
			w.log.Info("partitions updated", "added", len(plan.Add), "dropped", plan.Drop)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// lagLoop reports how far the writers are behind the stream.
func (w *writer) lagLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		groups, err := w.rdb.XInfoGroups(ctx, w.stream).Result()
		if err != nil {
			continue
		}
		for _, g := range groups {
			if g.Name == w.group {
				w.streamLag.Set(float64(g.Lag))
				w.pending.Set(float64(g.Pending))
			}
		}
	}
}
