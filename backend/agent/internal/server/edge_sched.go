package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	errEdgeTunnelStall = errors.New("edge: write queue stalled")
	errEdgeUnknownConn = errors.New("edge: unknown conn_id")
)

type edgeMemBudget struct {
	limit int64
	used  int64
}

func (b *edgeMemBudget) tryAdd(n int64) bool {
	if n < 0 {
		return false
	}
	if b.used+n > b.limit {
		return false
	}
	b.used += n
	return true
}

func (b *edgeMemBudget) release(n int64) {
	if n <= 0 {
		return
	}
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
}

func (b *edgeMemBudget) ratio() float64 {
	if b.limit <= 0 {
		return 1
	}
	return float64(b.used) / float64(b.limit)
}

func (b *edgeMemBudget) full() bool {
	return b.limit > 0 && b.used >= b.limit
}

// edgeScheduler 是一条 Edge WebSocket 上的单 writer 调度器
// （docs/design/DOOPS_EDGE_TUNNEL.md §4.5、§4.6）。
//
// 出队顺序：优先控制帧 → 已排空 DATA 的 SHUTDOWN → 按 quantum 轮转的 DATA。
// SHUTDOWN 不进优先队列，避免插到本连接剩余字节前面；它进 finQ，所以也不
// 会排在其他连接的大文件后面。
type edgeScheduler struct {
	mu          sync.Mutex
	ids         *edgeConnIDTable
	budget      edgeMemBudget
	control     []edgeFrame
	ready       []uint32
	finQ        []uint32
	current     uint32
	quantumLeft int
	quantum     int
	pauseSent   map[uint32]bool
	fullSince   time.Time
	stallAfter  time.Duration
	clock       edgeClock
	stalled     bool
	stopped     bool
	notify      chan struct{}
	space       chan struct{}
}

type edgeSchedulerOptions struct {
	clock      edgeClock
	queueCap   int
	quantum    int
	budget     int64
	stallAfter time.Duration
	quarantine time.Duration
	openingFor time.Duration
}

func newEdgeScheduler(opts edgeSchedulerOptions) *edgeScheduler {
	clock := opts.clock
	if clock == nil {
		clock = edgeRealClock{}
	}
	queueCap := opts.queueCap
	if queueCap <= 0 {
		queueCap = edgeConnQueueSize()
	}
	quantum := opts.quantum
	if quantum <= 0 {
		quantum = edgeSchedQuantum()
	}
	budget := opts.budget
	if budget <= 0 {
		budget = edgeMemBudgetBytes()
	}
	stallAfter := opts.stallAfter
	if stallAfter <= 0 {
		stallAfter = edgeWriteStall()
	}
	quarantine := opts.quarantine
	if quarantine <= 0 {
		quarantine = edgeConnIDQuarantine()
	}
	openingFor := opts.openingFor
	if openingFor <= 0 {
		openingFor = defaultEdgeDialTimeout + edgeOpeningAckSlack
	}
	return &edgeScheduler{
		ids:        newEdgeConnIDTable(clock, queueCap, quarantine, openingFor),
		budget:     edgeMemBudget{limit: budget},
		quantum:    quantum,
		pauseSent:  make(map[uint32]bool),
		stallAfter: stallAfter,
		clock:      clock,
		notify:     make(chan struct{}, 1),
		space:      make(chan struct{}, 1),
	}
}

func (s *edgeScheduler) Open() (*edgeConn, error) {
	return s.ids.Allocate()
}

func (s *edgeScheduler) Accept(id uint32) (*edgeConn, error) {
	return s.ids.Adopt(id)
}

func (s *edgeScheduler) Lookup(id uint32) (*edgeConn, edgeIDStatus) {
	return s.ids.Lookup(id)
}

func (s *edgeScheduler) Stalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkStallLocked()
	return s.stalled
}

func (s *edgeScheduler) BudgetUsed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.budget.used
}

func (s *edgeScheduler) LiveCount() int {
	return len(s.ids.Live())
}

func (s *edgeScheduler) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.kick(s.notify)
	s.kick(s.space)
}

func (s *edgeScheduler) WaitFrame(ctx context.Context) (edgeFrame, bool) {
	for {
		if frame, ok := s.NextFrame(); ok {
			return frame, true
		}
		s.mu.Lock()
		dead := s.stopped || s.stalled
		s.mu.Unlock()
		if dead {
			return edgeFrame{}, false
		}
		select {
		case <-ctx.Done():
			return edgeFrame{}, false
		case <-s.notify:
		}
	}
}

func (s *edgeScheduler) WaitEnqueue(ctx context.Context, id uint32, payload []byte) error {
	for {
		ok, err := s.EnqueueData(id, payload)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		s.mu.Lock()
		dead := s.stopped || s.stalled
		s.mu.Unlock()
		if dead {
			return errEdgeTunnelStall
		}
		if conn, status := s.Lookup(id); status == edgeIDLive && conn.pausedByPeer() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.space:
		}
	}
}

