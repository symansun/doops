package server

import "unicode/utf8"

// utf8Head keeps at most max bytes from the start of s without splitting a rune.
func utf8Head(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// utf8Tail keeps at most max bytes from the end of s without splitting a rune.
// A byte cut used to start Chinese text with U+FFFD.
func utf8Tail(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	start := len(s) - max
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
