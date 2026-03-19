package evalengine

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	"google.golang.org/protobuf/proto"
)

// Engine loads evaluation definitions, compiles CEL expressions, builds the
// dependency graph, and runs all evaluators against a proto input.
type Engine struct {
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

	// Auto-derive reads from CEL AST. Two kinds:
	// 1. input.* field paths — extracted from select chains (e.g. input.score,
	//    input.nested_object.is_active). These feed the fingerprint cache.
	// 2. Bare identifiers matching another evaluator's writes — these are
	//    eval-to-eval dependencies for the graph.
	// Explicit reads from YAML are preserved and deduplicated.
	writesSet := make(map[string]bool, len(evaluators))
	for _, ev := range evaluators {
		writesSet[string(ev.Writes())] = true
	}
	for _, ev := range evaluators {
		celEv := ev.(*CELEvaluator)
		existingReads := make(map[FieldRef]bool, len(celEv.def.Reads))
		for _, r := range celEv.def.Reads {
			existingReads[r] = true
		}

		// Auto-derive input.* reads from select chains in the AST.
		for _, path := range extractInputFieldPaths(celEv.ast) {
			fr := FieldRef(path)
			if !existingReads[fr] {
				celEv.def.Reads = append(celEv.def.Reads, fr)
				existingReads[fr] = true
			}
		}

		// Auto-derive eval-to-eval reads from bare identifiers in the AST.
		for _, ref := range extractIdentRefs(celEv.ast) {
			if ref == string(celEv.def.Writes) {
				continue // self-reference
			}
			if !writesSet[ref] {
				continue // not a known evaluator output
			}
			fr := FieldRef(ref)
			if !existingReads[fr] {
				celEv.def.Reads = append(celEv.def.Reads, fr)
				existingReads[fr] = true
			}
		}
	}

	graph, err := BuildGraph(evaluators)
	if err != nil {
		return nil, fmt.Errorf("build graph: %w", err)
	}

	return &Engine{
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
	results, _ := e.execute(input, nil, time.Time{})
	return results
}

// RunWithCache executes evaluators, reusing cached results that are still
// within their CacheTTL. The caller owns the cache — the engine is stateless.
// Pass time.Now() as now; a zero now disables caching (equivalent to Run).
// Returns the full result set and a map indicating which evaluators were
// served from cache (true = reused, absent = re-evaluated).
func (e *Engine) RunWithCache(input proto.Message, cache map[string]CachedResult, now time.Time) ([]Result, map[string]bool) {
	return e.execute(input, cache, now)
}

// RunMap executes all evaluators and returns results indexed by name.
func (e *Engine) RunMap(input proto.Message) map[string]Result {
	results := e.Run(input)
	return toResultMap(results)
}

// RunWithCacheMap is like RunWithCache but returns results indexed by name.
func (e *Engine) RunWithCacheMap(input proto.Message, cache map[string]CachedResult, now time.Time) (map[string]Result, map[string]bool) {
	results, reused := e.RunWithCache(input, cache, now)
	return toResultMap(results), reused
}

func toResultMap(results []Result) map[string]Result {
	m := make(map[string]Result, len(results))
	for _, r := range results {
		m[r.Name] = r
	}
	return m
}

func (e *Engine) execute(input proto.Message, cache map[string]CachedResult, now time.Time) ([]Result, map[string]bool) {
	results := make([]Result, 0, len(e.evaluators))
	resultsMap := make(map[string]Result, len(e.evaluators))
	reused := make(map[string]bool)

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

		// Check cache: reuse if TTL > 0, entry exists, and not expired.
		if cache != nil && !now.IsZero() {
			ttl := ev.CacheTTL()
			if cached, hasCached := cache[name]; hasCached && ttl > 0 && now.Sub(cached.EvaluatedAt) < ttl {
				results = append(results, cached.Result)
				resultsMap[name] = cached.Result
				activation[name] = cached.Result.Passed
				reused[name] = true
				continue
			}
		}

		// Fingerprint check: if TTL expired but the input fields haven't
		// changed, reuse the cached result. Only safe when the evaluator
		// reads exclusively from proto input fields (no upstream outputs).
		if cache != nil {
			if cached, hasCached := cache[name]; hasCached && cached.Fingerprint != "" && hasOnlyInputReads(ev.Reads()) {
				fp := ComputeFingerprint(ev.Reads(), input)
				if fp == cached.Fingerprint {
					results = append(results, cached.Result)
					resultsMap[name] = cached.Result
					activation[name] = cached.Result.Passed
					reused[name] = true
					continue
				}
			}
		}

		if !e.graph.DependenciesMet(name, resultsMap) {
			r := Result{
				Name:               ev.Name(),
				DisplayName:        ev.DisplayName(),
				Passed:             false,
				Resolution:         ev.Resolution(),
				ResolutionWorkflow: ev.ResolutionWorkflow(),
				Severity:           ev.Severity(),
				Category:           ev.Category(),
				FailureMode:        ev.FailureMode(),
			}
			results = append(results, r)
			resultsMap[name] = r
			activation[name] = false
			continue
		}

		// Precondition check: if any precondition fails, mark as pending.
		if ev.HasPreconditions() {
			failedPreconditions := ev.EvaluatePreconditions(activation)
			if len(failedPreconditions) > 0 {
				r := Result{
					Name:                 ev.Name(),
					DisplayName:          ev.DisplayName(),
					Passed:               false,
					Pending:              true,
					PendingPreconditions: failedPreconditions,
					Resolution:           ev.Resolution(),
					ResolutionWorkflow:   ev.ResolutionWorkflow(),
					Severity:             ev.Severity(),
					Category:             ev.Category(),
					FailureMode:          ev.FailureMode(),
				}
				results = append(results, r)
				resultsMap[name] = r
				activation[name] = false
				continue
			}
		}

		r := ev.Evaluate(activation)
		results = append(results, r)
		resultsMap[name] = r
		activation[name] = r.Passed
	}

	return results, reused
}

