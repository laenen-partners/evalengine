package evalengine

import "time"

// Result represents the outcome of a single evaluator run.
type Result struct {
	Name                 string
	DisplayName          string
	Passed               bool
	Pending              bool
	Error                string
	Resolution           string
	ResolutionWorkflow   string
	Severity             string
	Category             string
	FailureMode          string
	PendingPreconditions []string
}

// Evaluator is the interface for all evaluators.
type Evaluator interface {
	Name() string
	DisplayName() string
	Reads() []FieldRef
	Writes() FieldRef
	CacheTTL() time.Duration
	Resolution() string
	ResolutionWorkflow() string
	Severity() string
	Category() string
	FailureMode() string
	HasPreconditions() bool
	EvaluatePreconditions(activation map[string]any) []string
	Evaluate(activation map[string]any) Result
}
