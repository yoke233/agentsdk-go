package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/event"
)

func TestTodoStatusValidationAndGet(t *testing.T) {
	t.Parallel()
	if !TodoPending.Valid() || !TodoCompleted.Valid() {
		t.Fatalf("expected builtin statuses to be valid")
	}
	if TodoStatus("invalid").Valid() {
		t.Fatalf("unexpected valid status")
	}

	list := NewTodoList()
	created, _, err := list.ApplyTasks([]TodoTask{{Title: "alpha", Status: TodoInProgress}})
	if err != nil {
		t.Fatalf("apply tasks: %v", err)
	}
	got, ok := list.Get(created[0].ID)
	if !ok || got.ID != created[0].ID {
		t.Fatalf("get mismatch: %+v", got)
	}
	if _, ok := list.Get("missing"); ok {
		t.Fatalf("expected missing task")
	}
	added, err := list.AddTask("beta", []string{"alpha", "alpha"})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	if len(added.Dependencies) != 1 {
		t.Fatalf("dependencies not deduplicated: %+v", added.Dependencies)
	}
}

func TestTodoListSkipEqualAndDeps(t *testing.T) {
	t.Parallel()
	list := NewTodoList()
	created, _, err := list.ApplyTasks([]TodoTask{{Title: "alpha", Dependencies: []string{"#ref", "Ref"}}})
	if err != nil {
		t.Fatalf("apply tasks: %v", err)
	}
	if len(created[0].Dependencies) != 1 {
		t.Fatalf("expected normalized dependency, got %+v", created[0])
	}
	_, updated, err := list.ApplyTasks([]TodoTask{{ID: created[0].ID, Title: created[0].Title, Status: created[0].Status, Dependencies: created[0].Dependencies}})
	if err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if len(updated) != 0 {
		t.Fatalf("expected identical task to be skipped, got %d updates", len(updated))
	}
}

func TestTodoListApplyUpdatesErrors(t *testing.T) {
	t.Parallel()
	list := NewTodoList()
	if _, _, err := list.ApplyUpdates([]TodoUpdate{{}}); err == nil {
		t.Fatalf("expected missing id error")
	}
	created, _, err := list.ApplyTasks([]TodoTask{{Title: "alpha"}})
	if err != nil {
		t.Fatalf("apply tasks: %v", err)
	}
	if _, _, err := list.ApplyUpdates([]TodoUpdate{{ID: created[0].ID, Status: TodoStatus("bogus")}}); err == nil {
		t.Fatalf("expected invalid status error")
	}
	if _, _, err := list.ApplyUpdates([]TodoUpdate{{ID: "unknown", Delete: true}}); err == nil {
		t.Fatalf("expected not found error")
	}
}

func TestStripStatusHintVariants(t *testing.T) {
	t.Parallel()
	title, status := stripStatusHint("ship (DONE)", TodoPending)
	if title != "ship" || status != TodoCompleted {
		t.Fatalf("parentheses hint failed: %q %s", title, status)
	}
	title, status = stripStatusHint("deploy [pending]", TodoCompleted)
	if title != "deploy" || status != TodoPending {
		t.Fatalf("bracket hint failed: %q %s", title, status)
	}
	title, status = stripStatusHint("rollout status: in_progress", TodoPending)
	if title != "rollout" || status != TodoInProgress {
		t.Fatalf("status hint failed: %q %s", title, status)
	}
	task, ok := parseLine("1. finalize docs - completed")
	if !ok || task.Status != TodoCompleted || task.Title != "finalize docs" {
		t.Fatalf("parseLine fallback failed: %+v", task)
	}
}

