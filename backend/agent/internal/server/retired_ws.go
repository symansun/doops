package server

import (
	"io"
	"net/http"
)

const retiredInboundWSBody = `410 Gone: inbound /ws has been removed.
CLI clients must connect to /v1/rpc?cluster=&instance=
Agents must connect to /v1/agent/connect
Upgrade doops-cli; do not dial the agent directly.
`

func HandleRetiredInboundWS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusGone)
	_, _ = io.WriteString(w, retiredInboundWSBody)
}