// Graph returns the dependency graph.
func (e *Engine) Graph() *EvalGraph {
	return e.graph
}

// Evaluators returns all registered evaluators.
func (e *Engine) Evaluators() []Evaluator {
	return e.evaluators
}

// InputFields returns the input field paths referenced in the evaluator's CEL
// expression, extracted from the compiled AST. Only returns paths rooted at
// "input." (e.g. "input.score", "input.nested_object.is_active").
func (e *Engine) InputFields(name string) []string {
	for _, ev := range e.evaluators {
		if ev.Name() != name {
			continue
		}
		celEv, ok := ev.(*CELEvaluator)
		if !ok {
			return nil
		}
		return extractInputFieldPaths(celEv.ast)
	}
	return nil
}

// ToCachedResults converts results into a cache map with fingerprints
// computed from the evaluator's input reads and the proto message.
func (e *Engine) ToCachedResults(results []Result, input proto.Message, evaluatedAt time.Time) map[string]CachedResult {
	m := make(map[string]CachedResult, len(results))
	evalByName := make(map[string]Evaluator, len(e.evaluators))
	for _, ev := range e.evaluators {
		evalByName[ev.Name()] = ev
	}
	for _, r := range results {
		cr := CachedResult{Result: r, EvaluatedAt: evaluatedAt}
		if ev, ok := evalByName[r.Name]; ok && hasOnlyInputReads(ev.Reads()) {
			cr.Fingerprint = ComputeFingerprint(ev.Reads(), input)
		}
		m[r.Name] = cr
	}
	return m
}

// DeriveStatus derives the overall status from evaluation results.
func (e *Engine) DeriveStatus(results []Result) Status {
	return DeriveStatus(results, e.graph)
}

// hasInputPrefix returns true if the string starts with "input.".
func hasInputPrefix(s string) bool {
	return strings.HasPrefix(s, "input.")
}