func TestTodoListApplyUpdateDelete(t *testing.T) {
	t.Parallel()
	list := NewTodoList()

	created, _, err := list.ApplyTasks([]TodoTask{
		{Title: "design api", Status: TodoPending},
		{Title: "write tests", Status: TodoInProgress, Dependencies: []string{"design api"}},
	})
	if err != nil {
		t.Fatalf("apply tasks: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(created))
	}

	changed, deleted, err := list.ApplyUpdates([]TodoUpdate{{ID: created[0].ID, Status: TodoCompleted}, {ID: created[1].ID, Delete: true}})
	if err != nil {
		t.Fatalf("apply updates: %v", err)
	}
	if len(changed) != 1 || changed[0].Status != TodoCompleted {
		t.Fatalf("unexpected changed tasks: %+v", changed)
	}
	if len(deleted) != 1 || deleted[0] != created[1].ID {
		t.Fatalf("unexpected deleted list: %v", deleted)
	}
}

func TestTodoListSnapshotRoundTrip(t *testing.T) {
	t.Parallel()
	list := NewTodoList()
	if _, _, err := list.ApplyTasks([]TodoTask{{Title: "persist me", Status: TodoPending}}); err != nil {
		t.Fatalf("apply tasks: %v", err)
	}
	data, err := list.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	restored := NewTodoList()
	if err := restored.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := restored.UnmarshalBinary(nil); err != nil {
		t.Fatalf("unmarshal nil: %v", err)
	}
	tasks := restored.Tasks()
	if len(tasks) != 1 || tasks[0].Title != "persist me" || tasks[0].Status != TodoPending {
		t.Fatalf("unexpected restored tasks: %+v", tasks)
	}
}

func TestExtractTodoTasksMarkdownAndJSON(t *testing.T) {
	t.Parallel()
	text := "- [ ] open pr\n- [x] merge code\n* [>] rollout (status: in_progress) deps:open pr,merge code"
	tasks := ExtractTodoTasks(text)
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	if tasks[0].Status != TodoPending || tasks[1].Status != TodoCompleted || tasks[2].Status != TodoInProgress {
		t.Fatalf("unexpected statuses: %+v", tasks)
	}

	jsonText := `[{"title":"ship","status":"completed"},{"name":"burndown","status":"pending"}]`
	parsed := ExtractTodoTasks(jsonText)
	if len(parsed) != 2 || parsed[0].Title != "ship" || parsed[1].Status != TodoPending {
		t.Fatalf("unexpected json tasks: %+v", parsed)
	}

	jsonWithDeps := `[{"title":"plan","dependencies":["a","b"]}]`
	withDeps := ExtractTodoTasks(jsonWithDeps)
	if len(withDeps) != 1 || len(withDeps[0].Dependencies) != 2 {
		t.Fatalf("expected dependencies parsed, got %+v", withDeps)
	}
	if vals := asStringSlice([]string{"x"}); len(vals) != 1 {
		t.Fatalf("asStringSlice []string path failed")
	}
}

func TestMiddlewareAutoExtractAndPersist(t *testing.T) {
	t.Parallel()
	list := NewTodoList()
	base, _, err := list.ApplyTasks([]TodoTask{{Title: "existing", Status: TodoPending}})
	if err != nil {
		t.Fatalf("seed list: %v", err)
	}
	snap := list.Snapshot()

	execCtx := NewExecutionContext(context.Background(), map[string]any{
		"todo.state": snap,
		"llm.out":    "- [ ] new task\n- [x] done task",
	}, nil)

	mw := NewTodoListMiddleware(WithTodoContextKeys("todo.state", "llm.out"))
	if err := mw.BeforeStepContext(execCtx, Step{Name: "s"}); err != nil {
		t.Fatalf("before: %v", err)
	}
	if err := mw.AfterStepContext(execCtx, Step{Name: "s"}, nil); err != nil {
		t.Fatalf("after: %v", err)
	}

	raw, ok := execCtx.Get("todo.state")
	if !ok {
		t.Fatalf("expected persisted state")
	}
	snap2, ok := raw.(TodoListSnapshot)
	if !ok {
		t.Fatalf("unexpected type: %T", raw)
	}
	if len(snap2.Tasks) != 3 {
		t.Fatalf("expected 3 tasks after ingest, got %d", len(snap2.Tasks))
	}
	if snap2.Tasks[0].ID != base[0].ID {
		t.Fatalf("existing task id changed: %s vs %s", snap2.Tasks[0].ID, base[0].ID)
	}
}

func TestMiddlewareRestoreVariants(t *testing.T) {
	t.Parallel()
	list := NewTodoList()
	_, _, err := list.ApplyTasks([]TodoTask{{Title: "persisted"}})
	if err != nil {
		t.Fatalf("seed list: %v", err)
	}
	snap := list.Snapshot()
	data, err := list.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	ctx := NewExecutionContext(context.Background(), nil, nil)
	mw := NewTodoListMiddleware(WithTodoList(NewTodoList()))
	if err := mw.restore(snap.Tasks); err != nil {
		t.Fatalf("restore tasks: %v", err)
	}
	if err := mw.restore(data); err != nil {
		t.Fatalf("restore bytes: %v", err)
	}
	if err := mw.restore(string(data)); err != nil {
		t.Fatalf("restore string: %v", err)
	}
	if len(mw.List()) == 0 {
		t.Fatalf("list helper returned empty slice")
	}
	mw.persist(nil)
	if mw.BeforeStep("noop") != nil || mw.AfterStep("noop") != nil {
		t.Fatalf("legacy hooks should be no-op")
	}

	ctx.Set(defaultTodoParseKey, []byte("- [ ] from bytes"))
	if err := mw.BeforeStepContext(ctx, Step{Name: "demo"}); err != nil {
		t.Fatalf("before context: %v", err)
	}
	if err := mw.AfterStepContext(ctx, Step{Name: "demo"}, nil); err != nil {
		t.Fatalf("after context: %v", err)
	}
	val, ok := ctx.Get(defaultTodoListKey)
	if !ok {
		t.Fatalf("expected persisted snapshot")
	}
	if _, ok := val.(TodoListSnapshot); !ok {
		t.Fatalf("unexpected persisted type: %T", val)
	}
	if len(mw.List()) == 0 {
		t.Fatalf("expected todo list populated")
	}
	if mw.List()[0].ID == "" {
		t.Fatalf("expected task ids")
	}
}

func TestMiddlewareProgressEmission(t *testing.T) {
	t.Parallel()
	progress := make(chan event.Event, 8)
	control := make(chan event.Event, 1)
	monitor := make(chan event.Event, 1)
	bus := event.NewEventBus(progress, control, monitor, event.WithBufferSize(1))

	mw := NewTodoListMiddleware(WithTodoEventBus(bus, "sess-1"))
	created, err := mw.ApplyText("- [ ] alpha\n- [ ] beta")
	if err != nil || len(created) != 2 {
		t.Fatalf("apply text: %v", err)
	}
	if _, err := mw.UpdateStatus(created[0].ID, TodoCompleted); err != nil {
		t.Fatalf("update status: %v", err)
	}
	if _, err := mw.Delete(created[1].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// allow async forwarder to pump messages
	time.Sleep(10 * time.Millisecond)

	count := len(progress)
	if count != 4 {
		t.Fatalf("expected 4 progress events, got %d", count)
	}
	for i := 0; i < count; i++ {
		evt := <-progress
		if evt.Type != event.EventProgress {
			t.Fatalf("unexpected event type: %s", evt.Type)
		}
		data, ok := evt.Data.(event.ProgressData)
		if !ok {
			t.Fatalf("unexpected data type: %T", evt.Data)
		}
		if data.Stage == "" {
			t.Fatalf("empty stage in event: %+v", data)
		}
	}
}
