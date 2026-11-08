package server

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTunnelBodyNeverBlocksTheCaller(t *testing.T) {
	cases := []struct {
		name     string
		limit    int
		frames   []string
		eof      bool
		readAll  bool // handler drains the body
		wantRead string
		wantErr  error // first Push error, nil when every frame is accepted
	}{
		{"frames then EOF reach the reader in order", 1 << 20, []string{"ab", "cd", "ef"}, true, true, "abcdef", nil},
		{"empty body with EOF", 1 << 20, nil, true, true, "", nil},
		{"stalled reader within the limit still accepts", 8, []string{"abcd", "efgh"}, false, false, "", nil},
		{"stalled reader past the limit aborts the request", 8, []string{"abcd", "efgh", "i"}, false, false, "", errTunnelBodyOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := newTunnelBodyWithLimit(tc.limit)
			defer body.Abort(nil)
			var pushErr error
			done := make(chan struct{})
			go func() {
				defer close(done)
				for _, frame := range tc.frames {
					if err := body.Push([]byte(frame)); err != nil {
						pushErr = err
						return
					}
				}
				if tc.eof {
					body.Finish()
				}
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("Push blocked the caller")
			}
			if !errors.Is(pushErr, tc.wantErr) {
				t.Fatalf("push error = %v, want %v", pushErr, tc.wantErr)
			}
			if !tc.readAll {
				return
			}
			got, err := io.ReadAll(body.Reader())
			if err != nil || string(got) != tc.wantRead {
				t.Fatalf("read = %q, %v; want %q", got, err, tc.wantRead)
			}
		})
	}
}

func TestTunnelBodyAbortUnblocksReaderAndRejectsFrames(t *testing.T) {
	cases := []struct {
		name  string
		abort func(*tunnelBody)
	}{
		{"cancel aborts", func(b *tunnelBody) { b.Abort(nil) }},
		{"abort after finish is safe", func(b *tunnelBody) { b.Finish(); b.Abort(nil) }},
		{"double abort is safe", func(b *tunnelBody) { b.Abort(nil); b.Abort(errTunnelBodyOverflow) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := newTunnelBodyWithLimit(1 << 20)
			readDone := make(chan error, 1)
			go func() {
				_, err := io.ReadAll(body.Reader())
				readDone <- err
			}()
			tc.abort(body)
			select {
			case <-readDone:
			case <-time.After(2 * time.Second):
				t.Fatal("reader stayed blocked after abort")
			}
			if err := body.Push([]byte("late")); !errors.Is(err, errTunnelBodyClosed) {
				t.Fatalf("push after abort = %v, want errTunnelBodyClosed", err)
			}
		})
	}
}

func TestTunnelBodyClosedReaderAbortsInsteadOfQueueing(t *testing.T) {
	body := newTunnelBodyWithLimit(1 << 20)
	_ = body.Reader().Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := body.Push([]byte("x")); err != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("frames for a closed reader were queued forever")
}

// A Git handler that never reads its request body must not stop the Agent read
// loop: later requests on the same connection are still answered. Before the
// fix, the read loop blocked on the pipe write, Gateway pings went unanswered,
// and Gateway dropped the Agent after its read timeout.
func TestAgentReadLoopKeepsServingWhileGitBodyIsStalled(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // never reads r.Body
	})
	conn, messages := gitCancellationTestConnection(t, handler)
	gitCancellationTestRequest(t, conn, 7)

	chunk := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 256<<10)))
	sent := make(chan error, 1)
	go func() {
		for i := 0; i < 64; i++ { // 16 MiB, far beyond the pipe's zero buffer
			if err := conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "method": "git/body",
				"params": map[string]interface{}{"id": 7, "data_b64": chunk},
			}); err != nil {
				sent <- err
				return
			}
		}
		sent <- nil
	}()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatalf("send body frames: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Agent stopped reading while the Git handler stalled")
	}

	// The same connection still answers another request.
	gitCancellationTestSend(t, conn, 8, "git/cancel", map[string]interface{}{"id": 99})
	deadline := time.After(5 * time.Second)
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				t.Fatal("connection closed while the Git handler stalled")
			}
			if id, _ := message["id"].(float64); id == 8 {
				return
			}
		case <-deadline:
			t.Fatal("Agent did not answer a later request while a Git body stalled")
		}
	}
}
