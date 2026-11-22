package server

import (
	"strings"
	"testing"
)

func TestAuditTailBufferKeepsOnlyBoundedSuffix(t *testing.T) {
	buf := newAuditTailBuffer(16)
	buf.WriteString(strings.Repeat("a", 1024))
	if got := buf.Len(); got > 16 {
		t.Fatalf("tail buffer grew past bound: %d", got)
	}
	buf.WriteString("0123456789abcdef")
	buf.WriteString("XYZ")
	if got := buf.String(); got != "3456789abcdefXYZ" {
		t.Fatalf("unexpected tail suffix: %q", got)
	}
}
