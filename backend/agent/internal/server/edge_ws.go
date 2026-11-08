package server

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type edgeWSSession struct {
	conn           *websocket.Conn
	writeMu        sync.Mutex
	dataWriteState websocketDataWriteState
	sched          *edgeScheduler
	closed         chan struct{}
	closeOnce      sync.Once
}

func newEdgeWSSession(conn *websocket.Conn) *edgeWSSession {
	conn.SetReadLimit(maxWebSocketMessageBytes())
	session := &edgeWSSession{
		conn:   conn,
		sched:  newEdgeScheduler(edgeSchedulerOptions{}),
		closed: make(chan struct{}),
	}
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(agentWSReadTimeout))
		return nil
	})
	return session
}

func (s *edgeWSSession) close() {
	s.closeOnce.Do(func() {
		s.sched.Stop()
		_ = s.conn.Close()
		close(s.closed)
	})
}

func (s *edgeWSSession) writer(ctx context.Context) {
	for {
		frame, ok := s.sched.WaitFrame(ctx)
		if !ok {
			return
		}
		encoded, err := frame.encode()
		if err != nil {
			s.close()
			return
		}
		if err := writeWebSocketBinary(s.conn, &s.writeMu, &s.dataWriteState, encoded, agentWSDataWriteTimeout); err != nil {
			s.close()
			return
		}
	}
}

func (s *edgeWSSession) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(agentWSPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closed:
			return
		case <-ticker.C:
			if !writeHeartbeatPing(s.conn, &s.dataWriteState, []byte("edge"), agentWSPingWriteTimeout) {
				s.close()
				return
			}
		}
	}
}

func (s *edgeWSSession) readLoop(onFrame func(edgeFrame) error) {
	_ = s.conn.SetReadDeadline(time.Now().Add(agentWSReadTimeout))
	for {
		messageType, payload, err := readWebSocketMessage(s.conn, func() {
			_ = s.conn.SetReadDeadline(time.Now().Add(agentWSReadTimeout))
		})
		if err != nil {
			s.close()
			return
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		frame, err := decodeEdgeFrame(payload)
		if err != nil {
			s.close()
			return
		}
		if err := onFrame(frame); err != nil {
			s.close()
			return
		}
	}
}
