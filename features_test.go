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

// ---------------------------------------------------------------------------
// Repeated (array) field support
// ---------------------------------------------------------------------------

func TestArrayExistsPass(t *testing.T) {
	yaml := `
evaluations:
  - name: has_adult_party
    expression: 'input.parties.exists(p, p.role == 1 && p.age >= 18)'
    writes: has_adult
    severity: blocking
    category: parties
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	input := &testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{
			{Name: "Alice", Role: 1, Age: 25},
		},
	}
	results := eng.RunMap(input)
	if !results["has_adult"].Passed {
		t.Error("should pass when matching party exists")
	}
}

func TestArrayExistsFail(t *testing.T) {
	yaml := `
evaluations:
  - name: has_adult_party
    expression: 'input.parties.exists(p, p.role == 1 && p.age >= 18)'
    writes: has_adult
    severity: blocking
    category: parties
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	input := &testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{
			{Name: "Bob", Role: 1, Age: 16},
		},
	}
	results := eng.RunMap(input)
	if results["has_adult"].Passed {
		t.Error("should fail when no matching party exists")
	}
}

func TestArrayEmptyFails(t *testing.T) {
	yaml := `
evaluations:
  - name: has_adult_party
    expression: 'input.parties.exists(p, p.role == 1)'
    writes: has_adult
    severity: blocking
    category: parties
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	input := &testv1.TestEvaluatorContainer{}
	results := eng.RunMap(input)
	if results["has_adult"].Passed {
		t.Error("should fail when parties list is empty")
	}
}

func TestArraySizeCheck(t *testing.T) {
	yaml := `
evaluations:
  - name: has_parties
    expression: 'size(input.parties) > 0'
    writes: has_parties
    severity: blocking
    category: parties
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	empty := eng.RunMap(&testv1.TestEvaluatorContainer{})
	if empty["has_parties"].Passed {
		t.Error("should fail with no parties")
	}

	withParties := eng.RunMap(&testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{{Name: "Alice"}},
	})
	if !withParties["has_parties"].Passed {
		t.Error("should pass with parties")
	}
}

func TestArrayAllMacro(t *testing.T) {
	yaml := `
evaluations:
  - name: all_adults
    expression: 'input.parties.all(p, p.age >= 18)'
    writes: all_adults
    severity: blocking
    category: parties
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	// All adults — pass.
	allAdults := eng.RunMap(&testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{
			{Name: "Alice", Age: 25},
			{Name: "Bob", Age: 30},
		},
	})
	if !allAdults["all_adults"].Passed {
		t.Error("should pass when all parties are adults")
	}

	// One minor — fail.
	oneMinor := eng.RunMap(&testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{
			{Name: "Alice", Age: 25},
			{Name: "Charlie", Age: 16},
		},
	})
	if oneMinor["all_adults"].Passed {
		t.Error("should fail when one party is underage")
	}

	// Empty list — all() on empty returns true (CEL semantics).
	emptyResult := eng.RunMap(&testv1.TestEvaluatorContainer{})
	if !emptyResult["all_adults"].Passed {
		t.Error("all() on empty list returns true in CEL")
	}
}

func TestArrayAutoDerivesInputReads(t *testing.T) {
	yaml := `
evaluations:
  - name: party_check
    expression: 'input.parties.exists(p, p.role == 1)'
    writes: has_role_one
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	// reads should be auto-derived as [input.parties].
	for _, ev := range eng.Evaluators() {
		if ev.Name() == "has_role_one" {
			reads := ev.Reads()
			if len(reads) != 1 || string(reads[0]) != "input.parties" {
				t.Errorf("expected auto-derived reads [input.parties], got %v", reads)
			}
		}
	}

	// InputFields should return [input.parties].
	fields := eng.InputFields("has_role_one")
	if len(fields) != 1 || fields[0] != "input.parties" {
		t.Errorf("expected InputFields [input.parties], got %v", fields)
	}
}

