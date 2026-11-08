package server

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestEdgeConnStateMachineHalfClose(t *testing.T) {
	conn := newEdgeConn(1, 8, time.Now(), time.Second)
	if conn.state() != edgeConnOpening {
		t.Fatalf("state = %s, want OPENING", conn.state())
	}
	if err := conn.onOpenAck(); err != nil {
		t.Fatalf("open ack: %v", err)
	}
	if conn.state() != edgeConnOpen {
		t.Fatalf("state = %s, want OPEN", conn.state())
	}

	ready, err := conn.localShutdown()
	if err != nil || !ready {
		t.Fatalf("local shutdown ready = %v err = %v, want an immediate FIN", ready, err)
	}
	if conn.state() != edgeConnHalfLocal {
		t.Fatalf("state = %s, want HALF_LOCAL after local shutdown", conn.state())
	}

	action := conn.onRemoteShutdown()
	if action != edgeActionCloseWrite {
		t.Fatalf("remote shutdown action = %s, want close-write; FIN has not been sent yet", action)
	}
	if !conn.shouldCloseWrite() {
		t.Fatal("remote SHUTDOWN should ask the local TCP to CloseWrite")
	}
	if released := conn.markShutdownSent(); !released {
		t.Fatal("both halves are shut, sending FIN should release the conn")
	}
	if conn.state() != edgeConnClosed {
		t.Fatalf("state = %s, want CLOSED", conn.state())
	}
}

func TestEdgeConnRemoteShutdownFirstAsksCloseWrite(t *testing.T) {
	conn := newEdgeConn(2, 8, time.Now(), time.Second)
	if err := conn.onOpenAck(); err != nil {
		t.Fatalf("open ack: %v", err)
	}
	action := conn.onRemoteShutdown()
	if action != edgeActionCloseWrite {
		t.Fatalf("action = %s, want close-write", action)
	}
	if conn.state() != edgeConnHalfRemote {
		t.Fatalf("state = %s, want HALF_REMOTE", conn.state())
	}
	if _, _, err := conn.enqueueData([]byte("still sending")); err != nil {
		t.Fatalf("send after remote shutdown: %v", err)
	}
	if err := conn.onRemoteData(); err == nil {
		t.Fatal("DATA after remote SHUTDOWN should be rejected")
	}
}

func TestEdgeConnRejectsDataAfterLocalShutdown(t *testing.T) {
	conn := newEdgeConn(3, 8, time.Now(), time.Second)
	_ = conn.onOpenAck()
	if _, err := conn.localShutdown(); err != nil {
		t.Fatalf("local shutdown: %v", err)
	}
	if _, ok, err := conn.enqueueData([]byte("late")); err == nil && ok {
		t.Fatal("enqueue after local shutdown should fail")
	} else if !errors.Is(err, errEdgeConnSendClosed) {
		t.Fatalf("enqueue error = %v, want errEdgeConnSendClosed", err)
	}
}

func TestEdgeConnCloseDiscardsQueuedData(t *testing.T) {
	conn := newEdgeConn(4, 8, time.Now(), time.Second)
	_ = conn.onOpenAck()
	if _, ok, err := conn.enqueueData([]byte("one")); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}
	if _, ok, err := conn.enqueueData([]byte("two")); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}
	action, discarded := conn.onRemoteClose()
	if action != edgeActionClose {
		t.Fatalf("action = %s, want close", action)
	}
	if discarded != 6 {
		t.Fatalf("discarded = %d, want 6 bytes", discarded)
	}
	if _, _, _, _, ok := conn.popData(); ok {
		t.Fatal("CLOSE should drop every queued DATA frame")
	}
	if conn.state() != edgeConnClosed {
		t.Fatalf("state = %s, want CLOSED", conn.state())
	}
}

func TestEdgeConnQueueBackpressure(t *testing.T) {
	conn := newEdgeConn(5, 2, time.Now(), time.Second)
	_ = conn.onOpenAck()
	if _, ok, err := conn.enqueueData([]byte("a")); err != nil || !ok {
		t.Fatalf("first enqueue: ok=%v err=%v", ok, err)
	}
	if _, ok, err := conn.enqueueData([]byte("b")); err != nil || !ok {
		t.Fatalf("second enqueue: ok=%v err=%v", ok, err)
	}
	if _, ok, err := conn.enqueueData([]byte("c")); err != nil || ok {
		t.Fatalf("third enqueue: ok=%v err=%v, want backpressure", ok, err)
	}
}

