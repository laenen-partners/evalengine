package evalengine

// Status represents the logical outcome of evaluating all results.
type Status int

const (
	StatusAllPassed      Status = iota // every evaluation passed
	StatusWorkflowActive               // a resolution workflow is running
	StatusActionRequired               // a failing eval needs manual action
	StatusBlocked                      // a failing eval's dependencies aren't met
)

// DeriveStatus determines the overall status from evaluation results.
// Status is derived, never stored directly — it reflects the current state of
// all evaluations.
func DeriveStatus(results []Result, graph *EvalGraph) Status {
	allPassed := true
	hasRunningWorkflow := false
	hasBlockedEval := false
	hasActionRequired := false

	resultsMap := make(map[string]Result, len(results))
	for _, r := range results {
		resultsMap[r.Name] = r
	}

	for _, r := range results {
		if r.Passed {
			continue
		}
		allPassed = false

		if !graph.DependenciesMet(r.Name, resultsMap) {
			hasBlockedEval = true
			continue
		}

		if r.ResolutionWorkflow != "" {
			hasRunningWorkflow = true
		} else {
			hasActionRequired = true
		}
	}

	if allPassed {
		return StatusAllPassed
	}
	if hasRunningWorkflow {
		return StatusWorkflowActive
	}
	if hasActionRequired {
		return StatusActionRequired
	}
	if hasBlockedEval {
		return StatusBlocked
	}
	return StatusActionRequired
}
