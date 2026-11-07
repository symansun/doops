package main

import "testing"

func TestGatewayTargetsURLUsesVersionedEndpoint(t *testing.T) {
	got, err := gatewayTargetsURL("https://gateway.example.com")
	if err != nil {
		t.Fatalf("build targets URL: %v", err)
	}
	want := "https://gateway.example.com/v1/targets"
	if got != want {
		t.Fatalf("targets URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestGatewayTargetsAllowsHTTPGatewayByDefault(t *testing.T) {
	got, err := gatewayTargetsURL("http://192.0.2.10:42222")
	if err != nil {
		t.Fatalf("http gateway targets URL should be allowed by default: %v", err)
	}
	want := "http://192.0.2.10:42222/v1/targets"
	if got != want {
		t.Fatalf("targets URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestGatewayTargetsAllowsInsecureLocalhostForDevelopment(t *testing.T) {
	got, err := gatewayTargetsURL("http://localhost:42222")
	if err != nil {
		t.Fatalf("local insecure gateway should be allowed: %v", err)
	}
	want := "http://localhost:42222/v1/targets"
	if got != want {
		t.Fatalf("targets URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestGatewayTargetUnlockURLUsesVersionedEndpoint(t *testing.T) {
	got, err := gatewayTargetUnlockURL("https://gateway.example.com", "dev", "node-1")
	if err != nil {
		t.Fatalf("build unlock URL: %v", err)
	}
	want := "https://gateway.example.com/v1/targets/unlock?cluster=dev&instance=node-1"
	if got != want {
		t.Fatalf("unlock URL mismatch\nwant: %s\n got: %s", want, got)
	}
}
