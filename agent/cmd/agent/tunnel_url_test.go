package main

import "testing"

func TestBuildGatewayAgentURLUsesVersionedEndpoint(t *testing.T) {
	got, err := buildGatewayAgentURL("https://gateway.example.com", "dev", "local")
	if err != nil {
		t.Fatalf("build url: %v", err)
	}
	want := "wss://gateway.example.com/v1/agent/connect?cluster=dev&instance=local"
	if got != want {
		t.Fatalf("gateway URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestBuildGatewayAgentURLAllowsHTTPGatewayByDefault(t *testing.T) {
	got, err := buildGatewayAgentURL("http://192.0.2.10:42222", "dev", "local")
	if err != nil {
		t.Fatalf("http gateway URL should be allowed by default: %v", err)
	}
	want := "ws://192.0.2.10:42222/v1/agent/connect?cluster=dev&instance=local"
	if got != want {
		t.Fatalf("gateway URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestBuildGatewayAgentURLAllowsInsecureLocalhostForDevelopment(t *testing.T) {
	got, err := buildGatewayAgentURL("http://localhost:42222", "dev", "local")
	if err != nil {
		t.Fatalf("local insecure gateway should be allowed: %v", err)
	}
	want := "ws://localhost:42222/v1/agent/connect?cluster=dev&instance=local"
	if got != want {
		t.Fatalf("gateway URL mismatch\nwant: %s\n got: %s", want, got)
	}
}
