package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/session"
)

func TestSubAgentMiddlewareDelegationFlow(t *testing.T) {
	t.Parallel()
	execCtx := NewExecutionContext(context.Background(), nil, nil)
	baseSession, err := session.NewMemorySession("root")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	execCtx.Set(defaultSubAgentSessionKey, baseSession)
	req := SubAgentRequest{Instruction: "delegate task"}
	execCtx.Set(defaultSubAgentRequestsKey, []*SubAgentRequest{&req})

	exec := &stubSubAgentExecutor{
		result: SubAgentResult{Output: "done", StopReason: "complete"},
	}
	mw := NewSubAgentMiddleware(exec, WithSubAgentIDPrefix("sa"))
	if err := mw.BeforeStepContext(execCtx, Step{Name: "node"}); err != nil {
		t.Fatalf("before: %v", err)
	}
	if err := mw.AfterStepContext(execCtx, Step{Name: "node"}, nil); err != nil {
		t.Fatalf("after: %v", err)
	}
	if len(exec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(exec.calls))
	}
	call := exec.calls[0]
	if call.Session == nil || call.Session.ID() != baseSession.ID() {
		t.Fatalf("session not injected: %+v", call.Session)
	}
	raw, ok := execCtx.Get(defaultSubAgentResultsKey)
	if !ok {
		t.Fatal("results missing")
	}
	results, ok := raw.([]SubAgentResult)
	if !ok || len(results) != 1 {
		t.Fatalf("unexpected results %T %+v", raw, raw)
	}
	if results[0].Output != "done" || !strings.HasPrefix(results[0].ID, "sa-") {
		t.Fatalf("unexpected result %+v", results[0])
	}
}

func TestSubAgentMiddlewareAggregatesErrors(t *testing.T) {
	t.Parallel()
	execCtx := NewExecutionContext(context.Background(), nil, nil)
	execCtx.Set(defaultSubAgentRequestsKey, SubAgentRequest{Instruction: "fail"})
	exec := &stubSubAgentExecutor{err: errors.New("boom")}
	mw := NewSubAgentMiddleware(exec)
	if err := mw.BeforeStepContext(execCtx, Step{Name: "node"}); err != nil {
		t.Fatalf("before: %v", err)
	}
	err := mw.AfterStepContext(execCtx, Step{Name: "node"}, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected aggregated error, got %v", err)
	}
	raw, _ := execCtx.Get(defaultSubAgentResultsKey)
	results := raw.([]SubAgentResult)
	if results[0].Error == "" {
		t.Fatalf("expected error stored")
	}
}

func TestSubAgentMiddlewareTypeGuards(t *testing.T) {
	t.Parallel()
	execCtx := NewExecutionContext(context.Background(), nil, nil)
	execCtx.Set(defaultSubAgentRequestsKey, 42)
	mw := NewSubAgentMiddleware(nil)
	if err := mw.AfterStepContext(execCtx, Step{Name: "node"}, nil); err == nil {
		t.Fatal("expected type error")
	}
	execCtx.Set(defaultSubAgentRequestsKey, nil)
	if err := mw.AfterStepContext(execCtx, Step{Name: "node"}, nil); err != nil {
		t.Fatalf("expected nil error when no requests, got %v", err)
	}
}

func TestSubAgentMiddlewareCustomKeysAndHooks(t *testing.T) {
	t.Parallel()
	execCtx := NewExecutionContext(context.Background(), nil, nil)
	sess, err := session.NewMemorySession("root")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	execCtx.Set("sub.req", []SubAgentRequest{{Instruction: "delegate task"}})
	execCtx.Set("sub.sess", sess)
	exec := &stubSubAgentExecutor{
		result: SubAgentResult{Output: "ok"},
	}
	mw := NewSubAgentMiddleware(
		exec,
		WithSubAgentRequestKey("sub.req"),
		WithSubAgentResultKey("sub.res"),
		WithSubAgentSessionKey("sub.sess"),
		WithSubAgentIDPrefix("custom"),
	)
	if err := mw.BeforeStep("plain"); err != nil {
		t.Fatalf("before hook: %v", err)
	}
	if err := mw.AfterStep("plain"); err != nil {
		t.Fatalf("after hook: %v", err)
	}
	if err := mw.BeforeStepContext(execCtx, Step{Name: "node"}); err != nil {
		t.Fatalf("before context: %v", err)
	}
	if err := mw.AfterStepContext(execCtx, Step{Name: "node"}, nil); err != nil {
		t.Fatalf("after context: %v", err)
	}
	raw, ok := execCtx.Get("sub.res")
	if !ok {
		t.Fatal("results missing under custom key")
	}
	results, ok := raw.([]SubAgentResult)
	if !ok || len(results) != 1 {
		t.Fatalf("unexpected results %T %+v", raw, raw)
	}
	if results[0].ID == "" || !strings.HasPrefix(results[0].ID, "custom-") {
		t.Fatalf("expected custom prefix, got %+v", results[0])
	}
	if exec.calls[0].Session != sess {
		t.Fatalf("expected base session to propagate")
	}
}

type stubSubAgentExecutor struct {
	calls  []SubAgentRequest
	result SubAgentResult
	err    error
}

func (s *stubSubAgentExecutor) Delegate(ctx context.Context, req SubAgentRequest) (SubAgentResult, error) {
	s.calls = append(s.calls, req)
	res := s.result
	res.ID = req.ID
	return res, s.err
}
