package metrics

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeDB satisfies DBStatsGetter for tests that exercise the DB-pool collector.
type fakeDB struct{ stats sql.DBStats }

func (f *fakeDB) Stats() sql.DBStats { return f.stats }

// freshReg registers all alice metrics into an isolated registry and returns it.
// Using a fresh registry per test avoids any interaction with the default
// prometheus registry while still exercising the registration code paths.
func freshReg(t *testing.T) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := Register(reg, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return reg
}

func TestRegister_MetricNamesPresent(t *testing.T) {
	reg := freshReg(t)

	// CounterVec and HistogramVec only appear in Gather output once at least
	// one label combination has been observed. Initialise each vec here so the
	// subsequent Gather call returns all expected metric families.
	httpRequestsTotal.WithLabelValues("GET", "/__name_check__", "200").Inc()
	httpRequestDuration.WithLabelValues("GET", "/__name_check__").Observe(0.001)
	httpResponseSize.WithLabelValues("GET", "/__name_check__").Observe(50)
	rateLimitRejectionsTotal.WithLabelValues("__name_check__").Inc()
	auditEventsTotal.WithLabelValues("__name_check__").Inc()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	got := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		got[mf.GetName()] = true
	}

	want := []string{
		"alice_http_requests_total",
		"alice_http_request_duration_seconds",
		"alice_http_response_size_bytes",
		"alice_rate_limit_rejections_total",
		"alice_gatekeeper_auto_answers_total",
		"alice_audit_events_total",
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("metric %q not found in registry after Register", name)
		}
	}
}

func TestRegister_RuntimeMetrics(t *testing.T) {
	reg := freshReg(t)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	got := make(map[string]bool)
	for _, mf := range mfs {
		got[mf.GetName()] = true
	}
	// GoCollector should expose at least go_goroutines.
	if !got["go_goroutines"] {
		t.Error("go_goroutines not present; GoCollector may not be registered")
	}
}

func TestRegister_WithDBStats(t *testing.T) {
	reg := prometheus.NewRegistry()
	db := &fakeDB{stats: sql.DBStats{OpenConnections: 3, InUse: 1, Idle: 2}}
	if err := Register(reg, db); err != nil {
		t.Fatalf("Register with db: %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	got := make(map[string]bool)
	for _, mf := range mfs {
		got[mf.GetName()] = true
	}

	for _, name := range []string{
		"alice_db_pool_open_connections",
		"alice_db_pool_in_use_connections",
		"alice_db_pool_idle_connections",
	} {
		if !got[name] {
			t.Errorf("DB pool metric %q not registered", name)
		}
	}
}

func TestRegister_Idempotent_DifferentRegistries(t *testing.T) {
	// Registering the same package-level collectors into two independent
	// registries must not panic or error; prometheus allows a Collector to
	// appear in multiple registries.
	reg1 := prometheus.NewRegistry()
	reg2 := prometheus.NewRegistry()
	if err := Register(reg1, nil); err != nil {
		t.Fatalf("Register reg1: %v", err)
	}
	if err := Register(reg2, nil); err != nil {
		t.Fatalf("Register reg2: %v", err)
	}
}

func TestInstrumentHandler_RecordsRequest(t *testing.T) {
	freshReg(t) // ensure collectors are live

	h := InstrumentHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/test-instrument-path", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", rec.Code)
	}

	// The request counter for this path/method/status must have been incremented.
	val := testutil.ToFloat64(httpRequestsTotal.WithLabelValues("GET", "/test-instrument-path", "200"))
	if val < 1 {
		t.Errorf("alice_http_requests_total{GET,/test-instrument-path,200} = %v, want >= 1", val)
	}
}

func TestInstrumentHandler_CapturesNon200(t *testing.T) {
	freshReg(t)

	h := InstrumentHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test-404-path", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	val := testutil.ToFloat64(httpRequestsTotal.WithLabelValues("GET", "/test-404-path", "404"))
	if val < 1 {
		t.Errorf("alice_http_requests_total{GET,/test-404-path,404} = %v, want >= 1", val)
	}
}

func TestRecordRateLimitRejection(t *testing.T) {
	freshReg(t)

	label := "test_rl_limiter_unique_abc"
	before := testutil.ToFloat64(rateLimitRejectionsTotal.WithLabelValues(label))
	RecordRateLimitRejection(label)
	after := testutil.ToFloat64(rateLimitRejectionsTotal.WithLabelValues(label))

	if after-before != 1 {
		t.Errorf("expected counter increment of 1; before=%v after=%v", before, after)
	}
}

func TestRecordGatekeeperAutoAnswer(t *testing.T) {
	freshReg(t)

	before := testutil.ToFloat64(gatekeeperAutoAnswersTotal)
	RecordGatekeeperAutoAnswer()
	after := testutil.ToFloat64(gatekeeperAutoAnswersTotal)

	if after-before != 1 {
		t.Errorf("expected counter increment of 1; before=%v after=%v", before, after)
	}
}

func TestRecordAuditEvent(t *testing.T) {
	freshReg(t)

	kind := "test.audit.unique.xyz"
	before := testutil.ToFloat64(auditEventsTotal.WithLabelValues(kind))
	RecordAuditEvent(kind)
	after := testutil.ToFloat64(auditEventsTotal.WithLabelValues(kind))

	if after-before != 1 {
		t.Errorf("expected counter increment of 1; before=%v after=%v", before, after)
	}
}

func TestHandler_ServesPrometheusText(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	// The default registry always has go_goroutines via the built-in GoCollector.
	if !strings.Contains(string(body), "go_goroutines") {
		t.Errorf("Prometheus exposition text missing go_goroutines; body prefix: %.200s", body)
	}
}
