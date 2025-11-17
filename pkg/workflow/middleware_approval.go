package workflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/approval"
	"github.com/cexll/agentsdk-go/pkg/event"
)

const (
	defaultApprovalRequestsKey = "workflow.approval.requests"
	defaultApprovalResultsKey  = "workflow.approval.results"
	defaultApprovalSessionKey  = "workflow.session.id"
)

// ApprovalRequest describes a tool invocation that must be reviewed.
type ApprovalRequest struct {
	SessionID string
	Tool      string
	Params    map[string]any
	Reason    string
}

// ApprovalMiddleware intercepts pending tool calls and blocks until approved.
type ApprovalMiddleware struct {
	queue       *approval.Queue
	bus         *event.EventBus
	requestsKey string
	resultsKey  string
	sessionKey  string
	pollEvery   time.Duration
	decisionTTL time.Duration
}

// ApprovalOption customizes middleware behaviour.
type ApprovalOption func(*ApprovalMiddleware)

// NewApprovalMiddleware wires an approval queue into the workflow middleware chain.
func NewApprovalMiddleware(q *approval.Queue, opts ...ApprovalOption) *ApprovalMiddleware {
	mw := &ApprovalMiddleware{
		queue:       q,
		requestsKey: defaultApprovalRequestsKey,
		resultsKey:  defaultApprovalResultsKey,
		sessionKey:  defaultApprovalSessionKey,
		pollEvery:   100 * time.Millisecond,
		decisionTTL: 0, // bounded by ctx deadline
	}
	for _, opt := range opts {
		if opt != nil {
			opt(mw)
		}
	}
	return mw
}

// WithApprovalEventBus emits approval lifecycle events onto the provided bus.
func WithApprovalEventBus(bus *event.EventBus) ApprovalOption {
	return func(mw *ApprovalMiddleware) {
		mw.bus = bus
	}
}

// WithApprovalContextKeys overrides where requests/results/session are stored.
func WithApprovalContextKeys(requestsKey, resultsKey, sessionKey string) ApprovalOption {
	return func(mw *ApprovalMiddleware) {
		if strings.TrimSpace(requestsKey) != "" {
			mw.requestsKey = requestsKey
		}
		if strings.TrimSpace(resultsKey) != "" {
			mw.resultsKey = resultsKey
		}
		if strings.TrimSpace(sessionKey) != "" {
			mw.sessionKey = sessionKey
		}
	}
}

// WithApprovalPollInterval adjusts wait cadence when pending decisions exist.
func WithApprovalPollInterval(interval time.Duration) ApprovalOption {
	return func(mw *ApprovalMiddleware) {
		if interval > 0 {
			mw.pollEvery = interval
		}
	}
}

// WithApprovalDecisionTimeout caps how long the middleware waits for review.
func WithApprovalDecisionTimeout(limit time.Duration) ApprovalOption {
	return func(mw *ApprovalMiddleware) {
		if limit > 0 {
			mw.decisionTTL = limit
		}
	}
}

// BeforeStepContext submits requests then blocks until decisions are available.
func (mw *ApprovalMiddleware) BeforeStepContext(ctx *ExecutionContext, step Step) error {
	if ctx == nil {
		return errors.New("approval: execution context is nil")
	}
	if mw.queue == nil {
		return errors.New("approval: queue is nil")
	}

	requests, err := mw.dequeueRequests(ctx)
	if err != nil {
		return err
	}
	if len(requests) == 0 {
		return nil
	}

	results := make([]approval.Record, 0, len(requests))
	for _, req := range requests {
		rec, err := mw.processRequest(ctx, step, req)
		if err != nil {
			return err
		}
		results = append(results, rec)
	}
	ctx.Set(mw.resultsKey, results)
	return nil
}

// AfterStepContext is a no-op to satisfy ContextMiddleware.
func (mw *ApprovalMiddleware) AfterStepContext(_ *ExecutionContext, _ Step, _ error) error {
	return nil
}

