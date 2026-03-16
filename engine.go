package evalengine

import (
	"bytes"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	"google.golang.org/protobuf/proto"
)

// Engine loads evaluation definitions, compiles CEL expressions, builds the
// dependency graph, and runs all evaluators against a proto input.
type Engine struct {
	env        *cel.Env
	evaluators []Evaluator
	graph      *EvalGraph
}

// NewEngine creates an evaluation engine from a config and a proto message that
// serves as the input type. The proto is registered in the CEL environment as
// the variable "input" — YAML expressions reference fields as "input.<field>".
// Extra opts are forwarded to NewCELEnvironment for additional declarations.
func NewEngine(cfg *EvalConfig, input proto.Message, opts ...cel.EnvOption) (*Engine, error) {
	// Register the proto type and bind it to the reserved "input" variable.
	inputOpts := []cel.EnvOption{
		cel.Types(input),
		cel.Variable("input", cel.ObjectType(string(proto.MessageName(input)))),
	}

	// Auto-declare each evaluator's writes field as a CEL bool variable so
	// downstream evaluators can reference upstream results in their expressions.
	writeVars := make([]cel.EnvOption, 0, len(cfg.Evaluations))
	for _, def := range cfg.Evaluations {
		writeVars = append(writeVars, cel.Variable(string(def.Writes), cel.BoolType))
	}

	allOpts := append(inputOpts, writeVars...)
	allOpts = append(allOpts, opts...)

	env, err := newCELEnvironment(allOpts...)
	if err != nil {
		return nil, fmt.Errorf("create CEL environment: %w", err)
	}

	evaluators := make([]Evaluator, 0, len(cfg.Evaluations))
	for _, def := range cfg.Evaluations {
		e, err := NewCELEvaluator(env, def)
		if err != nil {
			return nil, fmt.Errorf("create evaluator: %w", err)
		}
		evaluators = append(evaluators, e)
	}

	graph, err := BuildGraph(evaluators)
	if err != nil {
		return nil, fmt.Errorf("build graph: %w", err)
	}

	return &Engine{
		env:        env,
		evaluators: evaluators,
		graph:      graph,
	}, nil
}

// newCELEnvironment creates a CEL environment with the given options.
func newCELEnvironment(opts ...cel.EnvOption) (*cel.Env, error) {
	base := []cel.EnvOption{ext.Strings()}
	return cel.NewEnv(append(base, opts...)...)
}

// NewEngineFromFile loads evaluation definitions from a YAML file.
func NewEngineFromFile(path string, input proto.Message, opts ...cel.EnvOption) (*Engine, error) {
	cfg, err := LoadDefinitionsFromFile(path)
	if err != nil {
		return nil, err
	}
	return NewEngine(cfg, input, opts...)
}

// NewEngineFromBytes loads evaluation definitions from raw YAML bytes.
func NewEngineFromBytes(data []byte, input proto.Message, opts ...cel.EnvOption) (*Engine, error) {
	cfg, err := LoadDefinitions(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return NewEngine(cfg, input, opts...)
}

// Run executes all evaluators in dependency order against the given input.
// The proto is bound to the "input" CEL variable. Upstream evaluator results
// are injected by their writes-field name for downstream expressions.
func (e *Engine) Run(input proto.Message) []Result {
	results := make([]Result, 0, len(e.evaluators))
	resultsMap := make(map[string]Result, len(e.evaluators))

	activation := map[string]any{"input": input}

	evalByName := make(map[string]Evaluator, len(e.evaluators))
	for _, ev := range e.evaluators {
		evalByName[ev.Name()] = ev
	}

	for _, name := range e.graph.ExecutionOrder() {
		ev, ok := evalByName[name]
		if !ok {
			continue
		}

		if !e.graph.DependenciesMet(name, resultsMap) {
			r := Result{
				Name:               ev.Name(),
				Passed:             false,
				Resolution:         ev.(*CELEvaluator).def.Resolution,
				ResolutionWorkflow: ev.(*CELEvaluator).def.ResolutionWorkflow,
				Severity:           ev.(*CELEvaluator).def.Severity,
				Category:           ev.(*CELEvaluator).def.Category,
			}
			results = append(results, r)
			resultsMap[name] = r
			activation[name] = false
			continue
		}

		r := ev.Evaluate(activation)
		results = append(results, r)
		resultsMap[name] = r
		activation[name] = r.Passed
	}

	return results
}

// RunMap executes all evaluators and returns results indexed by name.
func (e *Engine) RunMap(input proto.Message) map[string]Result {
	results := e.Run(input)
	m := make(map[string]Result, len(results))
	for _, r := range results {
		m[r.Name] = r
	}
	return m
}

// Graph returns the dependency graph.
func (e *Engine) Graph() *EvalGraph {
	return e.graph
}

// Evaluators returns all registered evaluators.
func (e *Engine) Evaluators() []Evaluator {
	return e.evaluators
}

// DeriveStatus derives the overall status from evaluation results.
func (e *Engine) DeriveStatus(results []Result) Status {
	return DeriveStatus(results, e.graph)
}
