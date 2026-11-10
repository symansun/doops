package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAdmissionStatusDoesNotTreatOldNodeInfoAsIdle(t *testing.T) {
	for _, raw := range []string{
		"--- System Info ---\nLinux",
		"{}",
		`{"limit":2,"inUse":0,"available":2}`,
		`{"limit":2,"inUse":0,"available":2,"holders":null,"queues":[]}`,
	} {
		var out bytes.Buffer
		if err := printAdmissionStatus(&out, raw); err == nil || out.Len() != 0 {
			t.Fatalf("unsupported response reported as admission state: %q, error %v", out.String(), err)
		}
	}
	var out bytes.Buffer
	raw := `{"limit":2,"inUse":2,"available":0,"holders":[{"sessionId":"active","turnId":"t2"}],"queues":[{"sessionId":"waiting","queued":1,"oldestWaitSecs":14}]}`
	if err := printAdmissionStatus(&out, raw); err != nil || !strings.Contains(out.String(), raw) {
		t.Fatalf("live admission evidence lost: %q, error %v", out.String(), err)
	}
}
