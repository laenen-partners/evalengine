package evalengine_test

import (
	"strings"
	"testing"

	"github.com/laenen-partners/evalengine"
	testv1 "github.com/laenen-partners/evalengine/proto/gen/go/test/v1"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// testYAML exercises the engine with plain CEL scalar variables (score int,
// active bool) so the eval package has no dependency on any application proto.
// eligible depends on score_sufficient and active to exercise the graph.
const testYAML = `
evaluations:
  - name: score_sufficient_eval
    description: Score meets minimum threshold
    expression: "input.score >= 100"
    reads: [input.score]
    writes: score_sufficient
    resolution_workflow: ScoreBoostWorkflow
    resolution: "Boost score"
    severity: blocking
    category: score

  - name: is_active_eval
    description: Account is active
    expression: "input.nested_object.is_active == true"
    reads: [input.nested_object.is_active]
    writes: is_active
    resolution_workflow: ActivationWorkflow
    resolution: "Activate account"
    severity: blocking
    category: status

  - name: eligible_eval
    description: Eligible when score sufficient and active
    expression: "score_sufficient == true && is_active == true"
    reads: [is_active, score_sufficient]
    writes: eligible
    resolution: "Meet all criteria"
    severity: blocking
    category: combined
`

func loadTestEngine(t *testing.T) *evalengine.Engine {
	t.Helper()
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(testYAML))
	if err != nil {
		t.Fatalf("load definitions: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	return eng
}

func TestAllExpressionsCompile(t *testing.T) {
	loadTestEngine(t)
}

func TestGraphIsValid(t *testing.T) {
	eng := loadTestEngine(t)
	for _, issue := range eng.Graph().Issues() {
		if issue.Severity == "error" {
			t.Errorf("[%s] %s", issue.Type, issue.Message)
		}
	}
}

func TestGraphExecutionOrder(t *testing.T) {
	eng := loadTestEngine(t)
	order := eng.Graph().ExecutionOrder()
	if len(order) != 3 {
		t.Fatalf("expected 3 evaluators in order, got %d: %v", len(order), order)
	}
	// eligible must come last — it depends on the other two.
	if order[len(order)-1] != "eligible" {
		t.Errorf("expected 'eligible' last, got %q", order[len(order)-1])
	}
}

func TestMaxDepthIsReasonable(t *testing.T) {
	eng := loadTestEngine(t)
	if eng.Graph().MaxDepth() > 5 {
		t.Errorf("max depth %d exceeds 5", eng.Graph().MaxDepth())
	}
}

func TestEvalAllPass(t *testing.T) {
	eng := loadTestEngine(t)

	input := &testv1.TestEvaluatorContainer{
		Score: 150,
		NestedObject: &testv1.NestedObject{
			IsActive: true,
		},
	}
	results := eng.RunMap(input)

	for name, r := range results {
		if !r.Passed {
			t.Errorf("eval %q should pass, got error: %s", name, r.Error)
		}
	}

	status := eng.DeriveStatus(eng.Run(input))
	if status != evalengine.StatusAllPassed {
		t.Errorf("expected status 'active', got %q", status)
	}
}

func TestEvalScoreTooLow(t *testing.T) {
	eng := loadTestEngine(t)

	input := &testv1.TestEvaluatorContainer{
		Score: 50,
		NestedObject: &testv1.NestedObject{
			IsActive: true,
		},
	}
	results := eng.RunMap(input)

	if results["score_sufficient"].Passed {
		t.Error("score_sufficient should fail when score < 100")
	}
	if !results["is_active"].Passed {
		t.Error("is_active should pass")
	}
	if results["eligible"].Passed {
		t.Error("eligible should be blocked by failing score_sufficient")
	}

	status := eng.DeriveStatus(eng.Run(input))
	if status == evalengine.StatusAllPassed {
		t.Error("status should not be 'is_active' when score is too low")
	}
}

func TestEvalAllFail(t *testing.T) {
	eng := loadTestEngine(t)

	input := &testv1.TestEvaluatorContainer{
		Score: 0,
		NestedObject: &testv1.NestedObject{
			IsActive: false,
		},
	}
	results := eng.Run(input)

	for _, r := range results {
		if r.Passed {
			t.Errorf("eval %q should fail for zero-value input", r.Name)
		}
	}
}

func TestFingerprintStability(t *testing.T) {
	msg := wrapperspb.Int64(42)
	reads := []evalengine.FieldRef{"input.value"}

	fp1 := evalengine.ComputeFingerprint(reads, msg)
	fp2 := evalengine.ComputeFingerprint(reads, msg)

	if fp1 != fp2 {
		t.Errorf("fingerprint should be stable: %s != %s", fp1, fp2)
	}
}

func TestFingerprintChangesOnDataChange(t *testing.T) {
	reads := []evalengine.FieldRef{"input.value"}

	fp1 := evalengine.ComputeFingerprint(reads, wrapperspb.Int64(42))
	fp2 := evalengine.ComputeFingerprint(reads, wrapperspb.Int64(43))

	if fp1 == fp2 {
		t.Error("fingerprint should change when value changes")
	}
}

func TestFingerprintNilMessage(t *testing.T) {
	fp := evalengine.ComputeFingerprint([]evalengine.FieldRef{"input.value"}, nil)
	if fp != "" {
		t.Errorf("expected empty string for nil message, got %q", fp)
	}
}

func TestFingerprintEmptyReads(t *testing.T) {
	msg := wrapperspb.Int64(42)
	fp1 := evalengine.ComputeFingerprint(nil, msg)
	fp2 := evalengine.ComputeFingerprint(nil, msg)
	if fp1 == "" {
		t.Error("fingerprint with no reads should return a valid hash, not empty string")
	}
	if fp1 != fp2 {
		t.Errorf("fingerprint with no reads should be stable: %s != %s", fp1, fp2)
	}
}

func TestFingerprintOrderIndependent(t *testing.T) {
	msg := &testv1.TestEvaluatorContainer{Score: 100}
	reads1 := []evalengine.FieldRef{"input.score", "score_sufficient"}
	reads2 := []evalengine.FieldRef{"score_sufficient", "input.score"}

	fp1 := evalengine.ComputeFingerprint(reads1, msg)
	fp2 := evalengine.ComputeFingerprint(reads2, msg)

	if fp1 != fp2 {
		t.Errorf("fingerprint should be independent of read order: %s != %s", fp1, fp2)
	}
}

func TestFingerprintInputPrefixStripped(t *testing.T) {
	msg := &testv1.TestEvaluatorContainer{Score: 77}
	fpBare := evalengine.ComputeFingerprint([]evalengine.FieldRef{"score"}, msg)
	fpPrefixed := evalengine.ComputeFingerprint([]evalengine.FieldRef{"input.score"}, msg)

	if fpBare != fpPrefixed {
		t.Errorf("input. prefix should be stripped and resolve the same field: %s != %s", fpBare, fpPrefixed)
	}
}

func TestFingerprintUnknownFieldIsStable(t *testing.T) {
	msg := &testv1.TestEvaluatorContainer{Score: 100}
	fp1 := evalengine.ComputeFingerprint([]evalengine.FieldRef{"no_such_field"}, msg)
	fp2 := evalengine.ComputeFingerprint([]evalengine.FieldRef{"no_such_field"}, msg)

	if fp1 == "" {
		t.Error("fingerprint for unknown field should still return a valid hash")
	}
	if fp1 != fp2 {
		t.Errorf("fingerprint for unknown field should be stable: %s != %s", fp1, fp2)
	}
}

func TestFingerprintTypeCollisionPrevented(t *testing.T) {
	// int(1) and the string representation "1" must not hash the same.
	// wrapperspb.Int64(1).value is int64(1); wrapperspb.String("1").value is "1".
	readsInt := []evalengine.FieldRef{"input.value"}
	fpInt := evalengine.ComputeFingerprint(readsInt, wrapperspb.Int64(1))

	readsStr := []evalengine.FieldRef{"input.value"}
	fpStr := evalengine.ComputeFingerprint(readsStr, wrapperspb.String("1"))

	if fpInt == fpStr {
		t.Error("fingerprints for int(1) and string(\"1\") must differ to prevent type collisions")
	}
}

// --- Graph tests ---

const circularYAML = `
evaluations:
  - name: a_eval
    expression: "b == true"
    reads: [b]
    writes: a
    severity: blocking
    category: test

  - name: b_eval
    expression: "a == true"
    reads: [a]
    writes: b
    severity: blocking
    category: test
`

func TestGraphCircularDependencyDetected(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(circularYAML))
	if err != nil {
		t.Fatalf("load definitions: %v", err)
	}
	_, err = evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err == nil {
		t.Fatal("expected error for circular dependency, got nil")
	}
}

const duplicateWriterYAML = `
evaluations:
  - name: a1_eval
    expression: "input.score >= 100"
    reads: [input.score]
    writes: score_ok
    severity: blocking
    category: test

  - name: a2_eval
    expression: "input.score >= 50"
    reads: [input.score]
    writes: score_ok
    severity: blocking
    category: test
`

func TestGraphDuplicateProducerDetected(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(duplicateWriterYAML))
	if err != nil {
		t.Fatalf("load definitions: %v", err)
	}
	_, err = evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err == nil {
		t.Fatal("expected error for duplicate producer, got nil")
	}
}

