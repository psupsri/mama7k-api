package app

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// limiter counts requests per key in fixed windows. It lives in memory, which is enough for
// a single API instance; counts reset when the process restarts.
type limiter struct {
	limit  int
	window time.Duration

	mu        sync.Mutex
	hits      map[string]*hitWindow
	lastSweep time.Time
}

type hitWindow struct {
	start time.Time
	count int
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, hits: map[string]*hitWindow{}}
}

// allow records a request for key and reports whether it is within the limit; when it is
// not, retry is how long until the window resets.
func (l *limiter) allow(key string, now time.Time) (ok bool, retry time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) > l.window { // drop finished windows so the map stays small
		for k, h := range l.hits {
			if now.Sub(h.start) >= l.window {
				delete(l.hits, k)
			}
		}
		l.lastSweep = now
	}
	h := l.hits[key]
	if h == nil || now.Sub(h.start) >= l.window {
		h = &hitWindow{start: now}
		l.hits[key] = h
	}
	h.count++
	if h.count > l.limit {
		return false, h.start.Add(l.window).Sub(now)
	}
	return true, 0
}

// rateLimits guard sign-in and sign-up against password guessing and mass account creation,
// and cap all other writes per client IP.
type rateLimits struct {
	login, register, writes *limiter
}

func defaultRateLimits() rateLimits {
	return rateLimits{
		login:    newLimiter(10, 5*time.Minute),
		register: newLimiter(5, time.Hour),
		writes:   newLimiter(120, time.Minute),
	}
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var l *limiter
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			l = s.limits.login
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/register":
			l = s.limits.register
		case r.Method != http.MethodGet && r.Method != http.MethodHead && strings.HasPrefix(r.URL.Path, "/api/"):
			l = s.limits.writes
		}
		if l != nil {
			if ok, retry := l.allow(s.clientIP(r), s.now()); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": &Error{Code: "RATE_LIMITED", Message: "ส่งคำขอถี่เกินไป กรุณารอสักครู่แล้วลองใหม่"}})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the address of the browser. Behind a hosting proxy (TRUSTED_PROXY_HOPS > 0) it
// is read from X-Forwarded-For, counting hops from the right: each trusted proxy appends the
// address it saw, while anything further left could have been sent by the client itself.
func (s *Server) clientIP(r *http.Request) string {
	if hops := s.cfg.TrustedProxyHops; hops > 0 {
		var parts []string
		for _, h := range r.Header.Values("X-Forwarded-For") {
			for _, p := range strings.Split(h, ",") {
				if p = strings.TrimSpace(p); p != "" {
					parts = append(parts, p)
				}
			}
		}
		if len(parts) >= hops {
			return parts[len(parts)-hops]
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
