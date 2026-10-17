package server

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// edgeConn 是一条多路复用 TCP 连接在隧道里的状态
//（docs/design/DOOPS_EDGE_TUNNEL.md §4.4）。
//
// 方向以本端为准：sendClosed 表示本端不再发 DATA（已或将发 SHUTDOWN），
// recvClosed 表示对端不再发 DATA（已收 SHUTDOWN）。CLOSE 是硬关闭，对应 RST。

type edgeConnState uint8

const (
	edgeConnOpening edgeConnState = iota
	edgeConnOpen
	edgeConnHalfLocal
	edgeConnHalfRemote
	edgeConnClosed
)

func (s edgeConnState) String() string {
	switch s {
	case edgeConnOpening:
		return "OPENING"
	case edgeConnOpen:
		return "OPEN"
	case edgeConnHalfLocal:
		return "HALF_LOCAL"
	case edgeConnHalfRemote:
		return "HALF_REMOTE"
	case edgeConnClosed:
		return "CLOSED"
	}
	return fmt.Sprintf("UNKNOWN(%d)", uint8(s))
}

type edgeLocalAction uint8

const (
	edgeActionNone edgeLocalAction = iota
	edgeActionCloseWrite
	edgeActionClose
)

func (a edgeLocalAction) String() string {
	switch a {
	case edgeActionNone:
		return "none"
	case edgeActionCloseWrite:
		return "close-write"
	case edgeActionClose:
		return "close"
	}
	return fmt.Sprintf("unknown(%d)", uint8(a))
}

var (
	errEdgeConnClosed       = errors.New("edge: connection is closed")
	errEdgeConnSendClosed   = errors.New("edge: connection send side is closed")
	errEdgeConnRecvClosed   = errors.New("edge: connection receive side is closed")
	errEdgeConnNotOpening   = errors.New("edge: connection is not waiting for OPEN_ACK")
	errEdgeConnIDsExhausted = errors.New("edge: conn_id space exhausted")
	errEdgeConnIDInUse      = errors.New("edge: conn_id already in use")
	errEdgeConnIDReserved   = errors.New("edge: conn_id 0 is reserved")
	errEdgeConnQuarantined  = errors.New("edge: conn_id is in quarantine")
)

type edgeClock interface {
	Now() time.Time
}

type edgeRealClock struct{}

func (edgeRealClock) Now() time.Time { return time.Now() }

type edgeFakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newEdgeFakeClock(now time.Time) *edgeFakeClock {
	return &edgeFakeClock{now: now}
}

func (c *edgeFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *edgeFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type edgeConn struct {
	id uint32

	mu              sync.Mutex
	acked           bool
	sendClosed      bool
	recvClosed      bool
	closed          bool
	shutdownQueued  bool
	shutdownSent    bool
	peerPaused      bool
	queued          bool
	closeWrite      bool
	dataQ           [][]byte
	queueCap        int
	inflight        int64
	openedAt        time.Time
	openingDeadline time.Time
}

func newEdgeConn(id uint32, queueCap int, now time.Time, openingTimeout time.Duration) *edgeConn {
	if queueCap <= 0 {
		queueCap = defaultEdgeConnQueue
	}
	if openingTimeout <= 0 {
		openingTimeout = defaultEdgeDialTimeout + edgeOpeningAckSlack
	}
	return &edgeConn{
		id:              id,
		queueCap:        queueCap,
		openedAt:        now,
		openingDeadline: now.Add(openingTimeout),
	}
}

func (c *edgeConn) ID() uint32 { return c.id }

func (c *edgeConn) state() edgeConnState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateLocked()
}

func (c *edgeConn) stateLocked() edgeConnState {
	if c.closed || (c.sendClosed && c.recvClosed && c.shutdownSent) {
		return edgeConnClosed
	}
	if !c.acked {
		return edgeConnOpening
	}
	if c.sendClosed && !c.recvClosed {
		return edgeConnHalfLocal
	}
	if c.recvClosed && !c.sendClosed {
		return edgeConnHalfRemote
	}
	if c.sendClosed && c.recvClosed {
		return edgeConnHalfLocal
	}
	return edgeConnOpen
}

