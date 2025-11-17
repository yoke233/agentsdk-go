package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cexll/agentsdk-go/pkg/tool"
)

// TraversalStrategy chooses DFS or BFS ordering when multiple paths are ready.
type TraversalStrategy int

const (
	TraversalDFS TraversalStrategy = iota
	TraversalBFS
)

const defaultMaxSteps = 10000

// Executor walks a graph safely with middleware, loops, and parallel branches.
type Executor struct {
	graph       *Graph
	middlewares []Middleware
	strategy    TraversalStrategy
	maxSteps    int
	start       string
	initialData map[string]any
	toolsTyped  map[string]tool.Tool
}

// ExecutorOption configures an executor at construction time.
type ExecutorOption func(*Executor)

// WithMiddleware appends middleware to the execution chain.
func WithMiddleware(mw ...Middleware) ExecutorOption {
	return func(e *Executor) {
		e.middlewares = append(e.middlewares, mw...)
	}
}

// WithStrategy sets traversal ordering, default is DFS.
func WithStrategy(strategy TraversalStrategy) ExecutorOption {
	return func(e *Executor) {
		e.strategy = strategy
	}
}

// WithMaxSteps caps how many steps a single run can execute to prevent infinite loops.
func WithMaxSteps(limit int) ExecutorOption {
	return func(e *Executor) {
		e.maxSteps = limit
	}
}

// WithStart overrides the graph's start node for this run.
func WithStart(name string) ExecutorOption {
	return func(e *Executor) {
		e.start = name
	}
}

// WithInitialData seeds ExecutionContext data map.
func WithInitialData(data map[string]any) ExecutorOption {
	return func(e *Executor) {
		if data == nil {
			return
		}
		e.initialData = make(map[string]any, len(data))
		for k, v := range data {
			e.initialData[k] = v
		}
	}
}

// WithTools injects tools into the execution context.
func WithTools(tools map[string]tool.Tool) ExecutorOption {
	return func(e *Executor) {
		if tools == nil {
			return
		}
		e.toolsTyped = make(map[string]tool.Tool, len(tools))
		for k, v := range tools {
			e.toolsTyped[k] = v
		}
	}
}

// NewExecutor constructs an Executor around a graph.
func NewExecutor(g *Graph, opts ...ExecutorOption) *Executor {
	ex := &Executor{
		graph:    g,
		strategy: TraversalDFS,
		maxSteps: defaultMaxSteps,
	}
	for _, opt := range opts {
		opt(ex)
	}
	return ex
}

// Run executes the graph from start node until exhaustion or error.
func (e *Executor) Run(ctx context.Context) error {
	if e.graph == nil {
		return errors.New("graph is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := e.graph.Validate(); err != nil {
		return err
	}
	start := e.start
	if start == "" {
		start = e.graph.Start()
	}
	if start == "" {
		return errors.New("start node is not set")
	}
	execCtx := NewExecutionContext(ctx, e.initialData, e.toolsTyped)
	return e.runBranch(execCtx, start)
}

func (e *Executor) runBranch(ctx *ExecutionContext, start string) error {
	work := newWorklist(e.strategy, start)
	steps := 0
	for {
		if err := ctx.Context().Err(); err != nil {
			return err
		}
		current, ok := work.pop()
		if !ok {
			return nil
		}
		steps++
		if e.maxSteps > 0 && steps > e.maxSteps {
			return fmt.Errorf("step limit exceeded (%d)", e.maxSteps)
		}
		if err := e.executeNode(ctx, current, work); err != nil {
			return err
		}
	}
}

func (e *Executor) executeNode(ctx *ExecutionContext, name string, work *worklist) error {
	node, ok := e.graph.Node(name)
	if !ok {
		return fmt.Errorf("node %q not found", name)
	}

	step := Step{Name: name, Kind: node.Kind()}
	return applyMiddleware(e.middlewares, ctx, step, func() error {
		res, err := node.Run(ctx)
		if err != nil {
			return err
		}
		targets := res.Next
		if targets == nil {
			targets, err = e.resolveNext(name, ctx)
			if err != nil {
				return err
			}
		}
		if len(targets) == 0 {
			return nil
		}
		if res.Parallel {
			return e.runParallel(ctx, targets)
		}
		work.push(targets)
		return nil
	})
}

func (e *Executor) resolveNext(name string, ctx *ExecutionContext) ([]string, error) {
	transitions := e.graph.transitions(name)
	if len(transitions) == 0 {
		return nil, nil
	}
	var next []string
	for _, t := range transitions {
		ok, err := t.Allows(ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			next = append(next, t.To)
		}
	}
	return next, nil
}

func (e *Executor) runParallel(ctx *ExecutionContext, targets []string) error {
	if len(targets) == 0 {
		return nil
	}
	parallelCtx, cancel := context.WithCancel(ctx.Context())
	defer cancel()
	shared := ctx.WithContext(parallelCtx)

	wg := sync.WaitGroup{}
	wg.Add(len(targets))
	errCh := make(chan error, len(targets))
	for _, name := range targets {
		go func(n string) {
			defer wg.Done()
			errCh <- e.runBranch(shared, n)
		}(name)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			cancel()
			return err
		}
	}
	return nil
}

type worklist struct {
	items    []string
	strategy TraversalStrategy
}

func newWorklist(strategy TraversalStrategy, start string) *worklist {
	return &worklist{items: []string{start}, strategy: strategy}
}

func (w *worklist) pop() (string, bool) {
	if len(w.items) == 0 {
		return "", false
	}
	if w.strategy == TraversalBFS {
		item := w.items[0]
		w.items = w.items[1:]
		return item, true
	}
	idx := len(w.items) - 1
	item := w.items[idx]
	w.items = w.items[:idx]
	return item, true
}

func (w *worklist) push(names []string) {
	if len(names) == 0 {
		return
	}
	w.items = append(w.items, names...)
}
