package server

import "unicode/utf8"

type auditTailBuffer struct {
	max int
	buf []byte
}

func newAuditTailBuffer(max int) *auditTailBuffer {
	if max < 0 {
		max = 0
	}
	return &auditTailBuffer{max: max}
}

func (b *auditTailBuffer) WriteString(s string) {
	if b.max == 0 || s == "" {
		return
	}
	if len(s) >= b.max {
		b.buf = append(b.buf[:0], s[len(s)-b.max:]...)
		return
	}
	b.buf = append(b.buf, s...)
	if overflow := len(b.buf) - b.max; overflow > 0 {
		copy(b.buf, b.buf[overflow:])
		b.buf = b.buf[:b.max]
	}
}

func (b *auditTailBuffer) Len() int {
	return len(b.buf)
}

// String drops a rune the byte bound split at the start of the tail.
func (b *auditTailBuffer) String() string {
	start := 0
	for start < len(b.buf) && start < utf8.UTFMax && !utf8.RuneStart(b.buf[start]) {
		start++
	}
	return string(b.buf[start:])
}
