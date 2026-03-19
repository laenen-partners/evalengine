# Upgrading to evalengine v0.4.0

## Breaking changes

### 1. `Evaluator` interface has new required methods

The `Evaluator` interface now includes metadata accessors. If you have a custom implementation of `Evaluator`, add these methods:

```go
// New required methods
DisplayName() string
Resolution() string
Severity() string
Category() string
FailureMode() string
HasPreconditions() bool
EvaluatePreconditions(activation map[string]any) []string
```

`ResolutionWorkflow() string` was already required — no change there.

If you only use `CELEvaluator` (the default), no action needed.

### 2. `Preconditions` field changed from `[]string` to `[]Precondition`

**Before (v0.3.0):**
```yaml
preconditions:
  - "has(input.score)"
  - "input.score > 0"
```

**After (v0.4.0):**
```yaml
preconditions:
  - expression: "has(input.score)"
    description: "Score must be provided"
  - expression: "input.score > 0"
    description: "Score must be submitted"
```

The `description` field is optional. If omitted, `PendingPreconditions` on `Result` will contain the raw expression as a fallback.

**If you construct `EvalDefinition` in Go code:**

```go
// Before
def := evalengine.EvalDefinition{
    Preconditions: []string{"has(input.score)"},
}

// After
def := evalengine.EvalDefinition{
    Preconditions: []evalengine.Precondition{
        {Expression: "has(input.score)", Description: "Score must be provided"},
    },
}
```

### 3. `Result` struct has new fields

New fields added to `Result`:

| Field | Type | Description |
|-------|------|-------------|
| `DisplayName` | `string` | The `name` from the YAML definition (human-readable) |
| `FailureMode` | `string` | Opaque string from YAML `failure_mode` |
| `Pending` | `bool` | `true` when preconditions failed |
| `PendingPreconditions` | `[]string` | Descriptions of failed preconditions |

These are additive — existing code that reads `Result` fields will continue to work. If you serialize `Result` to JSON/proto, you may see new fields in the output.

---

## New features (non-breaking)

### `reads` is now fully optional

All `reads` declarations are auto-derived from the CEL expression AST:
- `input.*` field paths are extracted from select chains
- Eval-to-eval dependencies are extracted from bare identifiers matching known `writes`

You can remove `reads` from your YAML entirely:

```yaml
# Before
- name: score_eval
  expression: "input.score >= 100"
  reads: [input.score]
  writes: score_sufficient

# After — reads auto-derived
- name: score_eval
  expression: "input.score >= 100"
  writes: score_sufficient
```

Explicit `reads` are still accepted and merged with auto-derived ones (deduplicated). No action required — your existing YAML works as-is.

### `Blocks()` reverse dependency API

```go
// Returns evaluators that depend on "score_sufficient"
dependents := eng.Graph().Blocks("score_sufficient")
```

If you were building reverse dependency maps manually from `Reads()`, you can replace that with `Blocks()`.

### `InputFields()` — query what proto fields an evaluator reads

```go
// Returns ["input.score"] or ["input.nested_object.is_active"]
fields := eng.InputFields("score_sufficient")
```

Useful for determining what data to collect for a given evaluator.

### `StatusPending`

New status in the priority chain: `AllPassed > WorkflowActive > ActionRequired > Pending > Blocked`.

If you switch on `Status` values, add a case for `evalengine.StatusPending`. If you don't handle it, the default/fallthrough behavior depends on your code.

### `DisplayName` on `Evaluator` interface

```go
for _, ev := range eng.Evaluators() {
    fmt.Println(ev.DisplayName()) // "score_eval" (the YAML name field)
    fmt.Println(ev.Name())        // "score_sufficient" (the writes field)
}
```

If you were building a `nameByWrites` map from parsed YAML, you can replace it with `ev.DisplayName()` or `result.DisplayName`.

### Nested field fingerprinting fix

`ComputeFingerprint` now correctly walks nested proto paths (e.g. `input.nested_object.is_active`). Previously, nested fields resolved to `nil` and produced constant fingerprints, causing stale cache hits. If you use `RunWithCache` with evaluators that read nested fields, cache behavior is now correct.

---

## Migration checklist

- [ ] Update `go.mod`: `go get github.com/laenen-partners/evalengine@v0.4.0`
- [ ] If using preconditions in YAML: convert from string list to `{expression, description}` objects
- [ ] If implementing custom `Evaluator`: add new interface methods
- [ ] If switching on `Status`: add `StatusPending` case
- [ ] Optional: remove `reads` from YAML (still works if left in)
- [ ] Optional: replace manual reverse-dependency maps with `Blocks()`
- [ ] Optional: replace manual `nameByWrites` maps with `DisplayName`
- [ ] Optional: remove `failure_mode` double-parsing workaround (now native)
