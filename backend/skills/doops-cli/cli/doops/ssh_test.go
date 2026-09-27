package main

import "testing"

func TestLooksLikeSSHCommand(t *testing.T) {
	for _, command := range []string{"ssh ops@example.com hostname", "/usr/bin/ssh ops@example.com hostname"} {
		if !looksLikeSSHCommand(command) {
			t.Fatalf("expected SSH command: %q", command)
		}
	}
	for _, command := range []string{"echo ssh", "sudo ssh ops@example.com hostname", "sshpass -e ssh ops@example.com hostname"} {
		if looksLikeSSHCommand(command) {
			t.Fatalf("unexpected direct SSH classification: %q", command)
		}
	}
}
