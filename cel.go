package evalengine

import (
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
)

// CELEvaluator implements Evaluator using a compiled CEL program.
type CELEvaluator struct {
	def                  EvalDefinition
	program              cel.Program
	ast                  *cel.Ast
	preconditionPrograms []cel.Program
	preconditions        []Precondition
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

	var preconditionPrograms []cel.Program
	for _, pc := range def.Preconditions {
		pcAst, pcIssues := env.Compile(pc.Expression)
		if pcIssues != nil && pcIssues.Err() != nil {
			return nil, fmt.Errorf("eval %q precondition %q compile error: %w", def.Name, pc.Expression, pcIssues.Err())
		}
		if pcAst.OutputType() != cel.BoolType {
			return nil, fmt.Errorf("eval %q precondition %q must return bool, got %s", def.Name, pc.Expression, pcAst.OutputType())
		}
		pcProg, err := env.Program(pcAst)
		if err != nil {
			return nil, fmt.Errorf("eval %q precondition %q program error: %w", def.Name, pc.Expression, err)
		}
		preconditionPrograms = append(preconditionPrograms, pcProg)
	}

	return &CELEvaluator{
		def:                  def,
		program:              program,
		ast:                  ast,
		preconditionPrograms: preconditionPrograms,
		preconditions:        def.Preconditions,
	}, nil
}

// Interface accessors — these expose definition metadata through the Evaluator
// interface so that execute() never needs to type-assert to *CELEvaluator.
func (e *CELEvaluator) Name() string               { return string(e.def.Writes) }
func (e *CELEvaluator) DisplayName() string        { return e.def.Name }
func (e *CELEvaluator) Reads() []FieldRef          { return e.def.Reads }
func (e *CELEvaluator) Writes() FieldRef           { return e.def.Writes }
func (e *CELEvaluator) CacheTTL() time.Duration    { return e.def.CacheTTLDuration }
func (e *CELEvaluator) Resolution() string         { return e.def.Resolution }
func (e *CELEvaluator) ResolutionWorkflow() string { return e.def.ResolutionWorkflow }
func (e *CELEvaluator) Severity() string           { return e.def.Severity }
func (e *CELEvaluator) Category() string           { return e.def.Category }
func (e *CELEvaluator) FailureMode() string        { return e.def.FailureMode }
func (e *CELEvaluator) HasPreconditions() bool     { return len(e.preconditionPrograms) > 0 }

// EvaluatePreconditions runs all precondition programs against the activation.
// Returns the descriptions of preconditions that evaluated to false (or errored).
// If a precondition has no description, the expression is returned instead.
func (e *CELEvaluator) EvaluatePreconditions(activation map[string]any) []string {
	var failed []string
	for i, prog := range e.preconditionPrograms {
		out, _, err := prog.Eval(activation)
		if err != nil || !out.Value().(bool) {
			desc := e.preconditions[i].Description
			if desc == "" {
				desc = e.preconditions[i].Expression
			}
			failed = append(failed, desc)
		}
	}
	return failed
}

func (e *CELEvaluator) Evaluate(activation map[string]any) Result {
	out, _, err := e.program.Eval(activation)
	if err != nil {
		return Result{
			Name:               string(e.def.Writes),
			DisplayName:        e.def.Name,
			Passed:             false,
			Error:              err.Error(),
			Resolution:         e.def.Resolution,
			ResolutionWorkflow: e.def.ResolutionWorkflow,
			Severity:           e.def.Severity,
			Category:           e.def.Category,
			FailureMode:        e.def.FailureMode,
		}
	}
	return Result{
		Name:               string(e.def.Writes),
		DisplayName:        e.def.Name,
		Passed:             out.Value().(bool),
		Resolution:         e.def.Resolution,
		ResolutionWorkflow: e.def.ResolutionWorkflow,
		Severity:           e.def.Severity,
		Category:           e.def.Category,
		FailureMode:        e.def.FailureMode,
	}
}
