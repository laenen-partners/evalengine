# evalengine

A Go evaluation engine that compiles YAML-defined rules into [CEL](https://cel.dev/) expressions, resolves their dependencies automatically, and executes them against a protobuf message.

## Install

```sh
go get github.com/laenen-partners/evalengine
```

## Usage

### Define evaluations in YAML

```yaml
evaluations:
  - name: score_eval
    description: Score meets minimum threshold
    expression: "input.score >= 100"
    reads: [input.score]
    writes: score_sufficient
    resolution_workflow: ScoreBoostWorkflow
    resolution: "Boost score to meet threshold"
    severity: blocking
    category: score
    cache_ttl: "10m"

  - name: active_eval
    description: Account is active
    expression: "input.is_active == true"
    reads: [input.is_active]
    writes: is_active
    resolution: "Activate account"
    severity: blocking
    category: status

  - name: eligible_eval
    description: Eligible when score sufficient and active
    expression: "score_sufficient == true && is_active == true"
    reads: [score_sufficient, is_active]
    writes: eligible
    resolution: "Meet all criteria"
    severity: blocking
    category: combined
    cache_ttl: "1m"
```

Reads prefixed with `input.` reference fields on the proto message. Bare names reference upstream evaluator outputs — the dependency graph is built automatically.

### Create and run the engine

```go
cfg, _ := evalengine.LoadDefinitionsFromFile("evals.yaml")
eng, _ := evalengine.NewEngine(cfg, &mypb.MyMessage{})

input := &mypb.MyMessage{Score: 150, IsActive: true}
results := eng.Run(input)

for _, r := range results {
    fmt.Printf("%s: passed=%v\n", r.Name, r.Passed)
}
```

### Derive status

```go
status := eng.DeriveStatus(results)

switch status {
case evalengine.StatusAllPassed:
    // all evaluations passed
case evalengine.StatusWorkflowActive:
    // a resolution workflow is running
case evalengine.StatusActionRequired:
    // manual action needed
case evalengine.StatusBlocked:
    // upstream dependencies not met
}
```

### Caching

Some input data may be expensive to retrieve. The cache lets you skip re-evaluation when results are still fresh.

```go
// First run — build cache with fingerprints.
results := eng.Run(input)
cache := eng.ToCachedResults(results, input, time.Now())
// Persist cache (Redis, database, etc.)

// Later — reuse cached results when possible.
results, reused := eng.RunWithCache(input, cache, time.Now())
// reused["score_sufficient"] == true means it was served from cache.
```

Two tiers:
1. **TTL** — if the cached result is within its `cache_ttl`, reuse it directly.
2. **Fingerprint** — if TTL expired but the proto input fields haven't changed (SHA256 match), reuse the result anyway. Only applies to evaluators that read exclusively from `input.*` fields.

### YAML config reference

| Field | Required | Description |
|---|---|---|
| `name` | yes | Evaluator identifier (used in error messages) |
| `description` | no | Human-readable description |
| `expression` | yes | CEL expression, must return `bool` |
| `reads` | yes | Dependencies: `input.*` for proto fields, bare names for upstream outputs |
| `writes` | yes | Output name — used as result key, CEL variable, and graph node |
| `resolution_workflow` | no | Workflow ID to trigger on failure |
| `resolution` | no | Human description of how to resolve failure |
| `severity` | no | Severity level (e.g., `blocking`) |
| `category` | no | Grouping category |
| `cache_ttl` | no | Go duration (e.g., `10m`, `1h`). Default: no caching |

### Dependency graph

The graph is built from `reads`/`writes` declarations and validated at engine creation:

```go
graph := eng.Graph()
graph.ExecutionOrder()              // topologically sorted evaluator names
graph.DependenciesMet(name, results) // true if all upstream deps passed
graph.BlockedBy(name, results)       // which upstream deps failed
graph.Issues()                       // validation issues (circular deps, missing producers, etc.)
```