func TestArrayMixedWithScalarAutoDerivesAll(t *testing.T) {
	yaml := `
evaluations:
  - name: mixed_check
    expression: 'input.score >= 100 && input.parties.exists(p, p.role == 1)'
    writes: mixed
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	fields := eng.InputFields("mixed")
	fieldSet := make(map[string]bool)
	for _, f := range fields {
		fieldSet[f] = true
	}
	if !fieldSet["input.score"] || !fieldSet["input.parties"] {
		t.Errorf("expected InputFields to contain input.score and input.parties, got %v", fields)
	}
}

func TestArrayFingerprintChangesWhenElementAdded(t *testing.T) {
	reads := []evalengine.FieldRef{"input.parties"}

	msg1 := &testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{{Name: "Alice", Role: 1, Age: 25}},
	}
	msg2 := &testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{
			{Name: "Alice", Role: 1, Age: 25},
			{Name: "Bob", Role: 2, Age: 30},
		},
	}

	fp1 := evalengine.ComputeFingerprint(reads, msg1)
	fp2 := evalengine.ComputeFingerprint(reads, msg2)

	if fp1 == "" {
		t.Error("fingerprint for array field should not be empty")
	}
	if fp1 == fp2 {
		t.Error("fingerprint should change when array element is added")
	}
}

func TestArrayFingerprintChangesWhenElementModified(t *testing.T) {
	reads := []evalengine.FieldRef{"input.parties"}

	msg1 := &testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{{Name: "Alice", Role: 1, Age: 25}},
	}
	msg2 := &testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{{Name: "Alice", Role: 1, Age: 30}},
	}

	fp1 := evalengine.ComputeFingerprint(reads, msg1)
	fp2 := evalengine.ComputeFingerprint(reads, msg2)

	if fp1 == fp2 {
		t.Error("fingerprint should change when array element field is modified")
	}
}

func TestArrayFingerprintStableForSameData(t *testing.T) {
	reads := []evalengine.FieldRef{"input.parties"}

	msg := &testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{
			{Name: "Alice", Role: 1, Age: 25},
			{Name: "Bob", Role: 2, Age: 30},
		},
	}

	fp1 := evalengine.ComputeFingerprint(reads, msg)
	fp2 := evalengine.ComputeFingerprint(reads, msg)

	if fp1 != fp2 {
		t.Errorf("fingerprint should be stable: %s != %s", fp1, fp2)
	}
}

func TestArrayFingerprintEmptyVsPopulated(t *testing.T) {
	reads := []evalengine.FieldRef{"input.parties"}

	empty := &testv1.TestEvaluatorContainer{}
	populated := &testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{{Name: "Alice"}},
	}

	fpEmpty := evalengine.ComputeFingerprint(reads, empty)
	fpPopulated := evalengine.ComputeFingerprint(reads, populated)

	if fpEmpty == fpPopulated {
		t.Error("fingerprint should differ between empty and populated array")
	}
}

func TestArrayWithPreconditions(t *testing.T) {
	yaml := `
evaluations:
  - name: adult_party_check
    preconditions:
      - expression: 'size(input.parties) > 0'
        description: "At least one party must be provided"
    expression: 'input.parties.exists(p, p.age >= 18)'
    writes: has_adult
    severity: blocking
    category: parties
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	// Empty parties → pending (precondition fails).
	empty := eng.RunMap(&testv1.TestEvaluatorContainer{})
	r := empty["has_adult"]
	if r.Passed {
		t.Error("should not pass with empty parties")
	}
	if !r.Pending {
		t.Error("should be pending when precondition fails")
	}
	if len(r.PendingPreconditions) != 1 || r.PendingPreconditions[0] != "At least one party must be provided" {
		t.Errorf("expected precondition description, got %v", r.PendingPreconditions)
	}

	// Parties present but all underage → fails (not pending).
	underage := eng.RunMap(&testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{{Name: "Charlie", Age: 16}},
	})
	r2 := underage["has_adult"]
	if r2.Passed {
		t.Error("should fail with underage party")
	}
	if r2.Pending {
		t.Error("should NOT be pending when precondition passes but expression fails")
	}

	// Parties present with adult → passes.
	adult := eng.RunMap(&testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{{Name: "Alice", Age: 25}},
	})
	if !adult["has_adult"].Passed {
		t.Error("should pass with adult party")
	}
}

func TestArrayWithDependencyGraph(t *testing.T) {
	yaml := `
evaluations:
  - name: has_parties_eval
    expression: 'size(input.parties) > 0'
    writes: has_parties
    severity: blocking
    category: parties

  - name: adult_check_eval
    expression: 'has_parties == true && input.parties.all(p, p.age >= 18)'
    writes: all_adults
    severity: blocking
    category: parties
`
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	// Verify auto-derived dependency: all_adults depends on has_parties.
	blocks := eng.Graph().Blocks("has_parties")
	if len(blocks) != 1 || blocks[0] != "all_adults" {
		t.Errorf("expected has_parties to block [all_adults], got %v", blocks)
	}

	// Empty parties → has_parties fails → all_adults blocked.
	empty := eng.RunMap(&testv1.TestEvaluatorContainer{})
	if empty["has_parties"].Passed {
		t.Error("has_parties should fail with empty list")
	}
	if empty["all_adults"].Passed {
		t.Error("all_adults should be blocked when has_parties fails")
	}

	// One adult → both pass.
	adult := eng.RunMap(&testv1.TestEvaluatorContainer{
		Parties: []*testv1.Party{{Name: "Alice", Age: 25}},
	})
	if !adult["has_parties"].Passed {
		t.Error("has_parties should pass")
	}
	if !adult["all_adults"].Passed {
		t.Error("all_adults should pass when all parties are adults")
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
