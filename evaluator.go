package evalengine

import (
	"time"
)

// Result represents the outcome of a single evaluator run.
type Result struct {
	Name               string
	Passed             bool
	Error              string
	Resolution         string
	ResolutionWorkflow string
	Severity           string
	Category           string
}

// Evaluator is the interface for all evaluators.
type Evaluator interface {
	Name() string
	Reads() []FieldRef
	Writes() FieldRef
	CacheTTL() time.Duration
	ResolutionWorkflow() string
	Evaluate(activation map[string]any) Result
}
