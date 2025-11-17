package workflow

import (
	"context"
	"sync"

	"github.com/cexll/agentsdk-go/pkg/tool"
)

// NodeKind describes the behavior of a node inside the StateGraph.
type NodeKind int

const (
	// NodeAction executes user business logic then follows transitions.
	NodeAction NodeKind = iota
	// NodeDecision chooses the next node directly, bypassing transitions.
	NodeDecision
	// NodeParallel fans out into multiple branches concurrently.
	NodeParallel
)

// Step carries runtime metadata used by middleware.
type Step struct {
	Name string
	Kind NodeKind
}

// Node encapsulates a unit of work inside the graph.
// Implementations must be safe to call from multiple goroutines.
type Node interface {
	Name() string
	Kind() NodeKind
	Run(*ExecutionContext) (NodeResult, error)
}

// NodeResult lets a node override graph routing.
type NodeResult struct {
	// Next explicitly lists subsequent nodes. When nil executor falls back to transitions.
	Next []string
	// Parallel signals that Next should be executed concurrently.
	Parallel bool
}

// ActionFunc performs business logic without altering routing.
type ActionFunc func(*ExecutionContext) error

// ActionNode is the simplest node: run the function, then follow transitions.
type ActionNode struct {
	name string
	fn   ActionFunc
}

func NewAction(name string, fn ActionFunc) *ActionNode {
	return &ActionNode{name: name, fn: fn}
}

func (n *ActionNode) Name() string   { return n.name }
func (n *ActionNode) Kind() NodeKind { return NodeAction }
func (n *ActionNode) Run(ctx *ExecutionContext) (NodeResult, error) {
	if n.fn == nil {
		return NodeResult{}, nil
	}
	return NodeResult{}, n.fn(ctx)
}

// DecisionFunc selects the next node name directly.
type DecisionFunc func(*ExecutionContext) (string, error)

// DecisionNode bypasses transitions and routes using its Decide function.
type DecisionNode struct {
	name string
	f    DecisionFunc
}

func NewDecision(name string, f DecisionFunc) *DecisionNode {
	return &DecisionNode{name: name, f: f}
}

func (n *DecisionNode) Name() string   { return n.name }
func (n *DecisionNode) Kind() NodeKind { return NodeDecision }
func (n *DecisionNode) Run(ctx *ExecutionContext) (NodeResult, error) {
	if n.f == nil {
		return NodeResult{Next: nil}, nil
	}
	next, err := n.f(ctx)
	if err != nil {
		return NodeResult{}, err
	}
	if next == "" {
		return NodeResult{}, nil
	}
	return NodeResult{Next: []string{next}}, nil
}

// ParallelNode starts multiple branches concurrently.
type ParallelNode struct {
	name     string
	branches []string
}

func NewParallel(name string, branches ...string) *ParallelNode {
	cp := append([]string(nil), branches...)
	return &ParallelNode{name: name, branches: cp}
}

func (n *ParallelNode) Name() string   { return n.name }
func (n *ParallelNode) Kind() NodeKind { return NodeParallel }
func (n *ParallelNode) Run(*ExecutionContext) (NodeResult, error) {
	return NodeResult{Next: append([]string(nil), n.branches...), Parallel: true}, nil
}

// ExecutionContext carries per-run data, tools and cancellation context.
// It is safe for concurrent use: all data mutations take the internal lock.
type ExecutionContext struct {
	ctx   context.Context
	data  map[string]any
	tools map[string]tool.Tool
	mu    sync.RWMutex
}

// NewExecutionContext builds an ExecutionContext with optional initial data and tools.
func NewExecutionContext(ctx context.Context, data map[string]any, tools map[string]tool.Tool) *ExecutionContext {
	if ctx == nil {
		ctx = context.Background()
	}
	c := &ExecutionContext{
		ctx:   ctx,
		data:  map[string]any{},
		tools: map[string]tool.Tool{},
	}
	for k, v := range data {
		c.data[k] = v
	}
	for k, v := range tools {
		c.tools[k] = v
	}
	return c
}

// Context returns the underlying context for cancellation or deadlines.
func (c *ExecutionContext) Context() context.Context {
	return c.ctx
}

// WithContext returns a shallow copy sharing data/tools but using the provided context.
func (c *ExecutionContext) WithContext(ctx context.Context) *ExecutionContext {
	if ctx == nil {
		return c
	}
	clone := *c
	clone.ctx = ctx
	return &clone
}

// Set stores a key/value pair.
func (c *ExecutionContext) Set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil {
		c.data = map[string]any{}
	}
	c.data[key] = value
}

// Get retrieves a value and a boolean indicating presence.
func (c *ExecutionContext) Get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.data[key]
	return val, ok
}

// Tools returns a shallow copy of the registered tools map.
func (c *ExecutionContext) Tools() map[string]tool.Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]tool.Tool, len(c.tools))
	for k, v := range c.tools {
		out[k] = v
	}
	return out
}

// AddTool registers a tool to the context.
func (c *ExecutionContext) AddTool(t tool.Tool) {
	if t == nil {
		return
	}
	name := t.Name()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tools == nil {
		c.tools = map[string]tool.Tool{}
	}
	c.tools[name] = t
}
