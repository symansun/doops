package server

import (
	"bytes"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type temporaryHeartbeatError struct{}
type fatalHeartbeatTimeoutError struct{}

func (temporaryHeartbeatError) Error() string   { return "temporary heartbeat write timeout" }
func (temporaryHeartbeatError) Timeout() bool   { return true }
func (temporaryHeartbeatError) Temporary() bool { return true }

func (fatalHeartbeatTimeoutError) Error() string   { return "fatal heartbeat network write timeout" }
func (fatalHeartbeatTimeoutError) Timeout() bool   { return true }
func (fatalHeartbeatTimeoutError) Temporary() bool { return false }

type fakeWebSocketControlWriter struct {
	writeErr error
	closes   int
	onWrite  func()
}

func (f *fakeWebSocketControlWriter) WriteControl(int, []byte, time.Time) error {
	if f.onWrite != nil {
		f.onWrite()
	}
	return f.writeErr
}

func (f *fakeWebSocketControlWriter) Close() error {
	f.closes++
	return nil
}

func TestWriteHeartbeatPingClosesConnectionAfterUnexplainedWriteFailure(t *testing.T) {
	tests := []struct {
		name             string
		writeErr         error
		dataWriteActive  bool
		dataWriteOverlap bool
		wantKeep         bool
		wantCloses       int
	}{
		{
			name:       "temporary write timeout",
			writeErr:   temporaryHeartbeatError{},
			wantKeep:   false,
			wantCloses: 1,
		},
		{
			name:            "temporary write timeout during data write",
			writeErr:        temporaryHeartbeatError{},
			dataWriteActive: true,
			wantKeep:        true,
			wantCloses:      0,
		},
		{
			name:             "temporary write timeout overlapping completed data write",
			writeErr:         temporaryHeartbeatError{},
			dataWriteOverlap: true,
			wantKeep:         true,
			wantCloses:       0,
		},
		{
			name:             "fatal network timeout overlapping completed data write",
			writeErr:         fatalHeartbeatTimeoutError{},
			dataWriteOverlap: true,
			wantKeep:         false,
			wantCloses:       1,
		},
		{
			name:       "permanent write failure",
			writeErr:   fmt.Errorf("connection closed"),
			wantKeep:   false,
			wantCloses: 1,
		},
		{
			name:       "successful heartbeat",
			wantKeep:   true,
			wantCloses: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dataWriteState websocketDataWriteState
			if tt.dataWriteActive {
				dataWriteState.begin()
				defer dataWriteState.end()
			}
			conn := &fakeWebSocketControlWriter{writeErr: tt.writeErr}
			if tt.dataWriteOverlap {
				conn.onWrite = func() {
					dataWriteState.begin()
					dataWriteState.end()
				}
			}
			if got := writeHeartbeatPing(conn, &dataWriteState, nil, time.Second); got != tt.wantKeep {
				t.Fatalf("keep connection = %t, want %t", got, tt.wantKeep)
			}
			if conn.closes != tt.wantCloses {
				t.Fatalf("close calls = %d, want %d", conn.closes, tt.wantCloses)
			}
		})
	}
}

type fakeWebSocketDataWriter struct {
	deadlines []time.Time
	writeErr  error
	closes    int
}

func (f *fakeWebSocketDataWriter) SetWriteDeadline(deadline time.Time) error {
	f.deadlines = append(f.deadlines, deadline)
	return nil
}

func (f *fakeWebSocketDataWriter) NextWriter(int) (io.WriteCloser, error) {
	return fakeMessageWriter{f}, nil
}

type fakeMessageWriter struct{ f *fakeWebSocketDataWriter }

func (w fakeMessageWriter) Write(p []byte) (int, error) {
	if w.f.writeErr != nil {
		return 0, w.f.writeErr
	}
	return len(p), nil
}

func (w fakeMessageWriter) Close() error { return nil }

func (f *fakeWebSocketDataWriter) Close() error {
	f.closes++
	return nil
}

