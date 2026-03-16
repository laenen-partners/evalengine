package evalengine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/laenen-partners/evalengine"
	testv1 "github.com/laenen-partners/evalengine/proto/gen/go/test/v1"
)

const cacheTestYAML = `
evaluations:
  - name: score_eval
    description: Score check
    expression: "input.score >= 100"
    reads: [input.score]
    writes: score_sufficient
    resolution: "Boost score"
    severity: blocking
    category: score
    cache_ttl: "10m"

  - name: active_eval
    description: Active check
    expression: "input.nested_object.is_active == true"
    reads: [input.nested_object.is_active]
    writes: is_active
    resolution: "Activate"
    severity: blocking
    category: status
    cache_ttl: "10m"

  - name: eligible_eval
    description: Downstream
    expression: "score_sufficient == true && is_active == true"
    reads: [score_sufficient, is_active]
    writes: eligible
    resolution: "Meet criteria"
    severity: blocking
    category: combined
    cache_ttl: "1m"
`

func loadCacheTestEngine(t *testing.T) *evalengine.Engine {
	t.Helper()
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(cacheTestYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	return eng
}

func TestCacheTTLParsed(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(cacheTestYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eng, err := evalengine.NewEngine(cfg, &testv1.TestEvaluatorContainer{})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	evs := eng.Evaluators()
	if evs[0].CacheTTL() != 10*time.Minute {
		t.Errorf("expected 10m, got %v", evs[0].CacheTTL())
	}
	if evs[2].CacheTTL() != 1*time.Minute {
		t.Errorf("expected 1m, got %v", evs[2].CacheTTL())
	}
}

func TestCacheTTLDefaultsToZero(t *testing.T) {
	eng := loadTestEngine(t) // testYAML has no cache_ttl
	for _, ev := range eng.Evaluators() {
		if ev.CacheTTL() != 0 {
			t.Errorf("evaluator %q: expected zero TTL, got %v", ev.Name(), ev.CacheTTL())
		}
	}
}

func TestInvalidCacheTTLRejected(t *testing.T) {
	y := `
evaluations:
  - name: bad
    expression: "input.score >= 0"
    reads: [input.score]
    writes: bad_result
    severity: blocking
    category: test
    cache_ttl: "not-a-duration"
`
	_, err := evalengine.LoadDefinitions(strings.NewReader(y))
	if err == nil {
		t.Fatal("expected error for invalid cache_ttl")
	}
}

func TestRunWithCacheAllFresh(t *testing.T) {
	eng := loadCacheTestEngine(t)
	now := time.Now()

	cache := map[string]evalengine.CachedResult{
		"score_sufficient": {Result: evalengine.Result{Name: "score_sufficient", Passed: true}, EvaluatedAt: now.Add(-5 * time.Minute)},
		"is_active":        {Result: evalengine.Result{Name: "is_active", Passed: true}, EvaluatedAt: now.Add(-5 * time.Minute)},
		"eligible":         {Result: evalengine.Result{Name: "eligible", Passed: true}, EvaluatedAt: now.Add(-30 * time.Second)},
	}

	// Input doesn't matter — everything should come from cache.
	input := &testv1.TestEvaluatorContainer{Score: 0}
	results, reused := eng.RunWithCache(input, cache, now)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if len(reused) != 3 {
		t.Errorf("expected all 3 reused, got %d", len(reused))
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("evaluator %q should be passed (from cache)", r.Name)
		}
	}
}

func TestRunWithCacheAllStale(t *testing.T) {
	eng := loadCacheTestEngine(t)
	now := time.Now()

	// All entries expired (evaluated 20 minutes ago, TTLs are 10m and 1m).
	cache := map[string]evalengine.CachedResult{
		"score_sufficient": {Result: evalengine.Result{Name: "score_sufficient", Passed: true}, EvaluatedAt: now.Add(-20 * time.Minute)},
		"is_active":        {Result: evalengine.Result{Name: "is_active", Passed: true}, EvaluatedAt: now.Add(-20 * time.Minute)},
		"eligible":         {Result: evalengine.Result{Name: "eligible", Passed: true}, EvaluatedAt: now.Add(-20 * time.Minute)},
	}

	input := &testv1.TestEvaluatorContainer{Score: 50, NestedObject: &testv1.NestedObject{IsActive: true}}
	results, reused := eng.RunWithCache(input, cache, now)

	if len(reused) != 0 {
		t.Errorf("expected nothing reused, got %d", len(reused))
	}
	// score=50 < 100, so score_sufficient should fail on re-evaluation.
	for _, r := range results {
		if r.Name == "score_sufficient" && r.Passed {
			t.Error("score_sufficient should fail with score=50")
		}
	}
}

func TestRunWithCacheNilEqualsRun(t *testing.T) {
	eng := loadCacheTestEngine(t)
	input := &testv1.TestEvaluatorContainer{Score: 200, NestedObject: &testv1.NestedObject{IsActive: true}}

	runResults := eng.Run(input)
	cacheResults, reused := eng.RunWithCache(input, nil, time.Now())

	if len(reused) != 0 {
		t.Errorf("expected nothing reused with nil cache, got %d", len(reused))
	}
	if len(runResults) != len(cacheResults) {
		t.Fatalf("result count mismatch: Run=%d, RunWithCache=%d", len(runResults), len(cacheResults))
	}
	for i := range runResults {
		if runResults[i].Passed != cacheResults[i].Passed {
			t.Errorf("result %d mismatch: Run.Passed=%v, RunWithCache.Passed=%v", i, runResults[i].Passed, cacheResults[i].Passed)
		}
	}
}

func TestRunWithCacheMixedFreshStale(t *testing.T) {
	eng := loadCacheTestEngine(t)
	now := time.Now()

	// score_sufficient and is_active are fresh (5m ago, TTL 10m).
	// eligible is stale (5m ago, TTL 1m).
	cache := map[string]evalengine.CachedResult{
		"score_sufficient": {Result: evalengine.Result{Name: "score_sufficient", Passed: true}, EvaluatedAt: now.Add(-5 * time.Minute)},
		"is_active":        {Result: evalengine.Result{Name: "is_active", Passed: true}, EvaluatedAt: now.Add(-5 * time.Minute)},
		"eligible":         {Result: evalengine.Result{Name: "eligible", Passed: false}, EvaluatedAt: now.Add(-5 * time.Minute)},
	}

	// Input doesn't matter for cached evals; eligible re-evaluates using
	// cached upstream values (both true) so it should pass.
	input := &testv1.TestEvaluatorContainer{Score: 0}
	results, reused := eng.RunWithCache(input, cache, now)

	if !reused["score_sufficient"] || !reused["is_active"] {
		t.Error("upstream evals should be reused from cache")
	}
	if reused["eligible"] {
		t.Error("eligible should have been re-evaluated (stale)")
	}

	for _, r := range results {
		if r.Name == "eligible" && !r.Passed {
			t.Error("eligible should pass: upstream cached values are both true")
		}
	}
}

func TestRunWithCacheZeroTTLAlwaysReEvaluates(t *testing.T) {
	eng := loadTestEngine(t) // testYAML has no cache_ttl (zero TTL)
	now := time.Now()

	cache := map[string]evalengine.CachedResult{
		"score_sufficient": {Result: evalengine.Result{Name: "score_sufficient", Passed: true}, EvaluatedAt: now.Add(-1 * time.Second)},
		"is_active":        {Result: evalengine.Result{Name: "is_active", Passed: true}, EvaluatedAt: now.Add(-1 * time.Second)},
		"eligible":         {Result: evalengine.Result{Name: "eligible", Passed: true}, EvaluatedAt: now.Add(-1 * time.Second)},
	}

	input := &testv1.TestEvaluatorContainer{Score: 50, NestedObject: &testv1.NestedObject{IsActive: true}}
	_, reused := eng.RunWithCache(input, cache, now)

	if len(reused) != 0 {
		t.Errorf("zero-TTL evaluators should never be reused, got %d reused", len(reused))
	}
}

func TestToCachedResults(t *testing.T) {
	results := []evalengine.Result{
		{Name: "a", Passed: true},
		{Name: "b", Passed: false},
	}
	ts := time.Now()
	cached := evalengine.ToCachedResults(results, ts)

	if len(cached) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(cached))
	}
	if cached["a"].Result.Passed != true {
		t.Error("expected a.Passed=true")
	}
	if !cached["b"].EvaluatedAt.Equal(ts) {
		t.Error("expected EvaluatedAt to match")
	}
}

