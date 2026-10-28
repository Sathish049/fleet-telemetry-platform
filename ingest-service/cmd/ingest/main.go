// Command ingest accepts vehicle telemetry over WebSockets and REST,
// validates it, and publishes it to a Redis stream.
//
// Flow:  vehicle --(WebSocket/REST)--> handler --> validate --> queue (Go channel)
//
//	--> publisher goroutines --(pipelined XADD)--> Redis stream "telemetry"
//
// The in-memory queue decouples network reads from Redis writes. Publishers
// send messages to Redis in small batches using pipelining, which cuts the
// number of network round trips and is the main reason the service can
// handle thousands of messages per second.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/config"
	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/metrics"
	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/telemetry"
)

type server struct {
	log      *slog.Logger
	rdb      *redis.Client
	stream   string
	maxLen   int64
	queue    chan []byte
	upgrader websocket.Upgrader

	// metrics
	received     *metrics.Counter
	rejected     *metrics.Counter
	published    *metrics.Counter
	dropped      *metrics.Counter
	publishErrs  *metrics.Counter
	wsConns      *metrics.Gauge
	queueDepth   *metrics.Gauge
	publishBatch *metrics.Histogram
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	addr := config.String("LISTEN_ADDR", ":8080")
	redisAddr := config.String("REDIS_ADDR", "localhost:6379")
	queueSize := config.Int("QUEUE_SIZE", 20000)
	publishers := config.Int("PUBLISHERS", 4)
	batchSize := config.Int("PUBLISH_BATCH", 500)
	flushEvery := config.Duration("PUBLISH_FLUSH", 20*time.Millisecond)

	reg := metrics.NewRegistry()
	s := &server{
		log:      log,
		rdb:      redis.NewClient(&redis.Options{Addr: redisAddr, PoolSize: publishers * 2}),
		stream:   config.String("STREAM", "telemetry"),
		maxLen:   int64(config.Int("STREAM_MAXLEN", 2_000_000)),
		queue:    make(chan []byte, queueSize),
		upgrader: websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 1024, CheckOrigin: func(*http.Request) bool { return true }},

		received:     reg.NewCounter("ingest_messages_received_total", "Telemetry messages received"),
		rejected:     reg.NewCounter("ingest_messages_rejected_total", "Messages that failed validation"),
		published:    reg.NewCounter("ingest_messages_published_total", "Messages written to the Redis stream"),
		dropped:      reg.NewCounter("ingest_messages_dropped_total", "REST messages refused because the queue was full"),
		publishErrs:  reg.NewCounter("ingest_publish_errors_total", "Failed Redis pipeline executions"),
		wsConns:      reg.NewGauge("ingest_ws_connections", "Open WebSocket connections"),
		queueDepth:   reg.NewGauge("ingest_queue_depth", "Messages waiting in the in-memory queue"),
		publishBatch: reg.NewHistogram("ingest_publish_batch_seconds", "Time to publish one batch to Redis", metrics.LatencyBuckets),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Wait for Redis (useful when Docker starts everything at once).
	for i := 0; ; i++ {
		if err := s.rdb.Ping(ctx).Err(); err == nil {
			break
		} else if i == 30 || ctx.Err() != nil {
			log.Error("redis not reachable", "addr", redisAddr, "err", err)
			os.Exit(1)
		}
		time.Sleep(time.Second)
	}

	// Start publisher goroutines.
	var wg sync.WaitGroup
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.publishLoop(batchSize, flushEvery) }()
	}
	go func() {
		for range time.Tick(time.Second) {
			s.queueDepth.Set(float64(len(s.queue)))
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("POST /v1/telemetry", s.handleREST)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.Handle("GET /metrics", reg.Handler())

	httpSrv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("ingest listening", "addr", addr, "redis", redisAddr, "publishers", publishers)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown: stop accepting, then flush what is already queued.
	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	close(s.queue)
	wg.Wait()
	log.Info("shutdown complete", "published", s.published.Value())
}

// decode accepts either one JSON object or a JSON array of objects.
func decode(body []byte) ([]telemetry.Reading, error) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		var rs []telemetry.Reading
		err := json.Unmarshal(body, &rs)
		return rs, err
	}
	var r telemetry.Reading
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return []telemetry.Reading{r}, nil
}

