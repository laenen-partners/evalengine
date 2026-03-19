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

**New files (prodcat integration):**
- `ast_walk.go` — CEL AST walking: `extractInputFieldPaths` (input field extraction), `extractIdentRefs` (bare identifier extraction for auto-derive reads)

## Key concepts

**FieldRef** — either `"input.<field>"` (proto field) or a bare name like `"score_sufficient"` (upstream evaluator output). The `"input."` prefix distinguishes proto reads from dependency reads throughout the codebase.

**Writes** — each evaluator's `writes` field serves triple duty: result key, CEL variable name for downstream evaluators, and graph node name. It is the canonical evaluator identifier (not `name`).

**Dependency graph** — built automatically from CEL AST analysis. `reads` is fully optional in YAML — all dependencies are auto-derived during `NewEngine`:
1. **`input.*` reads** — extracted from select chains in the AST (e.g. `input.score`, `input.nested_object.is_active`). These feed the fingerprint cache.
2. **Eval-to-eval reads** — bare identifiers in the expression matching another evaluator's `writes` create dependency edges.
Explicit `reads` in YAML are preserved and merged (deduplicated). Execution order is topologically sorted via Kahn's algorithm.

**Blocked evaluators** — when `DependenciesMet` returns false, the evaluator is skipped (marked `Passed: false`) and its metadata is populated via the `Evaluator` interface methods (no type assertions in `execute`).

**Preconditions** — optional array of `{expression, description}` objects on a definition. All must return true before the main expression runs. If any fail, the result is `Pending: true` with `PendingPreconditions` listing the descriptions of failing preconditions (falls back to the expression if description is empty). Skipping the main expression avoids conflating "data missing" with "data fails".

**Status derivation** — priority order: AllPassed > WorkflowActive > ActionRequired > Pending > Blocked. Status is derived, never stored. `ResolutionWorkflow != ""` on a failing result triggers `StatusWorkflowActive`. `Pending` indicates precondition failure (data not yet complete).

**Reverse dependencies** — `EvalGraph.Blocks(name)` returns evaluators that directly depend on the given evaluator. Built during `BuildGraph` alongside the forward `deps` map.

## Caching

Two-tier strategy in `execute`:
1. **TTL** — if `cache_ttl` set and `now - EvaluatedAt < TTL`, reuse cached result
2. **Fingerprint** — if TTL expired but `ComputeFingerprint` matches cached fingerprint, reuse. Only applies to evaluators with exclusively `input.*` reads (no upstream deps), because the fingerprint only captures proto field values

`Engine.ToCachedResults(results, proto, time.Now())` computes fingerprints. The standalone `ToCachedResults()` does not (no evaluator/proto access).

### Engine-level caching (compilation)

CEL compilation happens in `NewEngine` and is expensive. The `*Engine` is stateless and safe to reuse across requests — **cache the engine, not individual compiled programs**.

A `CacheStore` interface for compiled `cel.Program` objects is deliberately not provided because:
1. A compiled program is bound to the `cel.Env` it was compiled against. The env includes the proto type, the `input` variable, and every evaluator's `writes` declared as a `cel.BoolType` variable. Changing any evaluator's `writes` invalidates the entire environment and all cached programs.
2. The correct cache key would be `hash(expression + proto type + all writes names + all env options)`, which is effectively a hash of the entire config — at which point caching the `*Engine` by config hash is simpler and equally effective.
3. Preconditions and AST retention (needed for `InputFields` and auto-derive reads) multiply the caching surface without adding value over engine-level caching.

Consumers should cache the `*Engine` by config content hash:

```go
var engineCache sync.Map // key: sha256(yamlBytes) → *evalengine.Engine

func getEngine(yamlBytes []byte, input proto.Message) (*evalengine.Engine, error) {
    key := sha256.Sum256(yamlBytes)
    if cached, ok := engineCache.Load(key); ok {
        return cached.(*evalengine.Engine), nil
    }
    eng, err := evalengine.NewEngineFromBytes(yamlBytes, input)
    if err != nil {
        return nil, err
    }
    engineCache.Store(key, eng)
    return eng, nil
}
```

## YAML config format

```yaml
evaluations:
  - name: score_eval              # identifier (used in error messages, exposed as DisplayName)
    description: Score threshold   # optional, informational
    expression: "input.score >= 100"  # CEL, must return bool
    reads: [input.score]           # fully optional — auto-derived from CEL AST
    writes: score_sufficient       # output name (CEL var + result key)
    resolution_workflow: ScoreBoostWF  # optional workflow ID
    resolution: "Boost score"      # optional human description
    severity: blocking             # optional
    category: score                # optional
    failure_mode: "soft"           # optional, opaque string passed through to Result
    preconditions:                 # optional; all must pass before main expression runs
      - expression: "has(input.score)"
        description: "Score must be provided"
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

**Adding a new evaluator field** — add to `EvalDefinition` (with yaml tag), `Result`, add an accessor to the `Evaluator` interface in `evaluator.go`, implement it on `CELEvaluator` in `cel.go`, and populate it in `CELEvaluator.Evaluate` (both success and error paths) and in the blocked-evaluator and pending branches of `execute` (via the interface method, not type assertion).

**Adding an Evaluator interface method** — add to the interface in `evaluator.go`, implement on `CELEvaluator` in `cel.go`.

**Changing status logic** — edit `DeriveStatus` in `status.go`. Priority is determined by the `if` chain order.

**Changing cache behavior** — the cache check is in `Engine.execute` in `engine.go`. TTL check comes first, then fingerprint check.

**Adding a precondition** — add `{expression, description}` entries to `preconditions` in YAML. They compile and run before the main expression. Failures produce `Pending: true` results with descriptions.

**Querying input dependencies** — `Engine.InputFields(name)` walks the compiled CEL AST and returns all `input.*` field paths the expression accesses. Useful for determining what data to collect.

**Querying reverse dependencies** — `Engine.Graph().Blocks(name)` returns evaluators that depend on the given evaluator.