func TestEngineToCachedResultsComputesFingerprints(t *testing.T) {
	eng := loadCacheTestEngine(t)
	input := &testv1.TestEvaluatorContainer{Score: 200, NestedObject: &testv1.NestedObject{IsActive: true}}
	results := eng.Run(input)
	ts := time.Now()
	cached := eng.ToCachedResults(results, input, ts)

	// score_sufficient reads only input.score → should have fingerprint.
	if cached["score_sufficient"].Fingerprint == "" {
		t.Error("score_sufficient should have a fingerprint (reads input.score)")
	}
	// eligible reads upstream outputs only → no fingerprint.
	if cached["eligible"].Fingerprint != "" {
		t.Error("eligible should have no fingerprint (reads upstream outputs)")
	}
}

func TestFingerprintReusesWhenInputUnchanged(t *testing.T) {
	eng := loadCacheTestEngine(t)
	now := time.Now()
	input := &testv1.TestEvaluatorContainer{Score: 200, NestedObject: &testv1.NestedObject{IsActive: true}}

	// First run: build cache with fingerprints.
	results := eng.Run(input)
	cache := eng.ToCachedResults(results, input, now.Add(-20*time.Minute))

	// TTL expired (20m ago, TTL is 10m), but input is identical.
	// score_sufficient and is_active should be reused via fingerprint.
	results2, reused := eng.RunWithCache(input, cache, now)

	if !reused["score_sufficient"] {
		t.Error("score_sufficient should be reused via fingerprint match")
	}
	if !reused["is_active"] {
		t.Error("is_active should be reused via fingerprint match")
	}
	// eligible reads upstream outputs, not input fields → no fingerprint reuse,
	// but its upstream values haven't changed so it should still pass.
	for _, r := range results2 {
		if r.Name == "eligible" && !r.Passed {
			t.Error("eligible should pass")
		}
	}
}

func TestFingerprintReEvaluatesWhenInputChanged(t *testing.T) {
	eng := loadCacheTestEngine(t)
	now := time.Now()
	originalInput := &testv1.TestEvaluatorContainer{Score: 200, NestedObject: &testv1.NestedObject{IsActive: true}}

	// Build cache with fingerprints from score=200.
	results := eng.Run(originalInput)
	cache := eng.ToCachedResults(results, originalInput, now.Add(-20*time.Minute))

	// TTL expired AND input changed (score 200 → 50).
	newInput := &testv1.TestEvaluatorContainer{Score: 50, NestedObject: &testv1.NestedObject{IsActive: true}}
	results2, reused := eng.RunWithCache(newInput, cache, now)

	if reused["score_sufficient"] {
		t.Error("score_sufficient should NOT be reused (input changed)")
	}
	// is_active input didn't change (still true) → should be reused.
	if !reused["is_active"] {
		t.Error("is_active should be reused (nested_object.is_active unchanged)")
	}
	// score_sufficient should now fail with score=50.
	for _, r := range results2 {
		if r.Name == "score_sufficient" && r.Passed {
			t.Error("score_sufficient should fail with score=50")
		}
	}
}
