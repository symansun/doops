package gatewaycmd

import (
	"net/http"
	"testing"
	"time"
)

func TestGatewayHTTPServerBoundsHeaderAndIdleTime(t *testing.T) {
	server := gatewayHTTPServer(":0", http.NewServeMux())
	if server.ReadHeaderTimeout != 10*time.Second || server.IdleTimeout != 2*time.Minute {
		t.Fatalf("header=%s idle=%s", server.ReadHeaderTimeout, server.IdleTimeout)
	}
	if server.ReadTimeout != 0 || server.WriteTimeout != 0 {
		t.Fatal("tunneled connections must not inherit a request deadline")
	}
	if server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("max header bytes = %d", server.MaxHeaderBytes)
	}
}
