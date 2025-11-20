package hooks

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/core/events"
)

// TestWithEventDedup tests that the dedup window can be configured.
func TestWithEventDedup(t *testing.T) {
	t.Parallel()
	hook := newCountingHook(2)
	// Configure dedup window size
	exec := NewExecutor(WithEventDedup(10))
	defer exec.Close()
	exec.Register(hook)

	// Send 3 duplicate events with same ID
	evt1 := events.Event{Type: events.Notification, ID: "dedup-test", Payload: events.NotificationPayload{Message: "msg1"}}
	evt2 := events.Event{Type: events.Notification, ID: "dedup-test", Payload: events.NotificationPayload{Message: "msg2"}}
	// Different ID should go through
	evt3 := events.Event{Type: events.Notification, ID: "different", Payload: events.NotificationPayload{Message: "msg3"}}

	if err := exec.Publish(evt1); err != nil {
		t.Fatalf("publish evt1: %v", err)
	}
	if err := exec.Publish(evt2); err != nil {
		t.Fatalf("publish evt2: %v", err)
	}
	if err := exec.Publish(evt3); err != nil {
		t.Fatalf("publish evt3: %v", err)
	}

	hook.wait(t, time.Second)
	// Only 2 should be received (evt1 dedups evt2, evt3 is unique)
	if got := hook.count(events.Notification); got != 2 {
		t.Fatalf("expected 2 notifications after dedup, got %d", got)
	}
}

// TestInvalidPayloadTypes tests error handling for invalid payload types.
func TestInvalidPayloadTypes(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		eventType events.EventType
		payload   interface{}
	}{
		{"PreToolUse_InvalidPayload", events.PreToolUse, "wrong-type"},
		{"PostToolUse_InvalidPayload", events.PostToolUse, 123},
		{"UserPromptSubmit_InvalidPayload", events.UserPromptSubmit, true},
		{"SessionStart_InvalidPayload", events.SessionStart, []string{"wrong"}},
		{"Stop_InvalidPayload", events.Stop, map[string]int{"bad": 1}},
		{"SubagentStop_InvalidPayload", events.SubagentStop, struct{}{}},
		{"Notification_InvalidPayload", events.Notification, 3.14},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var errType events.EventType
			var errMsg string
			var mu sync.Mutex

			exec := NewExecutor(WithErrorHandler(func(t events.EventType, err error) {
				mu.Lock()
				defer mu.Unlock()
				errType = t
				if err != nil {
					errMsg = err.Error()
				}
			}))
			defer exec.Close()

			// Register a hook (even though it won't be called)
			exec.Register(newCountingHook(0))

			if err := exec.Publish(events.Event{Type: tc.eventType, Payload: tc.payload}); err != nil {
				t.Fatalf("publish: %v", err)
			}

			// Wait a bit for async processing
			time.Sleep(50 * time.Millisecond)

			mu.Lock()
			defer mu.Unlock()
			if errType != tc.eventType {
				t.Fatalf("expected error for %s, got %s", tc.eventType, errType)
			}
			if errMsg == "" || !strings.Contains(errMsg, "invalid payload") {
				t.Fatalf("expected invalid payload error, got: %q", errMsg)
			}
		})
	}
}

// TestInvalidPayloadPreToolUse specifically tests PreToolUse with wrong payload.
func TestInvalidPayloadPreToolUse(t *testing.T) {
	t.Parallel()
	errCh := make(chan error, 1)
	exec := NewExecutor(WithErrorHandler(func(t events.EventType, err error) {
		if t == events.PreToolUse && err != nil {
			errCh <- err
		}
	}))
	defer exec.Close()
	exec.Register(newCountingHook(0))

	if err := exec.Publish(events.Event{
		Type:    events.PreToolUse,
		Payload: "not-a-ToolUsePayload",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "invalid payload") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected payload error for PreToolUse")
	}
}

// TestInvalidPayloadPostToolUse specifically tests PostToolUse with wrong payload.
func TestInvalidPayloadPostToolUse(t *testing.T) {
	t.Parallel()
	errCh := make(chan error, 1)
	exec := NewExecutor(WithErrorHandler(func(t events.EventType, err error) {
		if t == events.PostToolUse && err != nil {
			errCh <- err
		}
	}))
	defer exec.Close()
	exec.Register(newCountingHook(0))

	if err := exec.Publish(events.Event{
		Type:    events.PostToolUse,
		Payload: 42,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "invalid payload") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected payload error for PostToolUse")
	}
}