func (c *edgeConn) inflightBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight
}

func (c *edgeConn) pausedByPeer() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peerPaused
}

func (c *edgeConn) shouldCloseWrite() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeWrite
}

func (c *edgeConn) openingTimedOut(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.acked && !c.closed && !now.Before(c.openingDeadline)
}

func (c *edgeConn) onOpenAck() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errEdgeConnClosed
	}
	if c.acked {
		return errEdgeConnNotOpening
	}
	c.acked = true
	return nil
}

func (c *edgeConn) onRemoteData() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errEdgeConnClosed
	}
	if c.recvClosed {
		return errEdgeConnRecvClosed
	}
	return nil
}

func (c *edgeConn) onRemoteShutdown() edgeLocalAction {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.recvClosed {
		return edgeActionNone
	}
	c.recvClosed = true
	c.closeWrite = true
	if c.sendClosed && c.shutdownSent && len(c.dataQ) == 0 {
		c.closed = true
		return edgeActionClose
	}
	return edgeActionCloseWrite
}

func (c *edgeConn) onRemoteClose() (edgeLocalAction, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	discarded := c.discardLocked()
	c.closed = true
	c.sendClosed = true
	c.recvClosed = true
	c.shutdownSent = true
	c.closeWrite = false
	return edgeActionClose, discarded
}

func (c *edgeConn) setPeerPaused(paused bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.peerPaused = paused
}

// enqueueData 把本端读到的字节排进待发队列。false 表示队列或预算已满，调用方应停 Read。
func (c *edgeConn) enqueueData(payload []byte) (int64, bool, error) {
	if len(payload) == 0 {
		return 0, false, errors.New("edge: DATA payload is empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, false, errEdgeConnClosed
	}
	if c.sendClosed {
		return 0, false, errEdgeConnSendClosed
	}
	if len(c.dataQ) >= c.queueCap {
		return 0, false, nil
	}
	c.dataQ = append(c.dataQ, cloneEdgePayload(payload))
	c.inflight += int64(len(payload))
	return int64(len(payload)), true, nil
}

// localShutdown 标记本端不再发 DATA。若待发队列已空，调用方应立刻把 SHUTDOWN 送进 finQ；
// 否则等最后一帧 DATA 出队后再送，保证本连接的 FIN 落在剩余字节之后。
func (c *edgeConn) localShutdown() (readyForFIN bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false, errEdgeConnClosed
	}
	if c.sendClosed {
		return false, nil
	}
	c.sendClosed = true
	if len(c.dataQ) == 0 && !c.shutdownQueued {
		c.shutdownQueued = true
		return true, nil
	}
	return false, nil
}

func (c *edgeConn) localClose() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	discarded := c.discardLocked()
	c.closed = true
	c.sendClosed = true
	c.recvClosed = true
	c.shutdownSent = true
	return discarded
}

func (c *edgeConn) popData() (payload []byte, n int64, more bool, finReady bool, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.dataQ) == 0 {
		return nil, 0, false, false, false
	}
	payload = c.dataQ[0]
	c.dataQ[0] = nil
	c.dataQ = c.dataQ[1:]
	n = int64(len(payload))
	c.inflight -= n
	if c.inflight < 0 {
		c.inflight = 0
	}
	more = len(c.dataQ) > 0
	if c.readyForFINLocked() {
		c.shutdownQueued = true
		finReady = true
	}
	return payload, n, more, finReady, true
}

func (c *edgeConn) markShutdownSent() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shutdownSent = true
	if c.recvClosed {
		c.closed = true
		return true
	}
	return false
}

func (c *edgeConn) readyForFINLocked() bool {
	return c.sendClosed && !c.shutdownQueued && !c.shutdownSent && !c.closed && len(c.dataQ) == 0
}

func (c *edgeConn) markQueued() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.queued {
		return false
	}
	c.queued = true
	return true
}

