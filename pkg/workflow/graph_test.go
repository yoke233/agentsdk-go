package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestExecutorTraversalOrder(t *testing.T) {
	t.Parallel()

	newGraphWithRecorder := func(order *[]string) *Graph {
		var mu sync.Mutex
		record := func(label string) ActionFunc {
			return func(*ExecutionContext) error {
				mu.Lock()
				*order = append(*order, label)
				mu.Unlock()
				return nil
			}
		}
		g := NewGraph()
		if err := g.AddNode(NewAction("start", record("start"))); err != nil {
			t.Fatalf("add start: %v", err)
		}
		if err := g.AddNode(NewAction("left", record("left"))); err != nil {
			t.Fatalf("add left: %v", err)
		}
		if err := g.AddNode(NewAction("right", record("right"))); err != nil {
			t.Fatalf("add right: %v", err)
		}
		if err := g.AddTransition("start", "left", nil); err != nil {
			t.Fatalf("add transition: %v", err)
		}
		if err := g.AddTransition("start", "right", nil); err != nil {
			t.Fatalf("add transition: %v", err)
		}
		return g
	}

	t.Run("BFS", func(t *testing.T) {
		order := []string{}
		g := newGraphWithRecorder(&order)
		exec := NewExecutor(g, WithStrategy(TraversalBFS))
		if err := exec.Run(context.Background()); err != nil {
			t.Fatalf("run bfs: %v", err)
		}
		if want := []string{"start", "left", "right"}; !slicesEqual(order, want) {
			t.Fatalf("bfs order mismatch: got %v want %v", order, want)
		}
	})

	t.Run("DFS", func(t *testing.T) {
		order := []string{}
		g := newGraphWithRecorder(&order)
		exec := NewExecutor(g, WithStrategy(TraversalDFS))
		if err := exec.Run(context.Background()); err != nil {
			t.Fatalf("run dfs: %v", err)
		}
		if want := []string{"start", "right", "left"}; !slicesEqual(order, want) {
			t.Fatalf("dfs order mismatch: got %v want %v", order, want)
		}
	})
}

func TestDecisionOverridesTransitions(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	if err := g.AddNode(NewDecision("start", func(ctx *ExecutionContext) (string, error) {
		if v, ok := ctx.Get("route"); ok && v == "b" {
			return "b", nil
		}
		return "a", nil
	})); err != nil {
		t.Fatalf("add start: %v", err)
	}
	var aCount, bCount int32
	if err := g.AddNode(NewAction("a", func(*ExecutionContext) error {
		atomic.AddInt32(&aCount, 1)
		return nil
	})); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if err := g.AddNode(NewAction("b", func(*ExecutionContext) error {
		atomic.AddInt32(&bCount, 1)
		return nil
	})); err != nil {
		t.Fatalf("add b: %v", err)
	}
	// Transitions should be ignored because Decision returns Next.
	_ = g.AddTransition("start", "a", nil)
	_ = g.AddTransition("start", "b", nil)

	exec := NewExecutor(g, WithInitialData(map[string]any{"route": "b"}))
	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("run decision: %v", err)
	}
	if atomic.LoadInt32(&aCount) != 0 {
		t.Fatalf("unexpected execution of branch a")
	}
	if atomic.LoadInt32(&bCount) != 1 {
		t.Fatalf("branch b not executed once, got %d", bCount)
	}
}

func TestLoopStopsOnCondition(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	var total int32
	doneCalled := atomic.Bool{}
	if err := g.AddNode(NewAction("counter", func(ctx *ExecutionContext) error {
		val := atomic.AddInt32(&total, 1)
		ctx.Set("count", int(val))
		return nil
	})); err != nil {
		t.Fatalf("add counter: %v", err)
	}
	if err := g.AddNode(NewAction("done", func(ctx *ExecutionContext) error {
		ctx.Set("done", true)
		doneCalled.Store(true)
		return nil
	})); err != nil {
		t.Fatalf("add done: %v", err)
	}

	lessThan := func(limit int) Condition {
		return func(ctx *ExecutionContext) (bool, error) {
			val, _ := ctx.Get("count")
			return toInt(val) < limit, nil
		}
	}
	atLeast := func(limit int) Condition {
		return func(ctx *ExecutionContext) (bool, error) {
			val, _ := ctx.Get("count")
			return toInt(val) >= limit, nil
		}
	}

	if err := g.AddTransition("counter", "counter", lessThan(3)); err != nil {
		t.Fatalf("add loop: %v", err)
	}
	if err := g.AddTransition("counter", "done", atLeast(3)); err != nil {
		t.Fatalf("add exit: %v", err)
	}

	exec := NewExecutor(g, WithInitialData(map[string]any{"count": 0}))
	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("run loop: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 iterations, got %d", total)
	}
	if !doneCalled.Load() {
		t.Fatalf("done node not executed")
	}
}

