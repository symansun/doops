package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type loginFailureLimiter struct {
	mu     sync.Mutex
	byIP   map[string][]time.Time
	byName map[string][]time.Time
}

func newLoginFailureLimiter() *loginFailureLimiter {
	return &loginFailureLimiter{
		byIP:   map[string][]time.Time{},
		byName: map[string][]time.Time{},
	}
}

func (l *loginFailureLimiter) limited(ip, username string, now time.Time, limit int, window time.Duration) bool {
	if l == nil || limit <= 0 || window <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	name := normalizeLoginName(username)
	return len(pruneLoginTimes(l.byIP, ip, now, window)) >= limit ||
		len(pruneLoginTimes(l.byName, name, now, window)) >= limit
}

func (l *loginFailureLimiter) record(ip, username string, now time.Time, window time.Duration) {
	if l == nil || window <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	name := normalizeLoginName(username)
	l.byIP[ip] = append(pruneLoginTimes(l.byIP, ip, now, window), now)
	l.byName[name] = append(pruneLoginTimes(l.byName, name, now, window), now)
}

func pruneLoginTimes(bucket map[string][]time.Time, key string, now time.Time, window time.Duration) []time.Time {
	cutoff := now.Add(-window)
	fresh := make([]time.Time, 0, len(bucket[key]))
	for _, ts := range bucket[key] {
		if ts.After(cutoff) {
			fresh = append(fresh, ts)
		}
	}
	bucket[key] = fresh
	return fresh
}

func normalizeLoginName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func loginClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil || host == "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
