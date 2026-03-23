package evalengine_test

import (
	"strings"
	"testing"

	"github.com/laenen-partners/evalengine"
	testv1 "github.com/laenen-partners/evalengine/proto/gen/go/test/v1"
)

func TestValidateConfigValid(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(testYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := evalengine.ValidateConfig(cfg); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateConfigEmptyEvaluations(t *testing.T) {
	cfg := &evalengine.EvalConfig{}
	err := evalengine.ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for empty evaluations")
	}
	assertContains(t, err.Error(), "empty")
}

func TestValidateConfigMissingExpression(t *testing.T) {
	cfg := &evalengine.EvalConfig{
		Evaluations: []evalengine.EvalDefinition{
			{Name: "test", Writes: "test_out"},
		},
	}
	err := evalengine.ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing expression")
	}
	assertContains(t, err.Error(), "expression is required")
}

func TestValidateConfigMissingWrites(t *testing.T) {
	cfg := &evalengine.EvalConfig{
		Evaluations: []evalengine.EvalDefinition{
			{Name: "test", Expression: "true"},
		},
	}
	err := evalengine.ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing writes")
	}
	assertContains(t, err.Error(), "writes is required")
}

func TestValidateConfigMissingName(t *testing.T) {
	cfg := &evalengine.EvalConfig{
		Evaluations: []evalengine.EvalDefinition{
			{Expression: "true", Writes: "out"},
		},
	}
	err := evalengine.ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	assertContains(t, err.Error(), "name is required")
}

func TestValidateConfigDuplicateWrites(t *testing.T) {
	cfg := &evalengine.EvalConfig{
		Evaluations: []evalengine.EvalDefinition{
			{Name: "a", Expression: "true", Writes: "dup"},
			{Name: "b", Expression: "true", Writes: "dup"},
		},
	}
	err := evalengine.ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for duplicate writes")
	}
	assertContains(t, err.Error(), "duplicate writes")
}

func TestValidateConfigBadCacheTTL(t *testing.T) {
	cfg := &evalengine.EvalConfig{
		Evaluations: []evalengine.EvalDefinition{
			{Name: "test", Expression: "true", Writes: "out", CacheTTL: "notaduration"},
		},
	}
	err := evalengine.ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for bad cache_ttl")
	}
	assertContains(t, err.Error(), "cache_ttl")
}

func TestValidateConfigEmptyPreconditionExpression(t *testing.T) {
	cfg := &evalengine.EvalConfig{
		Evaluations: []evalengine.EvalDefinition{
			{
				Name:       "test",
				Expression: "true",
				Writes:     "out",
				Preconditions: []evalengine.Precondition{
					{Description: "oops, no expression"},
				},
			},
		},
	}
	err := evalengine.ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for empty precondition expression")
	}
	assertContains(t, err.Error(), "preconditions[0].expression is required")
}

func TestValidateConfigMultipleErrors(t *testing.T) {
	cfg := &evalengine.EvalConfig{
		Evaluations: []evalengine.EvalDefinition{
			{Name: "a", Writes: "out"},        // missing expression
			{Expression: "true", Writes: "b"}, // missing name
		},
	}
	err := evalengine.ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	ve, ok := err.(*evalengine.ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(ve.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d: %v", len(ve.Errors), ve.Errors)
	}
}

func TestValidateFullValid(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(testYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := evalengine.Validate(cfg, &testv1.TestEvaluatorContainer{}); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateFullInvalidCEL(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(invalidCELYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := evalengine.Validate(cfg, &testv1.TestEvaluatorContainer{}); err == nil {
		t.Fatal("expected error for invalid CEL")
	}
}

func TestValidateFullCircularDep(t *testing.T) {
	cfg, err := evalengine.LoadDefinitions(strings.NewReader(circularYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := evalengine.Validate(cfg, &testv1.TestEvaluatorContainer{}); err == nil {
		t.Fatal("expected error for circular dependency")
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("expected error to contain %q, got: %s", want, got)
	}
}
