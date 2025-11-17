package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/approval"
	"github.com/cexll/agentsdk-go/pkg/event"
)

func TestApprovalMiddlewareAutoApprovedViaWhitelist(t *testing.T) {
	t.Parallel()
	wl := approval.NewWhitelist()
	wl.Add("s1", "echo", map[string]any{"k": "v"}, time.Now())
	q := approval.NewQueue(approval.NewMemoryStore(), wl)
	mw := NewApprovalMiddleware(q)

	execCtx := NewExecutionContext(context.Background(), map[string]any{
		defaultApprovalSessionKey:  "s1",
		defaultApprovalRequestsKey: ApprovalRequest{Tool: "echo", Params: map[string]any{"k": "v"}},
	}, nil)

	if err := mw.BeforeStepContext(execCtx, Step{Name: "tool"}); err != nil {
		t.Fatalf("before: %v", err)
	}
	raw, ok := execCtx.Get(defaultApprovalResultsKey)
	if !ok {
		t.Fatalf("results missing")
	}
	results, ok := raw.([]approval.Record)
	if !ok || len(results) != 1 {
		t.Fatalf("unexpected results: %#v", raw)
	}
	if !results[0].Auto || results[0].Decision != approval.DecisionApproved {
		t.Fatalf("expected auto approved record: %+v", results[0])
	}
}

func TestApprovalMiddlewarePendingThenApprovedEmitsEvents(t *testing.T) {
	t.Parallel()
	progress := make(chan event.Event, 2)
	control := make(chan event.Event, 4)
	monitor := make(chan event.Event, 1)
	bus := event.NewEventBus(progress, control, monitor)

	q := approval.NewQueue(approval.NewMemoryStore(), approval.NewWhitelist())
	mw := NewApprovalMiddleware(
		q,
		WithApprovalPollInterval(5*time.Millisecond),
		WithApprovalEventBus(bus),
	)

	execCtx := NewExecutionContext(context.Background(), map[string]any{
		defaultApprovalSessionKey: "s-req",
		defaultApprovalRequestsKey: []ApprovalRequest{{
			Tool:   "grep",
			Params: map[string]any{"pattern": "foo"},
			Reason: "manual review",
		}},
	}, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- mw.BeforeStepContext(execCtx, Step{Name: "grep-step"})
	}()

	time.Sleep(10 * time.Millisecond)
	pending := q.Pending("s-req")
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
	if _, err := q.Approve(pending[0].ID, "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("before returned error: %v", err)
	}
	raw, ok := execCtx.Get(defaultApprovalResultsKey)
	if !ok {
		t.Fatalf("results missing")
	}
	results := raw.([]approval.Record)
	if len(results) != 1 || results[0].Decision != approval.DecisionApproved {
		t.Fatalf("unexpected results %+v", results)
	}

	gotRequest := false
	gotDecided := false
	deadline := time.After(50 * time.Millisecond)
	for !gotRequest || !gotDecided {
		select {
		case evt := <-control:
			switch evt.Type {
			case event.EventApprovalRequested:
				req, ok := evt.Data.(event.ApprovalRequest)
				if !ok {
					t.Fatalf("unexpected request data type %T", evt.Data)
				}
				if req.Reason != "manual review" {
					t.Fatalf("expected provided reason, got %q", req.Reason)
				}
				gotRequest = true
			case event.EventApprovalDecided:
				gotDecided = true
			}
		case <-deadline:
			t.Fatalf("events missing requested=%v decided=%v", gotRequest, gotDecided)
		}
	}
	if !gotRequest || !gotDecided {
		t.Fatalf("expected both events, got requested=%v decided=%v", gotRequest, gotDecided)
	}
	// ensure no stray progress emission
	if len(progress) != 0 {
		t.Fatalf("unexpected progress events")
	}
	// monitor channel stays empty
	if len(monitor) != 0 {
		t.Fatalf("unexpected monitor events")
	}
}

func TestApprovalMiddlewareRejectsAndSurfacesError(t *testing.T) {
	t.Parallel()
	q := approval.NewQueue(approval.NewMemoryStore(), approval.NewWhitelist())
	mw := NewApprovalMiddleware(q, WithApprovalPollInterval(5*time.Millisecond))

	execCtx := NewExecutionContext(context.Background(), map[string]any{
		defaultApprovalSessionKey:  "s-reject",
		defaultApprovalRequestsKey: ApprovalRequest{Tool: "rm", Params: map[string]any{"path": "/tmp/data"}},
	}, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- mw.BeforeStepContext(execCtx, Step{Name: "danger"})
	}()

	time.Sleep(8 * time.Millisecond)
	pending := q.Pending("s-reject")
	if len(pending) != 1 {
		t.Fatalf("expected pending item")
	}
	if _, err := q.Reject(pending[0].ID, "denied"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected denial error, got %v", err)
	}
}

