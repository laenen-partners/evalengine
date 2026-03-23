package evalengine

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
)

// ValidationError collects multiple validation issues.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed with %d error(s):\n  - %s", len(e.Errors), strings.Join(e.Errors, "\n  - "))
}

// ValidateConfig performs structural validation on an EvalConfig without
// requiring a proto message. It checks required fields, duplicate writes,
// cache_ttl format, and precondition expressions.
func ValidateConfig(cfg *EvalConfig) error {
	var errs []string

	if len(cfg.Evaluations) == 0 {
		return &ValidationError{Errors: []string{"evaluations list is empty"}}
	}

	writes := make(map[string]string, len(cfg.Evaluations)) // writes → name

	for i, def := range cfg.Evaluations {
		prefix := fmt.Sprintf("evaluations[%d]", i)
		if def.Name != "" {
			prefix = fmt.Sprintf("evaluations[%d] (%s)", i, def.Name)
		}

		if def.Expression == "" {
			errs = append(errs, fmt.Sprintf("%s: expression is required", prefix))
		}
		if def.Writes == "" {
			errs = append(errs, fmt.Sprintf("%s: writes is required", prefix))
		} else if prev, ok := writes[string(def.Writes)]; ok {
			errs = append(errs, fmt.Sprintf("%s: duplicate writes %q (already defined by %s)", prefix, def.Writes, prev))
		} else {
			writes[string(def.Writes)] = def.Name
		}
		if def.Name == "" {
			errs = append(errs, fmt.Sprintf("%s: name is required", prefix))
		}

		if def.CacheTTL != "" {
			if err := def.parseCacheTTL(); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", prefix, err))
			}
		}

		for j, pc := range def.Preconditions {
			if pc.Expression == "" {
				errs = append(errs, fmt.Sprintf("%s: preconditions[%d].expression is required", prefix, j))
			}
		}
	}

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

// Validate performs full validation: structural checks followed by CEL
// compilation and dependency graph construction. This catches everything
// ValidateConfig catches plus invalid CEL expressions, type mismatches,
// circular dependencies, and missing producers.
func Validate(cfg *EvalConfig, input proto.Message) error {
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	_, err := NewEngine(cfg, input)
	return err
}
