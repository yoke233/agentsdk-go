package workflow

import "errors"

// Middleware allows step interception. Legacy implementations remain valid.
type Middleware interface {
	BeforeStep(name string) error
	AfterStep(name string) error
}

// ContextMiddleware offers richer hooks with execution context.
type ContextMiddleware interface {
	BeforeStepContext(*ExecutionContext, Step) error
	AfterStepContext(*ExecutionContext, Step, error) error
}

// applyMiddleware executes the chain in order and runs fn between before/after hooks.
func applyMiddleware(chain []Middleware, ctx *ExecutionContext, step Step, fn func() error) error {
	for _, m := range chain {
		if cm, ok := m.(ContextMiddleware); ok {
			if err := cm.BeforeStepContext(ctx, step); err != nil {
				return err
			}
			continue
		}
		if err := m.BeforeStep(step.Name); err != nil {
			return err
		}
	}

	runErr := fn()

	for i := len(chain) - 1; i >= 0; i-- {
		m := chain[i]
		if cm, ok := m.(ContextMiddleware); ok {
			if err := cm.AfterStepContext(ctx, step, runErr); err != nil {
				return errors.Join(runErr, err)
			}
			continue
		}
		if err := m.AfterStep(step.Name); err != nil {
			return errors.Join(runErr, err)
		}
	}
	return runErr
}
