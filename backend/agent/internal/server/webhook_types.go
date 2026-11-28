package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	webhookEventAPIVersion   = "1"
	webhookMaxBodyBytes      = 60 << 10
	webhookMaxURLLength      = 2048
	webhookMaxEndpoints      = 32
	webhookMaxSubscriptions  = 32
	webhookMaxNameLength     = 128
	webhookMaxDescription    = 512
	webhookSecretBytes       = 32
	webhookPreviousSecretTTL = 24 * time.Hour
)

var (
	ErrWebhookNotFound             = errors.New("webhook_not_found")
	ErrWebhookInvalidURL           = errors.New("webhook_invalid_url")
	ErrWebhookURLTooLong           = errors.New("webhook_url_too_long")
	ErrWebhookBlockedDestination   = errors.New("webhook_blocked_destination")
	ErrWebhookNameInvalid          = errors.New("webhook_name_invalid")
	ErrWebhookEventUnknown         = errors.New("webhook_event_unknown")
	ErrWebhookScopeInvalid         = errors.New("webhook_scope_invalid")
	ErrWebhookLimitExceeded        = errors.New("webhook_limit_exceeded")
	ErrWebhookEndpointDisabled     = errors.New("webhook_endpoint_disabled")
	ErrWebhookDeliveryNotRetryable = errors.New("webhook_delivery_not_retryable")
)

// webhookEventCatalog is the closed v1 subscription catalog. webhook.test is
// intentionally absent: it is only addressable through the test endpoint.
var webhookEventCatalog = map[string]struct{}{
	"execution.finished":        {},
	"execution.outcome_unknown": {},
	"agent.connected":           {},
	"agent.disconnected":        {},
	"monitor.alert.opened":      {},
	"monitor.alert.resolved":    {},
	"inspection.run.finished":   {},
}

type PlatformEvent struct {
	ID            string
	Type          string
	OccurredAt    time.Time
	ActorUserID   string
	Cluster       string
	Instance      string
	CorrelationID string
	Body          []byte
	DedupeKey     string
}

type WebhookEndpoint struct {
	ID                string
	Name              string
	Description       string
	URLHost           string
	URLSHA256         string
	Enabled           bool
	ScopeCluster      string
	ScopeInstance     string
	EventTypes        []string
	CreatedBy         string
	CreatedAt         string
	UpdatedAt         string
	LastSuccessAtMs   int64
	LastFailureAtMs   int64
	LastErrorCategory string
}

type WebhookCreateInput struct {
	Name          string
	Description   string
	URL           string
	EventTypes    []string
	ScopeCluster  string
	ScopeInstance string
	Enabled       *bool
	CreatedBy     string
}

type WebhookUpdateInput struct {
	Name          *string
	Description   *string
	URL           *string
	EventTypes    []string
	EventTypesSet bool
	ScopeCluster  *string
	ScopeInstance *string
	Enabled       *bool
}

type WebhookDelivery struct {
	ID              string
	EndpointID      string
	EventID         string
	Status          string
	Attempt         int
	NextAttemptAtMs int64
	ResponseStatus  int
	ErrorCategory   string
	CreatedAtMs     int64
	UpdatedAtMs     int64
	DeliveredAtMs   int64
}

type webhookDeliveryJob struct {
	DeliveryID            string
	EndpointID            string
	EventID               string
	EventType             string
	Body                  []byte
	URL                   string
	Secret                string
	PreviousSecret        string
	PreviousSecretExpires int64
	Attempt               int
	LeaseToken            string
}

func randomWebhookID(prefix string, n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return prefix + fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(buf)
}

func newPlatformEvent(eventType string, at time.Time, actor, cluster, instance, correlation string, data any, dedupe string) (PlatformEvent, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if eventType != "webhook.test" {
		if _, ok := webhookEventCatalog[eventType]; !ok {
			return PlatformEvent{}, ErrWebhookEventUnknown
		}
	}
	id := randomWebhookID("evt_", 12)
	envelope := map[string]any{
		"api_version": webhookEventAPIVersion,
		"id":          id,
		"type":        eventType,
		"occurred_at": at.UTC().Format(time.RFC3339Nano),
		"data":        data,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return PlatformEvent{}, err
	}
	if len(body) > webhookMaxBodyBytes {
		return PlatformEvent{}, fmt.Errorf("platform event body exceeds %d bytes", webhookMaxBodyBytes)
	}
	return PlatformEvent{
		ID: id, Type: eventType, OccurredAt: at.UTC(), ActorUserID: actor,
		Cluster: cluster, Instance: instance, CorrelationID: correlation,
		Body: body, DedupeKey: strings.TrimSpace(dedupe),
	}, nil
}

func normalizeWebhookEventTypes(types []string) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(types))
	for _, raw := range types {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if _, ok := webhookEventCatalog[t]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrWebhookEventUnknown, t)
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	if len(out) > webhookMaxSubscriptions {
		return nil, ErrWebhookLimitExceeded
	}
	return out, nil
}

func validateWebhookScope(cluster, instance string) error {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if instance != "" && cluster == "" {
		return ErrWebhookScopeInvalid
	}
	if len(cluster) > 64 || len(instance) > 64 {
		return ErrWebhookScopeInvalid
	}
	return nil
}

func validateWebhookName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > webhookMaxNameLength {
		return ErrWebhookNameInvalid
	}
	return nil
}

func validateWebhookURL(ctx context.Context, raw string) (normalized, host string, digest string, err error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > webhookMaxURLLength {
		return "", "", "", ErrWebhookURLTooLong
	}
	u, parseErr := url.Parse(raw)
	if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", "", "", ErrWebhookInvalidURL
	}
	if u.Port() != "" {
		var port int
		if _, portErr := fmt.Sscanf(u.Port(), "%d", &port); portErr != nil || port < 1 || port > 65535 {
			return "", "", "", ErrWebhookInvalidURL
		}
	}
	hostname := u.Hostname()
	if hostname == "" {
		return "", "", "", ErrWebhookInvalidURL
	}
	ips, resolveErr := resolveWebhookHost(ctx, hostname)
	if resolveErr != nil {
		return "", "", "", resolveErr
	}
	if len(ips) == 0 {
		return "", "", "", ErrWebhookInvalidURL
	}
	for _, ip := range ips {
		if webhookIPBlocked(ip) {
			return "", "", "", ErrWebhookBlockedDestination
		}
	}
	sum := sha256.Sum256([]byte(u.String()))
	return u.String(), u.Host, hex.EncodeToString(sum[:]), nil
}

func resolveWebhookHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, ErrWebhookInvalidURL
	}
	out := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.IP)
	}
	return out, nil
}

var webhookBlockedCIDRs = func() []*net.IPNet {
	raw := []string{
		"0.0.0.0/8", "127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12",
		"192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16",
		"192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
		"::/128", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48",
		"100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "ff00::/8",
	}
	out := make([]*net.IPNet, 0, len(raw))
	for _, s := range raw {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}()

func webhookIPBlocked(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, n := range webhookBlockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func webhookDeliveryID(endpointID, eventID string) string {
	sum := sha256.Sum256([]byte(endpointID + "|" + eventID))
	return "dlv_" + hex.EncodeToString(sum[:])[:24]
}

func webhookAADURL(id string) []byte      { return []byte("webhook:" + id + ":url") }
func webhookAADSecret(id string) []byte   { return []byte("webhook:" + id + ":secret") }
func webhookAADPrevious(id string) []byte { return []byte("webhook:" + id + ":secret:previous") }
