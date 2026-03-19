package evalengine_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/laenen-partners/evalengine"
	testv1 "github.com/laenen-partners/evalengine/proto/gen/go/test/v1"
)

// ---------------------------------------------------------------------------
// Item 1: FailureMode field
// ---------------------------------------------------------------------------

const failureModeYAML = `
evaluations:
  - name: score_eval
    expression: "input.score >= 100"
    reads: [input.score]
    writes: score_sufficient
    severity: blocking
    category: score
    failure_mode: "soft"

  - name: active_eval
    expression: "input.nested_object.is_active == true"
    reads: [input.nested_object.is_active]
    writes: is_active
    severity: blocking
    category: status
    failure_mode: "hard"

  - name: eligible_eval
    expression: "score_sufficient == true && is_active == true"
    reads: [score_sufficient, is_active]
    writes: eligible
    severity: blocking
    category: combined
    failure_mode: "soft"
`

func TestFailureModePassingEval(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(failureModeYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	input := &testv1.TestEvaluatorContainer{Score: 200, NestedObject: &testv1.NestedObject{IsActive: true}}
	results := eng.RunMap(input)

	if results["score_sufficient"].FailureMode != "soft" {
		t.Errorf("expected failure_mode 'soft', got %q", results["score_sufficient"].FailureMode)
	}
	if results["is_active"].FailureMode != "hard" {
		t.Errorf("expected failure_mode 'hard', got %q", results["is_active"].FailureMode)
	}
}

func TestFailureModeFailingEval(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(failureModeYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	input := &testv1.TestEvaluatorContainer{Score: 50, NestedObject: &testv1.NestedObject{IsActive: true}}
	results := eng.RunMap(input)

	if results["score_sufficient"].FailureMode != "soft" {
		t.Errorf("expected failure_mode 'soft' on failing eval, got %q", results["score_sufficient"].FailureMode)
	}
}

func TestFailureModeBlockedEval(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(failureModeYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	input := &testv1.TestEvaluatorContainer{Score: 50}
	results := eng.RunMap(input)

	if results["eligible"].FailureMode != "soft" {
		t.Errorf("expected failure_mode 'soft' on blocked eval, got %q", results["eligible"].FailureMode)
	}
}

// ---------------------------------------------------------------------------
// Item 3: DisplayName
// ---------------------------------------------------------------------------

func TestDisplayNameOnResult(t *testing.T) {
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{Score: 200, NestedObject: &testv1.NestedObject{IsActive: true}}
	results := eng.RunMap(input)

	r := results["score_sufficient"]
	if r.Name != "score_sufficient" {
		t.Errorf("Name should be writes field, got %q", r.Name)
	}
	if r.DisplayName != "score_sufficient_eval" {
		t.Errorf("DisplayName should be definition name, got %q", r.DisplayName)
	}
}

func TestDisplayNameOnBlockedResult(t *testing.T) {
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{Score: 50}
	results := eng.RunMap(input)

	r := results["eligible"]
	if r.DisplayName != "eligible_eval" {
		t.Errorf("blocked eval DisplayName should be 'eligible_eval', got %q", r.DisplayName)
	}
}

func TestDisplayNameOnFailingResult(t *testing.T) {
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{Score: 50, NestedObject: &testv1.NestedObject{IsActive: true}}
	results := eng.RunMap(input)

	r := results["score_sufficient"]
	if r.DisplayName != "score_sufficient_eval" {
		t.Errorf("failing eval DisplayName should be 'score_sufficient_eval', got %q", r.DisplayName)
	}
}

// ---------------------------------------------------------------------------
// Item 4: Blocks() reverse dependency API
// ---------------------------------------------------------------------------

func TestBlocksReturnsDependents(t *testing.T) {
	eng := loadTestEngine(t)
	blocks := eng.Graph().Blocks("score_sufficient")
	if len(blocks) != 1 || blocks[0] != "eligible" {
		t.Errorf("expected Blocks('score_sufficient') = [eligible], got %v", blocks)
	}
}

func TestBlocksLeafReturnsEmpty(t *testing.T) {
	eng := loadTestEngine(t)
	blocks := eng.Graph().Blocks("eligible")
	if len(blocks) != 0 {
		t.Errorf("expected Blocks('eligible') = [], got %v", blocks)
	}
}

func TestBlocksMultipleDependents(t *testing.T) {
	yaml := `
evaluations:
  - name: base_eval
    expression: "input.score >= 50"
    reads: [input.score]
    writes: base
    severity: blocking
    category: test

  - name: dep_a_eval
    expression: "base == true"
    reads: [base]
    writes: dep_a
    severity: blocking
    category: test

  - name: dep_b_eval
    expression: "base == true"
    reads: [base]
    writes: dep_b
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

	blocks := eng.Graph().Blocks("base")
	sort.Strings(blocks)
	if len(blocks) != 2 || blocks[0] != "dep_a" || blocks[1] != "dep_b" {
		t.Errorf("expected Blocks('base') = [dep_a, dep_b], got %v", blocks)
	}
}

// ---------------------------------------------------------------------------
// Item 7: InputFields from CEL AST
// ---------------------------------------------------------------------------

func TestInputFieldsScalar(t *testing.T) {
	eng := loadTestEngine(t)
	fields := eng.InputFields("score_sufficient")
	if len(fields) != 1 || fields[0] != "input.score" {
		t.Errorf("expected [input.score], got %v", fields)
	}
}

func TestInputFieldsNested(t *testing.T) {
	eng := loadTestEngine(t)
	fields := eng.InputFields("is_active")
	if len(fields) != 1 || fields[0] != "input.nested_object.is_active" {
		t.Errorf("expected [input.nested_object.is_active], got %v", fields)
	}
}

func TestInputFieldsUpstreamOnlyReturnsEmpty(t *testing.T) {
	eng := loadTestEngine(t)
	fields := eng.InputFields("eligible")
	if len(fields) != 0 {
		t.Errorf("expected empty for upstream-only eval, got %v", fields)
	}
}

func TestInputFieldsUnknownEval(t *testing.T) {
	eng := loadTestEngine(t)
	fields := eng.InputFields("nonexistent")
	if fields != nil {
		t.Errorf("expected nil for unknown eval, got %v", fields)
	}
}

// ---------------------------------------------------------------------------
// Item 6: Auto-derive eval-to-eval reads from CEL AST
// ---------------------------------------------------------------------------

const autoDeriveYAML = `
evaluations:
  - name: score_eval
    expression: "input.score >= 100"
    reads: [input.score]
    writes: score_sufficient
    severity: blocking
    category: score

  - name: active_eval
    expression: "input.nested_object.is_active == true"
    reads: [input.nested_object.is_active]
    writes: is_active
    severity: blocking
    category: status

  - name: eligible_eval
    description: Uses upstream refs in expression but omits reads
    expression: "score_sufficient == true && is_active == true"
    reads: []
    writes: eligible
    severity: blocking
    category: combined
`

func TestAutoDeriveReads(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(autoDeriveYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine should succeed with auto-derived reads: %v", err)
	}

	// Engine should work correctly: all pass when both conditions met.
	input := &testv1.TestEvaluatorContainer{Score: 200, NestedObject: &testv1.NestedObject{IsActive: true}}
	results := eng.RunMap(input)

	if !results["eligible"].Passed {
		t.Error("eligible should pass when both upstream evals pass")
	}

	// When score fails, eligible should be blocked.
	input2 := &testv1.TestEvaluatorContainer{Score: 50, NestedObject: &testv1.NestedObject{IsActive: true}}
	results2 := eng.RunMap(input2)

	if results2["eligible"].Passed {
		t.Error("eligible should fail when score_sufficient fails")
	}
}

const noReadsYAML = `
evaluations:
  - name: score_eval
    expression: "input.score >= 100"
    writes: score_sufficient
    severity: blocking
    category: score

  - name: active_eval
    expression: "input.nested_object.is_active == true"
    writes: is_active
    severity: blocking
    category: status

  - name: eligible_eval
    expression: "score_sufficient == true && is_active == true"
    writes: eligible
    severity: blocking
    category: combined
`

func TestFullAutoDeriveNoReadsAtAll(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(noReadsYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine should succeed with fully auto-derived reads: %v", err)
	}

	// All pass.
	input := &testv1.TestEvaluatorContainer{Score: 200, NestedObject: &testv1.NestedObject{IsActive: true}}
	results := eng.RunMap(input)
	for name, r := range results {
		if !r.Passed {
			t.Errorf("%s should pass, got error: %s", name, r.Error)
		}
	}

	// Score fails → eligible blocked.
	input2 := &testv1.TestEvaluatorContainer{Score: 50, NestedObject: &testv1.NestedObject{IsActive: true}}
	results2 := eng.RunMap(input2)
	if results2["score_sufficient"].Passed {
		t.Error("score_sufficient should fail with score=50")
	}
	if results2["eligible"].Passed {
		t.Error("eligible should be blocked when score_sufficient fails")
	}

	// Verify input.* reads were auto-derived (needed for fingerprint caching).
	for _, ev := range eng.Evaluators() {
		if ev.Name() == "score_sufficient" {
			hasInputScore := false
			for _, r := range ev.Reads() {
				if string(r) == "input.score" {
					hasInputScore = true
				}
			}
			if !hasInputScore {
				t.Error("score_sufficient should have auto-derived input.score read")
			}
		}
		if ev.Name() == "is_active" {
			hasNestedRead := false
			for _, r := range ev.Reads() {
				if string(r) == "input.nested_object.is_active" {
					hasNestedRead = true
				}
			}
			if !hasNestedRead {
				t.Error("is_active should have auto-derived input.nested_object.is_active read")
			}
		}
	}
}

func TestAutoDeriveDoesNotDuplicateExplicitReads(t *testing.T) {
	// eligible_eval in testYAML already has explicit reads for both deps.
	eng := loadTestEngine(t)

	// Find eligible evaluator and check its reads.
	for _, ev := range eng.Evaluators() {
		if ev.Name() == "eligible" {
			reads := ev.Reads()
			seen := make(map[string]int)
			for _, r := range reads {
				seen[string(r)]++
			}
			for name, count := range seen {
				if count > 1 {
					t.Errorf("read %q appears %d times (should be deduplicated)", name, count)
				}
			}
			return
		}
	}
	t.Fatal("eligible evaluator not found")
}

// ---------------------------------------------------------------------------
// Item 2: Preconditions / data completeness
// ---------------------------------------------------------------------------

const preconditionYAML = `
evaluations:
  - name: score_eval
    preconditions:
      - expression: "has(input.nested_object)"
        description: "Account details must be provided"
      - expression: "input.score > 0"
        description: "Score must be submitted"
    expression: "input.score >= 100"
    reads: [input.score]
    writes: score_sufficient
    resolution: "Boost score"
    severity: blocking
    category: score
    failure_mode: "soft"
`

func TestPreconditionsAllPass(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(preconditionYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	input := &testv1.TestEvaluatorContainer{Score: 200, NestedObject: &testv1.NestedObject{IsActive: true}}
	results := eng.RunMap(input)

	r := results["score_sufficient"]
	if !r.Passed {
		t.Error("should pass when all preconditions and expression pass")
	}
	if r.Pending {
		t.Error("should not be pending when preconditions pass")
	}
	if len(r.PendingPreconditions) != 0 {
		t.Errorf("should have no pending preconditions, got %v", r.PendingPreconditions)
	}
}

func TestPreconditionsOneFails(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(preconditionYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	// Score is 0 → second precondition "input.score > 0" fails, but first passes.
	input := &testv1.TestEvaluatorContainer{Score: 0, NestedObject: &testv1.NestedObject{IsActive: true}}
	results := eng.RunMap(input)

	r := results["score_sufficient"]
	if r.Passed {
		t.Error("should not pass when a precondition fails")
	}
	if !r.Pending {
		t.Error("should be pending when a precondition fails")
	}
	if len(r.PendingPreconditions) != 1 || r.PendingPreconditions[0] != "Score must be submitted" {
		t.Errorf("expected pending precondition 'Score must be submitted', got %v", r.PendingPreconditions)
	}
}

func TestPreconditionsAllFail(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(preconditionYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	// No nested object and score is 0 → both preconditions fail.
	input := &testv1.TestEvaluatorContainer{Score: 0}
	results := eng.RunMap(input)

	r := results["score_sufficient"]
	if r.Passed {
		t.Error("should not pass when all preconditions fail")
	}
	if !r.Pending {
		t.Error("should be pending when preconditions fail")
	}
	if len(r.PendingPreconditions) != 2 {
		t.Errorf("expected 2 pending preconditions, got %d: %v", len(r.PendingPreconditions), r.PendingPreconditions)
	}
}

func TestPreconditionsPreserveMetadata(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(preconditionYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	input := &testv1.TestEvaluatorContainer{Score: 0}
	results := eng.RunMap(input)

	r := results["score_sufficient"]
	if r.Resolution != "Boost score" {
		t.Errorf("expected resolution 'Boost score', got %q", r.Resolution)
	}
	if r.Severity != "blocking" {
		t.Errorf("expected severity 'blocking', got %q", r.Severity)
	}
	if r.FailureMode != "soft" {
		t.Errorf("expected failure_mode 'soft', got %q", r.FailureMode)
	}
	if r.DisplayName != "score_eval" {
		t.Errorf("expected display_name 'score_eval', got %q", r.DisplayName)
	}
}

func TestNoPreconditionsBehaviorUnchanged(t *testing.T) {
	// Standard testYAML has no preconditions — behavior should be the same.
	eng := loadTestEngine(t)
	input := &testv1.TestEvaluatorContainer{Score: 50, NestedObject: &testv1.NestedObject{IsActive: true}}
	results := eng.RunMap(input)

	r := results["score_sufficient"]
	if r.Pending {
		t.Error("should not be pending when no preconditions defined")
	}
}

// ---------------------------------------------------------------------------
// Status: StatusPending in priority chain
// ---------------------------------------------------------------------------

func TestDeriveStatusPending(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(preconditionYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	input := &testv1.TestEvaluatorContainer{Score: 0}
	results := eng.Run(input)
	status := eng.DeriveStatus(results)

	if status != evalengine.StatusPending {
		t.Errorf("expected StatusPending, got %s", status)
	}
}

func TestDeriveStatusActionRequiredOverPending(t *testing.T) {
	yaml := `
evaluations:
  - name: score_eval
    preconditions:
      - expression: "input.score > 0"
    expression: "input.score >= 100"
    reads: [input.score]
    writes: score_sufficient
    severity: blocking
    category: score

  - name: active_eval
    expression: "input.nested_object.is_active == true"
    reads: [input.nested_object.is_active]
    writes: is_active
    severity: blocking
    category: status
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	// score_eval is pending (score=0), active_eval fails (no nested_object) → ActionRequired
	input := &testv1.TestEvaluatorContainer{Score: 0}
	results := eng.Run(input)
	status := eng.DeriveStatus(results)

	if status != evalengine.StatusActionRequired {
		t.Errorf("expected StatusActionRequired (takes priority over Pending), got %s", status)
	}
}

func TestPreconditionDescriptionFallbackToExpression(t *testing.T) {
	// When description is empty, PendingPreconditions should return the expression.
	yaml := `
evaluations:
  - name: score_eval
    preconditions:
      - expression: "input.score > 0"
    expression: "input.score >= 100"
    reads: [input.score]
    writes: score_sufficient
    severity: blocking
    category: score
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	input := &testv1.TestEvaluatorContainer{Score: 0}
	results := eng.RunMap(input)
	r := results["score_sufficient"]
	if len(r.PendingPreconditions) != 1 || r.PendingPreconditions[0] != "input.score > 0" {
		t.Errorf("expected expression as fallback, got %v", r.PendingPreconditions)
	}
}

// ---------------------------------------------------------------------------
// Issue 1: Nested field fingerprinting
// ---------------------------------------------------------------------------

func TestFingerprintNestedFieldChanges(t *testing.T) {
	reads := []evalengine.FieldRef{"input.nested_object.is_active"}

	msg1 := &testv1.TestEvaluatorContainer{NestedObject: &testv1.NestedObject{IsActive: true}}
	msg2 := &testv1.TestEvaluatorContainer{NestedObject: &testv1.NestedObject{IsActive: false}}

	fp1 := evalengine.ComputeFingerprint(reads, msg1)
	fp2 := evalengine.ComputeFingerprint(reads, msg2)

	if fp1 == "" {
		t.Error("fingerprint for nested field should not be empty")
	}
	if fp1 == fp2 {
		t.Error("fingerprint should change when nested field value changes")
	}
}

func TestFingerprintNestedFieldNilVsSet(t *testing.T) {
	reads := []evalengine.FieldRef{"input.nested_object.is_active"}

	msgNil := &testv1.TestEvaluatorContainer{} // nested_object is nil
	msgSet := &testv1.TestEvaluatorContainer{NestedObject: &testv1.NestedObject{IsActive: true}}

	fpNil := evalengine.ComputeFingerprint(reads, msgNil)
	fpSet := evalengine.ComputeFingerprint(reads, msgSet)

	if fpNil == fpSet {
		t.Error("fingerprint should differ between nil nested object and set nested object")
	}
}

func TestFingerprintNestedFieldStable(t *testing.T) {
	reads := []evalengine.FieldRef{"input.nested_object.is_active"}
	msg := &testv1.TestEvaluatorContainer{NestedObject: &testv1.NestedObject{IsActive: true}}

	fp1 := evalengine.ComputeFingerprint(reads, msg)
	fp2 := evalengine.ComputeFingerprint(reads, msg)

	if fp1 != fp2 {
		t.Errorf("fingerprint for nested field should be stable: %s != %s", fp1, fp2)
	}
}

func TestPreconditionCompileError(t *testing.T) {
	yaml := `
evaluations:
  - name: bad_precond_eval
    preconditions:
      - expression: "invalid >== syntax"
    expression: "input.score >= 100"
    reads: [input.score]
    writes: bad_precond
    severity: blocking
    category: test
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err == nil {
		t.Fatal("expected compile error for invalid precondition")
	}
}
