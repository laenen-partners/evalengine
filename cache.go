package evalengine

import (
	"strings"
	"time"
)

// CachedResult wraps a Result with the time it was evaluated and an optional
// fingerprint of the input fields used to produce it.
// The caller owns persistence — the engine is stateless.
type CachedResult struct {
	Result      Result
	EvaluatedAt time.Time
	Fingerprint string // hash of proto input fields; empty if not computed
}

// ToCachedResults converts a slice of results into a cache map keyed by name,
// stamped with the given evaluation time. No fingerprints are computed; use
// Engine.ToCachedResults for fingerprint-aware caching.
func ToCachedResults(results []Result, evaluatedAt time.Time) map[string]CachedResult {
	m := make(map[string]CachedResult, len(results))
	for _, r := range results {
		m[r.Name] = CachedResult{Result: r, EvaluatedAt: evaluatedAt}
	}
	return m
}

// hasOnlyInputReads returns true if all reads reference proto input fields
// (prefixed with "input.") and there is at least one read.
func hasOnlyInputReads(reads []FieldRef) bool {
	if len(reads) == 0 {
		return false
	}
	for _, r := range reads {
		if !strings.HasPrefix(string(r), "input.") {
			return false
		}
	}
	return true
}
