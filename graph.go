package evalengine

import (
	"fmt"
	"strings"
)

// Issue represents a validation issue found in the dependency graph.
type Issue struct {
	Type     string // "circular_dependency", "missing_producer", "duplicate_producer", "orphan_output"
	Severity string // "error", "warning", "info"
	Message  string
}

// EvalGraph holds the auto-calculated dependency graph derived from evaluator
// reads/writes declarations.
type EvalGraph struct {
	producers  map[FieldRef]string // field -> evaluator name that writes it
	deps       map[string][]string // evaluator -> evaluators it depends on
	dependents map[string][]string // evaluator -> evaluators that depend on it (reverse of deps)
	order      []string            // topological execution order
	issues     []Issue
}

// BuildGraph derives the dependency graph from evaluator declarations.
// Reads prefixed with "input." are treated as raw proto field references —
// no producer dependency is created for them.
func BuildGraph(evaluators []Evaluator) (*EvalGraph, error) {
	g := &EvalGraph{
		producers:  make(map[FieldRef]string),
		deps:       make(map[string][]string),
		dependents: make(map[string][]string),
	}

	// Phase 1: Register all producers (writes).
	for _, eval := range evaluators {
		field := eval.Writes()
		if existing, ok := g.producers[field]; ok {
			g.issues = append(g.issues, Issue{
				Type:     "duplicate_producer",
				Severity: "error",
				Message:  fmt.Sprintf("both %q and %q write field %q", existing, eval.Name(), field),
			})
			continue
		}
		g.producers[field] = eval.Name()
	}

	// Phase 2: Derive dependencies (reads that match a producer).
	for _, eval := range evaluators {
		for _, field := range eval.Reads() {
			if strings.HasPrefix(string(field), "input.") {
				continue // proto input field, no producer dependency
			}
			producer, exists := g.producers[field]
			if !exists {
				g.issues = append(g.issues, Issue{
					Type:     "missing_producer",
					Severity: "error",
					Message:  fmt.Sprintf("eval %q reads %q but no evaluator writes it", eval.Name(), field),
				})
				continue
			}
			if producer == eval.Name() {
				continue // self-reference
			}
			g.deps[eval.Name()] = append(g.deps[eval.Name()], producer)
			g.dependents[producer] = append(g.dependents[producer], eval.Name())
		}
	}

	// Phase 3: Check for orphan outputs.
	readFields := make(map[FieldRef]bool)
	for _, eval := range evaluators {
		for _, field := range eval.Reads() {
			readFields[field] = true
		}
	}
	for _, eval := range evaluators {
		field := eval.Writes()
		if !readFields[field] {
			g.issues = append(g.issues, Issue{
				Type:     "orphan_output",
				Severity: "info",
				Message:  fmt.Sprintf("eval %q writes %q which is never read (leaf node)", eval.Name(), field),
			})
		}
	}

	// Phase 4: Topological sort for execution order.
	var err error
	g.order, err = g.topologicalSort(evaluators)
	if err != nil {
		g.issues = append(g.issues, Issue{
			Type:     "circular_dependency",
			Severity: "error",
			Message:  err.Error(),
		})
	}

	return g, g.criticalError()
}

// DependenciesMet returns true if all upstream dependencies of the given
// evaluator have passed.
func (g *EvalGraph) DependenciesMet(name string, results map[string]Result) bool {
	for _, dep := range g.deps[name] {
		r, ok := results[dep]
		if !ok || !r.Passed {
			return false
		}
	}
	return true
}

// BlockedBy returns the names of upstream evaluators that have not passed.
func (g *EvalGraph) BlockedBy(name string, results map[string]Result) []string {
	var blocked []string
	for _, dep := range g.deps[name] {
		r, ok := results[dep]
		if !ok || !r.Passed {
			blocked = append(blocked, dep)
		}
	}
	return blocked
}

// Blocks returns the names of evaluators that directly depend on the given
// evaluator (reverse dependency lookup).
func (g *EvalGraph) Blocks(name string) []string {
	return g.dependents[name]
}

// ExecutionOrder returns the topologically sorted evaluator names.
func (g *EvalGraph) ExecutionOrder() []string {
	return g.order
}

// Issues returns all validation issues found during graph construction.
func (g *EvalGraph) Issues() []Issue {
	return g.issues
}

// MaxDepth returns the longest dependency chain length.
func (g *EvalGraph) MaxDepth() int {
	memo := make(map[string]int)
	max := 0
	for name := range g.deps {
		d := g.depth(name, memo)
		if d > max {
			max = d
		}
	}
	// Also check nodes with no deps (depth 0).
	for _, name := range g.order {
		if _, ok := g.deps[name]; !ok {
			if 0 > max {
				max = 0
			}
		}
	}
	return max
}

func (g *EvalGraph) depth(name string, memo map[string]int) int {
	if d, ok := memo[name]; ok {
		return d
	}
	max := 0
	for _, dep := range g.deps[name] {
		d := g.depth(dep, memo) + 1
		if d > max {
			max = d
		}
	}
	memo[name] = max
	return max
}

func (g *EvalGraph) topologicalSort(evaluators []Evaluator) ([]string, error) {
	// Kahn's algorithm.
	inDegree := make(map[string]int)
	for _, eval := range evaluators {
		if _, ok := inDegree[eval.Name()]; !ok {
			inDegree[eval.Name()] = 0
		}
	}
	for name, deps := range g.deps {
		inDegree[name] += len(deps)
	}

	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}

	// Build reverse map: producer -> consumers.
	consumers := make(map[string][]string)
	for name, deps := range g.deps {
		for _, dep := range deps {
			consumers[dep] = append(consumers[dep], name)
		}
	}

	var order []string
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		order = append(order, name)
		for _, consumer := range consumers[name] {
			inDegree[consumer]--
			if inDegree[consumer] == 0 {
				queue = append(queue, consumer)
			}
		}
	}

	if len(order) != len(inDegree) {
		return nil, fmt.Errorf("circular dependency detected: sorted %d of %d evaluators", len(order), len(inDegree))
	}

	return order, nil
}

func (g *EvalGraph) criticalError() error {
	for _, issue := range g.issues {
		if issue.Severity == "error" {
			return fmt.Errorf("graph validation: [%s] %s", issue.Type, issue.Message)
		}
	}
	return nil
}
