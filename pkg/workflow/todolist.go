package workflow

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var (
	ErrTodoNotFound = errors.New("workflow: todo task not found")
	ErrTodoInvalid  = errors.New("workflow: invalid todo task")
)

type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

func (s TodoStatus) Valid() bool {
	switch canonicalStatus(string(s)) {
	case TodoPending, TodoInProgress, TodoCompleted:
		return true
	default:
		return false
	}
}

func canonicalStatus(v string) TodoStatus {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "pending", "todo", "backlog":
		return TodoPending
	case "in_progress", "in-progress", "in progress", "doing", "wip":
		return TodoInProgress
	case "completed", "complete", "done", "finished":
		return TodoCompleted
	default:
		return TodoStatus("invalid")
	}
}

type TodoTask struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Status       TodoStatus `json:"status"`
	Dependencies []string   `json:"dependencies,omitempty"`
}

type TodoUpdate struct {
	ID           string     `json:"id"`
	Title        string     `json:"title,omitempty"`
	Status       TodoStatus `json:"status,omitempty"`
	Dependencies []string   `json:"dependencies,omitempty"`
	Delete       bool       `json:"delete,omitempty"`
}

type TodoList struct {
	mu    sync.RWMutex
	seq   int64
	order []string
	tasks map[string]TodoTask
}

func NewTodoList() *TodoList {
	return &TodoList{tasks: make(map[string]TodoTask)}
}

func (l *TodoList) Tasks() []TodoTask {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]TodoTask, 0, len(l.order))
	for _, id := range l.order {
		if task, ok := l.tasks[id]; ok {
			out = append(out, cloneTask(task))
		}
	}
	return out
}

func (l *TodoList) Get(id string) (TodoTask, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	task, ok := l.tasks[strings.TrimSpace(id)]
	if !ok {
		return TodoTask{}, false
	}
	return cloneTask(task), true
}

func (l *TodoList) AddTask(title string, deps []string) (TodoTask, error) {
	created, _, err := l.ApplyTasks([]TodoTask{{Title: title, Dependencies: deps}})
	if err != nil {
		return TodoTask{}, err
	}
	if len(created) == 0 {
		return TodoTask{}, ErrTodoInvalid
	}
	return created[0], nil
}

func (l *TodoList) ApplyTasks(tasks []TodoTask) (created []TodoTask, updated []TodoTask, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, task := range tasks {
		cleaned, sanitizeErr := sanitizeTask(task)
		if sanitizeErr != nil {
			return nil, nil, sanitizeErr
		}
		if cleaned.ID == "" {
			l.seq++
			cleaned.ID = fmt.Sprintf("task-%04d", l.seq)
		}
		existing, exists := l.tasks[cleaned.ID]
		if exists {
			if tasksEqual(existing, cleaned) {
				continue
			}
			l.tasks[cleaned.ID] = cleaned
			updated = append(updated, cloneTask(cleaned))
			continue
		}
		l.tasks[cleaned.ID] = cleaned
		l.order = append(l.order, cleaned.ID)
		created = append(created, cloneTask(cleaned))
	}
	return created, updated, nil
}

func (l *TodoList) ApplyUpdates(updates []TodoUpdate) (changed []TodoTask, deleted []string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, upd := range updates {
		id := strings.TrimSpace(upd.ID)
		if id == "" {
			return nil, nil, fmt.Errorf("%w: missing id", ErrTodoInvalid)
		}
		existing, ok := l.tasks[id]
		if !ok {
			return nil, nil, fmt.Errorf("%w: %s", ErrTodoNotFound, id)
		}
		if upd.Delete {
			delete(l.tasks, id)
			removeOrder(&l.order, id)
			deleted = append(deleted, id)
			continue
		}
		next := existing
		if trimmed := strings.TrimSpace(upd.Title); trimmed != "" {
			next.Title = trimmed
		}
		if upd.Status != "" {
			status := canonicalStatus(string(upd.Status))
			if status == "invalid" {
				return nil, nil, fmt.Errorf("%w: status %q", ErrTodoInvalid, upd.Status)
			}
			next.Status = status
		}
		if len(upd.Dependencies) > 0 {
			next.Dependencies = normalizeDeps(upd.Dependencies)
		}
		if tasksEqual(existing, next) {
			continue
		}
		l.tasks[id] = next
		changed = append(changed, cloneTask(next))
	}
	return changed, deleted, nil
}

