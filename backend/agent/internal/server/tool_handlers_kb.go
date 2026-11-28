package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type knowledgeBaseParams struct {
	SessionID string `json:"session_id"`
	Operation string `json:"operation"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	Encoding  string `json:"encoding"`
}

// handleToolKb serves the knowledge base to allowed callers without going
// through a shell. Every operation resolves inside the knowledge base root, so
// a caller cannot use this tool to read or write anywhere else on the target.
func (gw *Gateway) handleToolKb(call toolInvocation) {
	reqID, writeJSON := call.reqID, call.writeJSON
	result, err := handleKnowledgeBaseCall(call.args)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
		return
	}
	writeJSON(buildSuccessResponse(reqID, result))
}

func handleKnowledgeBaseCall(argBytes json.RawMessage) (string, error) {
	var params knowledgeBaseParams
	if err := json.Unmarshal(argBytes, &params); err != nil {
		return "", fmt.Errorf("invalid knowledge base params: %w", err)
	}
	if err := validateSession(params.SessionID); err != nil {
		return "", err
	}
	root, err := knowledgeBaseRoot()
	if err != nil {
		return "", err
	}
	switch params.Operation {
	case "list":
		return listKnowledgeBaseResult(params.SessionID)
	case "read":
		if knowledgeBaseImageName(params.Name) {
			return readKnowledgeBaseImage(params.SessionID, filepath.Join(root, params.Name))
		}
		return readKnowledgeBaseFile(params.SessionID, filepath.Join(root, params.Name))
	case "write":
		if strings.EqualFold(strings.TrimSpace(params.Encoding), "base64") {
			data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(params.Content))
			if err != nil {
				return "", fmt.Errorf("invalid base64 content: %w", err)
			}
			return writeKnowledgeBaseBytes(params.SessionID, filepath.Join(root, params.Name), data)
		}
		return writeKnowledgeBaseFile(params.SessionID, filepath.Join(root, params.Name), params.Content)
	case "delete":
		trashed, err := deleteKnowledgeBaseDocument(params.SessionID, filepath.Join(root, params.Name))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Deleted %s to %s", params.Name, trashed), nil
	default:
		return "", fmt.Errorf("unsupported knowledge base operation %q: use list, read, write or delete", params.Operation)
	}
}

func listKnowledgeBaseResult(sessionID string) (string, error) {
	rootPath, entries, err := listKnowledgeBaseDocuments(sessionID)
	if err != nil {
		return "", err
	}
	count, bytes, err := knowledgeBaseUsage(rootPath)
	if err != nil {
		return "", err
	}
	result := map[string]interface{}{
		"root":      rootPath,
		"on_volume": knowledgeBaseOnVolume(rootPath),
		"documents": entries,
		"usage": map[string]interface{}{
			"documents":    count,
			"bytes":        bytes,
			"max_document": maxKnowledgeBaseDocumentBytes,
			"max_image":    maxKnowledgeBaseImageBytes,
		},
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
