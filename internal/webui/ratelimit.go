package webui

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// signinIPBucket is a per-IP token-bucket entry for the admin sign-in limiter.
type signinIPBucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

// signinRateLimiter is a simple per-IP token-bucket limiter used to protect
// the admin sign-in endpoints. It is intentionally separate from the JSON API
// limiter so that its configuration and lifecycle are independently controlled.
type signinRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*signinIPBucket
	rate    float64 // tokens added per nanosecond
	burst   float64
}

func newSigninRateLimiter(ratePerMin, burst float64) *signinRateLimiter {
	return &signinRateLimiter{
		buckets: make(map[string]*signinIPBucket),
		rate:    ratePerMin / 60e9,
		burst:   burst,
	}
}

func (l *signinRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	b, ok := l.buckets[ip]
	if !ok {
		b = &signinIPBucket{tokens: l.burst, lastSeen: time.Now()}
		l.buckets[ip] = b
	}
	l.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastSeen)
	b.tokens += float64(elapsed) * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// adminClientIP extracts the best-effort client IP from a request. It honours
// X-Forwarded-For only when RemoteAddr belongs to a trusted proxy CIDR.
func adminClientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remoteIP := net.ParseIP(host)

	if len(trusted) > 0 && remoteIP != nil {
		for _, cidr := range trusted {
			if cidr.Contains(remoteIP) {
				xff := r.Header.Get("X-Forwarded-For")
				if xff == "" {
					return host
				}
				// Take the first (leftmost) IP in XFF that is not a trusted proxy.
				for _, part := range splitXFF(xff) {
					ip := net.ParseIP(part)
					if ip == nil {
						continue
					}
					inTrusted := false
					for _, c := range trusted {
						if c.Contains(ip) {
							inTrusted = true
							break
						}
					}
					if !inTrusted {
						return part
					}
				}
				return host
			}
		}
	}
	return host
}

func splitXFF(xff string) []string {
	var out []string
	start := 0
	for i := 0; i < len(xff); i++ {
		if xff[i] == ',' {
			trimmed := trimSpace(xff[start:i])
			if trimmed != "" {
				out = append(out, trimmed)
			}
			start = i + 1
		}
	}
	if last := trimSpace(xff[start:]); last != "" {
		out = append(out, last)
	}
	return out
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