func (s *edgeScheduler) kick(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *edgeScheduler) EnqueueControl(frame edgeFrame) error {
	if err := frame.validate(); err != nil {
		return err
	}
	if !frame.isPriority() {
		return fmt.Errorf("edge: %s is not a priority frame", frame.Type)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.stalled {
		return errEdgeTunnelStall
	}
	s.control = append(s.control, frame)
	s.kick(s.notify)
	return nil
}

func (s *edgeScheduler) EnqueueData(id uint32, payload []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.stalled {
		return false, errEdgeTunnelStall
	}
	conn, status := s.ids.Lookup(id)
	if status != edgeIDLive {
		return false, errEdgeUnknownConn
	}
	if conn.pausedByPeer() {
		return false, nil
	}
	n := int64(len(payload))
	if !s.budget.tryAdd(n) {
		s.maybeFlowControlLocked()
		return false, nil
	}
	added, ok, err := conn.enqueueData(payload)
	if err != nil || !ok {
		s.budget.release(n)
		s.maybeFlowControlLocked()
		return false, err
	}
	if added != n {
		s.budget.release(n - added)
	}
	s.enqueueReadyLocked(id)
	s.maybeFlowControlLocked()
	s.kick(s.notify)
	return true, nil
}

func (s *edgeScheduler) LocalShutdown(id uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stalled {
		return errEdgeTunnelStall
	}
	conn, status := s.ids.Lookup(id)
	if status != edgeIDLive {
		return errEdgeUnknownConn
	}
	ready, err := conn.localShutdown()
	if err != nil {
		return err
	}
	if ready {
		s.finQ = append(s.finQ, id)
		s.kick(s.notify)
	}
	return nil
}

func (s *edgeScheduler) LocalClose(id uint32, reason edgeCloseReason, detail string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stalled {
		return errEdgeTunnelStall
	}
	conn, status := s.ids.Lookup(id)
	if status != edgeIDLive {
		return errEdgeUnknownConn
	}
	discarded := conn.localClose()
	s.budget.release(discarded)
	s.dropReadyLocked(id)
	s.dropFinLocked(id)
	frame := edgeFrame{Type: edgeFrameClose, ConnID: id, Payload: encodeEdgeClosePayload(reason, detail)}
	s.control = append(s.control, frame)
	s.releaseLocked(id)
	s.maybeFlowControlLocked()
	if discarded > 0 {
		s.kick(s.space)
	}
	s.kick(s.notify)
	return nil
}

func (s *edgeScheduler) HandleRemote(frame edgeFrame) (edgeLocalAction, error) {
	if err := frame.validate(); err != nil {
		return edgeActionNone, err
	}
	if frame.ConnID == edgeControlConnID {
		return edgeActionNone, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conn, status := s.ids.Lookup(frame.ConnID)
	if status != edgeIDLive {
		return edgeActionNone, nil
	}
	switch frame.Type {
	case edgeFrameOpenAck:
		return edgeActionNone, conn.onOpenAck()
	case edgeFrameData:
		return edgeActionNone, conn.onRemoteData()
	case edgeFrameShutdown:
		action := conn.onRemoteShutdown()
		if action == edgeActionClose {
			s.dropReadyLocked(frame.ConnID)
			s.dropFinLocked(frame.ConnID)
			s.budget.release(conn.inflightBytes())
			conn.localClose()
			s.releaseLocked(frame.ConnID)
		}
		return action, nil
	case edgeFrameClose:
		action, discarded := conn.onRemoteClose()
		s.budget.release(discarded)
		s.dropReadyLocked(frame.ConnID)
		s.dropFinLocked(frame.ConnID)
		s.releaseLocked(frame.ConnID)
		s.maybeFlowControlLocked()
		return action, nil
	case edgeFramePause:
		conn.setPeerPaused(true)
		return edgeActionNone, nil
	case edgeFrameResume:
		conn.setPeerPaused(false)
		return edgeActionNone, nil
	default:
		return edgeActionNone, fmt.Errorf("edge: unexpected remote %s", frame.Type)
	}
}

func (s *edgeScheduler) NextFrame() (edgeFrame, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkStallLocked()
	if s.stopped || s.stalled {
		return edgeFrame{}, false
	}
	if len(s.control) > 0 {
		frame := s.control[0]
		s.control = s.control[1:]
		return frame, true
	}
	if frame, ok := s.popFINLocked(); ok {
		return frame, true
	}
	return s.popDataLocked()
}

func (s *edgeScheduler) popFINLocked() (edgeFrame, bool) {
	for len(s.finQ) > 0 {
		id := s.finQ[0]
		s.finQ = s.finQ[1:]
		conn, status := s.ids.Lookup(id)
		if status != edgeIDLive {
			continue
		}
		released := conn.markShutdownSent()
		if released {
			s.dropReadyLocked(id)
			s.releaseLocked(id)
		}
		return edgeFrame{Type: edgeFrameShutdown, ConnID: id}, true
	}
	return edgeFrame{}, false
}

func (s *edgeScheduler) popDataLocked() (edgeFrame, bool) {
	for {
		if s.quantumLeft <= 0 || s.current == edgeControlConnID {
			id, ok := s.popReadyLocked()
			if !ok {
				s.current = 0
				s.quantumLeft = 0
				return edgeFrame{}, false
			}
			s.current = id
			s.quantumLeft = s.quantum
		}
		conn, status := s.ids.Lookup(s.current)
		if status != edgeIDLive {
			s.quantumLeft = 0
			s.current = 0
			continue
		}
		payload, n, more, finReady, ok := conn.popData()
		if !ok {
			s.quantumLeft = 0
			s.current = 0
			continue
		}
		s.budget.release(n)
		s.quantumLeft--
		if finReady {
			s.finQ = append(s.finQ, s.current)
		}
		if more {
			if s.quantumLeft <= 0 {
				s.enqueueReadyLocked(s.current)
				s.current = 0
			}
		} else {
			s.quantumLeft = 0
			s.current = 0
		}
		s.maybeFlowControlLocked()
		s.kick(s.space)
		return edgeFrame{Type: edgeFrameData, ConnID: conn.ID(), Payload: payload}, true
	}
}

func (s *edgeScheduler) enqueueReadyLocked(id uint32) {
	if id == s.current && s.quantumLeft > 0 {
		return
	}
	conn, status := s.ids.Lookup(id)
	if status != edgeIDLive {
		return
	}
	if !conn.markQueued() {
		return
	}
	s.ready = append(s.ready, id)
}

func (s *edgeScheduler) popReadyLocked() (uint32, bool) {
	if len(s.ready) == 0 {
		return 0, false
	}
	id := s.ready[0]
	s.ready = s.ready[1:]
	if conn, status := s.ids.Lookup(id); status == edgeIDLive {
		conn.clearQueued()
	}
	return id, true
}

func (s *edgeScheduler) dropReadyLocked(id uint32) {
	filtered := s.ready[:0]
	for _, pending := range s.ready {
		if pending == id {
			continue
		}
		filtered = append(filtered, pending)
	}
	s.ready = filtered
	if conn, status := s.ids.Lookup(id); status == edgeIDLive {
		conn.clearQueued()
	}
	if s.current == id {
		s.current = 0
		s.quantumLeft = 0
	}
}

func (s *edgeScheduler) dropFinLocked(id uint32) {
	filtered := s.finQ[:0]
	for _, pending := range s.finQ {
		if pending == id {
			continue
		}
		filtered = append(filtered, pending)
	}
	s.finQ = filtered
}

func (s *edgeScheduler) releaseLocked(id uint32) {
	delete(s.pauseSent, id)
	if s.current == id {
		s.current = 0
		s.quantumLeft = 0
	}
	s.ids.Release(id)
}

func (s *edgeScheduler) maybeFlowControlLocked() {
	s.checkStallLocked()
	if s.stalled {
		return
	}
	ratio := s.budget.ratio()
	if ratio >= edgeBudgetPauseRatio {
		s.pauseHeaviestLocked()
	}
	if ratio < edgeBudgetResumeRatio {
		for id, paused := range s.pauseSent {
			if !paused {
				continue
			}
			s.control = append(s.control, edgeFrame{Type: edgeFrameResume, ConnID: id})
			s.pauseSent[id] = false
			s.kick(s.notify)
		}
	}
}

func (s *edgeScheduler) pauseHeaviestLocked() {
	var (
		best  *edgeConn
		bestN int64
	)
	for _, conn := range s.ids.Live() {
		if s.pauseSent[conn.ID()] {
			continue
		}
		n := conn.inflightBytes()
		if n <= 0 {
			continue
		}
		if best == nil || n > bestN {
			best = conn
			bestN = n
		}
	}
	if best == nil {
		return
	}
	s.pauseSent[best.ID()] = true
	s.control = append(s.control, edgeFrame{Type: edgeFramePause, ConnID: best.ID()})
	s.kick(s.notify)
}

func (s *edgeScheduler) checkStallLocked() {
	if s.stalled {
		return
	}
	if !s.budget.full() {
		s.fullSince = time.Time{}
		return
	}
	now := s.clock.Now()
	if s.fullSince.IsZero() {
		s.fullSince = now
		return
	}
	if now.Sub(s.fullSince) >= s.stallAfter {
		s.stalled = true
		s.kick(s.notify)
		s.kick(s.space)
	}
}

func (s *edgeScheduler) ExpireOpening(now time.Time) int {
	timedOut := s.ids.OpeningTimedOut(now)
	expired := 0
	for _, conn := range timedOut {
		if err := s.LocalClose(conn.ID(), edgeCloseBackendTimeout, "opening timed out"); err == nil {
			expired++
		}
	}
	return expired
}