func (c *edgeConn) clearQueued() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queued = false
}

func (c *edgeConn) discardLocked() int64 {
	n := c.inflight
	for i := range c.dataQ {
		c.dataQ[i] = nil
	}
	c.dataQ = nil
	c.inflight = 0
	c.queued = false
	c.shutdownQueued = true
	return n
}

type edgeIDStatus uint8

const (
	edgeIDUnknown edgeIDStatus = iota
	edgeIDLive
	edgeIDQuarantined
)

type edgeConnIDTable struct {
	mu            sync.Mutex
	next          uint32
	live          map[uint32]*edgeConn
	quarantine    map[uint32]time.Time
	clock         edgeClock
	quarantineFor time.Duration
	queueCap      int
	openingFor    time.Duration
}

func newEdgeConnIDTable(clock edgeClock, queueCap int, quarantine time.Duration, opening time.Duration) *edgeConnIDTable {
	if clock == nil {
		clock = edgeRealClock{}
	}
	if queueCap <= 0 {
		queueCap = defaultEdgeConnQueue
	}
	if quarantine <= 0 {
		quarantine = defaultEdgeConnIDQuarantine
	}
	if opening <= 0 {
		opening = defaultEdgeDialTimeout + edgeOpeningAckSlack
	}
	return &edgeConnIDTable{
		next:          1,
		live:          make(map[uint32]*edgeConn),
		quarantine:    make(map[uint32]time.Time),
		clock:         clock,
		quarantineFor: quarantine,
		queueCap:      queueCap,
		openingFor:    opening,
	}
}

func (t *edgeConnIDTable) Allocate() (*edgeConn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock.Now()
	t.sweepLocked(now)
	if t.next == edgeControlConnID {
		t.next = 1
	}
	start := t.next
	for {
		id := t.next
		if t.next == math.MaxUint32 {
			t.next = 1
		} else {
			t.next++
		}
		if _, live := t.live[id]; live {
			if t.next == start {
				return nil, errEdgeConnIDsExhausted
			}
			continue
		}
		if _, blocked := t.quarantine[id]; blocked {
			if t.next == start {
				return nil, errEdgeConnIDsExhausted
			}
			continue
		}
		conn := newEdgeConn(id, t.queueCap, now, t.openingFor)
		t.live[id] = conn
		return conn, nil
	}
}

func (t *edgeConnIDTable) Adopt(id uint32) (*edgeConn, error) {
	if id == edgeControlConnID {
		return nil, errEdgeConnIDReserved
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock.Now()
	t.sweepLocked(now)
	if _, live := t.live[id]; live {
		return nil, errEdgeConnIDInUse
	}
	if _, blocked := t.quarantine[id]; blocked {
		return nil, errEdgeConnQuarantined
	}
	conn := newEdgeConn(id, t.queueCap, now, t.openingFor)
	t.live[id] = conn
	return conn, nil
}

func (t *edgeConnIDTable) Lookup(id uint32) (*edgeConn, edgeIDStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweepLocked(t.clock.Now())
	if conn, live := t.live[id]; live {
		return conn, edgeIDLive
	}
	if _, blocked := t.quarantine[id]; blocked {
		return nil, edgeIDQuarantined
	}
	return nil, edgeIDUnknown
}

func (t *edgeConnIDTable) Release(id uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.live, id)
	t.quarantine[id] = t.clock.Now().Add(t.quarantineFor)
}

func (t *edgeConnIDTable) Live() []*edgeConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*edgeConn, 0, len(t.live))
	for _, conn := range t.live {
		out = append(out, conn)
	}
	return out
}

func (t *edgeConnIDTable) OpeningTimedOut(now time.Time) []*edgeConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []*edgeConn
	for _, conn := range t.live {
		if conn.openingTimedOut(now) {
			out = append(out, conn)
		}
	}
	return out
}

func (t *edgeConnIDTable) sweepLocked(now time.Time) {
	for id, until := range t.quarantine {
		if !now.Before(until) {
			delete(t.quarantine, id)
		}
	}
}
