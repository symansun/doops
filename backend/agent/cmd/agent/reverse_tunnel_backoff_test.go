package main

import (
	"testing"
	"time"
)

func TestReverseTunnelAuthBackoffDoublesToCap(t *testing.T) {
	got := time.Duration(0)
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}
	for i, w := range want {
		got = nextReverseTunnelAuthBackoff(got, time.Second)
		if got != w {
			t.Fatalf("step %d = %s, want %s", i, got, w)
		}
	}
	if got := nextReverseTunnelAuthBackoff(0, 0); got != time.Second {
		t.Fatalf("zero base = %s", got)
	}
	if got := nextReverseTunnelAuthBackoff(0, 2*time.Minute); got != 2*time.Minute {
		t.Fatalf("base above cap = %s", got)
	}
}
