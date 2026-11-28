package server

import (
	"github.com/user/doops/agent/api"
)

// buildSuccessResponse 构建标准的 MCP ToolCall 成功响应包
func buildSuccessResponse(reqID interface{}, text string) api.JSONRPCResponse {
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      reqID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": text,
				},
			},
		},
	}
}

const commandResultStructuredKind = "doops.command-result"

func buildStructuredSuccessResponse(reqID interface{}, text string, structuredContent map[string]interface{}) api.JSONRPCResponse {
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      reqID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": text,
				},
			},
			"structuredContent": structuredContent,
		},
	}
}

// buildStreamedCommandSuccessResponse constructs a backward-compatible MCP
// success response for streaming command tools. Legacy clients keep reading
// content[0].text = "Operation complete.", while modern Gateway/CLI clients
// inspect structuredContent.streamed == true so placeholder text is never
// appended to command stdout.
func buildStreamedCommandSuccessResponse(reqID interface{}) api.JSONRPCResponse {
	return buildStructuredSuccessResponse(reqID, "Operation complete.", map[string]interface{}{
		"kind":     commandResultStructuredKind,
		"exitCode": 0,
		"streamed": true,
	})
}

// resultHasStreamedCommandContent reports whether a tool call result declares
// that command output was already streamed via notifications/message and the
// terminal text field is only a legacy compatibility placeholder.
func resultHasStreamedCommandContent(result map[string]interface{}) bool {
	if result == nil {
		return false
	}
	structured, ok := result["structuredContent"].(map[string]interface{})
	if !ok {
		return false
	}
	streamed, _ := structured["streamed"].(bool)
	return streamed
}

// buildErrorResponse 构建标准的 MCP ToolCall 异常响应包
func buildErrorResponse(reqID interface{}, code int, message string) api.JSONRPCResponse {
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      reqID,
		Error:   &api.RPCError{Code: code, Message: message},
	}
}

// buildToolErrorResponse 构建工具执行逻辑失败的响应包（退出码非 0）
func buildToolErrorResponse(reqID interface{}, text string) api.JSONRPCResponse {
	return buildStructuredToolErrorResponse(reqID, text, nil)
}

func buildStructuredToolErrorResponse(reqID interface{}, text string, fields map[string]interface{}) api.JSONRPCResponse {
	result := map[string]interface{}{
		"isError": true,
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": text,
			},
		},
	}
	for key, value := range fields {
		result[key] = value
	}
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      reqID,
		Result:  result,
	}
}