const missingProducerYAML = `
evaluations:
  - name: composite_eval
    expression: "ghost == true"
    reads: [ghost]
    writes: composite
    severity: blocking
    category: test
`

func TestGraphMissingProducerDetected(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(missingProducerYAML))
	if err != nil {
		t.Fatalf("load definitions: %v", err)
	}
	_, err = evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err == nil {
		t.Fatal("expected error for missing producer, got nil")
	}
}

func TestGraphDependenciesMet(t *testing.T) {
	eng := loadTestEngine(t)
	g := eng.Graph()

	passed := map[string]evalengine.Result{
		"score_sufficient": {Name: "score_sufficient", Passed: true},
		"is_active":        {Name: "is_active", Passed: true},
	}
	if !g.DependenciesMet("eligible", passed) {
		t.Error("DependenciesMet should be true when all deps passed")
	}

	oneFailing := map[string]evalengine.Result{
		"score_sufficient": {Name: "score_sufficient", Passed: false},
		"is_active":        {Name: "is_active", Passed: true},
	}
	if g.DependenciesMet("eligible", oneFailing) {
		t.Error("DependenciesMet should be false when a dep has not passed")
	}
}

func TestGraphBlockedBy(t *testing.T) {
	eng := loadTestEngine(t)
	g := eng.Graph()

	mixed := map[string]evalengine.Result{
		"score_sufficient": {Name: "score_sufficient", Passed: false},
		"is_active":        {Name: "is_active", Passed: true},
	}
	blocked := g.BlockedBy("eligible", mixed)
	if len(blocked) != 1 || blocked[0] != "score_sufficient" {
		t.Errorf("expected blocked by [score_sufficient], got %v", blocked)
	}
}

