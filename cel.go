package evalengine

import (
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
)

// CELEvaluator implements Evaluator using a compiled CEL program.
type CELEvaluator struct {
	def     EvalDefinition
	program cel.Program
}

// NewCELEvaluator compiles a CEL expression and returns an evaluator.
func NewCELEvaluator(env *cel.Env, def EvalDefinition) (*CELEvaluator, error) {
	ast, issues := env.Compile(def.Expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("eval %q compile error: %w", def.Name, issues.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return nil, fmt.Errorf("eval %q must return bool, got %s", def.Name, ast.OutputType())
	}
	program, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("eval %q program error: %w", def.Name, err)
	}
	return &CELEvaluator{def: def, program: program}, nil
}

// Name returns the writes field — the canonical identifier used as the result
// key, CEL variable name for downstream evaluators, and execution order node.
func (e *CELEvaluator) Name() string            { return string(e.def.Writes) }
func (e *CELEvaluator) Reads() []FieldRef       { return e.def.Reads }
func (e *CELEvaluator) Writes() FieldRef        { return e.def.Writes }
func (e *CELEvaluator) CacheTTL() time.Duration { return e.def.CacheTTLDuration }

func (e *CELEvaluator) Evaluate(activation map[string]any) Result {
	out, _, err := e.program.Eval(activation)
	if err != nil {
		return Result{
			Name:               string(e.def.Writes),
			Passed:             false,
			Error:              err.Error(),
			Resolution:         e.def.Resolution,
			ResolutionWorkflow: e.def.ResolutionWorkflow,
			Severity:           e.def.Severity,
			Category:           e.def.Category,
		}
	}
	return Result{
		Name:               string(e.def.Writes),
		Passed:             out.Value().(bool),
		Resolution:         e.def.Resolution,
		ResolutionWorkflow: e.def.ResolutionWorkflow,
		Severity:           e.def.Severity,
		Category:           e.def.Category,
	}
}
