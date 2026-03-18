# evalengine

CEL-based evaluation engine. YAML-defined rules are compiled into a dependency graph and executed against a protobuf input message. Results feed into status derivation and optional caching.

## Architecture

```
YAML config → EvalDefinition[] → NewEngine(cfg, proto) → Engine
                                       ↓
                              CEL compile + BuildGraph
                                       ↓
                              Engine.Run(proto) → []Result → DeriveStatus → Status
                              Engine.RunWithCache(proto, cache, now) → []Result, reused
```

**Core files:**
- `definition.go` — `EvalDefinition`, `EvalConfig`, `FieldRef`, YAML loading + `cache_ttl` parsing
- `evaluator.go` — `Result` struct, `Evaluator` interface
- `cel.go` — `CELEvaluator` (implements `Evaluator` using compiled CEL programs)
- `engine.go` — `Engine` construction (`NewEngine`), execution (`Run`, `RunWithCache`), shared `execute` loop
- `graph.go` — `EvalGraph`, `BuildGraph` (Kahn's topological sort), `DependenciesMet`, `BlockedBy`
- `cache.go` — `CachedResult`, `ToCachedResults`, `hasOnlyInputReads`
- `fingerprint.go` — `ComputeFingerprint` (SHA256 of proto input field values)
- `status.go` — `Status` enum (`StatusAllPassed`, `StatusWorkflowActive`, `StatusActionRequired`, `StatusBlocked`), `DeriveStatus`

## Key concepts

**FieldRef** — either `"input.<field>"` (proto field) or a bare name like `"score_sufficient"` (upstream evaluator output). The `"input."` prefix distinguishes proto reads from dependency reads throughout the codebase.

**Writes** — each evaluator's `writes` field serves triple duty: result key, CEL variable name for downstream evaluators, and graph node name. It is the canonical evaluator identifier (not `name`).

**Dependency graph** — built automatically from `reads`/`writes` declarations. `input.*` reads are proto fields (no dependency edge). Other reads must match another evaluator's `writes`. Execution order is topologically sorted via Kahn's algorithm.

**Blocked evaluators** — when `DependenciesMet` returns false, the evaluator is skipped (marked `Passed: false`) and its metadata is copied from the definition. The `(*CELEvaluator).def` is accessed via type assertion in `execute`.

**Status derivation** — priority order: AllPassed > WorkflowActive > ActionRequired > Blocked. Status is derived, never stored. `ResolutionWorkflow != ""` on a failing result triggers `StatusWorkflowActive`.

## Caching

Two-tier strategy in `execute`:
1. **TTL** — if `cache_ttl` set and `now - EvaluatedAt < TTL`, reuse cached result
2. **Fingerprint** — if TTL expired but `ComputeFingerprint` matches cached fingerprint, reuse. Only applies to evaluators with exclusively `input.*` reads (no upstream deps), because the fingerprint only captures proto field values

`Engine.ToCachedResults(results, proto, time.Now())` computes fingerprints. The standalone `ToCachedResults()` does not (no evaluator/proto access).

## YAML config format

```yaml
evaluations:
  - name: score_eval              # identifier (used in error messages only)
    description: Score threshold   # optional, informational
    expression: "input.score >= 100"  # CEL, must return bool
    reads: [input.score]           # input.* = proto field, bare = upstream dep
    writes: score_sufficient       # output name (CEL var + result key)
    resolution_workflow: ScoreBoostWF  # optional workflow ID
    resolution: "Boost score"      # optional human description
    severity: blocking             # optional
    category: score                # optional
    cache_ttl: "10m"               # optional, Go duration format
```

## Build and test

```sh
go build ./...
go test ./...
```

Proto generation requires `buf` (configured in `mise.toml`):
```sh
buf generate
```

Check all CI steps
```sh
task ci
```

Test proto is at `proto/test/v1/test.proto` — defines `TestEvaluatorContainer` with fields `score`, `nested_object.is_active`, etc.

## Common modifications

**Adding a new evaluator field** — add to `EvalDefinition` (with yaml tag), `Result`, and populate it in `CELEvaluator.Evaluate` (both success and error paths) and in the blocked-evaluator branch of `execute`.

**Adding an Evaluator interface method** — add to the interface in `evaluator.go`, implement on `CELEvaluator` in `cel.go`.

**Changing status logic** — edit `DeriveStatus` in `status.go`. Priority is determined by the `if` chain order.

**Changing cache behavior** — the cache check is in `Engine.execute` in `engine.go`. TTL check comes first, then fingerprint check.