func TestWriteWebSocketJSONBoundsBlockedWrites(t *testing.T) {
	t.Run("successful write clears deadline", func(t *testing.T) {
		conn := &fakeWebSocketDataWriter{}
		var dataWriteState websocketDataWriteState
		if err := writeWebSocketJSON(conn, &sync.Mutex{}, &dataWriteState, map[string]string{"ok": "true"}, time.Second); err != nil {
			t.Fatalf("write JSON: %v", err)
		}
		last := len(conn.deadlines) - 1
		if last < 1 || conn.deadlines[0].IsZero() || !conn.deadlines[last].IsZero() {
			t.Fatalf("write deadlines = %v, want finite deadlines followed by zero", conn.deadlines)
		}
		if conn.closes != 0 {
			t.Fatalf("close calls = %d, want 0", conn.closes)
		}
		if dataWriteState.sequence.Load()%2 != 0 {
			t.Fatal("data write state remained active after success")
		}
	})

	t.Run("write failure closes connection", func(t *testing.T) {
		conn := &fakeWebSocketDataWriter{writeErr: fmt.Errorf("write timeout")}
		var dataWriteState websocketDataWriteState
		if err := writeWebSocketJSON(conn, &sync.Mutex{}, &dataWriteState, map[string]string{"ok": "false"}, time.Second); err == nil {
			t.Fatal("write JSON succeeded, want failure")
		}
		if len(conn.deadlines) != 1 || conn.deadlines[0].IsZero() {
			t.Fatalf("write deadlines = %v, want one finite deadline", conn.deadlines)
		}
		if conn.closes != 1 {
			t.Fatalf("close calls = %d, want 1", conn.closes)
		}
		if dataWriteState.sequence.Load()%2 != 0 {
			t.Fatal("data write state remained active after failure")
		}
	})
}

func TestReadWebSocketMessageReportsProgressBeforeMessageCompletes(t *testing.T) {
	serverConn := make(chan *websocket.Conn, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConn <- conn
	}))
	defer ts.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(ts.URL, "http"),
		nil,
	)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer clientConn.Close()
	receiver := <-serverConn
	defer receiver.Close()

	type readResult struct {
		messageType int
		body        []byte
		err         error
	}
	progress := make(chan struct{}, 1)
	result := make(chan readResult, 1)
	go func() {
		messageType, body, err := readWebSocketMessage(receiver, func() {
			select {
			case progress <- struct{}{}:
			default:
			}
		})
		result <- readResult{messageType: messageType, body: body, err: err}
	}()

	writer, err := clientConn.NextWriter(websocket.TextMessage)
	if err != nil {
		t.Fatalf("open message writer: %v", err)
	}
	body := bytes.Repeat([]byte("x"), 64<<10)
	if _, err := writer.Write(body); err != nil {
		t.Fatalf("write partial message: %v", err)
	}

	select {
	case <-progress:
	case <-time.After(time.Second):
		t.Fatal("reader did not report progress before the message completed")
	}
	select {
	case <-result:
		t.Fatal("reader completed before the message writer closed")
	default:
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close message writer: %v", err)
	}
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("read message: %v", got.err)
		}
		if got.messageType != websocket.TextMessage {
			t.Fatalf("message type = %d, want text", got.messageType)
		}
		if !bytes.Equal(got.body, body) {
			t.Fatalf("message body has %d bytes, want %d", len(got.body), len(body))
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not complete after the message writer closed")
	}
}

// A message that takes longer than one write timeout on a slow link must still
// be delivered as long as every chunk makes progress within the timeout.
func TestWriteWebSocketMessageSurvivesSlowButProgressingLink(t *testing.T) {
	serverConn := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConn <- conn
	}))
	defer server.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	peer := <-serverConn
	defer peer.Close()

	payload := strings.Repeat("x", 6*websocketWriteChunkBytes)
	received := make(chan int, 1)
	go func() {
		_, reader, err := peer.NextReader()
		if err != nil {
			received <- -1
			return
		}
		total := 0
		buf := make([]byte, 4<<10)
		for {
			n, err := reader.Read(buf)
			total += n
			time.Sleep(25 * time.Millisecond) // ~160 KiB/s reader
			if err != nil {
				break
			}
		}
		received <- total
	}()
	var state websocketDataWriteState
	// The whole message takes well over 200ms; each chunk finishes in time.
	if err := writeWebSocketMessage(client, &sync.Mutex{}, &state, websocket.TextMessage, []byte(payload), 200*time.Millisecond); err != nil {
		t.Fatalf("slow but progressing write failed: %v", err)
	}
	if got := <-received; got != len(payload) {
		t.Fatalf("received %d bytes, want %d", got, len(payload))
	}
}
