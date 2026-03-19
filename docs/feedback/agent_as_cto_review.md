# CTO Review: Evalengine Implementation Feedback

## Overall Assessment

The core architecture is sound. CEL + dependency graph + stateless execution + caller-owned cache is the right composition. This is not reinventing the wheel — no existing tool provides this combination proto-natively. The codebase is well-tested (86 tests), cleanly separated, and the CLAUDE.md is excellent.

The feedback below covers six areas that need attention, ordered by severity.

---

## Issue 1: `resolveField` doesn't handle nested proto paths (Bug)

**Severity:** Critical — correctness issue in production path

**Problem:** `ComputeFingerprint` calls `resolveField(ref, "input.nested_object.is_active")`, but `resolveField` only does `desc.Fields().ByName(name)` on the top-level message descriptor. It does not walk dot-separated paths through nested messages. For any evaluator that reads a nested field, the fingerprint is computed against a `nil` value and never changes — so the cache will always reuse stale results.

**Impact:** `is_active` evaluator in the test suite has `reads: [input.nested_object.is_active]`. Its fingerprint-based cache reuse is silently broken. TTL-based caching still works, masking the bug.

**Fix:**

- `fingerprint.go` — modify `resolveField` to split the field path on `"."` and walk through nested message descriptors:

```go
func resolveField(msg protoreflect.Message, ref string) any {
    name := ref
    if len(name) > 6 && name[:6] == "input." {
        name = name[6:]
    }

    parts := strings.Split(name, ".")
    current := msg
    for i, part := range parts {
        desc := current.Descriptor()
        fd := desc.Fields().ByName(protoreflect.Name(part))
        if fd == nil {
            return nil
        }
        if i < len(parts)-1 {
            // Intermediate segment — must be a message to descend into.
            if fd.Kind() != protoreflect.MessageKind {
                return nil
            }
            nested := current.Get(fd).Message()
            if !nested.IsValid() {
                return nil
            }
            current = nested
        } else {
            // Leaf segment — extract value.
            val := current.Get(fd)
            if fd.IsList() {
                // existing list serialization logic
            }
            return val.Interface()
        }
    }
    return nil
}
```

- Add tests: fingerprint for `input.nested_object.is_active` must change when `is_active` changes, and must differ from the fingerprint when `nested_object` is nil.

---

## Issue 2: `Evaluator` interface leaks its only implementation (Design)

**Severity:** High — breaks the interface contract, prevents extensibility

**Problem:** The `execute` loop type-asserts `ev.(*CELEvaluator).def` to access `Resolution`, `Severity`, `Category`, `FailureMode`, and `DisplayName` on blocked evaluators. This means:
- The `Evaluator` interface is a fiction — no alternative implementation can exist without panicking at runtime.
- Adding new metadata fields requires touching `execute` with another type assertion rather than going through the interface.

**Recommendation:** Add the missing accessors to the `Evaluator` interface. This is the clean path since `CELEvaluator` already has the data.

**Files:**

- `evaluator.go` — extend the interface:

```go
type Evaluator interface {
    Name() string
    DisplayName() string
    Reads() []FieldRef
    Writes() FieldRef
    CacheTTL() time.Duration
    Resolution() string
    ResolutionWorkflow() string
    Severity() string
    Category() string
    FailureMode() string
    Evaluate(activation map[string]any) Result
}
```

- `cel.go` — add the new accessor methods on `CELEvaluator`:

```go
func (e *CELEvaluator) DisplayName() string { return e.def.Name }
func (e *CELEvaluator) Resolution() string  { return e.def.Resolution }
func (e *CELEvaluator) Severity() string    { return e.def.Severity }
func (e *CELEvaluator) Category() string    { return e.def.Category }
func (e *CELEvaluator) FailureMode() string { return e.def.FailureMode }
```

Note: `ResolutionWorkflow() string` already exists on the interface.

- `engine.go` — replace all `ev.(*CELEvaluator).def.X` in the blocked-evaluator branch with `ev.X()`:

```go
r := Result{
    Name:               ev.Name(),
    DisplayName:        ev.DisplayName(),
    Passed:             false,
    Resolution:         ev.Resolution(),
    ResolutionWorkflow: ev.ResolutionWorkflow(),
    Severity:           ev.Severity(),
    Category:           ev.Category(),
    FailureMode:        ev.FailureMode(),
}
```

- Also remove the `celEv := ev.(*CELEvaluator)` type assertion used for precondition checking. Instead, add a `HasPreconditions() bool` and `EvaluatePreconditions(activation map[string]any) []string` to the interface (or use a separate optional interface with a type-switch).

**Tests:** Existing tests cover this path. No new tests needed — just verify the existing 86 pass after refactoring.

---

## Issue 3: Precondition system design review (Design)

**Severity:** Medium — works correctly but adds concepts that may not pull their weight

**Problem:** Preconditions introduce a third evaluation phase (`DependenciesMet → Preconditions → Main expression`) and two new fields on `Result` (`Pending`, `PendingPreconditions`). The semantic question is: what does `Pending` give you that a separate evaluator with a dependency edge (producing `Blocked`) doesn't?

**Justified if:** Consumers genuinely need to distinguish "data not yet provided" (pending) from "upstream rule failed" (blocked) in their UI or workflow logic. This is the case for prodcat's onboarding flow where incomplete data should show "we're waiting for X" rather than "requirement Y failed".

**Two concrete improvements to make:**

### 3a. Add `description` field to preconditions

`PendingPreconditions` currently returns raw CEL expressions like `"has(input.nested_object)"`. This is not user-facing. Consumers will immediately need to map these to human-readable text.

**Option A (simple):** Change preconditions from `[]string` to a struct:

