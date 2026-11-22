package server

import ()

// Tool handlers for workspace and file transfer.

func (gw *Gateway) handleToolCosSync(call toolCall) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.write
	result, err := handleCOSSync(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolFileWrite(call toolCall) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.write
	result, err := handleFileWrite(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolFileRead(call toolCall) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.write
	result, err := handleFileRead(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspaceRead(call toolCall) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.write
	result, err := handleWorkspaceRead(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspaceBegin(call toolCall) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.write
	result, err := handleWorkspaceBegin(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspaceChunk(call toolCall) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.write
	result, err := handleWorkspaceChunk(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspaceCommit(call toolCall) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.write
	result, err := handleWorkspaceCommit(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspacePullBegin(call toolCall) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.write
	result, err := handleWorkspacePullBegin(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspacePullChunk(call toolCall) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.write
	result, err := handleWorkspacePullChunk(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}
