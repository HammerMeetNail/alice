// Package metrics exposes Prometheus instrumentation for the alice server.
// It registers a fixed set of collectors against the default registry and
// provides helpers that middleware and services can call to record events.
package metrics

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// HTTP metrics
	httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "alice_http_requests_total",
		Help: "Total number of HTTP requests handled, by method, route, and status code.",
	}, []string{"method", "route", "status"})

	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "alice_http_request_duration_seconds",
		Help:    "HTTP request latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})

	httpResponseSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "alice_http_response_size_bytes",
		Help:    "HTTP response body size in bytes.",
		Buckets: []float64{64, 256, 1024, 4096, 16384, 65536, 262144, 1048576},
	}, []string{"method", "route"})

	// Rate-limiter metrics
	rateLimitRejectionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "alice_rate_limit_rejections_total",
		Help: "Total number of requests rejected by rate limiters.",
	}, []string{"limiter"})

	// Gatekeeper metrics
	gatekeeperAutoAnswersTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "alice_gatekeeper_auto_answers_total",
		Help: "Total number of requests auto-answered by the gatekeeper.",
	})

	// Audit event metrics
	auditEventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "alice_audit_events_total",
		Help: "Total number of audit events recorded, by kind.",
	}, []string{"kind"})
)

// DBStatsGetter is implemented by *sql.DB and allows the metrics package to
// periodically read connection-pool statistics without importing the storage
// packages directly.
type DBStatsGetter interface {
	Stats() sql.DBStats
}

// dbStatsCollector is a Prometheus Collector that reads sql.DBStats on each
// Collect call, enabling accurate connection-pool gauges.
type dbStatsCollector struct {
	db      DBStatsGetter
	openC   *prometheus.Desc
	inUseC  *prometheus.Desc
	idleC   *prometheus.Desc
	waitC   *prometheus.Desc
	waitDur *prometheus.Desc
}

// newDBStatsCollector returns a Collector that exposes sql.DBStats from db.
func newDBStatsCollector(db DBStatsGetter) prometheus.Collector {
	return &dbStatsCollector{
		db: db,
		openC:   prometheus.NewDesc("alice_db_pool_open_connections", "Number of open DB connections (in-use + idle).", nil, nil),
		inUseC:  prometheus.NewDesc("alice_db_pool_in_use_connections", "Number of DB connections currently in use.", nil, nil),
		idleC:   prometheus.NewDesc("alice_db_pool_idle_connections", "Number of idle DB connections.", nil, nil),
		waitC:   prometheus.NewDesc("alice_db_pool_wait_total", "Total number of times a new connection was waited for.", nil, nil),
		waitDur: prometheus.NewDesc("alice_db_pool_wait_duration_seconds_total", "Total time blocked waiting for a new connection.", nil, nil),
	}
}

func (c *dbStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.openC
	ch <- c.inUseC
	ch <- c.idleC
	ch <- c.waitC
	ch <- c.waitDur
}

func (c *dbStatsCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.db.Stats()
	ch <- prometheus.MustNewConstMetric(c.openC, prometheus.GaugeValue, float64(stats.OpenConnections))
	ch <- prometheus.MustNewConstMetric(c.inUseC, prometheus.GaugeValue, float64(stats.InUse))
	ch <- prometheus.MustNewConstMetric(c.idleC, prometheus.GaugeValue, float64(stats.Idle))
	ch <- prometheus.MustNewConstMetric(c.waitC, prometheus.CounterValue, float64(stats.WaitCount))
	ch <- prometheus.MustNewConstMetric(c.waitDur, prometheus.CounterValue, stats.WaitDuration.Seconds())
}

// Register registers all alice metrics and optional DB stats against reg.
// Pass nil for db to skip connection-pool gauges (in-memory mode).
// Passing nil for reg is a caller bug; Register will panic to surface it early.
func Register(reg prometheus.Registerer, db DBStatsGetter) error {
	if reg == nil {
		panic("metrics.Register: nil Registerer — pass prometheus.DefaultRegisterer or a custom registry")
	}
	// Standard Go runtime metrics.
	if err := reg.Register(collectors.NewGoCollector()); err != nil {
		return err
	}
	if err := reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return err
	}

	for _, c := range []prometheus.Collector{
		httpRequestsTotal,
		httpRequestDuration,
		httpResponseSize,
		rateLimitRejectionsTotal,
		gatekeeperAutoAnswersTotal,
		auditEventsTotal,
	} {
		if err := reg.Register(c); err != nil {
			return err
		}
	}

	if db != nil {
		if err := reg.Register(newDBStatsCollector(db)); err != nil {
			return err
		}
	}
	return nil
}

// Handler returns an http.Handler that serves the Prometheus metrics endpoint.
func Handler() http.Handler {
	return promhttp.Handler()
}

// RecordRateLimitRejection increments the rejection counter for the named limiter.
func RecordRateLimitRejection(limiter string) {
	rateLimitRejectionsTotal.WithLabelValues(limiter).Inc()
}

// RecordGatekeeperAutoAnswer increments the gatekeeper auto-answer counter.
func RecordGatekeeperAutoAnswer() {
	gatekeeperAutoAnswersTotal.Inc()
}

// RecordAuditEvent increments the audit event counter for kind.
func RecordAuditEvent(kind string) {
	auditEventsTotal.WithLabelValues(kind).Inc()
}

// responseWriter wraps http.ResponseWriter to capture status code and body size.
type responseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.size += n
	return n, err
}

// InstrumentHandler wraps next with Prometheus request/duration/size recording.
// The route label is derived from the matched ServeMux pattern (Go 1.22+).
func InstrumentHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rw, r)
		dur := time.Since(start)

		route := r.Pattern
		if route == "" {
			route = r.URL.Path
		}
		method := r.Method
		status := strconv.Itoa(rw.status)

		httpRequestsTotal.WithLabelValues(method, route, status).Inc()
		httpRequestDuration.WithLabelValues(method, route).Observe(dur.Seconds())
		httpResponseSize.WithLabelValues(method, route).Observe(float64(rw.size))
	})
}
