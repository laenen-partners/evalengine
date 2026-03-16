package evalengine_test

import (
	"testing"

	"github.com/laenen-partners/evalengine"
)

// --- Status derivation tests ---

func TestDeriveStatusActive(t *testing.T) {
	eng := loadTestEngine(t)
	results := []evalengine.Result{
		{Name: "score_sufficient", Passed: true},
		{Name: "is_active", Passed: true},
		{Name: "eligible", Passed: true},
	}
	if s := eng.DeriveStatus(results); s != evalengine.StatusAllPassed {
		t.Errorf("expected StatusAllPassed, got %d", s)
	}
}

func TestDeriveStatusActionRequired(t *testing.T) {
	eng := loadTestEngine(t)
	// Failing eval with no workflow and no blocked deps.
	results := []evalengine.Result{
		{Name: "score_sufficient", Passed: false, Resolution: "Boost score"},
		{Name: "is_active", Passed: true},
		{Name: "eligible", Passed: false},
	}
	s := eng.DeriveStatus(results)
	// score_sufficient fails with no workflow → ActionRequired (or Blocked for eligible)
	if s != evalengine.StatusActionRequired && s != evalengine.StatusBlocked {
		t.Errorf("expected StatusActionRequired or StatusBlocked, got %d", s)
	}
}

func TestDeriveStatusOnboardingInProgress(t *testing.T) {
	eng := loadTestEngine(t)
	results := []evalengine.Result{
		{Name: "score_sufficient", Passed: false, ResolutionWorkflow: "ScoreBoostWorkflow"},
		{Name: "is_active", Passed: true},
		{Name: "eligible", Passed: false},
	}
	s := eng.DeriveStatus(results)
	if s != evalengine.StatusWorkflowActive {
		t.Errorf("expected StatusWorkflowActive, got %d", s)
	}
}
