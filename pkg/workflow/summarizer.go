package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/session"
)

const (
	defaultSummaryThreshold = 100000
	defaultKeepRecentTurns  = 5
)

// SummaryEntry captures a generated summary alongside bookkeeping metadata.
type SummaryEntry struct {
	SessionSummary string    `json:"session_summary"`
	StageSummary   string    `json:"stage_summary"`
	Created        time.Time `json:"created"`
	TokenCount     int       `json:"token_count"`
	Dropped        int       `json:"dropped"`
}

// SummaryResult is returned by a compression run.
type SummaryResult struct {
	Entry    SummaryEntry
	Messages []session.Message
}

// Summarizer performs hierarchical compression of chat transcripts.
type Summarizer struct {
	model      model.Model
	threshold  int
	keepRecent int
	now        func() time.Time
}

// NewSummarizer constructs a Summarizer with sane defaults.
func NewSummarizer(m model.Model, threshold, keepRecent int) Summarizer {
	if threshold <= 0 {
		threshold = defaultSummaryThreshold
	}
	if keepRecent <= 0 {
		keepRecent = defaultKeepRecentTurns
	}
	return Summarizer{
		model:      m,
		threshold:  threshold,
		keepRecent: keepRecent,
		now:        time.Now,
	}
}

func (s Summarizer) Threshold() int  { return s.threshold }
func (s Summarizer) KeepRecent() int { return s.keepRecent }

// ShouldCompress reports whether the transcript length crosses the threshold.
func (s Summarizer) ShouldCompress(msgs []session.Message) bool {
	if s.threshold <= 0 || len(msgs) == 0 {
		return false
	}
	return estimateTokens(msgs) > s.threshold
}

// Compress generates a layered summary and compacts the transcript.
// When force is true the threshold check is skipped.
func (s Summarizer) Compress(ctx context.Context, msgs []session.Message, force bool) (SummaryResult, bool, error) {
	if len(msgs) == 0 {
		return SummaryResult{}, false, nil
	}
	if !force && !s.ShouldCompress(msgs) {
		return SummaryResult{}, false, nil
	}
	if s.model == nil {
		return SummaryResult{}, false, errors.New("summarizer: model is nil")
	}

	keepRecent := s.keepRecent
	if keepRecent < 0 {
		keepRecent = 0
	}
	cutoff := len(msgs) - keepRecent
	if cutoff < 0 {
		cutoff = 0
	}

	preserved := make(map[int]bool, len(msgs))
	for i, msg := range msgs {
		if i >= cutoff || importantMessage(msg) {
			preserved[i] = true
		}
	}

	compressible := make([]session.Message, 0, len(msgs))
	for i, msg := range msgs {
		if !preserved[i] {
			compressible = append(compressible, msg)
		}
	}
	summarySource := compressible
	if len(summarySource) == 0 {
		summarySource = msgs
	}

	prompt := buildSummaryPrompt(summarySource)
	resp, err := s.model.Generate(ctx, []model.Message{{Role: "user", Content: prompt}})
	if err != nil {
		return SummaryResult{}, false, err
	}
	sessionText, stageText := parseSummary(resp.Content)
	entry := SummaryEntry{
		SessionSummary: strings.TrimSpace(sessionText),
		StageSummary:   strings.TrimSpace(stageText),
		Created:        s.now().UTC(),
		TokenCount:     estimateTokens(msgs),
		Dropped:        len(msgs) - len(preserved),
	}

	summaryMsg := session.Message{
		Role:      "system",
		Content:   fmt.Sprintf("Session summary:\n%s\n\nStage summary:\n%s", entry.SessionSummary, entry.StageSummary),
		Timestamp: entry.Created,
	}

	var older, recents []session.Message
	for i, msg := range msgs {
		if !preserved[i] {
			continue
		}
		if i < cutoff {
			older = append(older, msg)
		} else {
			recents = append(recents, msg)
		}
	}

	out := make([]session.Message, 0, len(older)+len(recents)+1)
	out = append(out, older...)
	out = append(out, summaryMsg)
	out = append(out, recents...)

	return SummaryResult{Entry: entry, Messages: out}, true, nil
}

func importantMessage(msg session.Message) bool {
	if len(msg.ToolCalls) == 0 {
		return false
	}
	for _, tc := range msg.ToolCalls {
		if tc.Error != "" || tc.Output != nil || tc.Name != "" {
			return true
		}
	}
	return false
}

func buildSummaryPrompt(msgs []session.Message) string {
	var sb strings.Builder
	sb.WriteString("Summarize the following conversation. Produce two concise sections labelled Session: and Stage:. ")
	sb.WriteString("Session should capture long-term facts and important tool outcomes. Stage should capture current goals, blockers, and next steps.\n\nHistory:\n")
	for _, m := range msgs {
		sb.WriteString("- ")
		sb.WriteString(strings.TrimSpace(m.Role))
		if txt := strings.TrimSpace(m.Content); txt != "" {
			sb.WriteString(": ")
			sb.WriteString(txt)
		}
		if len(m.ToolCalls) > 0 {
			sb.WriteString(" tool_calls=")
			sb.WriteString(renderToolCalls(m.ToolCalls))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderToolCalls(calls []session.ToolCall) string {
	parts := make([]string, 0, len(calls))
	for _, tc := range calls {
		segment := tc.Name
		if tc.Error != "" {
			segment += " err=" + tc.Error
		}
		if tc.Output != nil && tc.Error == "" {
			segment += " ok"
		}
		parts = append(parts, strings.TrimSpace(segment))
	}
	return strings.Join(parts, "; ")
}

func parseSummary(text string) (sessionSummary, stageSummary string) {
	lower := strings.ToLower(text)
	sessionIdx := strings.Index(lower, "session:")
	stageIdx := strings.Index(lower, "stage:")

	switch {
	case sessionIdx == -1 && stageIdx == -1:
		return strings.TrimSpace(text), ""
	case sessionIdx == -1:
		return strings.TrimSpace(text[:stageIdx]), strings.TrimSpace(text[stageIdx+len("stage:"):])
	case stageIdx == -1:
		return strings.TrimSpace(text[sessionIdx+len("session:"):]), ""
	}

	if sessionIdx < stageIdx {
		return strings.TrimSpace(text[sessionIdx+len("session:") : stageIdx]), strings.TrimSpace(text[stageIdx+len("stage:"):])
	}
	return strings.TrimSpace(text[sessionIdx+len("session:"):]), strings.TrimSpace(text[stageIdx+len("stage:") : sessionIdx])
}

func estimateTokens(msgs []session.Message) int {
	total := 0
	for _, msg := range msgs {
		total += roughTokens(msg.Content)
		for _, tc := range msg.ToolCalls {
			total += roughTokens(tc.Name)
			total += roughTokens(tc.Error)
			if tc.Output != nil {
				total += roughTokens(fmt.Sprint(tc.Output))
			}
		}
	}
	return total
}

func roughTokens(text string) int {
	if text == "" {
		return 0
	}
	runes := len([]rune(text))
	tokens := runes / 4
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}