func TestEdgeConnOptimisticDataBeforeAck(t *testing.T) {
	conn := newEdgeConn(6, 8, time.Now(), time.Second)
	if _, ok, err := conn.enqueueData([]byte("clienthello")); err != nil || !ok {
		t.Fatalf("optimistic enqueue: ok=%v err=%v", ok, err)
	}
	payload, n, more, finReady, ok := conn.popData()
	if !ok || more || finReady || n != 11 || string(payload) != "clienthello" {
		t.Fatalf("pop = %q n=%d more=%v fin=%v ok=%v", payload, n, more, finReady, ok)
	}
}

func TestEdgeConnOpeningTimeout(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	conn := newEdgeConn(7, 8, start, time.Second)
	if conn.openingTimedOut(start.Add(time.Second - time.Nanosecond)) {
		t.Fatal("should not time out before the deadline")
	}
	if !conn.openingTimedOut(start.Add(time.Second)) {
		t.Fatal("should time out at the deadline")
	}
	if err := conn.onOpenAck(); err != nil {
		t.Fatalf("open ack: %v", err)
	}
	if conn.openingTimedOut(start.Add(time.Minute)) {
		t.Fatal("an acked conn must not time out")
	}
}

func TestEdgeConnIDTableAllocatesAndQuarantines(t *testing.T) {
	clock := newEdgeFakeClock(time.Unix(1_700_000_000, 0))
	table := newEdgeConnIDTable(clock, 8, 30*time.Second, time.Second)

	first, err := table.Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if first.ID() != 1 {
		t.Fatalf("first id = %d, want 1", first.ID())
	}
	second, err := table.Allocate()
	if err != nil {
		t.Fatalf("allocate second: %v", err)
	}
	if second.ID() != 2 {
		t.Fatalf("second id = %d, want 2", second.ID())
	}

	if _, err := table.Adopt(1); !errors.Is(err, errEdgeConnIDInUse) {
		t.Fatalf("adopt live id: %v, want errEdgeConnIDInUse", err)
	}
	if _, err := table.Adopt(0); !errors.Is(err, errEdgeConnIDReserved) {
		t.Fatalf("adopt 0: %v, want reserved", err)
	}

	table.Release(1)
	if _, status := table.Lookup(1); status != edgeIDQuarantined {
		t.Fatalf("lookup after release = %v, want quarantined", status)
	}
	if _, err := table.Adopt(1); !errors.Is(err, errEdgeConnQuarantined) {
		t.Fatalf("adopt during quarantine: %v", err)
	}

	clock.Advance(30 * time.Second)
	adopted, err := table.Adopt(1)
	if err != nil {
		t.Fatalf("adopt after quarantine: %v", err)
	}
	if adopted.ID() != 1 {
		t.Fatalf("reused id = %d, want 1", adopted.ID())
	}
	if adopted.state() != edgeConnOpening {
		t.Fatalf("adopted state = %s, want OPENING until this side acks", adopted.state())
	}
}

func TestEdgeConnIDTableWrapsPastQuarantinedMax(t *testing.T) {
	clock := newEdgeFakeClock(time.Unix(1_700_000_000, 0))
	table := newEdgeConnIDTable(clock, 8, time.Hour, time.Second)
	first, err := table.Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	table.quarantine[math.MaxUint32] = clock.Now().Add(time.Hour)
	table.next = math.MaxUint32
	wrapped, err := table.Allocate()
	if err != nil {
		t.Fatalf("allocate after wrap: %v", err)
	}
	if wrapped.ID() == 0 || wrapped.ID() == math.MaxUint32 || wrapped.ID() == first.ID() {
		t.Fatalf("wrapped id = %d, want a skipped-and-wrapped live id", wrapped.ID())
	}
}

func TestEdgeConnFINWaitsForRemainingData(t *testing.T) {
	conn := newEdgeConn(8, 8, time.Now(), time.Second)
	_ = conn.onOpenAck()
	if _, ok, err := conn.enqueueData([]byte("abc")); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}
	ready, err := conn.localShutdown()
	if err != nil {
		t.Fatalf("local shutdown: %v", err)
	}
	if ready {
		t.Fatal("FIN must wait until the remaining DATA has been popped")
	}
	_, _, more, finReady, ok := conn.popData()
	if !ok || more {
		t.Fatalf("pop remaining DATA: more=%v ok=%v", more, ok)
	}
	if !finReady {
		t.Fatal("popping the last DATA after local shutdown should arm FIN")
	}
}
