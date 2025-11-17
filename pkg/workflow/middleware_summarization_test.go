package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/session"
)

func TestSummarizerCompressKeepsRecentsAndImportant(t *testing.T) {
	t.Parallel()
	llm := &fakeModel{response: "Session: keep state\nStage: ship feature"}
	sum := NewSummarizer(llm, 1, 2)
	fixed := time.Unix(1700000000, 0).UTC()
	sum.now = func() time.Time { return fixed }

	msgs := []session.Message{
		{Role: "user", Content: strings.Repeat("a", 20)},
		{Role: "assistant", Content: "tool output", ToolCalls: []session.ToolCall{{Name: "bash", Output: "ls"}}},
		{Role: "user", Content: "noise"},
		{Role: "assistant", Content: "calc", ToolCalls: []session.ToolCall{{Name: "calc", Error: "boom"}}},
		{Role: "user", Content: "recent update"},
		{Role: "assistant", Content: "latest result"},
	}

	res, compressed, err := sum.Compress(context.Background(), msgs, false)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if !compressed {
		t.Fatalf("expected compression")
	}
	if len(res.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(res.Messages))
	}
	if res.Messages[2].Role != "system" {
		t.Fatalf("expected summary message at index 2, got role %s", res.Messages[2].Role)
	}
	if res.Messages[0].Content != "tool output" || len(res.Messages[0].ToolCalls) != 1 {
		t.Fatalf("important message missing: %+v", res.Messages[0])
	}
	if res.Messages[3].Content != "recent update" || res.Messages[4].Content != "latest result" {
		t.Fatalf("recent turns lost: %+v", res.Messages[3:])
	}
	if res.Entry.SessionSummary != "keep state" || res.Entry.StageSummary != "ship feature" {
		t.Fatalf("unexpected summary entry: %+v", res.Entry)
	}
	if res.Entry.Dropped != 2 {
		t.Fatalf("expected dropped=2, got %d", res.Entry.Dropped)
	}
	if !res.Entry.Created.Equal(fixed) {
		t.Fatalf("expected fixed timestamp, got %v", res.Entry.Created)
	}
	if llm.calls != 1 {
		t.Fatalf("expected generate call")
	}
}

func TestSummarizerRespectsThresholdAndNilModel(t *testing.T) {
	t.Parallel()
	llm := &fakeModel{response: "Session: a\nStage: b"}
	sum := NewSummarizer(llm, 1000, 1)
	msgs := []session.Message{{Role: "user", Content: "short"}}

	if sum.ShouldCompress(nil) {
		t.Fatalf("should not compress empty transcript")
	}
	if sum.ShouldCompress(msgs) {
		t.Fatalf("should not trigger above threshold")
	}
	_, compressed, err := sum.Compress(context.Background(), msgs, false)
	if err != nil {
		t.Fatalf("compress below threshold: %v", err)
	}
	if compressed {
		t.Fatalf("unexpected compression when below threshold")
	}
	if llm.calls != 0 {
		t.Fatalf("model should not be invoked when skipping compression")
	}

	noModel := NewSummarizer(nil, 1, 1)
	noModel.now = func() time.Time { return time.Now().UTC() }
	if _, compressed, err := noModel.Compress(context.Background(), msgs, true); err == nil || compressed {
		t.Fatalf("expected error for nil model, got %v compressed=%v", err, compressed)
	}
}

