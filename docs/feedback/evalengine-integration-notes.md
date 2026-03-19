# Evalengine Integration Notes

## Current State

Prodcat now uses evalengine for all CEL expression evaluation. The hardcoded Go matchers are gone. The integration works but has some workarounds.

## What Works Well

- CEL expressions evaluate directly against the proto `EvaluationInput`
- Dependency graph (reads/writes) handled by evalengine's Kahn's topological sort
- Blocked evaluators are skipped automatically
- `failure_mode` is natively supported in evalengine

## Resolved Items

### 1. failure_mode ✅

`FailureMode string` added to `EvalDefinition` and `Result`. Populated in all code paths (passing, failing, blocked). Opaque string, no validation — same pattern as `Severity`/`Category`.

### 2. "data missing" vs "data fails" ✅

Preconditions system added. Optional `preconditions` array of CEL expressions on each definition. All must pass before the main expression runs. Failures produce `Pending: true` with `PendingPreconditions` listing failing expressions. `StatusPending` added to status priority chain (AllPassed > WorkflowActive > ActionRequired > Pending > Blocked).

### 3. `name` vs `writes` as result key ✅

`DisplayName string` added to `Result`, populated from `EvalDefinition.Name`. `Result.Name` remains the `writes` field (cache key / graph key). Consumers use `DisplayName` for UI.

### 4. No `Blocks` reverse dependency API ✅

`Blocks(name string) []string` added to `EvalGraph`. Returns evaluators that directly depend on the given evaluator. Built during `BuildGraph` alongside the forward `deps` map.

### 5. CEL compilation on every call — prodcat-side concern

Not an evalengine change. The `*Engine` is stateless and safe to reuse. Prodcat should cache the engine by config content hash. See CLAUDE.md "Engine-level caching" section for the recommended pattern.

A `CacheStore` interface for individual compiled programs was considered and rejected:
- Compiled `cel.Program` is bound to the `cel.Env` (includes proto type + all writes declarations)
- Changing any evaluator's `writes` invalidates the entire environment
- Cache key would effectively be a hash of the entire config anyway
- Engine-level caching is simpler and equally effective

### 6. Reads auto-derivation ✅

In `NewEngine`, after compiling all evaluators, each evaluator's CEL AST is walked for bare identifiers matching a known `writes` field. Matches are merged into `def.Reads` (deduplicated). Explicit `reads` for upstream dependencies can now be omitted — they're auto-derived from the expression.

### 7. Input dependency extraction from CEL AST ✅

`Engine.InputFields(name string) []string` walks the compiled CEL AST and returns all `input.*` field paths (e.g., `["input.score"]`, `["input.nested_object.is_active"]`). Only leaf select chains are returned (intermediate paths like `input.nested_object` are filtered). Evaluators with only upstream references return empty.
