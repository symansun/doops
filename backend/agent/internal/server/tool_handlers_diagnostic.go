package server

// Tool handler for the diagnostic sample the Gateway relays to collect machine
// evidence; never reachable from a caller.

import (
	"github.com/user/doops/agent/api"
)

func (gw *Gateway) handleDiagnosticReadTool(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	sample, err := agentDiagnosticSample(argBytes)
	if err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
		return
	}
	writeJSON(api.JSONRPCResponse{JSONRPC: "2.0", ID: reqID, Result: sample})
}
