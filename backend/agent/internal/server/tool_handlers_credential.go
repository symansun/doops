package server

// Tool handlers for credential materialisation, import and cleanup.

import ()

func (gw *Gateway) handleToolCredentialImport(call toolInvocation) {
	ctx, reqID, argBytes, writeJSON := call.ctx, call.reqID, call.args, call.writeJSON
	result, err := handleCredentialSecretImportTool(ctx, argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolCredentialMaterialize(call toolInvocation) {
	ctx, reqID, argBytes, writeJSON := call.ctx, call.reqID, call.args, call.writeJSON
	result, err := handleCredentialMaterializeTool(ctx, argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolCredentialCleanup(call toolInvocation) {
	ctx, reqID, argBytes, writeJSON := call.ctx, call.reqID, call.args, call.writeJSON
	result, err := handleCredentialCleanupTool(ctx, argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}
