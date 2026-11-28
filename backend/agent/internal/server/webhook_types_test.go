package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestValidateWebhookURLRejectsUnsafeDestinations(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"http", "http://93.184.216.34/hook", ErrWebhookInvalidURL},
		{"loopback-v4", "https://127.0.0.1/hook", ErrWebhookBlockedDestination},
		{"loopback-v6", "https://[::1]/hook", ErrWebhookBlockedDestination},
		{"private-v4", "https://10.1.2.3/hook", ErrWebhookBlockedDestination},
		{"link-local", "https://169.254.169.254/latest/meta-data", ErrWebhookBlockedDestination},
		{"cgnat", "https://100.64.0.1/hook", ErrWebhookBlockedDestination},
		{"ula", "https://[fc00::1]/hook", ErrWebhookBlockedDestination},
		{"userinfo", "https://user:pass@93.184.216.34/hook", ErrWebhookInvalidURL},
		{"fragment", "https://93.184.216.34/hook#frag", ErrWebhookInvalidURL},
		{"bad-port", "https://93.184.216.34:99999/hook", ErrWebhookInvalidURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := validateWebhookURL(ctx, tc.raw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("validateWebhookURL(%q) error = %v, want %v", tc.raw, err, tc.want)
			}
		})
	}
}

func TestValidateWebhookURLNormalizesPublicHTTPS(t *testing.T) {
	normalized, host, digest, err := validateWebhookURL(context.Background(), " https://93.184.216.34/hooks/doops ")
	if err != nil {
		t.Fatalf("validateWebhookURL: %v", err)
	}
	if normalized != "https://93.184.216.34/hooks/doops" {
		t.Fatalf("normalized = %q", normalized)
	}
	if host != "93.184.216.34" {
		t.Fatalf("host = %q", host)
	}
	sum := sha256.Sum256([]byte(normalized))
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %q", digest)
	}
}

func TestWebhookSignUsesTimestampDotBody(t *testing.T) {
	body := []byte(`{"id":"evt_1","type":"webhook.test"}`)
	got := webhookSign("whsec_test", "1700000000", body)
	mac := hmac.New(sha256.New, []byte("whsec_test"))
	mac.Write([]byte("1700000000"))
	mac.Write([]byte("."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
	if len(got) != 64 || strings.Contains(got, "whsec") {
		t.Fatalf("unexpected signature shape %q", got)
	}
}

func TestWebhookDeliveryIDIsStableAndScoped(t *testing.T) {
	a := webhookDeliveryID("wh_aaaaaaaaaaaaaaaaaaaaaaaa", "evt_1")
	b := webhookDeliveryID("wh_aaaaaaaaaaaaaaaaaaaaaaaa", "evt_1")
	c := webhookDeliveryID("wh_bbbbbbbbbbbbbbbbbbbbbbbb", "evt_1")
	if a != b {
		t.Fatalf("delivery id is not stable: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("delivery id must be scoped to the endpoint: %q", a)
	}
	if !strings.HasPrefix(a, "dlv_") || len(a) != len("dlv_")+24 {
		t.Fatalf("delivery id shape = %q", a)
	}
}

func TestWebhookRetryBackoffCoversDesignWindow(t *testing.T) {
	want := []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour}
	if len(webhookRetryBackoff) != len(want) {
		t.Fatalf("backoff length = %d, want %d", len(webhookRetryBackoff), len(want))
	}
	for i := range want {
		if webhookRetryBackoff[i] != want[i] {
			t.Fatalf("backoff[%d] = %v, want %v", i, webhookRetryBackoff[i], want[i])
		}
	}
}

func TestWebhookIPBlocked(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1"}
	for _, raw := range blocked {
		if !webhookIPBlocked(net.ParseIP(raw)) {
			t.Errorf("webhookIPBlocked(%s) = false, want true", raw)
		}
	}
	for _, raw := range []string{"93.184.216.34", "1.1.1.1", "2606:2800:220:1:248:1893:25c8:1946"} {
		if webhookIPBlocked(net.ParseIP(raw)) {
			t.Errorf("webhookIPBlocked(%s) = true, want false", raw)
		}
	}
}
