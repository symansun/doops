package server

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A byte cut used to start a Chinese DoSql answer with U+FFFD.
func TestUTF8CutsKeepRuneBoundaries(t *testing.T) {
	zh := strings.Repeat("凭", 500) + "猜测补全。"
	for _, tt := range []struct {
		name string
		got  string
		max  int
		want string
	}{
		{"tail of short text is unchanged", utf8Tail("abc", 10), 10, "abc"},
		{"head of short text is unchanged", utf8Head("abc", 10), 10, "abc"},
		{"tail drops a split rune", utf8Tail("凭猜测", 7), 7, "猜测"},
		{"head drops a split rune", utf8Head("凭猜测", 4), 4, "凭"},
		{"zero tail is empty", utf8Tail("凭", 0), 0, ""},
		{"zero head is empty", utf8Head("凭", 0), 0, ""},
		{"sanitizeSecretText tail", sanitizeSecretText(zh), 1200, ""},
		{"trimTail", trimTail(zh, 1001), 1001, ""},
		{"tailText", tailText(zh, 1001), 1001, ""},
		{"capToolOutput", capToolOutput(strings.Repeat("测", maxToolOutputBytes)), len(strings.Repeat("测", maxToolOutputBytes)), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !utf8.ValidString(tt.got) || strings.ContainsRune(tt.got, utf8.RuneError) {
				t.Fatalf("cut split a rune: %q", tt.got[:min(len(tt.got), 12)])
			}
			if len(tt.got) > tt.max {
				t.Fatalf("cut kept %d bytes, max %d", len(tt.got), tt.max)
			}
			if tt.want != "" && tt.got != tt.want {
				t.Fatalf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestAuditTailBufferDropsSplitLeadingRune(t *testing.T) {
	buf := newAuditTailBuffer(7)
	buf.WriteString("凭猜测")
	if got := buf.String(); got != "猜测" {
		t.Fatalf("tail = %q, want 猜测", got)
	}
}

func TestDoSQLResultSummaryKeepsRuneBoundary(t *testing.T) {
	result := doSQLResultSummary(map[string]interface{}{
		"executionResult": map[string]interface{}{"stdout": strings.Repeat("库", 20<<10)},
	}, "dbop", "/tmp/j", "/tmp/e")
	if strings.Contains(result, `�`) || strings.ContainsRune(result, utf8.RuneError) {
		t.Fatal("summary output split a rune")
	}
}
