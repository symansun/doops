package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func handleCredentialMaterializeTool(ctx context.Context, argBytes json.RawMessage) (string, error) {
	var args CredentialMaterializeRequest
	decoder := json.NewDecoder(bytes.NewReader(argBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("invalid credential materialize params")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("invalid credential materialize params")
	}
	if err := validateSession(strings.TrimSpace(args.SessionID)); err != nil {
		return "", fmt.Errorf("invalid credential materialize session: %w", err)
	}
	result, err := materializeCredential(ctx, args)
	clear(args.Payload)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode credential materialization: %w", err)
	}
	return string(encoded), nil
}

func handleCredentialCleanupTool(ctx context.Context, argBytes json.RawMessage) (string, error) {
	var args CredentialCleanupRequest
	decoder := json.NewDecoder(bytes.NewReader(argBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("invalid credential cleanup params")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("invalid credential cleanup params")
	}
	if err := validateSession(strings.TrimSpace(args.SessionID)); err != nil {
		return "", fmt.Errorf("invalid credential cleanup session: %w", err)
	}
	if err := cleanupCredential(ctx, args); err != nil {
		return "", err
	}
	return `{"status":"removed"}`, nil
}
