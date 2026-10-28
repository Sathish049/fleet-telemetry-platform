// Package metrics is a small, dependency-free Prometheus exporter.
//
// It supports the three metric types this project needs (counter, gauge,
// histogram) and writes them in the Prometheus text exposition format, so
// Prometheus can scrape /metrics directly. The official client library
// (github.com/prometheus/client_golang) does the same thing with many more
// features; this version keeps the services small and easy to read.
package metrics

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type metric interface {
	write(sb *strings.Builder)
}

// Registry holds all metrics for one process.
type Registry struct {
	mu      sync.Mutex
	metrics map[string]metric
}

func NewRegistry() *Registry { return &Registry{metrics: map[string]metric{}} }

func (r *Registry) register(name string, m metric) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.metrics[name]; dup {
		panic("metric registered twice: " + name)
	}
	r.metrics[name] = m
}

// Handler serves every registered metric in text format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.mu.Lock()
		names := make([]string, 0, len(r.metrics))
		for n := range r.metrics {
			names = append(names, n)
		}
		sort.Strings(names)
		var sb strings.Builder
		for _, n := range names {
			r.metrics[n].write(&sb)
		}
		r.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(sb.String()))
	})
}

// ---------- Counter ----------

// Counter only goes up (messages received, rows written, ...).
type Counter struct {
	name, help string
	v          atomic.Uint64
}

func (r *Registry) NewCounter(name, help string) *Counter {
	c := &Counter{name: name, help: help}
	r.register(name, c)
	return c
}

func (c *Counter) Inc()          { c.v.Add(1) }
func (c *Counter) Add(n uint64)  { c.v.Add(n) }
func (c *Counter) Value() uint64 { return c.v.Load() }

func (c *Counter) write(sb *strings.Builder) {
	fmt.Fprintf(sb, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", c.name, c.help, c.name, c.name, c.v.Load())
}

// ---------- Gauge ----------

// Gauge can go up and down (open connections, queue depth, ...).
type Gauge struct {
	name, help string
	bits       atomic.Uint64
}

func (r *Registry) NewGauge(name, help string) *Gauge {
	g := &Gauge{name: name, help: help}
	r.register(name, g)
	return g
}

func (g *Gauge) Set(v float64) { g.bits.Store(math.Float64bits(v)) }
func (g *Gauge) Add(d float64) {
	for {
		old := g.bits.Load()
		nv := math.Float64bits(math.Float64frombits(old) + d)
		if g.bits.CompareAndSwap(old, nv) {
			return
		}
	}
}
func (g *Gauge) Value() float64 { return math.Float64frombits(g.bits.Load()) }

func (g *Gauge) write(sb *strings.Builder) {
	fmt.Fprintf(sb, "# HELP %s %s\n# TYPE %s gauge\n%s %g\n", g.name, g.help, g.name, g.name, g.Value())
}

// ---------- Histogram ----------

// Histogram records a distribution (latencies, batch sizes). Prometheus
// computes percentiles from the buckets with histogram_quantile().
type Histogram struct {
	name, help string
	buckets    []float64
	mu         sync.Mutex
	counts     []uint64
	sum        float64
	count      uint64
}

// LatencyBuckets are in seconds: 0.5ms .. 5s.
var LatencyBuckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

func (r *Registry) NewHistogram(name, help string, buckets []float64) *Histogram {
	h := &Histogram{name: name, help: help, buckets: buckets, counts: make([]uint64, len(buckets))}
	r.register(name, h)
	return h
}

func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i]++
		}
	}
	h.sum += v
	h.count++
	h.mu.Unlock()
}

func (h *Histogram) write(sb *strings.Builder) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(sb, "# HELP %s %s\n# TYPE %s histogram\n", h.name, h.help, h.name)
	for i, b := range h.buckets {
		fmt.Fprintf(sb, "%s_bucket{le=\"%g\"} %d\n", h.name, b, h.counts[i])
	}
	fmt.Fprintf(sb, "%s_bucket{le=\"+Inf\"} %d\n%s_sum %g\n%s_count %d\n", h.name, h.count, h.name, h.sum, h.name, h.count)
}