// --- Engine constructor tests ---

func TestNewEngineFromBytes(t *testing.T) {
	data := []byte(testYAML)
	eng, err := evalengine.NewEngineFromBytes(data, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("NewEngineFromBytes: %v", err)
	}
	if len(eng.Evaluators()) != 3 {
		t.Errorf("expected 3 evaluators, got %d", len(eng.Evaluators()))
	}
}

func TestEvaluatorsReturnsAll(t *testing.T) {
	eng := loadTestEngine(t)
	evs := eng.Evaluators()
	if len(evs) != 3 {
		t.Errorf("expected 3 evaluators, got %d", len(evs))
	}
}

const invalidCELYAML = `
evaluations:
  - name: bad_eval
    expression: "input.score >=="
    reads: [input.score]
    writes: bad
    severity: blocking
    category: test
`

func TestInvalidCELExpressionRejected(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(invalidCELYAML))
	if err != nil {
		t.Fatalf("load definitions: %v", err)
	}
	_, err = evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err == nil {
		t.Fatal("expected compile error for invalid CEL expression, got nil")
	}
}

const nonBoolCELYAML = `
evaluations:
  - name: str_eval
    expression: "input.score + 1"
    reads: [input.score]
    writes: str_result
    severity: blocking
    category: test
`

func TestNonBoolCELExpressionRejected(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(nonBoolCELYAML))
	if err != nil {
		t.Fatalf("load definitions: %v", err)
	}
	_, err = evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err == nil {
		t.Fatal("expected error for non-bool CEL expression, got nil")
	}
}

func TestInvalidYAMLRejected(t *testing.T) {
	_, err := evalengine.LoadDefinitions(strings.NewReader("not: valid: yaml: [[["))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestInvalidCacheTTLRejected(t *testing.T) {
	badTTL := `
evaluations:
  - name: ttl_eval
    expression: "input.score >= 100"
    reads: [input.score]
    writes: score_ok
    severity: blocking
    category: test
    cache_ttl: "not-a-duration"
`
	_, err := evalengine.LoadDefinitions(strings.NewReader(badTTL))
	if err == nil {
		t.Fatal("expected error for invalid cache_ttl, got nil")
	}
}
