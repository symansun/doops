package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"
)

func newSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("ses_%x", b)
}

// resolveSession returns the caller's session, or generates one when none was
// given. The generated name goes to stderr so command output stays unchanged,
// and reusing it with -session continues the same workspace.
func resolveSession(name string) string {
	if s := strings.TrimSpace(name); s != "" {
		return s
	}
	s := newSessionID()
	fmt.Fprintf(os.Stderr, "[session] -session not given; using %s (pass -session %s to reuse it)\n", s, s)
	return s
}