func TestParallelStopsOnError(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	if err := g.AddNode(NewParallel("start", "good", "bad")); err != nil {
		t.Fatalf("add start: %v", err)
	}
	var hits int32
	if err := g.AddNode(NewAction("good", func(*ExecutionContext) error {
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&hits, 1)
		return nil
	})); err != nil {
		t.Fatalf("add good: %v", err)
	}
	errBoom := errors.New("boom")
	if err := g.AddNode(NewAction("bad", func(*ExecutionContext) error {
		time.Sleep(5 * time.Millisecond)
		return errBoom
	})); err != nil {
		t.Fatalf("add bad: %v", err)
	}

	exec := NewExecutor(g)
	if err := exec.Run(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("expected boom error, got %v", err)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatalf("parallel branch did not execute before error")
	}
}

func TestMiddlewareChain(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	errBoom := errors.New("boom")
	if err := g.AddNode(NewAction("start", func(*ExecutionContext) error { return errBoom })); err != nil {
		t.Fatalf("add start: %v", err)
	}

	calls := []string{}
	legacy := &legacyMiddleware{calls: &calls}
	ctxAware := &ctxMiddleware{}

	exec := NewExecutor(g, WithMiddleware(legacy, ctxAware))
	if err := exec.Run(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("expected boom, got %v", err)
	}
	want := []string{"before:start", "after:start"}
	if !slicesEqual(calls, want) {
		t.Fatalf("middleware order mismatch: %v", calls)
	}
	if ctxAware.seenErr == nil || !errors.Is(ctxAware.seenErr, errBoom) {
		t.Fatalf("context middleware did not receive error")
	}
}

func TestToolsContext(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	dummy := &noopTool{name: "echo"}
	if err := g.AddNode(NewAction("start", func(ctx *ExecutionContext) error {
		tools := ctx.Tools()
		if len(tools) != 1 {
			return fmt.Errorf("expected 1 tool, got %d", len(tools))
		}
		if _, ok := tools["echo"]; !ok {
			return errors.New("tool missing")
		}
		return nil
	})); err != nil {
		t.Fatalf("add start: %v", err)
	}

	exec := NewExecutor(g, WithTools(map[string]tool.Tool{"echo": dummy}))
	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("run tools: %v", err)
	}
}

func TestGraphValidationAndClosing(t *testing.T) {
	t.Parallel()
	g := NewGraph()

	if err := g.Validate(); err == nil {
		t.Fatal("expected validate error when start unset")
	}

	if err := g.AddNode(nil); err == nil {
		t.Fatal("expected error for nil node")
	}
	if err := g.AddNode(NewAction("", nil)); err == nil {
		t.Fatal("expected error for empty node name")
	}
	if err := g.AddNode(NewAction("start", nil)); err != nil {
		t.Fatalf("add start: %v", err)
	}
	if err := g.AddNode(NewAction("start", nil)); err == nil {
		t.Fatal("expected duplicate node error")
	}
	if err := g.SetStart("missing"); err == nil {
		t.Fatal("expected set start error for unknown node")
	}
	if err := g.AddNode(NewAction("end", nil)); err != nil {
		t.Fatalf("add end: %v", err)
	}
	if err := g.AddTransition("missing", "end", nil); err == nil {
		t.Fatal("expected missing from error")
	}
	if err := g.AddTransition("start", "missing", nil); err == nil {
		t.Fatal("expected missing to error")
	}
	if err := g.AddTransition("start", "end", nil); err != nil {
		t.Fatalf("add transition: %v", err)
	}
	g.Close()
	if err := g.AddNode(NewAction("late", nil)); err == nil {
		t.Fatal("expected closed graph node error")
	}
	if err := g.AddTransition("start", "end", nil); err == nil {
		t.Fatal("expected closed graph transition error")
	}
}

