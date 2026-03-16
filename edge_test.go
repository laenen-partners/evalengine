package evalengine_test

import (
	"strings"
	"testing"

	"github.com/laenen-partners/evalengine"
	testv1 "github.com/laenen-partners/evalengine/proto/gen/go/test/v1"
)

// ---------------------------------------------------------------------------
// Graph: orphan output is info, not error
// ---------------------------------------------------------------------------

func TestOrphanOutputIsNotError(t *testing.T) {
	yaml := `
evaluations:
  - name: orphan_eval
    description: Writes a field nobody reads
    expression: "input.score >= 100"
    reads: [input.score]
    writes: orphan_field
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine should succeed: %v", err)
	}

	hasOrphan := false
	for _, issue := range eng.Graph().Issues() {
		if issue.Type == "orphan_output" {
			hasOrphan = true
			if issue.Severity != "info" {
				t.Errorf("orphan_output should be info, got %q", issue.Severity)
			}
		}
	}
	if !hasOrphan {
		t.Error("expected orphan_output issue")
	}
}

// ---------------------------------------------------------------------------
// Graph: self-reference is not a dependency
// ---------------------------------------------------------------------------

func TestSelfReferenceIgnored(t *testing.T) {
	yaml := `
evaluations:
  - name: self_ref_eval
    description: Reads its own output
    expression: "input.score >= 100"
    reads: [input.score, self_ref]
    writes: self_ref
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine should succeed: %v", err)
	}

	for _, issue := range eng.Graph().Issues() {
		if issue.Severity == "error" {
			t.Errorf("unexpected error issue: %s", issue.Message)
		}
	}

	results := eng.RunMap(&testv1.TestEvaluatorContainer{Score: 200})
	if !results["self_ref"].Passed {
		t.Error("self-referencing eval should pass when expression is satisfied")
	}
}

// ---------------------------------------------------------------------------
// Graph: diamond dependency (two branches merging)
// ---------------------------------------------------------------------------

func TestDiamondDependency(t *testing.T) {
	yaml := `
evaluations:
  - name: left_eval
    description: Left branch
    expression: "input.score >= 50"
    reads: [input.score]
    writes: left
    severity: blocking
    category: test

  - name: right_eval
    description: Right branch
    expression: "input.score >= 75"
    reads: [input.score]
    writes: right
    severity: blocking
    category: test

  - name: merge_eval
    description: Merges both branches
    expression: "left == true && right == true"
    reads: [left, right]
    writes: merged
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	order := eng.Graph().ExecutionOrder()
	mergedIdx := -1
	for i, name := range order {
		if name == "merged" {
			mergedIdx = i
		}
	}
	if mergedIdx < 2 {
		t.Errorf("merged should be last (index >= 2), got index %d in %v", mergedIdx, order)
	}

	// Score 50: left passes, right fails -> merged blocked.
	results := eng.RunMap(&testv1.TestEvaluatorContainer{Score: 50})
	if !results["left"].Passed {
		t.Error("left should pass at score 50")
	}
	if results["right"].Passed {
		t.Error("right should fail at score 50")
	}
	if results["merged"].Passed {
		t.Error("merged should fail when right fails")
	}

	// Score 100: both pass -> merged passes.
	results = eng.RunMap(&testv1.TestEvaluatorContainer{Score: 100})
	if !results["merged"].Passed {
		t.Error("merged should pass when both deps pass")
	}
}

// ---------------------------------------------------------------------------
// Engine: nil nested proto field (CEL should handle gracefully)
// ---------------------------------------------------------------------------

func TestNilNestedProtoField(t *testing.T) {
	eng := loadTestEngine(t)

	// NestedObject is nil — CEL should evaluate without panic.
	input := &testv1.TestEvaluatorContainer{Score: 200}
	results := eng.Run(input)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	rm := eng.RunMap(input)
	if rm["is_active"].Passed {
		t.Error("is_active should fail when nested_object is nil")
	}
}

// ---------------------------------------------------------------------------
// Engine: boundary score values
// ---------------------------------------------------------------------------

func TestBoundaryScoreExactlyAtThreshold(t *testing.T) {
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{
		Score:        100,
		NestedObject: &testv1.NestedObject{IsActive: true},
	}
	results := eng.RunMap(input)
	if !results["score_sufficient"].Passed {
		t.Error("score_sufficient should pass at exactly 100")
	}
}

func TestBoundaryScoreOneBelow(t *testing.T) {
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{
		Score:        99,
		NestedObject: &testv1.NestedObject{IsActive: true},
	}
	results := eng.RunMap(input)
	if results["score_sufficient"].Passed {
		t.Error("score_sufficient should fail at 99")
	}
}

func TestNegativeScore(t *testing.T) {
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{
		Score:        -1,
		NestedObject: &testv1.NestedObject{IsActive: true},
	}
	results := eng.RunMap(input)
	if results["score_sufficient"].Passed {
		t.Error("score_sufficient should fail with negative score")
	}
}

// ---------------------------------------------------------------------------
// Engine: empty evaluations config
// ---------------------------------------------------------------------------

func TestEmptyEvaluationsConfig(t *testing.T) {
	yaml := `
evaluations: []
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	results := eng.Run(&testv1.TestEvaluatorContainer{Score: 100})
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty config, got %d", len(results))
	}

	status := eng.DeriveStatus(results)
	if status != evalengine.StatusAllPassed {
		t.Errorf("expected 'active' for empty results, got %q", status)
	}
}

