package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// loginIPLimitMultiplier gives one client address room for several accounts
// (for example many users behind one NAT) before the address itself is limited.
const loginIPLimitMultiplier = 5

// loginLimiterMaxKeys caps the failure table. Credential checks are slow
// (bcrypt) and run concurrently, so a flood of simultaneous failures from one
// address can evict the account keys recorded just before it. Expired entries are swept first;
// if the table is still full, arbitrary entries are evicted so memory stays
// bounded no matter how many distinct usernames or addresses are tried.
const loginLimiterMaxKeys = 8192

// loginFailureMaxAge bounds how long a successful login suppresses the
// failures recorded for the same pair before it: no login window exceeds it.
const loginFailureMaxAge = time.Hour

// loginFailureLimiter counts failed password logins per client address and per
// (client address, username). It never limits a username on its own, so a
// client cannot lock another user out from a different address.
type loginFailureLimiter struct {
	mu sync.Mutex
	// verifiedAt holds the last successful login per (address, username). A
	// failure recorded before it belongs to an earlier, successful attempt
	// sequence and is not counted; that lets a user clear their own history
	// from this address with the right password without letting anyone else
	// clear it, because the reset is tied to an address the caller's bucket
	// does not cover (behind the console every user shares the console host).
	verifiedAt map[string]time.Time
	byIP       map[string][]time.Time
	byIPKey    map[string][]time.Time
	maxKeys    int
}

func newLoginFailureLimiter() *loginFailureLimiter {
	return &loginFailureLimiter{
		verifiedAt: map[string]time.Time{},
		byIP:       map[string][]time.Time{},
		byIPKey:    map[string][]time.Time{},
		maxKeys:    loginLimiterMaxKeys,
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
	addressFailures := append([]time.Time(nil), pruneLoginTimes(l.byIP, ip, now, window)...)
	key := loginAccountKey(ip, username)
	accountFailures := pruneLoginTimes(l.byIPKey, key, now, window)
	// Failures recorded before this pair's last success came from an earlier
	// attempt sequence. They stop counting against the account once the address
	// itself is over budget: the address budget counts every account, so an
	// address-wide spray then still limits even after a successful sign-in.
	if verifiedAt, ok := l.verifiedAt[key]; ok && len(addressFailures) < limit*loginIPLimitMultiplier {
		accountFailures = failuresAfter(accountFailures, verifiedAt)
	}
	l.byIPKey[key] = accountFailures
	return len(accountFailures) >= limit || len(addressFailures) >= limit*loginIPLimitMultiplier
}

// failuresAfter drops the failures recorded before since.
func failuresAfter(failures []time.Time, since time.Time) []time.Time {
	kept := failures[:0]
	for _, failure := range failures {
		if !failure.Before(since) {
			kept = append(kept, failure)
		}
	}
	return kept
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

// reset forgets the failures recorded for one (address, username) pair. A
// successful login therefore leaves the account exactly one typo from its
// limit instead of one earlier typo away from a lock, and every window the
// pair is seen through stays bounded by one limit. The per-address budget is
// deliberately left alone: it counts failures, not addresses, and forgetting
// it after every success would let a spray of unknown users reset the
// spraying address indefinitely.
func (l *loginFailureLimiter) reset(ip, username string, now time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := loginAccountKey(ip, username)
	l.verifiedAt[key] = now
	delete(l.byIPKey, key)
	if len(l.verifiedAt) > l.maxKeys {
		// Attempt sequences older than any window no longer matter; drop them
		// first, then the least recently successful pairs if still over.
		cutoff := now.Add(-loginFailureMaxAge)
		for k, t := range l.verifiedAt {
			if t.Before(cutoff) {
				delete(l.verifiedAt, k)
			}
		}
		for len(l.verifiedAt) > l.maxKeys {
			oldestKey, oldest := "", now
			for k, t := range l.verifiedAt {
				if !t.After(oldest) {
					oldestKey, oldest = k, t
				}
			}
			delete(l.verifiedAt, oldestKey)
		}
	}
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
	// The per-address table is the hard budget with at most one small entry
	// per distinct address; evicting it would blind the limiter at the moment
	// it is under attack. Account keys go first.
	for key := range l.byIPKey {
		if len(l.byIP)+len(l.byIPKey) <= maxKeys {
			return
		}
		delete(l.byIPKey, key)
	}
	for key := range l.byIP {
		if len(l.byIP)+len(l.byIPKey) <= maxKeys {
			return
		}
		delete(l.byIP, key)
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

// Headers the Web console sends with a signed browser address on
// /v1/auth/login. The console forwards every login from the console host, so
// without them all console users share one address bucket.
const (
	consoleClientIPHeader    = "X-DoOps-Client-IP"
	consoleClientIPSigHeader = "X-DoOps-Client-IP-Signature"
	// consoleForwardMaxSkew bounds the timestamp the console signs.
	consoleForwardMaxSkew = 2 * time.Minute
)

// consoleForwardedIP returns the browser address the console signed with
// secret, or "" when the request carries no valid signature. The signature is
// `t=<unix seconds>,v1=<hex HMAC-SHA256(secret, t + "\n" + ip)>`; the
// timestamp must be within consoleForwardMaxSkew of now. No secret, a missing
// or duplicated header, or any malformed value yields "" and the caller keeps
// its usual address. Header values and the secret are never logged.
func consoleForwardedIP(r *http.Request, secret []byte, now time.Time) string {
	if len(secret) == 0 || r == nil {
		return ""
	}
	ips := r.Header.Values(consoleClientIPHeader)
	sigs := r.Header.Values(consoleClientIPSigHeader)
	if len(ips) != 1 || len(sigs) != 1 {
		return ""
	}
	ip := strings.TrimSpace(ips[0])
	if parsed := net.ParseIP(ip); parsed == nil || parsed.String() != ip {
		return ""
	}
	fields := strings.Split(strings.TrimSpace(sigs[0]), ",")
	if len(fields) != 2 {
		return ""
	}
	timestamp, ok := strings.CutPrefix(strings.TrimSpace(fields[0]), "t=")
	if !ok {
		return ""
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ""
	}
	signedAt := time.Unix(ts, 0)
	if signedAt.Before(now.Add(-consoleForwardMaxSkew)) || signedAt.After(now.Add(consoleForwardMaxSkew)) {
		return ""
	}
	v1, ok := strings.CutPrefix(strings.TrimSpace(fields[1]), "v1=")
	if !ok {
		return ""
	}
	got, err := hex.DecodeString(v1)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp + "\n" + ip))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return ""
	}
	return ip
}