func TestExecutionContextDataAndTools(t *testing.T) {
	t.Parallel()
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := map[string]any{"foo": 1}
	tools := map[string]tool.Tool{"echo": &noopTool{name: "echo"}}
	ctx := NewExecutionContext(baseCtx, d, tools)

	if ctx.Context() != baseCtx {
		t.Fatal("context mismatch")
	}
	if v, ok := ctx.Get("foo"); !ok || v.(int) != 1 {
		t.Fatalf("unexpected get foo: %v %v", v, ok)
	}
	ctx.Set("bar", 2)
	if v, ok := ctx.Get("bar"); !ok || v.(int) != 2 {
		t.Fatalf("unexpected get bar: %v %v", v, ok)
	}
	if got := ctx.Tools(); len(got) != 1 {
		t.Fatalf("unexpected tool len: %d", len(got))
	}
	ctx.AddTool(&noopTool{name: "adder"})
	if _, ok := ctx.Tools()["adder"]; !ok {
		t.Fatal("tool not added")
	}
	ctx.AddTool(nil) // should be no-op
	nextCtx := ctx.WithContext(context.Background())
	if nextCtx == ctx {
		t.Fatal("expected shallow copy")
	}
	if nextCtx.Context() == ctx.Context() {
		t.Fatal("expected context override")
	}
	if ctx.WithContext(nil) != ctx {
		t.Fatal("nil context should return self")
	}
}

func TestTransitionAllows(t *testing.T) {
	t.Parallel()
	tr := Transition{From: "a", To: "b"}
	ok, err := tr.Allows(NewExecutionContext(context.Background(), nil, nil))
	if err != nil || !ok {
		t.Fatalf("expected transition allow, got %v %v", ok, err)
	}

	expect := errors.New("blocked")
	tr.Condition = func(*ExecutionContext) (bool, error) { return false, expect }
	if _, err := tr.Allows(NewExecutionContext(context.Background(), nil, nil)); !errors.Is(err, expect) {
		t.Fatalf("expected condition error, got %v", err)
	}
	if _, err := tr.Allows(nil); err == nil {
		t.Fatal("expected nil context error")
	}
	tr.Condition = func(*ExecutionContext) (bool, error) { return false, nil }
	ok, err = tr.Allows(NewExecutionContext(context.Background(), nil, nil))
	if err != nil || ok {
		t.Fatalf("expected false without error, got %v %v", ok, err)
	}
}

func TestMiddlewareFailures(t *testing.T) {
	t.Parallel()
	ctx := NewExecutionContext(context.Background(), nil, nil)
	step := Step{Name: "x", Kind: NodeAction}

	beforeErr := errors.New("before")
	mw := &failingMiddleware{before: beforeErr}
	err := applyMiddleware([]Middleware{mw}, ctx, step, func() error {
		t.Fatal("should not execute handler on before error")
		return nil
	})
	if !errors.Is(err, beforeErr) {
		t.Fatalf("expected before error, got %v", err)
	}

	afterErr := errors.New("after")
	mw = &failingMiddleware{after: afterErr}
	callCount := 0
	err = applyMiddleware([]Middleware{mw}, ctx, step, func() error {
		callCount++
		return nil
	})
	if callCount != 1 {
		t.Fatalf("handler not called, count=%d", callCount)
	}
	if !errors.Is(err, afterErr) {
		t.Fatalf("expected after error, got %v", err)
	}

	runErr := errors.New("run")
	mw = &failingMiddleware{after: afterErr}
	err = applyMiddleware([]Middleware{mw}, ctx, step, func() error { return runErr })
	if !errors.Is(err, runErr) || !errors.Is(err, afterErr) {
		t.Fatalf("expected joined errors, got %v", err)
	}
}

func TestExecutorMaxSteps(t *testing.T) {
	t.Parallel()
	g := NewGraph()
	if err := g.AddNode(NewAction("loop", func(*ExecutionContext) error { return nil })); err != nil {
		t.Fatalf("add loop: %v", err)
	}
	if err := g.AddTransition("loop", "loop", nil); err != nil {
		t.Fatalf("add self transition: %v", err)
	}
	exec := NewExecutor(g, WithMaxSteps(2))
	if err := exec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "step limit") {
		t.Fatalf("expected step limit error, got %v", err)
	}
}