func (l *TodoList) DeleteTask(id string) (TodoTask, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	existing, ok := l.tasks[strings.TrimSpace(id)]
	if !ok {
		return TodoTask{}, ErrTodoNotFound
	}
	delete(l.tasks, existing.ID)
	removeOrder(&l.order, existing.ID)
	return cloneTask(existing), nil
}

type TodoListSnapshot struct {
	Seq   int64      `json:"seq"`
	Tasks []TodoTask `json:"tasks"`
}

func (l *TodoList) Snapshot() TodoListSnapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	snap := TodoListSnapshot{Seq: l.seq, Tasks: make([]TodoTask, 0, len(l.order))}
	for _, id := range l.order {
		if task, ok := l.tasks[id]; ok {
			snap.Tasks = append(snap.Tasks, cloneTask(task))
		}
	}
	return snap
}

func (l *TodoList) Restore(snapshot TodoListSnapshot) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	restored := make(map[string]TodoTask, len(snapshot.Tasks))
	order := make([]string, 0, len(snapshot.Tasks))
	for _, task := range snapshot.Tasks {
		cleaned, err := sanitizeTask(task)
		if err != nil {
			return err
		}
		if cleaned.ID == "" {
			l.seq++
			cleaned.ID = fmt.Sprintf("task-%04d", l.seq)
		}
		restored[cleaned.ID] = cleaned
		order = append(order, cleaned.ID)
	}
	l.tasks = restored
	l.order = order
	if snapshot.Seq > 0 {
		l.seq = snapshot.Seq
	} else {
		l.seq = int64(len(order))
	}
	return nil
}

func (l *TodoList) MarshalBinary() ([]byte, error) {
	snap := l.Snapshot()
	return json.Marshal(snap)
}

func (l *TodoList) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var snap TodoListSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	return l.Restore(snap)
}

func sanitizeTask(task TodoTask) (TodoTask, error) {
	title := strings.TrimSpace(task.Title)
	if title == "" {
		return TodoTask{}, fmt.Errorf("%w: empty title", ErrTodoInvalid)
	}
	status := canonicalStatus(string(task.Status))
	if status == "invalid" {
		status = TodoPending
	}
	cleaned := TodoTask{
		ID:           strings.TrimSpace(task.ID),
		Title:        title,
		Status:       status,
		Dependencies: normalizeDeps(task.Dependencies),
	}
	return cleaned, nil
}

func normalizeDeps(deps []string) []string {
	if len(deps) == 0 {
		return nil
	}
	uniq := make([]string, 0, len(deps))
	seen := make(map[string]struct{}, len(deps))
	for _, dep := range deps {
		trimmed := strings.TrimSpace(strings.TrimPrefix(dep, "#"))
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		uniq = append(uniq, trimmed)
	}
	if len(uniq) == 0 {
		return nil
	}
	return uniq
}

func tasksEqual(a, b TodoTask) bool {
	if a.ID != b.ID || a.Title != b.Title || a.Status != b.Status {
		return false
	}
	if len(a.Dependencies) != len(b.Dependencies) {
		return false
	}
	for i := range a.Dependencies {
		if a.Dependencies[i] != b.Dependencies[i] {
			return false
		}
	}
	return true
}

func cloneTask(task TodoTask) TodoTask {
	out := task
	if len(task.Dependencies) > 0 {
		out.Dependencies = append([]string(nil), task.Dependencies...)
	}
	return out
}

func removeOrder(order *[]string, id string) {
	list := *order
	for i, item := range list {
		if item == id {
			*order = append(list[:i], list[i+1:]...)
			return
		}
	}
}

var (
	checklistPattern = regexp.MustCompile(`^(?:[-*+]|\d+[.)])?\s*\[([ xX>~-])\]\s*(.+)$`)
	leadingPattern   = regexp.MustCompile(`^(?:[-*+•]|\d+[.)])\s*`)
	depsPattern      = regexp.MustCompile(`(?i)(?:deps?|depends(?:\s+on)?)[:=]\s*([#\w,\s-]+)`)
)

