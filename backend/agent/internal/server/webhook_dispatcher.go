package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	webhookGlobalConcurrency      = 8
	webhookPerEndpointConcurrency = 2
)

var errWebhookRedirect = errors.New("webhook redirects are not followed")

var webhookRetryBackoff = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	time.Hour,
	3 * time.Hour,
	6 * time.Hour,
	12 * time.Hour,
}

type webhookDispatcher struct {
	store     *GatewayStore
	client    *http.Client
	retention time.Duration
	wake      chan struct{}

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newWebhookDispatcher(store *GatewayStore, retention time.Duration) *webhookDispatcher {
	d := &webhookDispatcher{
		store:     store,
		retention: retention,
		wake:      make(chan struct{}, 1),
	}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext:           webhookDialContext,
	}
	d.client = &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errWebhookRedirect
		},
	}
	return d
}

func (d *webhookDispatcher) Start() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.done = make(chan struct{})
	go d.run(ctx, d.done)
}

func (d *webhookDispatcher) Stop(timeout time.Duration) {
	if d == nil {
		return
	}
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	d.cancel, d.done = nil, nil
	d.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case <-done:
	case <-time.After(timeout):
		log.Printf("[gateway] webhook dispatcher did not stop within %s", timeout)
	}
}

func (d *webhookDispatcher) Wake() {
	if d == nil {
		return
	}
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *webhookDispatcher) run(ctx context.Context, done chan<- struct{}) {
	var inFlight sync.WaitGroup
	defer func() {
		inFlight.Wait()
		close(done)
	}()
	log.Printf("[gateway] webhook dispatcher started")
	defer log.Printf("[gateway] webhook dispatcher stopped")
	global := make(chan struct{}, webhookGlobalConcurrency)
	perEndpoint := make(map[string]chan struct{})
	var lastPrune time.Time
	for {
		if d.retention > 0 && time.Since(lastPrune) >= time.Hour {
			if events, deliveries, err := d.store.PruneWebhookData(ctx, time.Now().UTC().Add(-d.retention), 5000); err != nil {
				if ctx.Err() == nil {
					log.Printf("[gateway] webhook retention pass failed")
				}
			} else if events > 0 || deliveries > 0 {
				log.Printf("[gateway] webhook retention removed %d events and %d deliveries", events, deliveries)
			}
			lastPrune = time.Now()
		}
		select {
		case global <- struct{}{}:
		case <-ctx.Done():
			return
		}
		job, err := d.store.ClaimDueWebhookDelivery(ctx, time.Now().UTC())
		if err != nil {
			<-global
			if ctx.Err() != nil {
				return
			}
			if !waitWebhook(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if job != nil {
			slot := perEndpoint[job.EndpointID]
			if slot == nil {
				slot = make(chan struct{}, webhookPerEndpointConcurrency)
				perEndpoint[job.EndpointID] = slot
			}
			select {
			case slot <- struct{}{}:
			case <-ctx.Done():
				<-global
				return
			}
			inFlight.Add(1)
			go func(job *webhookDeliveryJob) {
				defer inFlight.Done()
				defer func() { <-slot; <-global }()
				d.deliver(ctx, job)
			}(job)
			continue
		}
		<-global
		due, ok, err := d.store.NextWebhookDueAt(ctx)
		if err != nil {
			if !waitWebhook(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-d.wake:
			}
			continue
		}
		wait := time.Until(due)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-d.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func waitWebhook(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (d *webhookDispatcher) deliver(parent context.Context, job *webhookDeliveryJob) {
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	status, category, retryable, retryAfter := d.post(ctx, job)
	if retryable {
		terminal := job.Attempt > len(webhookRetryBackoff)
		delay := time.Duration(0)
		if !terminal {
			delay = webhookRetryBackoff[job.Attempt-1]
		}
		if retryAfter > 0 {
			delay = retryAfter
		}
		if err := d.store.FailWebhookDelivery(ctx, job, status, category, time.Now().Add(delay), terminal); err != nil {
			log.Printf("[gateway] webhook delivery %s state update failed: %v", job.DeliveryID, err)
		}
		if terminal {
			log.Printf("[gateway] webhook delivery %s failed permanently (%s)", job.DeliveryID, category)
		}
		return
	}
	if err := d.store.CompleteWebhookDelivery(ctx, job, status); err != nil {
		log.Printf("[gateway] webhook delivery %s completion failed: %v", job.DeliveryID, err)
	}
}

func (d *webhookDispatcher) post(ctx context.Context, job *webhookDeliveryJob) (int, string, bool, time.Duration) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, job.URL, bytes.NewReader(job.Body))
	if err != nil {
		return 0, "invalid_url", false, 0
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "DoOps-Webhook/1")
	req.Header.Set("X-DoOps-Event", job.EventType)
	req.Header.Set("X-DoOps-Event-Id", job.EventID)
	req.Header.Set("X-DoOps-Delivery-Id", job.DeliveryID)
	req.Header.Set("X-DoOps-Endpoint-Id", job.EndpointID)
	req.Header.Set("X-DoOps-Attempt", strconv.Itoa(job.Attempt))
	req.Header.Set("X-DoOps-Timestamp", timestamp)
	signatures := []string{webhookSign(job.Secret, timestamp, job.Body)}
	if job.PreviousSecret != "" && job.PreviousSecretExpires > time.Now().UnixMilli() {
		signatures = append(signatures, webhookSign(job.PreviousSecret, timestamp, job.Body))
	}
	req.Header.Set("X-DoOps-Signature", "v1="+strings.Join(signatures, ",v1="))
	resp, err := d.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, "timeout", true, 0
		}
		var certErr *tls.CertificateVerificationError
		var unknownAuthority x509.UnknownAuthorityError
		var hostnameErr x509.HostnameError
		var invalidCert x509.CertificateInvalidError
		var dnsErr *net.DNSError
		switch {
		case errors.Is(err, ErrWebhookBlockedDestination):
			return 0, "blocked_destination", false, 0
		case errors.Is(err, errWebhookRedirect):
			return 0, "redirect", false, 0
		case errors.As(err, &certErr), errors.As(err, &unknownAuthority), errors.As(err, &hostnameErr), errors.As(err, &invalidCert):
			return 0, "tls_error", false, 0
		case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
			return 0, "dns_nxdomain", false, 0
		}
		return 0, "network", true, 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, "", false, 0
	}
	retryAfter := webhookRetryAfter(resp.Header.Get("Retry-After"))
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooEarly || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return resp.StatusCode, fmt.Sprintf("http_%d", resp.StatusCode), true, retryAfter
	}
	return resp.StatusCode, fmt.Sprintf("http_%d", resp.StatusCode), false, 0
}

func webhookRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	delay := time.Duration(0)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		delay = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(value); err == nil {
		if wait := time.Until(at); wait > 0 {
			delay = wait
		}
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	return delay
}

func webhookSign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func webhookDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := resolveWebhookHost(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if webhookIPBlocked(ip) {
			return nil, ErrWebhookBlockedDestination
		}
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	var last error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	if last == nil {
		last = ErrWebhookInvalidURL
	}
	return nil, last
}

func (h *GatewayHub) StartWebhookDispatcher() {
	if h == nil || h.webhook == nil {
		return
	}
	h.webhook.Start()
}

func (h *GatewayHub) StopWebhookDispatcher(timeout time.Duration) {
	if h == nil || h.webhook == nil {
		return
	}
	h.webhook.Stop(timeout)
}

func (h *GatewayHub) EnqueueWebhookEvent(ctx context.Context, ev PlatformEvent) (string, bool, error) {
	id, inserted, err := h.store.EnqueuePlatformEvent(ctx, ev)
	if err == nil && inserted && h.webhook != nil {
		h.webhook.Wake()
	}
	return id, inserted, err
}