func (mw *ApprovalMiddleware) dequeueRequests(ctx *ExecutionContext) ([]ApprovalRequest, error) {
	raw, ok := ctx.Get(mw.requestsKey)
	if !ok || raw == nil {
		return nil, nil
	}
	ctx.Set(mw.requestsKey, nil)
	switch val := raw.(type) {
	case ApprovalRequest:
		return []ApprovalRequest{val}, nil
	case *ApprovalRequest:
		if val == nil {
			return nil, nil
		}
		return []ApprovalRequest{*val}, nil
	case []ApprovalRequest:
		return append([]ApprovalRequest(nil), val...), nil
	case []*ApprovalRequest:
		out := make([]ApprovalRequest, 0, len(val))
		for _, item := range val {
			if item != nil {
				out = append(out, *item)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("approval: unexpected request type %T", raw)
	}
}

func (mw *ApprovalMiddleware) processRequest(ctx *ExecutionContext, step Step, req ApprovalRequest) (approval.Record, error) {
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		if raw, ok := ctx.Get(mw.sessionKey); ok {
			if val, ok := raw.(string); ok {
				sessionID = strings.TrimSpace(val)
			}
		}
	}
	if sessionID == "" {
		return approval.Record{}, errors.New("approval: session id is empty")
	}
	toolName := strings.TrimSpace(req.Tool)
	if toolName == "" {
		return approval.Record{}, errors.New("approval: tool name is empty")
	}
	params := cloneMap(req.Params)

	rec, approvedImmediately, err := mw.queue.Request(sessionID, toolName, params)
	if err != nil {
		return approval.Record{}, err
	}
	if approvedImmediately {
		mw.emitDecided(sessionID, rec, true)
		return rec, nil
	}

	mw.emitRequested(sessionID, rec, req.Reason, step)

	waitCtx := ctx.Context()
	if mw.decisionTTL > 0 {
		var cancel func()
		waitCtx, cancel = context.WithTimeout(waitCtx, mw.decisionTTL)
		defer cancel()
	}
	decided, err := mw.waitForDecision(waitCtx, rec.ID)
	if err != nil {
		return approval.Record{}, err
	}
	switch decided.Decision {
	case approval.DecisionApproved:
		mw.emitDecided(sessionID, decided, true)
		return decided, nil
	case approval.DecisionRejected, approval.DecisionTimeout:
		mw.emitDecided(sessionID, decided, false)
		return decided, fmt.Errorf("approval: %s %s denied (%s)", sessionID, toolName, decided.Comment)
	default:
		return decided, fmt.Errorf("approval: %s %s undecided", sessionID, toolName)
	}
}

func (mw *ApprovalMiddleware) waitForDecision(ctx context.Context, id string) (approval.Record, error) {
	var snapshot approval.Record
	ticker := time.NewTicker(mw.pollEvery)
	defer ticker.Stop()
	for {
		rec, ok := mw.queue.Lookup(id)
		if ok {
			snapshot = rec
		}
		if snapshot.Decision != approval.DecisionPending && snapshot.ID != "" {
			return snapshot, nil
		}
		select {
		case <-ctx.Done():
			if snapshot.ID == "" {
				return approval.Record{}, ctx.Err()
			}
			return snapshot, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (mw *ApprovalMiddleware) emitRequested(sessionID string, rec approval.Record, reason string, step Step) {
	if mw.bus == nil {
		return
	}
	_ = mw.bus.Emit(event.NewEvent(event.EventApprovalRequested, sessionID, event.ApprovalRequest{
		ID:       rec.ID,
		ToolName: rec.Tool,
		Params:   maps.Clone(rec.Params),
		Reason:   requestReason(reason, step),
	}))
}

func (mw *ApprovalMiddleware) emitDecided(sessionID string, rec approval.Record, approved bool) {
	if mw.bus == nil {
		return
	}
	_ = mw.bus.Emit(event.NewEvent(event.EventApprovalDecided, sessionID, event.ApprovalResponse{
		ID:       rec.ID,
		Approved: approved,
		Comment:  rec.Comment,
	}))
}

func requestReason(reason string, step Step) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	if strings.TrimSpace(step.Name) != "" {
		return fmt.Sprintf("step %s", step.Name)
	}
	return "workflow"
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	return maps.Clone(src)
}