// ---------------------------------------------------------------------------
// Engine: single evaluator, no dependencies — MaxDepth should be 0
// ---------------------------------------------------------------------------

func TestSingleEvaluatorNoDeps(t *testing.T) {
	yaml := `
evaluations:
  - name: solo_eval
    description: Standalone
    expression: "input.score > 0"
    reads: [input.score]
    writes: solo
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	if eng.Graph().MaxDepth() != 0 {
		t.Errorf("single eval max depth should be 0, got %d", eng.Graph().MaxDepth())
	}

	results := eng.RunMap(&testv1.TestEvaluatorContainer{Score: 1})
	if !results["solo"].Passed {
		t.Error("solo should pass with score > 0")
	}
}

// ---------------------------------------------------------------------------
// Status: blocked eval (deps not met, no resolution workflow)
// ---------------------------------------------------------------------------

func TestDeriveStatusBlocked(t *testing.T) {
	yaml := `
evaluations:
  - name: gate_eval
    description: Gate
    expression: "input.score >= 100"
    reads: [input.score]
    writes: gate
    severity: blocking
    category: test

  - name: after_gate_eval
    description: After gate
    expression: "gate == true"
    reads: [gate]
    writes: after_gate
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	results := eng.Run(&testv1.TestEvaluatorContainer{Score: 50})
	status := eng.DeriveStatus(results)
	// gate fails (no workflow, deps met) -> hasActionRequired=true.
	// after_gate blocked (deps not met) -> hasBlockedEval=true.
	// ActionRequired is checked before Blocked, so ActionRequired wins.
	if status != evalengine.StatusActionRequired {
		t.Errorf("expected StatusActionRequired, got %s", status)
	}
}

// ---------------------------------------------------------------------------
// Status: empty results -> active
// ---------------------------------------------------------------------------

func TestDeriveStatusEmptyResults(t *testing.T) {
	eng := loadTestEngine(t)
	status := eng.DeriveStatus(nil)
	if status != evalengine.StatusAllPassed {
		t.Errorf("expected 'active' for nil results, got %q", status)
	}
}

// ---------------------------------------------------------------------------
// Status: onboarding_in_progress takes priority over blocked
// ---------------------------------------------------------------------------

