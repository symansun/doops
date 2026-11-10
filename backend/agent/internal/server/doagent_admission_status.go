package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func readDoagentAdmission(ctx context.Context, baseURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := doagentRPCContext(ctx, baseURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "admission-status",
		"method":  "runtime/gcStats",
		"params":  map[string]interface{}{},
	})
	if err != nil || response["error"] != nil {
		return "", fmt.Errorf("doagent_admission_status_unavailable")
	}
	result, _ := response["result"].(map[string]interface{})
	admission, _ := result["admission"].(map[string]interface{})
	current, _ := admission["current"].(map[string]interface{})
	for _, key := range []string{"limit", "inUse", "available"} {
		if value, ok := current[key].(float64); !ok || value < 0 {
			return "", fmt.Errorf("doagent_admission_status_unsupported")
		}
	}
	for _, key := range []string{"holders", "queues"} {
		if _, ok := current[key].([]interface{}); !ok {
			return "", fmt.Errorf("doagent_admission_status_unsupported")
		}
	}
	encoded, err := json.MarshalIndent(current, "", "  ")
	return string(encoded), err
}
