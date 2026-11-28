package server

// Tool handler for the DoWeb site tool.

import (
	"errors"
)

func (gw *Gateway) handleToolWeb(call toolInvocation) {
	ctx, reqID, argBytes, writeJSON, pushProgress := call.ctx, call.reqID, call.args, call.writeJSON, call.pushProgress
	result, err := handleWebTool(ctx, argBytes, pushProgress)
	if errors.Is(err, errExecutionOutcomeUnknown) {
		writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
	} else if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}
