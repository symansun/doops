package server

// Tool handler for in-cluster Agent upgrades.

import ()

func (gw *Gateway) handleToolAgentUpgrade(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	writeJSON(buildAgentUpgradeResponse(reqID, argBytes))
}
