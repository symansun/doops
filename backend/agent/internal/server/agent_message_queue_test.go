package server

import (
	"errors"
	"testing"
)

func TestGatewayMessageQueueEnqueueDoesNotBlockAndPreservesOrder(t *testing.T) {
	budget := newGatewayMessageBudget(2 << 20)
	queue := newGatewayMessageQueue(budget, 1<<20, 1, "")
	defer queue.close()

	for i := 0; i < 512; i++ {
		if err := queue.enqueue(gatewayWSMessage{
			Parsed: map[string]interface{}{"sequence": float64(i)},
		}); err != nil {
			t.Fatalf("enqueue message %d: %v", i, err)
		}
	}
	for i := 0; i < 512; i++ {
		msg, ok, err := queue.dequeue()
		if err != nil {
			t.Fatalf("dequeue message %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("message %d was not delivered", i)
		}
		if sequence, _ := msg.Parsed["sequence"].(float64); sequence != float64(i) {
			t.Fatalf("message %d arrived out of order: %#v", i, msg.Parsed)
		}
	}
}

func TestGatewayMessageQueueLimitFailsRequestAndReleasesBudget(t *testing.T) {
	budget := newGatewayMessageBudget(4 << 10)
	queue := newGatewayMessageQueue(budget, 1500, 1, "")
	defer queue.close()

	first := gatewayWSMessage{Raw: []byte("12345678")}
	if err := queue.enqueue(first); err != nil {
		t.Fatalf("enqueue first message: %v", err)
	}
	if err := queue.enqueue(gatewayWSMessage{Raw: []byte("9")}); !errors.Is(err, errGatewayMessageQueueFull) {
		t.Fatalf("expected queue byte limit error, got %v", err)
	}
	if _, ok, err := queue.dequeue(); !errors.Is(err, errGatewayMessageQueueFull) || ok {
		t.Fatalf("expected failed request state, got ok=%v err=%v", ok, err)
	}
	budget.mu.Lock()
	used := budget.used
	budget.mu.Unlock()
	if used != 0 {
		t.Fatalf("failed queue retained %d budget bytes", used)
	}
}

func TestGatewayMessageQueueUsesSharedBudgetAcrossRequests(t *testing.T) {
	budget := newGatewayMessageBudget(3000)
	first := newGatewayMessageQueue(budget, 3000, 1, "")
	second := newGatewayMessageQueue(budget, 3000, 2, "")
	defer first.close()
	defer second.close()

	message := gatewayWSMessage{Raw: make([]byte, 400)}
	if err := first.enqueue(message); err != nil {
		t.Fatalf("enqueue first request: %v", err)
	}
	if err := second.enqueue(message); !errors.Is(err, errGatewayMessageQueueFull) {
		t.Fatalf("expected shared budget error, got %v", err)
	}
	if _, ok, err := first.dequeue(); err != nil || !ok {
		t.Fatalf("first request should remain readable, ok=%v err=%v", ok, err)
	}
}
