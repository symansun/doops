package server

// Tool handlers for the session workspace: file reads and writes, chunked
// upload and pull, COS transfer and workspace cleanup.

import ()

func (gw *Gateway) handleToolFileRead(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleFileRead(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolFileWrite(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleFileWrite(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspaceRead(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleWorkspaceRead(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspaceBegin(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleWorkspaceBegin(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspaceChunk(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleWorkspaceChunk(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspaceCommit(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleWorkspaceCommit(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspacePullBegin(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleWorkspacePullBegin(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolWorkspacePullChunk(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleWorkspacePullChunk(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolCosSync(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleCOSSync(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolCleanWorkspace(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleCleanWorkspace(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}