func ExtractTodoTasks(raw string) []TodoTask {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if tasks := parseJSONTasks(trimmed); len(tasks) > 0 {
		return tasks
	}
	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	buf := make([]byte, 0, 1024)
	scanner.Buffer(buf, 1024*1024)
	var tasks []TodoTask
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if task, ok := parseLine(line); ok {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func parseLine(line string) (TodoTask, bool) {
	if match := checklistPattern.FindStringSubmatch(line); len(match) == 3 {
		title, deps := stripDependencies(match[2])
		title, status := stripStatusHint(title, statusFromMark(match[1]))
		if title == "" {
			return TodoTask{}, false
		}
		return TodoTask{Title: title, Status: status, Dependencies: deps}, true
	}
	stripped := leadingPattern.ReplaceAllString(line, "")
	title, deps := stripDependencies(stripped)
	title, status := stripStatusHint(title, TodoPending)
	if title == "" {
		return TodoTask{}, false
	}
	return TodoTask{Title: title, Status: status, Dependencies: deps}, true
}

func stripDependencies(text string) (string, []string) {
	if !depsPattern.MatchString(text) {
		return strings.TrimSpace(text), nil
	}
	deps := []string{}
	cleaned := depsPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := depsPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return ""
		}
		for _, token := range strings.FieldsFunc(parts[1], func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		}) {
			token = strings.TrimSpace(strings.TrimPrefix(token, "#"))
			if token != "" {
				deps = append(deps, token)
			}
		}
		return ""
	})
	return strings.TrimSpace(cleaned), normalizeDeps(deps)
}

func stripStatusHint(text string, fallback TodoStatus) (string, TodoStatus) {
	title := strings.TrimSpace(text)
	if title == "" {
		return "", fallback
	}
	if inner, ok := statusFromSuffix(title, '(', ')'); ok {
		return inner.text, inner.status
	}
	if inner, ok := statusFromSuffix(title, '[', ']'); ok {
		return inner.text, inner.status
	}
	if idx := strings.LastIndex(title, " - "); idx != -1 {
		token := strings.TrimSpace(title[idx+3:])
		if status := canonicalStatus(token); status != "invalid" {
			return strings.TrimSpace(title[:idx]), status
		}
	}
	if parts := strings.Split(strings.ToLower(title), "status:"); len(parts) == 2 {
		if status := canonicalStatus(parts[1]); status != "invalid" {
			return strings.TrimSpace(parts[0]), status
		}
	}
	return title, fallback
}

type statusResult struct {
	text   string
	status TodoStatus
}

func statusFromSuffix(title string, open, close rune) (statusResult, bool) {
	if !strings.HasSuffix(title, string(close)) {
		return statusResult{}, false
	}
	idx := strings.LastIndex(title, string(open))
	if idx == -1 {
		return statusResult{}, false
	}
	token := strings.TrimSpace(title[idx+1 : len(title)-1])
	status := canonicalStatus(token)
	if status == "invalid" {
		return statusResult{}, false
	}
	return statusResult{text: strings.TrimSpace(title[:idx]), status: status}, true
}

func statusFromMark(mark string) TodoStatus {
	switch strings.TrimSpace(strings.ToLower(mark)) {
	case "x", "*", "✔":
		return TodoCompleted
	case ">", "-", "~":
		return TodoInProgress
	default:
		return TodoPending
	}
}

func parseJSONTasks(payload string) []TodoTask {
	if len(payload) == 0 {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(payload), &arr); err != nil {
		return nil
	}
	var tasks []TodoTask
	for _, item := range arr {
		task := TodoTask{}
		if v, ok := item["id"].(string); ok {
			task.ID = v
		}
		if v, ok := item["title"].(string); ok {
			task.Title = v
		}
		if v, ok := item["name"].(string); ok && task.Title == "" {
			task.Title = v
		}
		if v, ok := item["status"].(string); ok {
			task.Status = TodoStatus(v)
		}
		if deps := asStringSlice(item["dependencies"]); len(deps) > 0 {
			task.Dependencies = deps
		}
		if cleaned, err := sanitizeTask(task); err == nil {
			tasks = append(tasks, cleaned)
		}
	}
	return tasks
}

func asStringSlice(v any) []string {
	switch val := v.(type) {
	case []string:
		return append([]string(nil), val...)
	case []any:
		out := make([]string, 0, len(val))
		for _, entry := range val {
			if s, ok := entry.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
