package workflow

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/event"
)

const (
	defaultTodoListKey  = "workflow.todo.list"
	defaultTodoParseKey = "workflow.todo.text"
)

type TodoListMiddleware struct {
	list      *TodoList
	listKey   string
	parseKey  string
	bus       *event.EventBus
	sessionID string
	loaded    bool
}

type TodoMiddlewareOption func(*TodoListMiddleware)

func NewTodoListMiddleware(opts ...TodoMiddlewareOption) *TodoListMiddleware {
	mw := &TodoListMiddleware{list: NewTodoList(), listKey: defaultTodoListKey, parseKey: defaultTodoParseKey}
	for _, opt := range opts {
		if opt != nil {
			opt(mw)
		}
	}
	return mw
}

func WithTodoEventBus(bus *event.EventBus, sessionID string) TodoMiddlewareOption {
	return func(mw *TodoListMiddleware) {
		mw.bus = bus
		mw.sessionID = sessionID
	}
}

func WithTodoContextKeys(listKey, parseKey string) TodoMiddlewareOption {
	return func(mw *TodoListMiddleware) {
		if strings.TrimSpace(listKey) != "" {
			mw.listKey = listKey
		}
		if strings.TrimSpace(parseKey) != "" {
			mw.parseKey = parseKey
		}
	}
}

func WithTodoList(list *TodoList) TodoMiddlewareOption {
	return func(mw *TodoListMiddleware) {
		if list != nil {
			mw.list = list
		}
	}
}

func (mw *TodoListMiddleware) BeforeStepContext(ctx *ExecutionContext, _ Step) error {
	if ctx == nil {
		return errors.New("execution context is nil")
	}
	if mw.list == nil {
		mw.list = NewTodoList()
	}
	if mw.loaded {
		return nil
	}
	if raw, ok := ctx.Get(mw.listKey); ok {
		_ = mw.restore(raw)
	}
	mw.loaded = true
	return nil
}

func (mw *TodoListMiddleware) AfterStepContext(ctx *ExecutionContext, _ Step, _ error) error {
	if ctx == nil {
		return errors.New("execution context is nil")
	}
	created := mw.extractFromContext(ctx)
	if len(created) > 0 {
		mw.emit("todo_added", fmt.Sprintf("%d task(s) captured", len(created)), map[string]any{"count": len(created)})
	}
	mw.persist(ctx)
	return nil
}

func (mw *TodoListMiddleware) BeforeStep(_ string) error { return nil }
func (mw *TodoListMiddleware) AfterStep(_ string) error  { return nil }

func (mw *TodoListMiddleware) List() []TodoTask { return mw.list.Tasks() }

func (mw *TodoListMiddleware) ApplyText(text string) ([]TodoTask, error) {
	tasks := ExtractTodoTasks(text)
	created, _, err := mw.list.ApplyTasks(tasks)
	if err != nil {
		return nil, err
	}
	for _, task := range created {
		mw.emit("todo_added", task.Title, map[string]any{"id": task.ID, "status": task.Status})
	}
	return created, nil
}

func (mw *TodoListMiddleware) UpdateStatus(id string, status TodoStatus) (TodoTask, error) {
	updates := []TodoUpdate{{ID: id, Status: status}}
	changed, _, err := mw.list.ApplyUpdates(updates)
	if err != nil {
		return TodoTask{}, err
	}
	if len(changed) == 0 {
		return TodoTask{}, ErrTodoNotFound
	}
	task := changed[0]
	mw.emit("todo_status", task.Title, map[string]any{"id": task.ID, "status": task.Status})
	return task, nil
}

func (mw *TodoListMiddleware) Delete(id string) (TodoTask, error) {
	task, err := mw.list.DeleteTask(id)
	if err != nil {
		return TodoTask{}, err
	}
	mw.emit("todo_deleted", task.Title, map[string]any{"id": task.ID})
	return task, nil
}

func (mw *TodoListMiddleware) persist(ctx *ExecutionContext) {
	if ctx == nil {
		return
	}
	ctx.Set(mw.listKey, mw.list.Snapshot())
}

func (mw *TodoListMiddleware) restore(raw any) error {
	switch val := raw.(type) {
	case TodoListSnapshot:
		return mw.list.Restore(val)
	case []TodoTask:
		return mw.list.Restore(TodoListSnapshot{Tasks: val})
	case []byte:
		return mw.list.UnmarshalBinary(val)
	case string:
		return mw.list.UnmarshalBinary([]byte(val))
	default:
		return nil
	}
}

func (mw *TodoListMiddleware) extractFromContext(ctx *ExecutionContext) []TodoTask {
	raw, ok := ctx.Get(mw.parseKey)
	if !ok {
		return nil
	}
	var text string
	switch v := raw.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return nil
	}
	created, _ := mw.ApplyText(text)
	return created
}

func (mw *TodoListMiddleware) emit(stage, message string, details map[string]any) {
	if mw.bus == nil {
		return
	}
	_ = mw.bus.Emit(event.NewEvent(event.EventProgress, mw.sessionID, event.ProgressData{
		Stage:   stage,
		Message: message,
		Details: details,
	}))
}
