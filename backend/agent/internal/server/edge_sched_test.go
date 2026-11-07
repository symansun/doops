package server

import (
	"bytes"
	"testing"
	"time"
)

func TestEdgeSchedulerControlFramesJumpTheDataQueue(t *testing.T) {
	sched := newTestScheduler(t, 8, 2, 1<<20)
	conn, err := sched.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := conn.onOpenAck(); err != nil {
		t.Fatalf("ack: %v", err)
	}
	for i := 0; i < 4; i++ {
		if ok, err := sched.EnqueueData(conn.ID(), []byte("xxxx")); err != nil || !ok {
			t.Fatalf("enqueue DATA: ok=%v err=%v", ok, err)
		}
	}
	open := edgeFrame{Type: edgeFrameOpen, ConnID: 9, Payload: mustEncodeEdgeOpen(t, 1, "amp.jm.aiedulab.cn")}
	if err := sched.EnqueueControl(open); err != nil {
		t.Fatalf("enqueue OPEN: %v", err)
	}

	first, ok := sched.NextFrame()
	if !ok || first.Type != edgeFrameOpen || first.ConnID != 9 {
		t.Fatalf("first frame = %+v ok=%v, want OPEN conn 9", first, ok)
	}
	second, ok := sched.NextFrame()
	if !ok || second.Type != edgeFrameData {
		t.Fatalf("second frame = %+v ok=%v, want DATA after the control frame", second, ok)
	}
}

func TestEdgeSchedulerWeightedRoundRobinDoesNotStarveSmallFlows(t *testing.T) {
	sched := newTestScheduler(t, 32, 2, 1<<20)
	large, err := sched.Open()
	if err != nil {
		t.Fatalf("open large: %v", err)
	}
	small, err := sched.Open()
	if err != nil {
		t.Fatalf("open small: %v", err)
	}
	_ = large.onOpenAck()
	_ = small.onOpenAck()

	for i := 0; i < 10; i++ {
		if ok, err := sched.EnqueueData(large.ID(), []byte("L")); err != nil || !ok {
			t.Fatalf("enqueue large: ok=%v err=%v", ok, err)
		}
	}
	if ok, err := sched.EnqueueData(small.ID(), []byte("S")); err != nil || !ok {
		t.Fatalf("enqueue small: ok=%v err=%v", ok, err)
	}

	var order []uint32
	for {
		frame, ok := sched.NextFrame()
		if !ok {
			break
		}
		if frame.Type != edgeFrameData {
			t.Fatalf("unexpected %s while draining DATA", frame.Type)
		}
		order = append(order, frame.ConnID)
	}
	if len(order) != 11 {
		t.Fatalf("drained %d DATA frames, want 11", len(order))
	}
	smallAt := -1
	for i, id := range order {
		if id == small.ID() {
			smallAt = i
			break
		}
	}
	if smallAt < 0 {
		t.Fatal("small flow never ran")
	}
	if smallAt > 2 {
		t.Fatalf("small flow ran at position %d, want it within one quantum of the large flow (index <= 2)", smallAt)
	}
	if order[smallAt] == large.ID() {
		t.Fatal("small flow slot was overwritten")
	}
}

func TestEdgeSchedulerShutdownFlushesSameConnThenJumpsOthers(t *testing.T) {
	sched := newTestScheduler(t, 16, 2, 1<<20)
	busy, err := sched.Open()
	if err != nil {
		t.Fatalf("open busy: %v", err)
	}
	idle, err := sched.Open()
	if err != nil {
		t.Fatalf("open idle: %v", err)
	}
	_ = busy.onOpenAck()
	_ = idle.onOpenAck()

	for i := 0; i < 6; i++ {
		if ok, err := sched.EnqueueData(busy.ID(), []byte("B")); err != nil || !ok {
			t.Fatalf("enqueue busy: ok=%v err=%v", ok, err)
		}
	}
	if err := sched.LocalShutdown(idle.ID()); err != nil {
		t.Fatalf("shutdown idle: %v", err)
	}
	if err := sched.LocalShutdown(busy.ID()); err != nil {
		t.Fatalf("shutdown busy: %v", err)
	}

	frames := drainFrames(t, sched)
	if frames[0].Type != edgeFrameShutdown || frames[0].ConnID != idle.ID() {
		t.Fatalf("first frame = %s conn %d, want idle SHUTDOWN so close is not stuck behind the large flow", frames[0].Type, frames[0].ConnID)
	}

	var busyData int
	sawBusyFIN := false
	for _, frame := range frames[1:] {
		switch {
		case frame.Type == edgeFrameData && frame.ConnID == busy.ID():
			if sawBusyFIN {
				t.Fatal("DATA for busy arrived after its SHUTDOWN")
			}
			busyData++
		case frame.Type == edgeFrameShutdown && frame.ConnID == busy.ID():
			sawBusyFIN = true
		default:
			t.Fatalf("unexpected frame %s conn %d", frame.Type, frame.ConnID)
		}
	}
	if busyData != 6 {
		t.Fatalf("busy DATA frames = %d, want 6 before its SHUTDOWN", busyData)
	}
	if !sawBusyFIN {
		t.Fatal("busy SHUTDOWN never sent")
	}
}

