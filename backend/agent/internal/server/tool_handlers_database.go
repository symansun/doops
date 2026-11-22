package server

import ()

// Tool handler for database inventory discovery.

func (gw *Gateway) handleToolDatabaseInventory(call toolCall) {
	ctx, reqID, writeJSON := call.ctx, call.reqID, call.write
	result, err := discoverDatabaseCandidates(ctx)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}
