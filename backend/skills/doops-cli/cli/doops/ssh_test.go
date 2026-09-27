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

func TestNormalizeSSHOptionsRequiresOpenSSHArgv(t *testing.T) {
	got, err := normalizeSSHOptions([]string{"BatchMode=yes", "-p2222"})
	if err != nil {
		t.Fatalf("normalize SSH options: %v", err)
	}
	want := []string{"-oBatchMode=yes", "-p2222"}
	if len(got) != len(want) {
		t.Fatalf("normalized options = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("normalized option %d = %q, want %q", index, got[index], want[index])
		}
	}

	if _, err := normalizeSSHOptions([]string{"BatchMode"}); err == nil {
		t.Fatal("unprefixed SSH option without a value must be rejected")
	}
}
