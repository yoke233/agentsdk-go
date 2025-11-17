package workflow

import "errors"

// Condition evaluates whether a transition can be taken.
// Returning an error aborts the workflow immediately.
type Condition func(*ExecutionContext) (bool, error)

// Transition describes a directed edge between two nodes with an optional condition.
type Transition struct {
	From      string
	To        string
	Condition Condition
}

// Allows returns true when the condition passes (or no condition exists).
func (t Transition) Allows(ctx *ExecutionContext) (bool, error) {
	if ctx == nil {
		return false, errors.New("execution context is nil")
	}
	if t.Condition == nil {
		return true, nil
	}
	return t.Condition(ctx)
}

// Always returns a condition that always evaluates to true.
func Always() Condition {
	return func(*ExecutionContext) (bool, error) { return true, nil }
}
