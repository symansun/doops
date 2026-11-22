package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

var (
	errGatewayMessageQueueClosed = errors.New("gateway message queue closed")
	errGatewayMessageQueueFull   = errors.New("gateway message queue full")
)

const (
	gatewayMessageBudgetMaxBytes = 128 << 20
	gatewayMessageQueueMaxBytes  = 64 << 20
)

type gatewayWSMessage struct {
	Raw    []byte
	Parsed map[string]interface{}
}

type gatewayQueuedMessage struct {
	message gatewayWSMessage
	size    int64
}

type gatewayMessageBudget struct {
	mu       sync.Mutex
	used     int64
	maxBytes int64
}

type gatewayMessageQueue struct {
	mu              sync.Mutex
	items           []gatewayQueuedMessage
	queuedBytes     int64
	maxBytes        int64
	budget          *gatewayMessageBudget
	notify          chan struct{}
	requestID       int64
	cancelMethod    string
	cancelOnce      sync.Once
	cancelDone      chan struct{}
	terminal        chan struct{}
	terminalOnce    sync.Once
	terminalErr     error
	terminalMessage gatewayWSMessage
	closed          bool
	err             error
}

func newGatewayMessageBudget(maxBytes int64) *gatewayMessageBudget {
	return &gatewayMessageBudget{maxBytes: maxBytes}
}

func (b *gatewayMessageBudget) reserve(size int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if size <= 0 || b.used+size > b.maxBytes {
		return false
	}
	b.used += size
	return true
}

func (b *gatewayMessageBudget) release(size int64) {
	if size <= 0 {
		return
	}
	b.mu.Lock()
	b.used -= size
	b.mu.Unlock()
}

func newGatewayMessageQueue(budget *gatewayMessageBudget, maxBytes int64, requestID int64, cancelMethod string) *gatewayMessageQueue {
	if budget == nil {
		panic("gateway message budget is not initialized")
	}
	if maxBytes <= 0 {
		maxBytes = gatewayMessageQueueMaxBytes
	}
	return &gatewayMessageQueue{
		maxBytes:     maxBytes,
		budget:       budget,
		notify:       make(chan struct{}, 1),
		requestID:    requestID,
		cancelMethod: cancelMethod,
		cancelDone:   make(chan struct{}),
		terminal:     make(chan struct{}),
	}
}

func (q *gatewayMessageQueue) enqueue(message gatewayWSMessage) error {
	size := int64(len(message.Raw))*2 + 1024
	if len(message.Raw) == 0 {
		data, err := json.Marshal(message.Parsed)
		if err != nil {
			return fmt.Errorf("encode queued message: %w", err)
		}
		size = int64(len(data))*2 + 1024
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return errGatewayMessageQueueClosed
	}
	if q.queuedBytes+size > q.maxBytes {
		err := fmt.Errorf("%w: queued=%d incoming=%d limit=%d", errGatewayMessageQueueFull, q.queuedBytes, size, q.maxBytes)
		released := q.failLocked(err)
		q.mu.Unlock()
		q.budget.release(released)
		q.signal()
		return err
	}
	if !q.budget.reserve(size) {
		err := fmt.Errorf("%w: shared budget limit=%d", errGatewayMessageQueueFull, q.budget.maxBytes)
		released := q.failLocked(err)
		q.mu.Unlock()
		q.budget.release(released)
		q.signal()
		return err
	}
	q.items = append(q.items, gatewayQueuedMessage{message: message, size: size})
	q.queuedBytes += size
	q.mu.Unlock()
	q.signal()
	return nil
}

func (q *gatewayMessageQueue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *gatewayMessageQueue) dequeue() (gatewayWSMessage, bool, error) {
	q.mu.Lock()
	if q.closed {
		err := q.err
		q.mu.Unlock()
		return gatewayWSMessage{}, false, err
	}
	if len(q.items) == 0 {
		q.mu.Unlock()
		return gatewayWSMessage{}, false, nil
	}
	item := q.items[0]
	q.items[0] = gatewayQueuedMessage{}
	if len(q.items) == 1 {
		q.items = nil
	} else {
		q.items = q.items[1:]
	}
	q.queuedBytes -= item.size
	q.budget.release(item.size)
	q.mu.Unlock()
	return item.message, true, nil
}

func (q *gatewayMessageQueue) failLocked(err error) int64 {
	q.closed = true
	q.err = err
	released := q.queuedBytes
	q.queuedBytes = 0
	for i := range q.items {
		q.items[i] = gatewayQueuedMessage{}
	}
	q.items = nil
	return released
}

func (q *gatewayMessageQueue) fail(err error) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	released := q.failLocked(err)
	q.mu.Unlock()
	q.budget.release(released)
	q.signal()
}

func (q *gatewayMessageQueue) close() {
	q.fail(errGatewayMessageQueueClosed)
}
