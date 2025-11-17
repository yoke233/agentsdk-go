package workflow

import (
	"errors"
	"fmt"

	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/session"
)

const (
	defaultSummaryMessagesKey = "workflow.summary.messages"
	defaultSummaryKey         = "workflow.summary.current"
	defaultSummaryHistoryKey  = "workflow.summary.history"
	defaultSummaryManualKey   = "workflow.summary.manual"
)

// SummarizationMiddleware performs context compression before each step.
type SummarizationMiddleware struct {
	summarizer Summarizer

	messagesKey string
	summaryKey  string
	historyKey  string
	manualKey   string
}

// SummarizationOption customizes middleware behavior.
type SummarizationOption func(*SummarizationMiddleware)

// NewSummarizationMiddleware wires a Summarizer into the middleware chain.
func NewSummarizationMiddleware(m model.Model, opts ...SummarizationOption) *SummarizationMiddleware {
	mw := &SummarizationMiddleware{
		summarizer:  NewSummarizer(m, defaultSummaryThreshold, defaultKeepRecentTurns),
		messagesKey: defaultSummaryMessagesKey,
		summaryKey:  defaultSummaryKey,
		historyKey:  defaultSummaryHistoryKey,
		manualKey:   defaultSummaryManualKey,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(mw)
		}
	}
	return mw
}

// WithSummaryThreshold overrides the token threshold.
func WithSummaryThreshold(tokens int) SummarizationOption {
	return func(mw *SummarizationMiddleware) {
		if tokens > 0 {
			mw.summarizer.threshold = tokens
		}
	}
}

// WithSummaryKeepRecent controls how many recent turns are retained verbatim.
func WithSummaryKeepRecent(n int) SummarizationOption {
	return func(mw *SummarizationMiddleware) {
		if n > 0 {
			mw.summarizer.keepRecent = n
		}
	}
}

// WithSummaryKeys overrides context keys for messages, current summary, and history.
func WithSummaryKeys(messagesKey, summaryKey, historyKey string) SummarizationOption {
	return func(mw *SummarizationMiddleware) {
		if messagesKey != "" {
			mw.messagesKey = messagesKey
		}
		if summaryKey != "" {
			mw.summaryKey = summaryKey
		}
		if historyKey != "" {
			mw.historyKey = historyKey
		}
	}
}

// WithSummaryManualKey sets the flag key used to trigger manual compression.
func WithSummaryManualKey(key string) SummarizationOption {
	return func(mw *SummarizationMiddleware) {
		if key != "" {
			mw.manualKey = key
		}
	}
}

// WithSummaryModel swaps the underlying model.
func WithSummaryModel(m model.Model) SummarizationOption {
	return func(mw *SummarizationMiddleware) {
		if m != nil {
			mw.summarizer.model = m
		}
	}
}

// Trigger performs a forced compression, ignoring the threshold.
func (mw *SummarizationMiddleware) Trigger(ctx *ExecutionContext) error {
	return mw.compress(ctx, true)
}

// History returns prior summaries captured in the execution context.
func (mw *SummarizationMiddleware) History(ctx *ExecutionContext) []SummaryEntry {
	if ctx == nil {
		return nil
	}
	raw, ok := ctx.Get(mw.historyKey)
	if !ok {
		return nil
	}
	entries, ok := raw.([]SummaryEntry)
	if !ok {
		return nil
	}
	cpy := make([]SummaryEntry, len(entries))
	copy(cpy, entries)
	return cpy
}

// BeforeStepContext checks whether compression is needed prior to execution.
func (mw *SummarizationMiddleware) BeforeStepContext(ctx *ExecutionContext, _ Step) error {
	return mw.compress(ctx, mw.consumeManualFlag(ctx))
}

// AfterStepContext is a no-op to satisfy ContextMiddleware.
func (*SummarizationMiddleware) AfterStepContext(*ExecutionContext, Step, error) error { return nil }
func (*SummarizationMiddleware) BeforeStep(string) error                               { return nil }
func (*SummarizationMiddleware) AfterStep(string) error                                { return nil }

func (mw *SummarizationMiddleware) compress(ctx *ExecutionContext, force bool) error {
	if ctx == nil {
		return errors.New("execution context is nil")
	}
	msgs, err := mw.loadMessages(ctx)
	if err != nil {
		return err
	}
	res, compressed, err := mw.summarizer.Compress(ctx.Context(), msgs, force)
	if err != nil {
		return err
	}
	if !compressed {
		return nil
	}
	ctx.Set(mw.messagesKey, res.Messages)
	ctx.Set(mw.summaryKey, res.Entry)
	mw.appendHistory(ctx, res.Entry)
	return nil
}

func (mw *SummarizationMiddleware) loadMessages(ctx *ExecutionContext) ([]session.Message, error) {
	raw, ok := ctx.Get(mw.messagesKey)
	if !ok || raw == nil {
		return nil, nil
	}
	switch val := raw.(type) {
	case []session.Message:
		return cloneSessionMessages(val), nil
	case []model.Message:
		return convertModelMessages(val), nil
	default:
		return nil, fmt.Errorf("summarization: unexpected messages type %T", raw)
	}
}

func (mw *SummarizationMiddleware) appendHistory(ctx *ExecutionContext, entry SummaryEntry) {
	raw, _ := ctx.Get(mw.historyKey)
	var history []SummaryEntry
	if existing, ok := raw.([]SummaryEntry); ok {
		history = append(history, existing...)
	}
	history = append(history, entry)
	ctx.Set(mw.historyKey, history)
}

func (mw *SummarizationMiddleware) consumeManualFlag(ctx *ExecutionContext) bool {
	if ctx == nil {
		return false
	}
	raw, ok := ctx.Get(mw.manualKey)
	if !ok {
		return false
	}
	flag, _ := raw.(bool)
	if flag {
		ctx.Set(mw.manualKey, false)
	}
	return flag
}

func cloneSessionMessages(src []session.Message) []session.Message {
	out := make([]session.Message, len(src))
	for i, msg := range src {
		out[i] = session.Message{
			ID:        msg.ID,
			Role:      msg.Role,
			Content:   msg.Content,
			ToolCalls: cloneToolCalls(msg.ToolCalls),
			Timestamp: msg.Timestamp,
		}
	}
	return out
}

func cloneToolCalls(src []session.ToolCall) []session.ToolCall {
	if len(src) == 0 {
		return nil
	}
	out := make([]session.ToolCall, len(src))
	for i, tc := range src {
		out[i] = tc
		if tc.Arguments != nil {
			out[i].Arguments = make(map[string]any, len(tc.Arguments))
			for k, v := range tc.Arguments {
				out[i].Arguments[k] = v
			}
		}
		if tc.Metadata != nil {
			out[i].Metadata = make(map[string]any, len(tc.Metadata))
			for k, v := range tc.Metadata {
				out[i].Metadata[k] = v
			}
		}
	}
	return out
}

func convertModelMessages(msgs []model.Message) []session.Message {
	out := make([]session.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = session.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
		if len(msg.ToolCalls) > 0 {
			out[i].ToolCalls = make([]session.ToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				out[i].ToolCalls[j] = session.ToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: cloneArgs(tc.Arguments),
				}
			}
		}
	}
	return out
}

func cloneArgs(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
