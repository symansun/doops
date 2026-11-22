package server

import ()

// Tool handlers for credential materialisation and cleanup.

func (gw *Gateway) handleToolCredentialImport(call toolCall) {
	ctx, reqID, argBytes, writeJSON := call.ctx, call.reqID, call.args, call.write
	result, err := handleCredentialSecretImportTool(ctx, argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolCredentialMaterialize(call toolCall) {
	ctx, reqID, argBytes, writeJSON := call.ctx, call.reqID, call.args, call.write
	result, err := handleCredentialMaterializeTool(ctx, argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolCredentialCleanup(call toolCall) {
	ctx, reqID, argBytes, writeJSON := call.ctx, call.reqID, call.args, call.write
	result, err := handleCredentialCleanupTool(ctx, argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}
