package server

import (
	"strings"
	"testing"
)

// A tool result travels back to doagent inside one SSE line. A line beyond the
// Agent's read limit killed the whole event stream, so a turn that had already
// run its tool ended as execution_outcome_unknown.
func TestCapToolOutputKeepsAReadableResult(t *testing.T) {
	for _, tt := range []struct {
		name     string
		size     int
		wantCut  bool
		wantHead string
		wantTail string
	}{
		{"short output is untouched", 100, false, "", ""},
		{"output at the limit is untouched", maxToolOutputBytes, false, "", ""},
		{"output beyond the limit is cut", maxToolOutputBytes + 1000, true, "HEAD", "TAIL"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text := ""
			if tt.size > 0 {
				text = strings.Repeat("x", tt.size-8) + "HEADTAIL"
			}
			got := capToolOutput(text)
			if tt.wantCut {
				if !strings.Contains(got, "doops cut") {
					t.Fatalf("cut output has no marker: %d bytes", len(got))
				}
				if len(got) > maxToolOutputBytes+200 {
					t.Fatalf("cut output is %d bytes, want near %d", len(got), maxToolOutputBytes)
				}
				if !strings.Contains(got, "HEAD") || !strings.Contains(got, "TAIL") {
					t.Fatal("cut output lost its head or tail")
				}
				return
			}
			if got != text {
				t.Fatalf("output was changed: %d bytes, want %d", len(got), len(text))
			}
		})
	}
}

// The SSE line limit must hold a whole tool result, or a big result would
// still kill the stream.
func TestDoagentSSELineLimitHoldsTheWholeToolResult(t *testing.T) {
	if maxDoagentSSELineBytes <= maxToolOutputBytes {
		t.Fatalf("SSE line limit %d does not hold a tool result of %d bytes",
			maxDoagentSSELineBytes, maxToolOutputBytes)
	}
}

// "token too long" is prose about an error, not a credential. Redacting it
// made the message unreadable and hid the real reason.
func TestRedactSecretTextKeepsProseAndRedactsValues(t *testing.T) {
	for _, tt := range []struct {
		name     string
		text     string
		wantKept []string
		wantGone []string
	}{
		{
			name:     "an error phrase about a token stays readable",
			text:     "SSE read: bufio.Scanner: token too long",
			wantKept: []string{"token too long"},
		},
		{
			name:     "a short phrase after a credential word stays readable",
			text:     "the token was rejected",
			wantKept: []string{"the token was rejected"},
		},
		{
			name:     "a long value after a credential word is redacted",
			text:     "authorization Bearer abcdefghijklmnopqrstuvwxyz0123456789",
			wantGone: []string{"abcdefghijklmnopqrstuvwxyz0123456789"},
		},
		{
			name:     "a credential with a separator is still redacted",
			text:     "password=hunter2-canary",
			wantGone: []string{"hunter2-canary"},
		},
		{
			name:     "a credential with a space and a long value is redacted",
			text:     "password hunter2-canary-value-0123456789",
			wantGone: []string{"hunter2-canary-value-0123456789"},
		},
		{
			name:     "an api key with a separator is still redacted",
			text:     "api_key=abcdefghijklmnop",
			wantGone: []string{"abcdefghijklmnop"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := redactSecretText(tt.text)
			for _, want := range tt.wantKept {
				if !strings.Contains(got, want) {
					t.Fatalf("text was mangled: %q", got)
				}
			}
			for _, unwanted := range tt.wantGone {
				if strings.Contains(got, unwanted) {
					t.Fatalf("credential material survived: %q", got)
				}
			}
		})
	}
}