func TestEdgeSchedulerCloseDiscardsQueuedData(t *testing.T) {
	sched := newTestScheduler(t, 8, 2, 1<<20)
	conn, err := sched.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = conn.onOpenAck()
	if ok, err := sched.EnqueueData(conn.ID(), bytes.Repeat([]byte("x"), 32)); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}
	if err := sched.LocalClose(conn.ID(), edgeCloseUpstreamFailure, "reset"); err != nil {
		t.Fatalf("close: %v", err)
	}
	frame, ok := sched.NextFrame()
	if !ok || frame.Type != edgeFrameClose || frame.ConnID != conn.ID() {
		t.Fatalf("frame = %+v ok=%v, want CLOSE", frame, ok)
	}
	if extra, ok := sched.NextFrame(); ok {
		t.Fatalf("got extra frame %s after CLOSE, queued DATA should have been discarded", extra.Type)
	}
	if sched.BudgetUsed() != 0 {
		t.Fatalf("budget used = %d, want 0 after discard", sched.BudgetUsed())
	}
	if _, status := sched.Lookup(conn.ID()); status != edgeIDQuarantined {
		t.Fatalf("lookup after close = %v, want quarantined", status)
	}
}

func TestEdgeSchedulerRemoteCloseDropsDelayedFrames(t *testing.T) {
	sched := newTestScheduler(t, 8, 2, 1<<20)
	conn, err := sched.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = conn.onOpenAck()
	id := conn.ID()
	action, err := sched.HandleRemote(edgeFrame{Type: edgeFrameClose, ConnID: id, Payload: encodeEdgeClosePayload(edgeCloseNormal, "")})
	if err != nil {
		t.Fatalf("remote close: %v", err)
	}
	if action != edgeActionClose {
		t.Fatalf("action = %s, want close", action)
	}
	action, err = sched.HandleRemote(edgeFrame{Type: edgeFrameData, ConnID: id, Payload: []byte("late")})
	if err != nil {
		t.Fatalf("delayed DATA: %v", err)
	}
	if action != edgeActionNone {
		t.Fatalf("delayed DATA action = %s, want it dropped", action)
	}
}

func TestEdgeSchedulerBothShutdownsReleaseConn(t *testing.T) {
	sched := newTestScheduler(t, 8, 2, 1<<20)
	conn, err := sched.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = conn.onOpenAck()
	id := conn.ID()
	if err := sched.LocalShutdown(id); err != nil {
		t.Fatalf("local shutdown: %v", err)
	}
	frame, ok := sched.NextFrame()
	if !ok || frame.Type != edgeFrameShutdown {
		t.Fatalf("frame = %+v ok=%v, want SHUTDOWN", frame, ok)
	}
	action, err := sched.HandleRemote(edgeFrame{Type: edgeFrameShutdown, ConnID: id})
	if err != nil {
		t.Fatalf("remote shutdown: %v", err)
	}
	if action != edgeActionClose {
		t.Fatalf("action = %s, want close after both FINs", action)
	}
	if _, status := sched.Lookup(id); status != edgeIDQuarantined {
		t.Fatalf("lookup = %v, want quarantined", status)
	}
}

func TestEdgeSchedulerExpireOpeningClosesUnackedConns(t *testing.T) {
	clock := newEdgeFakeClock(time.Unix(1_700_000_000, 0))
	sched := newEdgeScheduler(edgeSchedulerOptions{
		clock:      clock,
		queueCap:   8,
		quantum:    2,
		budget:     1 << 20,
		stallAfter: 30 * time.Second,
		quarantine: 30 * time.Second,
		openingFor: time.Second,
	})
	conn, err := sched.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if ok, err := sched.EnqueueData(conn.ID(), []byte("hello")); err != nil || !ok {
		t.Fatalf("optimistic data: ok=%v err=%v", ok, err)
	}
	if n := sched.ExpireOpening(clock.Now()); n != 0 {
		t.Fatalf("expired %d conns before the deadline", n)
	}
	clock.Advance(time.Second)
	if n := sched.ExpireOpening(clock.Now()); n != 1 {
		t.Fatalf("expired %d conns, want 1 after the opening deadline", n)
	}
	frame, ok := sched.NextFrame()
	if !ok || frame.Type != edgeFrameClose || frame.ConnID != conn.ID() {
		t.Fatalf("frame = %s conn %d ok=%v, want CLOSE for the timed-out opening", frame.Type, frame.ConnID, ok)
	}
	if extra, ok := sched.NextFrame(); ok {
		t.Fatalf("got extra %s, optimistic DATA should have been discarded", extra.Type)
	}
}

func TestEdgeSchedulerPeerPauseStopsLocalEnqueue(t *testing.T) {
	sched := newTestScheduler(t, 8, 2, 1<<20)
	conn, err := sched.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = conn.onOpenAck()
	if _, err := sched.HandleRemote(edgeFrame{Type: edgeFramePause, ConnID: conn.ID()}); err != nil {
		t.Fatalf("remote pause: %v", err)
	}
	ok, err := sched.EnqueueData(conn.ID(), []byte("x"))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if ok {
		t.Fatal("enqueue succeeded while paused by peer; local Read should stop")
	}
	if _, err := sched.HandleRemote(edgeFrame{Type: edgeFrameResume, ConnID: conn.ID()}); err != nil {
		t.Fatalf("remote resume: %v", err)
	}
	ok, err = sched.EnqueueData(conn.ID(), []byte("x"))
	if err != nil || !ok {
		t.Fatalf("enqueue after resume: ok=%v err=%v", ok, err)
	}
}

func newTestScheduler(t *testing.T, queueCap, quantum int, budget int64) *edgeScheduler {
	t.Helper()
	return newEdgeScheduler(edgeSchedulerOptions{
		clock:      newEdgeFakeClock(time.Unix(1_700_000_000, 0)),
		queueCap:   queueCap,
		quantum:    quantum,
		budget:     budget,
		stallAfter: 30 * time.Second,
		quarantine: 30 * time.Second,
		openingFor: time.Second,
	})
}

func drainFrames(t *testing.T, sched *edgeScheduler) []edgeFrame {
	t.Helper()
	var frames []edgeFrame
	for {
		frame, ok := sched.NextFrame()
		if !ok {
			return frames
		}
		frames = append(frames, frame)
	}
}