// TestInvalidPayloadUserPrompt specifically tests UserPromptSubmit with wrong payload.
func TestInvalidPayloadUserPrompt(t *testing.T) {
	t.Parallel()
	errCh := make(chan error, 1)
	exec := NewExecutor(WithErrorHandler(func(t events.EventType, err error) {
		if t == events.UserPromptSubmit && err != nil {
			errCh <- err
		}
	}))
	defer exec.Close()
	exec.Register(newCountingHook(0))

	if err := exec.Publish(events.Event{
		Type:    events.UserPromptSubmit,
		Payload: []byte("bytes"),
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "invalid payload") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected payload error for UserPromptSubmit")
	}
}

// TestInvalidPayloadSessionStart specifically tests SessionStart with wrong payload.
func TestInvalidPayloadSessionStart(t *testing.T) {
	t.Parallel()
	errCh := make(chan error, 1)
	exec := NewExecutor(WithErrorHandler(func(t events.EventType, err error) {
		if t == events.SessionStart && err != nil {
			errCh <- err
		}
	}))
	defer exec.Close()
	exec.Register(newCountingHook(0))

	if err := exec.Publish(events.Event{
		Type:    events.SessionStart,
		Payload: map[string]string{"bad": "payload"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "invalid payload") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected payload error for SessionStart")
	}
}

// TestInvalidPayloadStop specifically tests Stop with wrong payload.
func TestInvalidPayloadStop(t *testing.T) {
	t.Parallel()
	errCh := make(chan error, 1)
	exec := NewExecutor(WithErrorHandler(func(t events.EventType, err error) {
		if t == events.Stop && err != nil {
			errCh <- err
		}
	}))
	defer exec.Close()
	exec.Register(newCountingHook(0))

	if err := exec.Publish(events.Event{
		Type:    events.Stop,
		Payload: 3.14159,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "invalid payload") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected payload error for Stop")
	}
}

// TestInvalidPayloadSubagentStop specifically tests SubagentStop with wrong payload.
func TestInvalidPayloadSubagentStop(t *testing.T) {
	t.Parallel()
	errCh := make(chan error, 1)
	exec := NewExecutor(WithErrorHandler(func(t events.EventType, err error) {
		if t == events.SubagentStop && err != nil {
			errCh <- err
		}
	}))
	defer exec.Close()
	exec.Register(newCountingHook(0))

	if err := exec.Publish(events.Event{
		Type:    events.SubagentStop,
		Payload: &struct{ Field int }{Field: 42},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "invalid payload") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected payload error for SubagentStop")
	}
}

// TestInvalidPayloadNotification specifically tests Notification with wrong payload.
func TestInvalidPayloadNotification(t *testing.T) {
	t.Parallel()
	errCh := make(chan error, 1)
	exec := NewExecutor(WithErrorHandler(func(t events.EventType, err error) {
		if t == events.Notification && err != nil {
			errCh <- err
		}
	}))
	defer exec.Close()
	exec.Register(newCountingHook(0))

	if err := exec.Publish(events.Event{
		Type:    events.Notification,
		Payload: func() {},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "invalid payload") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected payload error for Notification")
	}
}

// TestCloseNilExecutor tests that Close is safe on nil executor.
func TestCloseNilExecutor(t *testing.T) {
	var exec *Executor
	// Should not panic
	exec.Close()
}

// TestCloseWithNilBus tests Close when bus is nil.
func TestCloseWithNilBus(t *testing.T) {
	exec := &Executor{} // No bus
	// Should not panic
	exec.Close()
}

// TestExecutorErrorCallbackForEachEventType verifies error callback is invoked
// for each event type when payload validation fails.
func TestExecutorErrorCallbackForEachEventType(t *testing.T) {
	t.Parallel()

	eventTypes := []events.EventType{
		events.PreToolUse,
		events.PostToolUse,
		events.UserPromptSubmit,
		events.SessionStart,
		events.Stop,
		events.SubagentStop,
		events.Notification,
	}

	for _, evtType := range eventTypes {
		evtType := evtType
		t.Run(fmt.Sprintf("ErrorCallback_%s", evtType), func(t *testing.T) {
			t.Parallel()
			errCh := make(chan events.EventType, 1)
			exec := NewExecutor(WithErrorHandler(func(t events.EventType, err error) {
				if err != nil {
					select {
					case errCh <- t:
					default:
					}
				}
			}))
			defer exec.Close()
			exec.Register(newCountingHook(0))

			// Send event with invalid payload
			if err := exec.Publish(events.Event{Type: evtType, Payload: "invalid"}); err != nil {
				t.Fatalf("publish: %v", err)
			}

			select {
			case receivedType := <-errCh:
				if receivedType != evtType {
					t.Fatalf("expected error for %s, got %s", evtType, receivedType)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatalf("expected error callback for %s", evtType)
			}
		})
	}
}