```go
// definition.go
type Precondition struct {
    Expression  string `yaml:"expression"`
    Description string `yaml:"description"`
}

type EvalDefinition struct {
    // ...
    Preconditions []Precondition `yaml:"preconditions"`
}
```

```yaml
preconditions:
  - expression: "has(input.nested_object)"
    description: "Account details must be provided"
  - expression: "input.score > 0"
    description: "Score must be submitted"
```

`PendingPreconditions` on `Result` would then carry descriptions (or both expression + description).

**Option B (minimal):** Keep `[]string` but document that consumers are responsible for mapping expressions to display text. Accept that preconditions are developer-facing only.

**Recommendation:** Option A. The incremental cost is low and it avoids every consumer building the same mapping table.

### 3b. Document when to use preconditions vs separate evaluators

Add a section to CLAUDE.md explaining the decision criteria:

- Use **preconditions** when you need to distinguish "data incomplete" from "rule failed" for the same logical check.
- Use **separate evaluators with dependency edges** when the guard condition is itself a meaningful business rule with its own resolution/workflow.

---

## Issue 4: `Result` struct growth (Design)

**Severity:** Medium — manageable now, but trending toward a maintenance burden

**Problem:** `Result` has 12 fields and grows with every prodcat request. `FailureMode` especially feels like opaque consumer metadata rather than a first-class engine concept.

**Options:**

**Option A: Metadata map.** Add `Metadata map[string]string` to `Result` and `EvalDefinition`. Move `FailureMode` (and future opaque fields) into it. The engine passes through any key-value pairs from YAML without knowing what they mean.

```go
type Result struct {
    Name               string
    DisplayName        string
    Passed             bool
    Pending            bool
    Error              string
    Resolution         string
    ResolutionWorkflow string
    Severity           string
    Category           string
    Metadata           map[string]string   // opaque consumer-defined fields
    PendingPreconditions []string
}
```

```yaml
metadata:
  failure_mode: "soft"
  team: "onboarding"
```

**Pros:** Future consumer-specific fields don't require evalengine changes. Clear separation between engine-meaningful fields (Passed, Pending, Resolution, ResolutionWorkflow) and pass-through metadata.

**Cons:** Loses type safety on metadata keys. Consumers need to know the key names.

**Option B: Accept the flat struct.** Keep adding fields. Document that `Result` is the API contract. This is fine if the field count stays under ~15 and all fields are genuinely used by multiple consumers.

**Recommendation:** Option A. `FailureMode`, `Severity`, and `Category` are all opaque strings the engine never inspects. Moving them to `Metadata` is honest about what they are. Keep `Resolution` and `ResolutionWorkflow` as first-class fields since `DeriveStatus` inspects `ResolutionWorkflow`.

**Note:** This is a breaking change. If adopted, deprecate the old fields in a minor version and remove in the next major.

---

## Issue 5: Status derivation is hardcoded business logic (Design)

**Severity:** Low — correct for prodcat, but limits reuse

**Problem:** The priority chain `AllPassed > WorkflowActive > ActionRequired > Pending > Blocked` is an opinionated business decision baked into the library. Other consumers may need different priority (e.g., `ActionRequired` should surface even when a workflow is active).

**Recommendation:** Don't make `DeriveStatus` pluggable now — that's over-engineering. Instead:

1. Document in CLAUDE.md that `DeriveStatus` is a **default implementation** and consumers with different priority needs should implement their own from `[]Result` + `*EvalGraph`.
2. Keep `DeriveStatus` as a convenience, not a requirement.
3. Ensure all building blocks are public: `Result.Pending`, `Result.ResolutionWorkflow`, `EvalGraph.DependenciesMet` — these are already public, which is correct.

No code changes needed. Documentation only.

---

## Issue 6: No observability hooks (Feature gap)

**Severity:** Low — not blocking, but will be needed when debugging production issues

**Problem:** No way to instrument evaluation. When an evaluator is slow in production, there's no visibility into which one without wrapping the engine.

**Recommendation:** Add a simple callback, not a full middleware stack:

```go
// engine.go
type EvalHook func(name string, duration time.Duration, result Result)

type Engine struct {
    evaluators []Evaluator
    graph      *EvalGraph
    onEvaluate EvalHook // optional, nil = no-op
}
```

Set via an option on `NewEngine`:

```go
func WithEvalHook(hook EvalHook) EngineOption { ... }
eng, err := evalengine.NewEngine(cfg, input, evalengine.WithEvalHook(func(name string, d time.Duration, r Result) {
    slog.Info("eval", "name", name, "duration", d, "passed", r.Passed)
}))
```

**Defer this** until prodcat actually hits a performance issue in production. Don't build it speculatively.

---

## Implementation Plan

### Phase 1 — Critical fix (Issue 1)

Fix `resolveField` nested path walking. This is a correctness bug in the cache fingerprinting path.

### Phase 2 — Interface cleanup (Issue 2)

Promote all metadata accessors to the `Evaluator` interface. Remove type assertions from `execute`. This unblocks any future alternative evaluator implementations and makes the code honest.

### Phase 3 — Precondition refinement (Issue 3)

Add `description` to preconditions. Document when to use preconditions vs separate evaluators.

### Phase 4 — Evaluate and decide (Issues 4, 5, 6)

These are design decisions, not bugs. Evaluate after more consumer feedback:
- **Issue 4 (Metadata map):** Decide when the next consumer-specific field is requested. If it's opaque, that's the signal to refactor.
- **Issue 5 (Status derivation):** Document-only change. Add a note to CLAUDE.md.
- **Issue 6 (Observability):** Build when prodcat hits a real debugging need, not before.
