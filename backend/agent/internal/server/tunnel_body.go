package server

import (
	"encoding/base64"
	"errors"
	"io"
	"sync"
)

// maxTunnelBodyQueuedBytes bounds request body bytes waiting for a slow
// handler. Gateway sends 256 KiB frames, so this holds 256 frames.
const maxTunnelBodyQueuedBytes = 64 << 20

var (
	errTunnelBodyOverflow = errors.New("tunnel request body consumer is too slow; request aborted")
	errTunnelBodyClosed   = errors.New("tunnel request body closed")
)

// tunnelBody hands git/body and proxy/body frames to a request handler without
// letting that handler block the WebSocket read loop. The read loop only
// queues frames; one pump goroutine writes them into the pipe the handler reads.
// A stalled handler fails its own request instead of stopping ping/pong and
// every other request on the connection.
type tunnelBody struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	frames chan []byte

	mu           sync.Mutex
	queued       int
	limit        int
	finished     bool // EOF received; no more frames accepted
	framesClosed bool
}

func newTunnelBody() *tunnelBody {
	return newTunnelBodyWithLimit(maxTunnelBodyQueuedBytes)
}

func newTunnelBodyWithLimit(limit int) *tunnelBody {
	reader, writer := io.Pipe()
	b := &tunnelBody{reader: reader, writer: writer, frames: make(chan []byte, 1024), limit: limit}
	go b.pump()
	return b
}

func (b *tunnelBody) Reader() io.ReadCloser { return b.reader }

func (b *tunnelBody) pump() {
	for data := range b.frames {
		_, err := b.writer.Write(data)
		b.mu.Lock()
		b.queued -= len(data)
		b.mu.Unlock()
		if err != nil {
			// The pipe is already closed (handler done or body aborted); stop
			// accepting frames without overwriting the error the reader sees.
			b.closeFrames()
			return
		}
	}
	_ = b.writer.Close()
}

func (b *tunnelBody) closeFrames() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finished = true
	if !b.framesClosed {
		b.framesClosed = true
		close(b.frames)
	}
}

// Push queues one frame without blocking. It fails, and aborts the body, when
// the handler has fallen too far behind.
func (b *tunnelBody) Push(data []byte) error {
	b.mu.Lock()
	if b.finished || b.framesClosed {
		b.mu.Unlock()
		return errTunnelBodyClosed
	}
	if b.queued+len(data) > b.limit {
		b.mu.Unlock()
		b.Abort(errTunnelBodyOverflow)
		return errTunnelBodyOverflow
	}
	select {
	case b.frames <- data:
		b.queued += len(data)
		b.mu.Unlock()
		return nil
	default:
		b.mu.Unlock()
		b.Abort(errTunnelBodyOverflow)
		return errTunnelBodyOverflow
	}
}

// Finish marks EOF: queued frames are still delivered, then the reader sees io.EOF.
func (b *tunnelBody) Finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished || b.framesClosed {
		return
	}
	b.finished = true
	b.framesClosed = true
	close(b.frames)
}

// Abort discards queued frames and unblocks both the pump and the handler.
// A nil err ends the body cleanly (the handler reads io.EOF), matching a
// cancelled or finished request; a non-nil err is what the handler reads.
// Closing the writer also releases a pump blocked in Write.
func (b *tunnelBody) Abort(err error) {
	b.closeFrames()
	_ = b.writer.CloseWithError(err)
}

// pushTunnelBodyFrame decodes one body frame and queues it. It reports false
// when the request must be torn down (bad frame or overflow).
func pushTunnelBodyFrame(body *tunnelBody, dataB64 string, eof bool) bool {
	if dataB64 != "" {
		data, err := base64.StdEncoding.DecodeString(dataB64)
		if err != nil {
			body.Abort(err)
			return false
		}
		if err := body.Push(data); err != nil {
			return false
		}
	}
	if eof {
		body.Finish()
	}
	return true
}