type rejection struct {
	VehicleID string `json:"vehicle_id,omitempty"`
	Error     string `json:"error"`
}

// validate splits readings into encoded valid messages and rejections.
func (s *server) validate(rs []telemetry.Reading) ([][]byte, []rejection) {
	now := time.Now().UTC()
	valid := make([][]byte, 0, len(rs))
	var bad []rejection
	for _, r := range rs {
		s.received.Inc()
		if err := r.Validate(now); err != nil {
			s.rejected.Inc()
			bad = append(bad, rejection{VehicleID: r.VehicleID, Error: err.Error()})
			continue
		}
		b, _ := json.Marshal(r)
		valid = append(valid, b)
	}
	return valid, bad
}

// handleWS keeps one connection open per vehicle (or per gateway) and reads
// messages in a loop. If the queue is full, the send blocks, which stops us
// reading from the socket: TCP flow control then slows the sender down.
// This is called backpressure, and it means we never silently lose data.
func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	s.wsConns.Add(1)
	defer s.wsConns.Add(-1)

	conn.SetReadLimit(1 << 20)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return // client closed or network error
		}
		readings, err := decode(data)
		if err != nil {
			s.rejected.Inc()
			_ = conn.WriteJSON(rejection{Error: "invalid JSON: " + err.Error()})
			continue
		}
		valid, bad := s.validate(readings)
		for _, b := range valid {
			select {
			case s.queue <- b:
			case <-r.Context().Done():
				return
			}
		}
		for _, rej := range bad {
			_ = conn.WriteJSON(rej)
		}
	}
}

// handleREST accepts a batch over plain HTTP. Unlike WebSockets it never
// blocks: if the queue is full it answers 503 so the client can retry later.
func (s *server) handleREST(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, 5<<20)
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(body); err != nil {
		http.Error(w, "body too large or unreadable", http.StatusBadRequest)
		return
	}
	readings, err := decode(buf.Bytes())
	if err != nil {
		s.rejected.Inc()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	valid, bad := s.validate(readings)
	accepted := 0
	for _, b := range valid {
		select {
		case s.queue <- b:
			accepted++
		default:
			s.dropped.Add(uint64(len(valid) - accepted))
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "ingest queue is full, retry later", "accepted": accepted, "rejected": bad,
			})
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": accepted, "rejected": bad})
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.rdb.Ping(ctx).Err(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "redis": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "queue_depth": len(s.queue)})
}

// publishLoop collects up to batchSize messages (or whatever arrived within
// flushEvery) and writes them to Redis in a single pipelined round trip.
func (s *server) publishLoop(batchSize int, flushEvery time.Duration) {
	batch := make([][]byte, 0, batchSize)
	timer := time.NewTimer(flushEvery)
	defer timer.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		start := time.Now()
		for attempt := 1; ; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			pipe := s.rdb.Pipeline()
			for _, b := range batch {
				pipe.XAdd(ctx, &redis.XAddArgs{
					Stream: s.stream,
					MaxLen: s.maxLen,
					Approx: true, // "~" trimming is much cheaper than exact trimming
					Values: map[string]any{"d": b},
				})
			}
			_, err := pipe.Exec(ctx)
			cancel()
			if err == nil {
				break
			}
			s.publishErrs.Inc()
			s.log.Warn("publish failed, retrying", "attempt", attempt, "batch", len(batch), "err", err)
			time.Sleep(time.Duration(min(attempt, 10)) * 200 * time.Millisecond)
		}
		s.publishBatch.Observe(time.Since(start).Seconds())
		s.published.Add(uint64(len(batch)))
		batch = batch[:0]
	}

	for {
		select {
		case b, ok := <-s.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, b)
			if len(batch) >= batchSize {
				flush()
			}
		case <-timer.C:
			flush()
			timer.Reset(flushEvery)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