func TestDeriveStatusWorkflowPriorityOverBlocked(t *testing.T) {
	yaml := `
evaluations:
  - name: workflow_eval
    description: Has workflow
    expression: "input.score >= 100"
    reads: [input.score]
    writes: workflow_check
    resolution_workflow: FixWorkflow
    severity: blocking
    category: test

  - name: blocked_eval
    description: Blocked by workflow_check
    expression: "workflow_check == true"
    reads: [workflow_check]
    writes: blocked_check
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	results := eng.Run(&testv1.TestEvaluatorContainer{Score: 50})
	status := eng.DeriveStatus(results)
	if status != evalengine.StatusWorkflowActive {
		t.Errorf("expected 'onboarding_in_progress', got %q", status)
	}
}

// ---------------------------------------------------------------------------
// Graph: BlockedBy returns all failing upstream deps
// ---------------------------------------------------------------------------

func TestBlockedByReturnsAllBlockers(t *testing.T) {
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{
		Score: 50, // score_sufficient fails, is_active fails (nil nested)
	}
	results := eng.RunMap(input)

	blockers := eng.Graph().BlockedBy("eligible", results)
	if len(blockers) != 2 {
		t.Fatalf("expected 2 blockers, got %d: %v", len(blockers), blockers)
	}
}

// ---------------------------------------------------------------------------
// Graph: DependenciesMet for node with no deps is always true
// ---------------------------------------------------------------------------

func TestDependenciesMetNoDepsAlwaysTrue(t *testing.T) {
	eng := loadTestEngine(t)
	met := eng.Graph().DependenciesMet("score_sufficient", map[string]evalengine.Result{})
	if !met {
		t.Error("node with no dependencies should always have deps met")
	}
}

// ---------------------------------------------------------------------------
// Graph: MaxDepth on deep chain
// ---------------------------------------------------------------------------

func TestMaxDepthDeepChain(t *testing.T) {
	yaml := `
evaluations:
  - name: level0_eval
    description: Level 0
    expression: "input.score >= 0"
    reads: [input.score]
    writes: level0
    severity: blocking
    category: test

  - name: level1_eval
    description: Level 1
    expression: "level0 == true"
    reads: [level0]
    writes: level1
    severity: blocking
    category: test

  - name: level2_eval
    description: Level 2
    expression: "level1 == true"
    reads: [level1]
    writes: level2
    severity: blocking
    category: test

  - name: level3_eval
    description: Level 3
    expression: "level2 == true"
    reads: [level2]
    writes: level3
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	depth := eng.Graph().MaxDepth()
	if depth != 3 {
		t.Errorf("expected max depth 3 for 4-level chain, got %d", depth)
	}

	// All should pass with score >= 0.
	results := eng.Run(&testv1.TestEvaluatorContainer{Score: 1})
	for _, r := range results {
		if !r.Passed {
			t.Errorf("eval %q should pass in deep chain", r.Name)
		}
	}

	// Execution order must be strictly level0 -> level1 -> level2 -> level3.
	order := eng.Graph().ExecutionOrder()
	for i, expected := range []string{"level0", "level1", "level2", "level3"} {
		if order[i] != expected {
			t.Errorf("order[%d] = %q, want %q", i, order[i], expected)
		}
	}
}

// ---------------------------------------------------------------------------
// Engine: deep chain — first failure cascades to block all downstream
// ---------------------------------------------------------------------------

func TestDeepChainCascadingBlock(t *testing.T) {
	yaml := `
evaluations:
  - name: root_eval
    description: Root
    expression: "input.score >= 100"
    reads: [input.score]
    writes: root
    severity: blocking
    category: test

  - name: mid_eval
    description: Mid
    expression: "root == true"
    reads: [root]
    writes: mid
    severity: blocking
    category: test

  - name: leaf_eval
    description: Leaf
    expression: "mid == true"
    reads: [mid]
    writes: leaf
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	results := eng.RunMap(&testv1.TestEvaluatorContainer{Score: 50})
	if results["root"].Passed {
		t.Error("root should fail")
	}
	if results["mid"].Passed {
		t.Error("mid should be blocked by root")
	}
	if results["leaf"].Passed {
		t.Error("leaf should be blocked by mid")
	}
}

// ---------------------------------------------------------------------------
// Engine: CEL string extension functions work
// ---------------------------------------------------------------------------

func TestCELStringExtensions(t *testing.T) {
	yaml := `
evaluations:
  - name: string_ext_eval
    description: Uses string extension
    expression: "'hello world'.contains('hello')"
    reads: []
    writes: string_ext
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	results := eng.RunMap(&testv1.TestEvaluatorContainer{})
	if !results["string_ext"].Passed {
		t.Errorf("string extension eval should pass, error: %s", results["string_ext"].Error)
	}
}