func TestExecutorStartOverride(t *testing.T) {
	t.Parallel()
	g := NewGraph()
	var alpha, beta atomic.Int32
	if err := g.AddNode(NewAction("alpha", func(*ExecutionContext) error {
		alpha.Add(1)
		return nil
	})); err != nil {
		t.Fatalf("add alpha: %v", err)
	}
	if err := g.AddNode(NewAction("beta", func(*ExecutionContext) error {
		beta.Add(1)
		return nil
	})); err != nil {
		t.Fatalf("add beta: %v", err)
	}
	if err := g.AddTransition("alpha", "beta", nil); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	exec := NewExecutor(g, WithStart("beta"))
	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("run with start override: %v", err)
	}
	if alpha.Load() != 0 {
		t.Fatalf("alpha should not run, got %d", alpha.Load())
	}
	if beta.Load() != 1 {
		t.Fatalf("beta should run once, got %d", beta.Load())
	}
}

func TestExecutorRespectsContextCancel(t *testing.T) {
	t.Parallel()
	g := NewGraph()
	if err := g.AddNode(NewAction("wait", func(ctx *ExecutionContext) error {
		select {
		case <-ctx.Context().Done():
			return ctx.Context().Err()
		case <-time.After(100 * time.Millisecond):
			return nil
		}
	})); err != nil {
		t.Fatalf("add wait: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := NewExecutor(g)
	if err := exec.Run(ctx); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled error, got %v", err)
	}
}

func TestNodeImplementations(t *testing.T) {
	t.Parallel()

	actionHit := atomic.Bool{}
	action := NewAction("act", func(*ExecutionContext) error {
		actionHit.Store(true)
		return nil
	})
	if action.Kind() != NodeAction || action.Name() != "act" {
		t.Fatalf("unexpected action metadata")
	}
	if _, err := action.Run(NewExecutionContext(context.Background(), nil, nil)); err != nil {
		t.Fatalf("action run err: %v", err)
	}
	if !actionHit.Load() {
		t.Fatalf("action func not executed")
	}
	nilAction := NewAction("noop", nil)
	if res, err := nilAction.Run(NewExecutionContext(context.Background(), nil, nil)); err != nil || len(res.Next) != 0 {
		t.Fatalf("nil action should no-op, got %v %v", res, err)
	}

	decision := NewDecision("decide", func(ctx *ExecutionContext) (string, error) {
		ctx.Set("decision", true)
		return "next", nil
	})
	if decision.Kind() != NodeDecision {
		t.Fatalf("decision kind mismatch")
	}
	res, err := decision.Run(NewExecutionContext(context.Background(), nil, nil))
	if err != nil || len(res.Next) != 1 || res.Next[0] != "next" {
		t.Fatalf("decision result unexpected: %v %v", res, err)
	}

	parallel := NewParallel("fan", "a", "b")
	if parallel.Kind() != NodeParallel {
		t.Fatalf("parallel kind mismatch")
	}
	res, err = parallel.Run(NewExecutionContext(context.Background(), nil, nil))
	if err != nil || !res.Parallel || len(res.Next) != 2 {
		t.Fatalf("parallel result unexpected: %v %v", res, err)
	}

	emptyDecision := NewDecision("empty", nil)
	res, err = emptyDecision.Run(NewExecutionContext(context.Background(), nil, nil))
	if err != nil || res.Next != nil {
		t.Fatalf("empty decision should no-op, got %v %v", res, err)
	}
	blankDecision := NewDecision("blank", func(*ExecutionContext) (string, error) { return "", nil })
	if res, err = blankDecision.Run(NewExecutionContext(context.Background(), nil, nil)); err != nil || res.Next != nil {
		t.Fatalf("blank decision should no-op, got %v %v", res, err)
	}
	expect := errors.New("boom")
	errorDecision := NewDecision("error", func(*ExecutionContext) (string, error) { return "", expect })
	if _, err := errorDecision.Run(NewExecutionContext(context.Background(), nil, nil)); !errors.Is(err, expect) {
		t.Fatalf("expected decision error, got %v", err)
	}
}

func TestAlwaysCondition(t *testing.T) {
	t.Parallel()
	g := NewGraph()
	if err := g.AddNode(NewAction("start", nil)); err != nil {
		t.Fatalf("add start: %v", err)
	}
	if err := g.AddNode(NewAction("end", nil)); err != nil {
		t.Fatalf("add end: %v", err)
	}
	if err := g.AddTransition("start", "end", Always()); err != nil {
		t.Fatalf("add transition: %v", err)
	}
	exec := NewExecutor(g)
	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("run graph with always: %v", err)
	}
}

