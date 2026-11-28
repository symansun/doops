package server

import (
	"testing"
	"time"
)

func TestEdgeBudgetPauseTargetsHeaviestConn(t *testing.T) {
	sched := newTestScheduler(t, 16, 2, 1000)
	heavy, err := sched.Open()
	if err != nil {
		t.Fatalf("open heavy: %v", err)
	}
	light, err := sched.Open()
	if err != nil {
		t.Fatalf("open light: %v", err)
	}
	_ = heavy.onOpenAck()
	_ = light.onOpenAck()

	if ok, err := sched.EnqueueData(light.ID(), make([]byte, 100)); err != nil || !ok {
		t.Fatalf("enqueue light: ok=%v err=%v", ok, err)
	}
	for i := 0; i < 7; i++ {
		if ok, err := sched.EnqueueData(heavy.ID(), make([]byte, 100)); err != nil || !ok {
			t.Fatalf("enqueue heavy %d: ok=%v err=%v", i, ok, err)
		}
	}
	// 800/1000 = 0.80, over the 0.75 pause line. The next frame must be PAUSE
	// for the heavy conn, not the light one and not DATA.
	frame, ok := sched.NextFrame()
	if !ok || frame.Type != edgeFramePause || frame.ConnID != heavy.ID() {
		t.Fatalf("frame = %s conn %d ok=%v, want PAUSE on the heaviest conn", frame.Type, frame.ConnID, ok)
	}
}

func TestEdgeBudgetResumeAfterDrain(t *testing.T) {
	sched := newTestScheduler(t, 16, 8, 1000)
	conn, err := sched.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = conn.onOpenAck()
	for i := 0; i < 8; i++ {
		if ok, err := sched.EnqueueData(conn.ID(), make([]byte, 100)); err != nil || !ok {
			t.Fatalf("enqueue %d: ok=%v err=%v", i, ok, err)
		}
	}
	if frame, ok := sched.NextFrame(); !ok || frame.Type != edgeFramePause {
		t.Fatalf("frame = %s ok=%v, want PAUSE once the budget crosses 75%%", frame.Type, ok)
	}
	for sched.BudgetUsed() > 300 {
		frame, ok := sched.NextFrame()
		if !ok {
			t.Fatal("queue emptied before the budget dropped under the resume line")
		}
		if frame.Type == edgeFrameResume {
			t.Fatal("RESUME arrived before the budget fell under 40%")
		}
	}
	frame, ok := sched.NextFrame()
	if !ok || frame.Type != edgeFrameResume || frame.ConnID != conn.ID() {
		t.Fatalf("frame = %s conn %d ok=%v, want RESUME after draining under 40%%", frame.Type, frame.ConnID, ok)
	}
}

func TestEdgeBudgetStallClosesTheTunnel(t *testing.T) {
	clock := newEdgeFakeClock(time.Unix(1_700_000_000, 0))
	sched := newEdgeScheduler(edgeSchedulerOptions{
		clock:      clock,
		queueCap:   8,
		quantum:    2,
		budget:     200,
		stallAfter: 30 * time.Second,
		quarantine: 30 * time.Second,
		openingFor: time.Second,
	})
	conn, err := sched.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = conn.onOpenAck()
	if ok, err := sched.EnqueueData(conn.ID(), make([]byte, 100)); err != nil || !ok {
		t.Fatalf("first enqueue: ok=%v err=%v", ok, err)
	}
	if ok, err := sched.EnqueueData(conn.ID(), make([]byte, 100)); err != nil || !ok {
		t.Fatalf("fill enqueue: ok=%v err=%v", ok, err)
	}
	if ok, err := sched.EnqueueData(conn.ID(), make([]byte, 1)); err != nil || ok {
		t.Fatalf("over-budget enqueue: ok=%v err=%v, want admission denied", ok, err)
	}
	if sched.Stalled() {
		t.Fatal("budget just filled, stall timer should not have fired yet")
	}
	clock.Advance(30 * time.Second)
	if !sched.Stalled() {
		t.Fatal("budget stayed full for the stall window, tunnel should be unhealthy")
	}
	if _, ok := sched.NextFrame(); ok {
		t.Fatal("a stalled scheduler must not emit more frames")
	}
}

func TestEdgeBudgetRejectsWhenFull(t *testing.T) {
	sched := newTestScheduler(t, 8, 2, 50)
	conn, err := sched.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = conn.onOpenAck()
	if ok, err := sched.EnqueueData(conn.ID(), make([]byte, 50)); err != nil || !ok {
		t.Fatalf("fill: ok=%v err=%v", ok, err)
	}
	ok, err := sched.EnqueueData(conn.ID(), []byte("x"))
	if err != nil {
		t.Fatalf("over-budget enqueue: %v", err)
	}
	if ok {
		t.Fatal("enqueue succeeded over the session memory budget")
	}
	if sched.BudgetUsed() != 50 {
		t.Fatalf("budget used = %d, want 50; a rejected frame must not be counted", sched.BudgetUsed())
	}
}
