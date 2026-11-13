package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// loginIPLimitMultiplier gives one client address room for several accounts
// (for example many users behind one NAT) before the address itself is limited.
const loginIPLimitMultiplier = 5

// loginLimiterMaxKeys caps the failure table. Expired entries are swept first;
// if the table is still full, arbitrary entries are evicted so memory stays
// bounded no matter how many distinct usernames or addresses are tried.
const loginLimiterMaxKeys = 8192

// loginFailureLimiter counts failed password logins per client address and per
// (client address, username). It never limits a username on its own, so a
// client cannot lock another user out from a different address.
type loginFailureLimiter struct {
	mu      sync.Mutex
	byIP    map[string][]time.Time
	byIPKey map[string][]time.Time
	maxKeys int
}

func newLoginFailureLimiter() *loginFailureLimiter {
	return &loginFailureLimiter{
		byIP:    map[string][]time.Time{},
		byIPKey: map[string][]time.Time{},
		maxKeys: loginLimiterMaxKeys,
	}
}

func loginAccountKey(ip, username string) string {
	return ip + "\x00" + normalizeLoginName(username)
}

func (l *loginFailureLimiter) limited(ip, username string, now time.Time, limit int, window time.Duration) bool {
	if l == nil || limit <= 0 || window <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(pruneLoginTimes(l.byIPKey, loginAccountKey(ip, username), now, window)) >= limit ||
		len(pruneLoginTimes(l.byIP, ip, now, window)) >= limit*loginIPLimitMultiplier
}

func (l *loginFailureLimiter) record(ip, username string, now time.Time, window time.Duration) {
	if l == nil || window <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := loginAccountKey(ip, username)
	l.byIP[ip] = append(pruneLoginTimes(l.byIP, ip, now, window), now)
	l.byIPKey[key] = append(pruneLoginTimes(l.byIPKey, key, now, window), now)
	l.enforceCap(now, window)
}

func (l *loginFailureLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.byIP) + len(l.byIPKey)
}

func (l *loginFailureLimiter) enforceCap(now time.Time, window time.Duration) {
	maxKeys := l.maxKeys
	if maxKeys <= 0 {
		maxKeys = loginLimiterMaxKeys
	}
	if len(l.byIP)+len(l.byIPKey) <= maxKeys {
		return
	}
	for key := range l.byIP {
		pruneLoginTimes(l.byIP, key, now, window)
	}
	for key := range l.byIPKey {
		pruneLoginTimes(l.byIPKey, key, now, window)
	}
	for _, bucket := range []map[string][]time.Time{l.byIPKey, l.byIP} {
		for key := range bucket {
			if len(l.byIP)+len(l.byIPKey) <= maxKeys {
				return
			}
			delete(bucket, key)
		}
	}
}

func pruneLoginTimes(bucket map[string][]time.Time, key string, now time.Time, window time.Duration) []time.Time {
	current, ok := bucket[key]
	if !ok {
		return nil
	}
	cutoff := now.Add(-window)
	fresh := current[:0]
	for _, ts := range current {
		if ts.After(cutoff) {
			fresh = append(fresh, ts)
		}
	}
	if len(fresh) == 0 {
		delete(bucket, key)
		return nil
	}
	bucket[key] = fresh
	return fresh
}

func normalizeLoginName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// loginClientIP returns the address used for login failure buckets. The
// Gateway normally runs behind a local reverse proxy or an in-cluster ingress
// (see backend/gateway/nginx/doops-proxy.conf), so every request arrives from
// a loopback or private peer. Keying on that peer would put all clients in one
// bucket. The proxy-supplied client address (X-Real-IP, then the last
// X-Forwarded-For hop) is therefore used only when the direct peer is loopback
// or private; a direct public peer can never choose its own bucket.
func loginClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil || host == "" {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	peer := net.ParseIP(host)
	if peer == nil || !(peer.IsLoopback() || peer.IsPrivate()) {
		return host
	}
	if real := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); real != nil {
		return real.String()
	}
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if last := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); last != nil {
			return last.String()
		}
	}
	return host
}