// ---------------------------------------------------------------------------
// Engine: result carries metadata from definition
// ---------------------------------------------------------------------------

func TestResultMetadataFromDefinition(t *testing.T) {
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{Score: 50}
	results := eng.RunMap(input)

	r := results["score_sufficient"]
	if r.Resolution != "Boost score" {
		t.Errorf("expected resolution 'Boost score', got %q", r.Resolution)
	}
	if r.ResolutionWorkflow != "ScoreBoostWorkflow" {
		t.Errorf("expected workflow 'ScoreBoostWorkflow', got %q", r.ResolutionWorkflow)
	}
	if r.Severity != "blocking" {
		t.Errorf("expected severity 'blocking', got %q", r.Severity)
	}
	if r.Category != "score" {
		t.Errorf("expected category 'score', got %q", r.Category)
	}
}

// ---------------------------------------------------------------------------
// Engine: blocked evaluator still carries metadata
// ---------------------------------------------------------------------------

func TestBlockedEvalCarriesMetadata(t *testing.T) {
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{Score: 50}
	results := eng.RunMap(input)

	r := results["eligible"]
	if r.Passed {
		t.Error("eligible should be blocked/failed")
	}
	if r.Resolution != "Meet all criteria" {
		t.Errorf("blocked eval should still have resolution, got %q", r.Resolution)
	}
	if r.Category != "combined" {
		t.Errorf("blocked eval should still have category, got %q", r.Category)
	}
}

// ---------------------------------------------------------------------------
// Engine: RunMap returns all results indexed by writes-name
// ---------------------------------------------------------------------------

func TestRunMapContainsAllResults(t *testing.T) {
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{
		Score:        200,
		NestedObject: &testv1.NestedObject{IsActive: true},
	}
	results := eng.RunMap(input)

	expected := []string{"score_sufficient", "is_active", "eligible"}
	for _, name := range expected {
		if _, ok := results[name]; !ok {
			t.Errorf("RunMap missing result for %q", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Engine: evaluator with no reads (constant expression)
// ---------------------------------------------------------------------------

func TestEvaluatorNoReads(t *testing.T) {
	yaml := `
evaluations:
  - name: constant_eval
    description: Always true
    expression: "true"
    reads: []
    writes: always_true
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	results := eng.RunMap(&testv1.TestEvaluatorContainer{})
	if !results["always_true"].Passed {
		t.Error("constant true expression should pass")
	}
}

// ---------------------------------------------------------------------------
// Engine: multiple independent evaluators (no deps between them)
// ---------------------------------------------------------------------------

func TestMultipleIndependentEvaluators(t *testing.T) {
	yaml := `
evaluations:
  - name: check_a_eval
    description: Check A
    expression: "input.score >= 50"
    reads: [input.score]
    writes: check_a
    severity: blocking
    category: test

  - name: check_b_eval
    description: Check B
    expression: "input.score >= 100"
    reads: [input.score]
    writes: check_b
    severity: blocking
    category: test

  - name: check_c_eval
    description: Check C
    expression: "input.nested_object.is_active == true"
    reads: [input.nested_object.is_active]
    writes: check_c
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	// MaxDepth should be 0 — no dependencies.
	if eng.Graph().MaxDepth() != 0 {
		t.Errorf("independent evaluators max depth should be 0, got %d", eng.Graph().MaxDepth())
	}

	// Score 75, active: A passes, B fails, C passes — each independent.
	results := eng.RunMap(&testv1.TestEvaluatorContainer{
		Score:        75,
		NestedObject: &testv1.NestedObject{IsActive: true},
	})
	if !results["check_a"].Passed {
		t.Error("check_a should pass at 75")
	}
	if results["check_b"].Passed {
		t.Error("check_b should fail at 75")
	}
	if !results["check_c"].Passed {
		t.Error("check_c should pass when active")
	}
}

// ---------------------------------------------------------------------------
// Definition: LoadDefinitionsFromFile with non-existent file
// ---------------------------------------------------------------------------

func TestLoadDefinitionsFromFileMissing(t *testing.T) {
	_, err := evalengine.LoadDefinitionsFromFile("/tmp/definitely-does-not-exist-evalengine.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