func TestApprovalMiddlewareGuards(t *testing.T) {
	t.Parallel()
	mw := NewApprovalMiddleware(approval.NewQueue(approval.NewMemoryStore(), approval.NewWhitelist()))
	if err := mw.BeforeStepContext(nil, Step{}); err == nil {
		t.Fatalf("expected nil ctx error")
	}

	badCtx := NewExecutionContext(context.Background(), map[string]any{
		defaultApprovalSessionKey:  "s",
		defaultApprovalRequestsKey: 123,
	}, nil)
	if err := mw.BeforeStepContext(badCtx, Step{}); err == nil {
		t.Fatalf("expected type error")
	}

	noSession := NewExecutionContext(context.Background(), map[string]any{
		defaultApprovalRequestsKey: ApprovalRequest{Tool: "echo"},
	}, nil)
	if err := mw.BeforeStepContext(noSession, Step{}); err == nil {
		t.Fatalf("expected session error")
	}

	noQueue := NewApprovalMiddleware(nil)
	ctx := NewExecutionContext(context.Background(), nil, nil)
	if err := noQueue.BeforeStepContext(ctx, Step{}); err == nil {
		t.Fatalf("expected nil queue error")
	}

	// cover no-op AfterStepContext
	if err := mw.AfterStepContext(ctx, Step{}, nil); err != nil {
		t.Fatalf("after should be nil, got %v", err)
	}
}

func TestApprovalMiddlewareDefaultReason(t *testing.T) {
	t.Parallel()
	q := approval.NewQueue(approval.NewMemoryStore(), approval.NewWhitelist())
	progress := make(chan event.Event, 1)
	control := make(chan event.Event, 4)
	monitor := make(chan event.Event, 1)
	bus := event.NewEventBus(progress, control, monitor)
	mw := NewApprovalMiddleware(q, WithApprovalEventBus(bus), WithApprovalPollInterval(time.Millisecond))

	execCtx := NewExecutionContext(context.Background(), map[string]any{
		defaultApprovalSessionKey:  "sess-default",
		defaultApprovalRequestsKey: ApprovalRequest{Tool: "plan", Params: nil},
	}, nil)

	errCh := make(chan error, 1)
	go func() { errCh <- mw.BeforeStepContext(execCtx, Step{}) }()

	waitUntil := time.Now().Add(20 * time.Millisecond)
	for {
		select {
		case evt := <-control:
			if evt.Type == event.EventApprovalRequested {
				req := evt.Data.(event.ApprovalRequest)
				if req.Reason != "workflow" {
					t.Fatalf("expected fallback reason, got %q", req.Reason)
				}
				pending := q.Pending("sess-default")
				if len(pending) == 0 {
					t.Fatalf("pending entry missing")
				}
				_, _ = q.Approve(pending[0].ID, "ok")
			}
		case err := <-errCh:
			if err != nil {
				t.Fatalf("before returned error: %v", err)
			}
			if len(progress) != 0 || len(monitor) != 0 {
				t.Fatalf("unexpected channels activity")
			}
			return
		case <-time.After(time.Until(waitUntil)):
			t.Fatal("timeout waiting for approval flow")
		}
	}
}

func TestApprovalMiddlewareStepReason(t *testing.T) {
	t.Parallel()
	q := approval.NewQueue(approval.NewMemoryStore(), approval.NewWhitelist())
	control := make(chan event.Event, 2)
	bus := event.NewEventBus(make(chan event.Event, 1), control, make(chan event.Event, 1))
	mw := NewApprovalMiddleware(q, WithApprovalEventBus(bus), WithApprovalPollInterval(time.Millisecond))

	execCtx := NewExecutionContext(context.Background(), map[string]any{
		defaultApprovalSessionKey:  "sess-step",
		defaultApprovalRequestsKey: ApprovalRequest{Tool: "fmt"},
	}, nil)

	errCh := make(chan error, 1)
	go func() { errCh <- mw.BeforeStepContext(execCtx, Step{Name: "format-config"}) }()

	var pendingID string
	timeout := time.After(30 * time.Millisecond)
	for {
		select {
		case evt := <-control:
			if evt.Type != event.EventApprovalRequested {
				continue
			}
			req := evt.Data.(event.ApprovalRequest)
			if req.Reason != "step format-config" {
				t.Fatalf("expected step reason, got %q", req.Reason)
			}
			pending := q.Pending("sess-step")
			if len(pending) == 0 {
				t.Fatalf("pending missing")
			}
			pendingID = pending[0].ID
			_, _ = q.Approve(pendingID, "ok")
		case err := <-errCh:
			if err != nil {
				t.Fatalf("before returned error: %v", err)
			}
			if pendingID == "" {
				t.Fatalf("approval not processed")
			}
			return
		case <-timeout:
			t.Fatal("timeout waiting for approval request")
		}
	}
}

