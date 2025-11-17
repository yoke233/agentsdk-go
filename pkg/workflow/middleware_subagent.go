package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/session"
)

const (
	defaultSubAgentRequestsKey = "workflow.subagent.requests"
	defaultSubAgentResultsKey  = "workflow.subagent.results"
	defaultSubAgentSessionKey  = "workflow.subagent.session"
)

// SubAgentRequest represents a pending delegation instruction.
type SubAgentRequest struct {
	ID            string
	Instruction   string
	Session       session.Session
	SessionID     string
	ShareSession  bool
	ShareEventBus bool
	ToolWhitelist []string
	Metadata      map[string]any
}

// SubAgentToolCall captures tool executions performed by a delegate.
type SubAgentToolCall struct {
	Name     string
	Params   map[string]any
	Output   any
	Error    string
	Duration time.Duration
	Metadata map[string]any
}

// SubAgentUsage summarizes lightweight token accounting.
type SubAgentUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	CacheTokens  int
}

// SubAgentResult stores the aggregated output from a delegate run.
type SubAgentResult struct {
	ID            string
	Output        string
	StopReason    string
	Error         string
	SessionID     string
	SharedSession bool
	Session       session.Session
	Usage         SubAgentUsage
	ToolCalls     []SubAgentToolCall
	Events        []event.Event
	Metadata      map[string]any
}

// SubAgentExecutor defines the minimal surface required by the middleware.
type SubAgentExecutor interface {
	Delegate(ctx context.Context, req SubAgentRequest) (SubAgentResult, error)
}

// SubAgentMiddleware consumes queued requests and invokes the executor.
type SubAgentMiddleware struct {
	exec       SubAgentExecutor
	requestKey string
	resultKey  string
	sessionKey string
	idPrefix   string
	seq        atomic.Uint64
}

// SubAgentOption customizes middleware behavior.
type SubAgentOption func(*SubAgentMiddleware)

// NewSubAgentMiddleware wires an executor into the workflow chain.
func NewSubAgentMiddleware(exec SubAgentExecutor, opts ...SubAgentOption) *SubAgentMiddleware {
	mw := &SubAgentMiddleware{
		exec:       exec,
		requestKey: defaultSubAgentRequestsKey,
		resultKey:  defaultSubAgentResultsKey,
		sessionKey: defaultSubAgentSessionKey,
		idPrefix:   "delegate",
	}
	for _, opt := range opts {
		if opt != nil {
			opt(mw)
		}
	}
	return mw
}

// WithSubAgentRequestKey overrides the context key used to fetch pending requests.
func WithSubAgentRequestKey(key string) SubAgentOption {
	return func(mw *SubAgentMiddleware) {
		if strings.TrimSpace(key) != "" {
			mw.requestKey = key
		}
	}
}

// WithSubAgentResultKey overrides where aggregated results are stored.
func WithSubAgentResultKey(key string) SubAgentOption {
	return func(mw *SubAgentMiddleware) {
		if strings.TrimSpace(key) != "" {
			mw.resultKey = key
		}
	}
}

// WithSubAgentSessionKey sets the context key for session references.
func WithSubAgentSessionKey(key string) SubAgentOption {
	return func(mw *SubAgentMiddleware) {
		if strings.TrimSpace(key) != "" {
			mw.sessionKey = key
		}
	}
}

// WithSubAgentIDPrefix customizes generated request identifiers.
func WithSubAgentIDPrefix(prefix string) SubAgentOption {
	return func(mw *SubAgentMiddleware) {
		if strings.TrimSpace(prefix) != "" {
			mw.idPrefix = strings.TrimSpace(prefix)
		}
	}
}

// BeforeStepContext validates the execution context before delegations occur.
func (mw *SubAgentMiddleware) BeforeStepContext(ctx *ExecutionContext, _ Step) error {
	if ctx == nil {
		return errors.New("execution context is nil")
	}
	return nil
}

// AfterStepContext drains the queued requests and invokes the executor sequentially.
func (mw *SubAgentMiddleware) AfterStepContext(ctx *ExecutionContext, _ Step, _ error) error {
	if ctx == nil {
		return errors.New("execution context is nil")
	}
	requests, err := mw.dequeue(ctx)
	if err != nil {
		return err
	}
	if len(requests) == 0 {
		return nil
	}
	if mw.exec == nil {
		return errors.New("subagent: executor is nil")
	}
	baseSession := mw.sessionFromContext(ctx)
	results := make([]SubAgentResult, 0, len(requests))
	var joined error
	for _, req := range requests {
		if req.ID == "" {
			req.ID = mw.nextID()
		}
		if req.Session == nil {
			req.Session = baseSession
		}
		res, callErr := mw.exec.Delegate(ctx.Context(), req)
		if callErr != nil {
			if res.ID == "" {
				res.ID = req.ID
			}
			if res.Error == "" {
				res.Error = callErr.Error()
			}
			joined = errors.Join(joined, fmt.Errorf("subagent %s: %w", req.ID, callErr))
		}
		results = append(results, res)
	}
	ctx.Set(mw.resultKey, results)
	return joined
}

func (mw *SubAgentMiddleware) dequeue(ctx *ExecutionContext) ([]SubAgentRequest, error) {
	raw, ok := ctx.Get(mw.requestKey)
	if !ok || raw == nil {
		return nil, nil
	}
	ctx.Set(mw.requestKey, nil)
	switch val := raw.(type) {
	case SubAgentRequest:
		return []SubAgentRequest{val}, nil
	case *SubAgentRequest:
		if val == nil {
			return nil, nil
		}
		return []SubAgentRequest{*val}, nil
	case []SubAgentRequest:
		out := make([]SubAgentRequest, len(val))
		copy(out, val)
		return out, nil
	case []*SubAgentRequest:
		out := make([]SubAgentRequest, 0, len(val))
		for _, ptr := range val {
			if ptr != nil {
				out = append(out, *ptr)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("subagent: unexpected request type %T", raw)
	}
}

func (mw *SubAgentMiddleware) sessionFromContext(ctx *ExecutionContext) session.Session {
	if ctx == nil {
		return nil
	}
	raw, ok := ctx.Get(mw.sessionKey)
	if !ok {
		return nil
	}
	if sess, ok := raw.(session.Session); ok {
		return sess
	}
	return nil
}

func (mw *SubAgentMiddleware) nextID() string {
	n := mw.seq.Add(1)
	prefix := mw.idPrefix
	if prefix == "" {
		prefix = "delegate"
	}
	return fmt.Sprintf("%s-%06d", prefix, n)
}

// BeforeStep is required to satisfy the Middleware interface.
func (*SubAgentMiddleware) BeforeStep(string) error { return nil }

// AfterStep is required to satisfy the Middleware interface.
func (*SubAgentMiddleware) AfterStep(string) error { return nil }
