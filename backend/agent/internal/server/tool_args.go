package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

func numericID(v interface{}) (int64, bool) {
	switch id := v.(type) {
	case float64:
		return int64(id), true
	case int64:
		return id, true
	case int:
		return int64(id), true
	default:
		return 0, false
	}
}

func extractSession(args json.RawMessage) string {
	var m map[string]interface{}
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	for _, key := range []string{"session_id", "session"} {
		if v, _ := m[key].(string); v != "" {
			return v
		}
	}
	return ""
}

func extractStringArg(args json.RawMessage, key string) string {
	var m map[string]interface{}
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func validateSession(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || sessionID == "." || sessionID == "/" || strings.Contains(sessionID, "..") || strings.ContainsAny(sessionID, `/\`) {
		return fmt.Errorf("unsafe session id: %q", sessionID)
	}
	return nil
}