func TestSummarizationMiddlewareAutoCompress(t *testing.T) {
	t.Parallel()
	llm := &fakeModel{response: "Session: stable\nStage: shipping"}
	mw := NewSummarizationMiddleware(llm, WithSummaryThreshold(1), WithSummaryKeepRecent(1))
	fixed := time.Unix(42, 0).UTC()
	mw.summarizer.now = func() time.Time { return fixed }
	msgs := []session.Message{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "ack"},
	}
	execCtx := NewExecutionContext(context.Background(), map[string]any{
		defaultSummaryMessagesKey: msgs,
	}, nil)

	if err := mw.BeforeStepContext(execCtx, Step{Name: "alpha"}); err != nil {
		t.Fatalf("before step: %v", err)
	}
	rawMsgs, ok := execCtx.Get(defaultSummaryMessagesKey)
	if !ok {
		t.Fatalf("messages not persisted")
	}
	sliced, ok := rawMsgs.([]session.Message)
	if !ok || len(sliced) != 2 {
		t.Fatalf("unexpected stored messages: %#v", rawMsgs)
	}
	if sliced[0].Role != "system" {
		t.Fatalf("expected summary inserted, got %+v", sliced[0])
	}

	entryRaw, ok := execCtx.Get(defaultSummaryKey)
	if !ok {
		t.Fatalf("summary entry missing")
	}
	entry, ok := entryRaw.(SummaryEntry)
	if !ok {
		t.Fatalf("unexpected entry type: %T", entryRaw)
	}
	if entry.StageSummary != "shipping" || entry.SessionSummary != "stable" {
		t.Fatalf("entry mismatch: %+v", entry)
	}
	if !entry.Created.Equal(fixed) {
		t.Fatalf("timestamp mismatch: %v", entry.Created)
	}

	historyRaw, ok := execCtx.Get(defaultSummaryHistoryKey)
	if !ok {
		t.Fatalf("history missing")
	}
	history := historyRaw.([]SummaryEntry)
	if len(history) != 1 {
		t.Fatalf("expected history length 1, got %d", len(history))
	}
	view := mw.History(execCtx)
	view[0].SessionSummary = "mutated"
	if storedRaw, ok := execCtx.Get(defaultSummaryHistoryKey); ok {
		if stored := storedRaw.([]SummaryEntry); stored[0].SessionSummary != "stable" {
			t.Fatalf("history should be immutable copy")
		}
	}
	if llm.calls != 1 {
		t.Fatalf("model not invoked")
	}
}

func TestSummarizationMiddlewareManualTriggerAndConversion(t *testing.T) {
	t.Parallel()
	llm := &fakeModel{response: "Session: manual\nStage: cleanup"}
	mw := NewSummarizationMiddleware(llm, WithSummaryThreshold(1_000_000))
	execCtx := NewExecutionContext(context.Background(), map[string]any{
		defaultSummaryMessagesKey: []session.Message{{Role: "user", Content: "tiny"}},
		defaultSummaryManualKey:   true,
	}, nil)

	if err := mw.BeforeStepContext(execCtx, Step{Name: "beta"}); err != nil {
		t.Fatalf("manual compression: %v", err)
	}
	raw, _ := execCtx.Get(defaultSummaryManualKey)
	if flag, _ := raw.(bool); flag {
		t.Fatalf("manual flag not cleared")
	}
	entryRaw, ok := execCtx.Get(defaultSummaryKey)
	if !ok {
		t.Fatalf("manual summary entry missing")
	}
	entry := entryRaw.(SummaryEntry)
	if entry.SessionSummary != "manual" {
		t.Fatalf("manual entry mismatch: %+v", entry)
	}
	if history := mw.History(execCtx); len(history) != 1 {
		t.Fatalf("history not recorded")
	}
}

