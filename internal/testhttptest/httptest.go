package httptest

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	stdhttptest "net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
)

// Server is a listener-free in-process HTTP test server. It mirrors the small
// subset of net/http/httptest.Server used by this repo's tests: URL and Close.
// Requests are routed through a custom RoundTripper instead of a loopback
// socket, which keeps the tests compatible with sandboxes that forbid bind().
type Server struct {
	URL  string
	host string

	closeOnce sync.Once
}

type registryTransport struct {
	fallback http.RoundTripper
}

var (
	transportOnce     sync.Once
	installOnce       sync.Once
	registeredMu      sync.RWMutex
	registered        = map[string]http.Handler{}
	serverCounter     uint64
	installedFallback http.RoundTripper
)

func NewServer(handler http.Handler) *Server {
	installTransport()

	id := atomic.AddUint64(&serverCounter, 1)
	host := "alice-test-" + strconv.FormatUint(id, 10) + ".internal"

	registeredMu.Lock()
	registered[host] = handler
	registeredMu.Unlock()

	return &Server{
		URL:  "http://" + host,
		host: host,
	}
}

func (s *Server) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		registeredMu.Lock()
		delete(registered, s.host)
		registeredMu.Unlock()
	})
}

func installTransport() {
	ensureFallback()
	installOnce.Do(func() {
		http.DefaultTransport = registryTransport{fallback: installedFallback}
	})
}

func WrapTransport(base http.RoundTripper) http.RoundTripper {
	ensureFallback()
	if base == nil {
		base = installedFallback
	}
	return registryTransport{fallback: base}
}

func ensureFallback() {
	transportOnce.Do(func() {
		installedFallback = http.DefaultTransport
	})
}

func (t registryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("nil request")
	}

	registeredMu.RLock()
	handler := registered[req.URL.Host]
	registeredMu.RUnlock()
	if handler == nil {
		return t.fallback.RoundTrip(req)
	}

	body, err := readRequestBody(req)
	if err != nil {
		return nil, err
	}

	cloned := req.Clone(req.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	cloned.ContentLength = int64(len(body))
	cloned.RequestURI = cloned.URL.RequestURI()
	if cloned.Host == "" {
		cloned.Host = cloned.URL.Host
	}

	recorder := stdhttptest.NewRecorder()
	handler.ServeHTTP(recorder, cloned)
	return recorder.Result(), nil
}

func readRequestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}