func TestApprovalMiddlewareNilParamsCloned(t *testing.T) {
	t.Parallel()
	q := approval.NewQueue(approval.NewMemoryStore(), approval.NewWhitelist())
	mw := NewApprovalMiddleware(q)

	execCtx := NewExecutionContext(context.Background(), map[string]any{
		defaultApprovalSessionKey:  "sess-empty",
		defaultApprovalRequestsKey: ApprovalRequest{Tool: "noop"},
	}, nil)
	go func() {
		time.Sleep(5 * time.Millisecond)
		pending := q.Pending("sess-empty")
		if len(pending) > 0 {
			_, _ = q.Approve(pending[0].ID, "ok")
		}
	}()
	if err := mw.BeforeStepContext(execCtx, Step{Name: ""}); err != nil {
		t.Fatalf("before: %v", err)
	}
	raw, _ := execCtx.Get(defaultApprovalResultsKey)
	results := raw.([]approval.Record)
	if len(results) != 1 {
		t.Fatalf("expected record stored")
	}
	if results[0].Params == nil || len(results[0].Params) != 0 {
		t.Fatalf("expected empty params map, got %+v", results[0].Params)
	}
}

func TestApprovalMiddlewareCustomKeysAndTimeout(t *testing.T) {
	t.Parallel()
	q := approval.NewQueue(approval.NewMemoryStore(), approval.NewWhitelist())
	mw := NewApprovalMiddleware(
		q,
		WithApprovalContextKeys("reqKey", "resKey", "sessKey"),
		WithApprovalPollInterval(time.Millisecond),
		WithApprovalDecisionTimeout(5*time.Millisecond),
	)

	execCtx := NewExecutionContext(context.Background(), map[string]any{
		"reqKey":  ApprovalRequest{Tool: "echo", Params: map[string]any{"x": 1}},
		"sessKey": "s-custom",
	}, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- mw.BeforeStepContext(execCtx, Step{Name: "custom"})
	}()

	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected timeout error, got %v", err)
	}

	// restore a fresh request to test pointer & slice paths.
	execCtx.Set("reqKey", []*ApprovalRequest{{Tool: "echo", Params: map[string]any{"x": 1}}})
	go func() { errCh <- mw.BeforeStepContext(execCtx, Step{Name: "custom"}) }()
	waitUntil := time.Now().Add(30 * time.Millisecond)
	for {
		pending := q.Pending("s-custom")
		if len(pending) > 0 {
			_, _ = q.Approve(pending[0].ID, "ok")
			break
		}
		if time.Now().After(waitUntil) {
			t.Fatalf("pending request never arrived")
		}
		time.Sleep(1 * time.Millisecond)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error after approval: %v", err)
	}
	raw, ok := execCtx.Get("resKey")
	if !ok {
		t.Fatalf("custom results key missing")
	}
	if records := raw.([]approval.Record); len(records) != 1 || records[0].Decision != approval.DecisionApproved {
		t.Fatalf("unexpected records %+v", records)
	}

	// pointer path coverage
	execCtx.Set("reqKey", &ApprovalRequest{Tool: "echo", Params: map[string]any{"y": 3}})
	go func() { errCh <- mw.BeforeStepContext(execCtx, Step{Name: "custom"}) }()
	waitUntil = time.Now().Add(30 * time.Millisecond)
	for {
		pending := q.Pending("s-custom")
		if len(pending) > 0 {
			_, _ = q.Approve(pending[0].ID, "ok")
			break
		}
		if time.Now().After(waitUntil) {
			t.Fatalf("pending request never arrived (pointer)")
		}
		time.Sleep(1 * time.Millisecond)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error for pointer request: %v", err)
	}
}
