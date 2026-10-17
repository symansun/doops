package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type gatewayErrorResponse struct {
	Error      string `json:"error"`
	Code       string `json:"code"`
	AuthDomain string `json:"auth_domain"`
	Message    string `json:"message"`
	NextAction string `json:"next_action"`
}

type gatewayHTTPError struct {
	Operation  string
	Status     string
	Code       string
	AuthDomain string
	Message    string
	NextAction string
}

func (e *gatewayHTTPError) Error() string {
	operation := strings.TrimSpace(e.Operation)
	if operation == "" {
		operation = "gateway request"
	}
	code := strings.TrimSpace(e.Code)
	if code == "" {
		code = "http_" + strings.TrimPrefix(strings.TrimSpace(e.Status), "HTTP ")
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = strings.TrimSpace(e.Status)
	}
	authDomain := strings.TrimSpace(e.AuthDomain)
	if authDomain != "" {
		code += " (auth_domain=" + authDomain + ")"
	}
	result := fmt.Sprintf("%s failed: %s: %s", operation, code, message)
	if next := strings.TrimSpace(e.NextAction); next != "" {
		result += "; " + next
	}
	return result
}

func decodeGatewayHTTPError(operation string, resp *http.Response, body []byte) error {
	if resp == nil {
		return fmt.Errorf("%s failed: gateway returned no HTTP response", strings.TrimSpace(operation))
	}

	var payload gatewayErrorResponse
	_ = json.Unmarshal(body, &payload)
	if strings.TrimSpace(payload.Message) == "" {
		trimmed := strings.TrimSpace(string(body))
		if trimmed != "" && trimmed != "Unauthorized" {
			payload.Message = trimmed
		}
	}
	return &gatewayHTTPError{
		Operation:  operation,
		Status:     resp.Status,
		Code:       payload.Code,
		AuthDomain: payload.AuthDomain,
		Message:    payload.Message,
		NextAction: payload.NextAction,
	}
}