func TestGraphSetStartAndNodeLookup(t *testing.T) {
	t.Parallel()
	g := NewGraph()
	if err := g.AddNode(NewAction("first", nil)); err != nil {
		t.Fatalf("add first: %v", err)
	}
	if err := g.AddNode(NewAction("second", nil)); err != nil {
		t.Fatalf("add second: %v", err)
	}
	if err := g.SetStart("second"); err != nil {
		t.Fatalf("set start: %v", err)
	}
	if g.Start() != "second" {
		t.Fatalf("expected start second, got %s", g.Start())
	}
	if _, ok := g.Node("first"); !ok {
		t.Fatalf("expected node lookup success")
	}
}

func TestWorklistPushPop(t *testing.T) {
	t.Parallel()
	w := newWorklist(TraversalBFS, "start")
	if item, ok := w.pop(); !ok || item != "start" {
		t.Fatalf("unexpected initial pop: %v %v", item, ok)
	}
	w.push(nil) // should no-op
	w.push([]string{"x", "y"})
	if item, ok := w.pop(); !ok || item != "x" {
		t.Fatalf("expected bfs order x, got %v %v", item, ok)
	}
	stack := newWorklist(TraversalDFS, "root")
	stack.push([]string{"a", "b"})
	if item, ok := stack.pop(); !ok || item != "b" {
		t.Fatalf("expected dfs order b, got %v %v", item, ok)
	}
	stack.push([]string{})
	if item, ok := stack.pop(); !ok || item != "a" {
		t.Fatalf("expected dfs order a, got %v %v", item, ok)
	}
}

func TestExecutorNilGraphAndContext(t *testing.T) {
	t.Parallel()
	exec := NewExecutor(nil)
	if err := exec.Run(context.Background()); err == nil {
		t.Fatal("expected error for nil graph")
	}

	g := NewGraph()
	if err := g.AddNode(NewAction("start", nil)); err != nil {
		t.Fatalf("add start: %v", err)
	}
	exec = NewExecutor(g)
	if err := exec.Run(nil); err != nil {
		t.Fatalf("expected nil context to default, got %v", err)
	}

	bad := NewGraph()
	// Graph has no nodes -> validate should fail via executor.
	exec = NewExecutor(bad)
	if err := exec.Run(context.Background()); err == nil {
		t.Fatal("expected validation error for empty graph")
	}
}

type legacyMiddleware struct {
	calls *[]string
}

func (l *legacyMiddleware) BeforeStep(name string) error {
	*l.calls = append(*l.calls, "before:"+name)
	return nil
}

func (l *legacyMiddleware) AfterStep(name string) error {
	*l.calls = append(*l.calls, "after:"+name)
	return nil
}

type ctxMiddleware struct {
	seenErr error
}

func (c *ctxMiddleware) BeforeStepContext(*ExecutionContext, Step) error { return nil }
func (c *ctxMiddleware) AfterStepContext(_ *ExecutionContext, _ Step, err error) error {
	c.seenErr = err
	return nil
}
func (c *ctxMiddleware) BeforeStep(string) error { return nil }
func (c *ctxMiddleware) AfterStep(string) error  { return nil }

type failingMiddleware struct {
	before error
	after  error
}

func (f *failingMiddleware) BeforeStep(string) error { return f.before }
func (f *failingMiddleware) AfterStep(string) error  { return f.after }

type noopTool struct {
	name string
}

func (n *noopTool) Name() string             { return n.name }
func (n *noopTool) Description() string      { return n.name }
func (n *noopTool) Schema() *tool.JSONSchema { return nil }
func (n *noopTool) Execute(context.Context, map[string]any) (*tool.ToolResult, error) {
	return &tool.ToolResult{Output: "ok"}, nil
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	default:
		return 0
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
