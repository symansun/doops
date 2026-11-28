package main

import (
	"encoding/json"
	"fmt"
	"io"
)

func printAdmissionStatus(out io.Writer, raw string) error {
	var status struct {
		Limit     *int            `json:"limit"`
		InUse     *int            `json:"inUse"`
		Available *int            `json:"available"`
		Holders   json.RawMessage `json:"holders"`
		Queues    json.RawMessage `json:"queues"`
	}
	if err := json.Unmarshal([]byte(raw), &status); err != nil ||
		status.Limit == nil || status.InUse == nil || status.Available == nil ||
		len(status.Holders) == 0 || status.Holders[0] != '[' ||
		len(status.Queues) == 0 || status.Queues[0] != '[' {
		return fmt.Errorf("doagent_admission_status_unsupported: upgrade the target Agent and its do-agent runtime")
	}
	_, err := fmt.Fprintln(out, "do-agent admission (runtime/gcStats):\n"+raw)
	return err
}
