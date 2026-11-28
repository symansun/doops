package server

import (
	"strings"
	"unicode/utf8"
)

// normalizeUTF8ForStore replaces any malformed UTF-8 byte sequences with U+FFFD
// so strict MySQL utf8mb4 writes never fail with Error 1366 (Incorrect string
// value) when inputs or command outputs contain arbitrary bytes.
func normalizeUTF8ForStore(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

// truncateUTF8Bytes bounds s to at most maxBytes bytes without splitting a
// multi-byte UTF-8 rune, and normalizes any malformed UTF-8 bytes so the result
// is always valid UTF-8 safe for MySQL utf8mb4 columns and JSON payloads.
func truncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 || s == "" {
		return ""
	}
	s = normalizeUTF8ForStore(s)
	if len(s) <= maxBytes {
		return s
	}
	// Walk back at most utf8.UTFMax-1 bytes to land on a rune boundary.
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
