package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestDecodeGatewayHTTPErrorDoesNotInferGatewayAuthFromBare401(t *testing.T) {
	err := decodeGatewayHTTPError(
		"gateway targets",
		&http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized"},
		[]byte("Unauthorized\n"),
	)
	if err == nil {
		t.Fatal("expected bare 401 error")
	}
	message := err.Error()
	if strings.Contains(message, "gateway_auth_failed") || strings.Contains(message, "auth_domain=gateway") {
		t.Fatalf("bare 401 was classified as Gateway auth failure: %s", message)
	}
	if !strings.Contains(message, "http_401") {
		t.Fatalf("bare 401 lost its generic HTTP status: %s", message)
	}
}

func TestDecodeGatewayHTTPErrorPreservesStructuredGatewayAuth401(t *testing.T) {
	err := decodeGatewayHTTPError(
		"gateway targets",
		&http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized"},
		[]byte(`{"error":"unauthorized","code":"gateway_auth_failed","auth_domain":"gateway","message":"token rejected","next_action":"Run doops login"}`),
	)
	if err == nil {
		t.Fatal("expected structured 401 error")
	}
	message := err.Error()
	for _, want := range []string{"gateway_auth_failed", "auth_domain=gateway", "token rejected", "Run doops login"} {
		if !strings.Contains(message, want) {
			t.Fatalf("structured 401 missing %q: %s", want, message)
		}
	}
}
