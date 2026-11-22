package server

// Tool handler for database inventory discovery.

import ()

func (gw *Gateway) handleToolDatabaseInventory(call toolInvocation) {
	ctx, reqID, writeJSON := call.ctx, call.reqID, call.writeJSON
	result, err := discoverDatabaseCandidates(ctx)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}
