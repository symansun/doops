package main

import (
	"net/http"

	"github.com/user/doops/agent/internal/server"
)

func registerAgentHTTP(mux *http.ServeMux) {
	mux.HandleFunc("/health", handleHealthLive)
	mux.HandleFunc("/health/live", handleHealthLive)
	mux.HandleFunc("/health/ready", handleHealthReady)
	mux.HandleFunc("/ws", server.HandleRetiredInboundWS)
}

func handleHealthLive(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func handleHealthReady(w http.ResponseWriter, _ *http.Request) {
	if isReverseTunnelConnected() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not ready"))
}
