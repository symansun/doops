package server

import (
	"net/http"
	"testing"
)

func TestCheckWebSocketOriginRequiresSameHostAndPort(t *testing.T) {
	t.Setenv("DOOPS_ALLOW_ALL_WS_ORIGINS", "")
	t.Setenv("DOOPS_ALLOWED_WS_ORIGINS", "")

	cases := []struct {
		name   string
		host   string
		origin string
		allow  bool
	}{
		{name: "no origin", host: "gw.example.com", origin: "", allow: true},
		{name: "same host default port", host: "gw.example.com", origin: "https://gw.example.com", allow: true},
		{name: "same host and port", host: "gw.example.com:42222", origin: "http://gw.example.com:42222", allow: true},
		{name: "same host and port ignores scheme", host: "gw.example.com:8443", origin: "https://GW.Example.com:8443", allow: true},
		{name: "cross port rejected", host: "gw.example.com", origin: "https://gw.example.com:8443", allow: false},
		{name: "cross port rejected with explicit request port", host: "gw.example.com:42222", origin: "http://gw.example.com:3000", allow: false},
		{name: "other host rejected", host: "gw.example.com", origin: "https://console.example.com", allow: false},
		{name: "null origin rejected", host: "gw.example.com", origin: "null", allow: false},
		{name: "file origin rejected", host: "gw.example.com", origin: "file:///tmp/page.html", allow: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &http.Request{Host: tc.host, Header: http.Header{}}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := checkWebSocketOrigin(req); got != tc.allow {
				t.Fatalf("checkWebSocketOrigin(host=%q origin=%q) = %v, want %v", tc.host, tc.origin, got, tc.allow)
			}
		})
	}
}

func TestCheckWebSocketOriginAllowlist(t *testing.T) {
	t.Setenv("DOOPS_ALLOW_ALL_WS_ORIGINS", "")
	req := func(host, origin string) *http.Request {
		r := &http.Request{Host: host, Header: http.Header{}}
		r.Header.Set("Origin", origin)
		return r
	}

	t.Run("legacy bare host means https default port", func(t *testing.T) {
		t.Setenv("DOOPS_ALLOWED_WS_ORIGINS", "console.example.com")
		if !checkWebSocketOrigin(req("gw.example.com", "https://console.example.com")) {
			t.Fatal("https legacy allowlist entry should match the default port")
		}
		if checkWebSocketOrigin(req("gw.example.com", "http://console.example.com")) {
			t.Fatal("legacy entry must not match plain http on port 80")
		}
	})

	t.Run("full origin matches exactly", func(t *testing.T) {
		t.Setenv("DOOPS_ALLOWED_WS_ORIGINS", "https://console.example.com:8443")
		if !checkWebSocketOrigin(req("gw.example.com", "https://console.example.com:8443")) {
			t.Fatal("full origin should match")
		}
		if checkWebSocketOrigin(req("gw.example.com", "https://console.example.com:9443")) {
			t.Fatal("full origin must not match a different port")
		}
		if checkWebSocketOrigin(req("gw.example.com", "http://console.example.com:8443")) {
			t.Fatal("full origin must not match a different scheme")
		}
	})

	t.Run("permissive mode", func(t *testing.T) {
		t.Setenv("DOOPS_ALLOWED_WS_ORIGINS", "")
		t.Setenv("DOOPS_ALLOW_ALL_WS_ORIGINS", "1")
		if !checkWebSocketOrigin(req("gw.example.com", "https://anything.example.com:9999")) {
			t.Fatal("permissive mode should allow any origin")
		}
	})
}
