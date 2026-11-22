package server

import (
	"errors"
)

// Tool handler for the DoWeb site tool.

func (gw *Gateway) handleToolWeb(call toolCall) {
	ctx, reqID, argBytes, writeJSON, pushProgress := call.ctx, call.reqID, call.args, call.write, call.progress
	result, err := handleWebTool(ctx, argBytes, pushProgress)
	if errors.Is(err, errExecutionOutcomeUnknown) {
		writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
	} else if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}