func TestSummarizationMiddlewareTriggerWithModelMessages(t *testing.T) {
	t.Parallel()
	llm := &fakeModel{response: "Session: convert\nStage: ready"}
	mw := NewSummarizationMiddleware(llm, WithSummaryThreshold(1))
	args := map[string]any{"x": 1}
	execCtx := NewExecutionContext(context.Background(), map[string]any{
		defaultSummaryMessagesKey: []model.Message{
			{Role: "assistant", Content: "tool run", ToolCalls: []model.ToolCall{{ID: "1", Name: "calc", Arguments: args}}},
		},
	}, nil)

	if err := mw.Trigger(execCtx); err != nil {
		t.Fatalf("trigger compress: %v", err)
	}
	args["x"] = 999
	rawMsgs, _ := execCtx.Get(defaultSummaryMessagesKey)
	msgs := rawMsgs.([]session.Message)
	found := false
	for _, msg := range msgs {
		if msg.Role == "system" || len(msg.ToolCalls) == 0 {
			continue
		}
		if msg.ToolCalls[0].Arguments["x"].(int) != 1 {
			t.Fatalf("arguments not cloned: %+v", msg.ToolCalls[0].Arguments)
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("expected preserved message with tool call, got %+v", msgs)
	}
	if entry := mw.History(execCtx); len(entry) != 1 {
		t.Fatalf("history missing after trigger")
	}
}

func TestSummarizationMiddlewareOptionOverridesAndCloning(t *testing.T) {
	t.Parallel()
	llm := &fakeModel{response: "Session: opt\nStage: stage"}
	customMessages := "ctx.msgs"
	customSummary := "ctx.summary"
	customHistory := "ctx.history"
	customManual := "ctx.manual"
	base := []session.Message{
		{Role: "user", Content: strings.Repeat("z", 200)},
		{Role: "assistant", Content: "with tool", ToolCalls: []session.ToolCall{{
			Name:      "bash",
			Arguments: map[string]any{"cmd": "ls"},
			Metadata:  map[string]any{"source": "orig"},
			Output:    "ok",
		}}},
	}
	mw := NewSummarizationMiddleware(nil,
		WithSummaryModel(llm),
		WithSummaryThreshold(10_000),
		WithSummaryKeepRecent(1),
		WithSummaryKeys(customMessages, customSummary, customHistory),
		WithSummaryManualKey(customManual),
	)
	execCtx := NewExecutionContext(context.Background(), map[string]any{
		customMessages: cloneSessionMessages(base),
		customManual:   true,
	}, nil)

	if mw.History(nil) != nil {
		t.Fatalf("nil history should return nil")
	}
	if err := mw.BeforeStep("noop"); err != nil {
		t.Fatalf("before legacy hook: %v", err)
	}
	if err := mw.AfterStep("noop"); err != nil {
		t.Fatalf("after legacy hook: %v", err)
	}
	if err := mw.AfterStepContext(execCtx, Step{Name: "noop"}, nil); err != nil {
		t.Fatalf("after context: %v", err)
	}
	if err := mw.BeforeStepContext(execCtx, Step{Name: "compress"}); err != nil {
		t.Fatalf("before context: %v", err)
	}

	rawMessages, ok := execCtx.Get(customMessages)
	if !ok {
		t.Fatalf("expected custom messages key")
	}
	stored := rawMessages.([]session.Message)
	if len(stored) != 2 || stored[0].Role != "system" {
		t.Fatalf("unexpected stored messages: %+v", stored)
	}
	origArgs := stored[1].ToolCalls[0].Arguments["cmd"].(string)
	if origArgs != "ls" {
		t.Fatalf("tool call not preserved: %s", origArgs)
	}

	base[1].ToolCalls[0].Arguments["cmd"] = "rm"
	base[1].ToolCalls[0].Metadata["source"] = "mutated"
	if stored[1].ToolCalls[0].Arguments["cmd"].(string) != "ls" {
		t.Fatalf("arguments not cloned: %+v", stored[1].ToolCalls[0].Arguments)
	}
	if stored[1].ToolCalls[0].Metadata["source"].(string) != "orig" {
		t.Fatalf("metadata not cloned: %+v", stored[1].ToolCalls[0].Metadata)
	}

	summaryRaw, ok := execCtx.Get(customSummary)
	if !ok {
		t.Fatalf("summary entry missing for custom key")
	}
	entry := summaryRaw.(SummaryEntry)
	if entry.SessionSummary != "opt" || entry.StageSummary != "stage" {
		t.Fatalf("custom entry mismatch: %+v", entry)
	}
	flagRaw, ok := execCtx.Get(customManual)
	if !ok || flagRaw.(bool) {
		t.Fatalf("manual flag should be cleared, got %v", flagRaw)
	}
	historyRaw, ok := execCtx.Get(customHistory)
	if !ok {
		t.Fatalf("custom history missing")
	}
	history := historyRaw.([]SummaryEntry)
	if len(history) != 1 {
		t.Fatalf("expected history entries, got %d", len(history))
	}
	if llm.calls == 0 {
		t.Fatalf("model should be invoked through WithSummaryModel")
	}
}

func TestSummarizationMiddlewareErrors(t *testing.T) {
	t.Parallel()
	llm := &fakeModel{response: "Session: err\nStage: err"}
	mw := NewSummarizationMiddleware(llm)
	if err := mw.BeforeStepContext(nil, Step{}); err == nil {
		t.Fatalf("expected nil context error")
	}
	ctx := NewExecutionContext(context.Background(), map[string]any{
		defaultSummaryMessagesKey: 123,
	}, nil)
	if err := mw.BeforeStepContext(ctx, Step{}); err == nil {
		t.Fatalf("expected type error for messages")
	}
	if err := mw.Trigger(nil); err == nil {
		t.Fatalf("expected trigger error on nil context")
	}
}

type fakeModel struct {
	response string
	err      error
	calls    int
}

func (m *fakeModel) Generate(_ context.Context, messages []model.Message) (model.Message, error) {
	m.calls++
	if m.err != nil {
		return model.Message{}, m.err
	}
	if len(messages) != 1 {
		return model.Message{}, errors.New("unexpected prompt count")
	}
	return model.Message{Role: "assistant", Content: m.response}, nil
}

func (m *fakeModel) GenerateStream(context.Context, []model.Message, model.StreamCallback) error {
	return errors.New("not implemented")
}
