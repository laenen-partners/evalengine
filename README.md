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
    writes: score_sufficient
    resolution_workflow: ScoreBoostWorkflow
    resolution: "Boost score to meet threshold"
    severity: blocking
    category: score
    failure_mode: "soft"
    cache_ttl: "10m"

  - name: active_eval
    description: Account is active
    expression: "input.is_active == true"
    writes: is_active
    resolution: "Activate account"
    severity: blocking
    category: status

  - name: eligible_eval
    description: Eligible when score sufficient and active
    expression: "score_sufficient == true && is_active == true"
    writes: eligible
    resolution: "Meet all criteria"
    severity: blocking
    category: combined
    cache_ttl: "1m"
```

All dependencies are auto-derived from the CEL expression:
- `input.*` field accesses are extracted from select chains (e.g. `input.score`)
- Bare identifiers matching another evaluator's `writes` create dependency edges (e.g. `score_sufficient`)

Explicit `reads` can still be provided and will be merged with auto-derived ones.

### Create and run the engine

```go
cfg, _ := evalengine.LoadDefinitionsFromFile("evals.yaml")
eng, _ := evalengine.NewEngine(cfg, &mypb.MyMessage{})

input := &mypb.MyMessage{Score: 150, IsActive: true}
results := eng.Run(input)

for _, r := range results {
    fmt.Printf("%s (%s): passed=%v\n", r.DisplayName, r.Name, r.Passed)
}
```

`Result.Name` is the `writes` field (canonical key). `Result.DisplayName` is the YAML `name` field (human-readable).

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
case evalengine.StatusPending:
    // preconditions not met (data incomplete)
case evalengine.StatusBlocked:
    // upstream dependencies not met
}
```

Priority: AllPassed > WorkflowActive > ActionRequired > Pending > Blocked.

### Preconditions

Preconditions let you distinguish "data not yet provided" from "data fails the check":

```yaml
- name: adult_party_check
  preconditions:
    - expression: 'size(input.parties) > 0'
      description: "At least one party must be provided"
  expression: 'input.parties.exists(p, p.age >= 18)'
  writes: has_adult
```

When preconditions fail, the result is `Pending: true` with `PendingPreconditions` listing the descriptions of the failing checks. The main expression is not evaluated.

```go
r := results["has_adult"]
if r.Pending {
    fmt.Println("Waiting for:", r.PendingPreconditions)
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

The `*Engine` is stateless and safe to reuse across requests. Cache it by config content hash to avoid recompilation:

```go
var engineCache sync.Map

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

### YAML config reference

| Field | Required | Description |
|---|---|---|
| `name` | yes | Evaluator identifier, exposed as `Result.DisplayName` |
| `description` | no | Human-readable description |
| `expression` | yes | CEL expression, must return `bool` |
| `reads` | no | Auto-derived from CEL AST. Explicit values merged and deduplicated |
| `writes` | yes | Output name — used as result key, CEL variable, and graph node |
| `resolution_workflow` | no | Workflow ID to trigger on failure |
| `resolution` | no | Human description of how to resolve failure |
| `severity` | no | Severity level (e.g., `blocking`). Passed through to `Result` |
| `category` | no | Grouping category. Passed through to `Result` |
| `failure_mode` | no | Opaque string (e.g., `soft`, `hard`). Passed through to `Result` |
| `preconditions` | no | List of `{expression, description}` — CEL bool guards before main expression |
| `cache_ttl` | no | Go duration (e.g., `10m`, `1h`). Default: no caching |

### Validation

evalengine provides multiple ways to validate YAML configuration files.

#### JSON Schema

The repository includes a [JSON Schema](evalengine.schema.json) that defines the YAML format. Use it for editor autocomplete and CI validation.

**VS Code** — add to your YAML file or workspace settings:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/laenen-partners/evalengine/main/evalengine.schema.json
evaluations:
  - name: ...
```

**CLI with [ajv-cli](https://github.com/ajv-validator/ajv-cli)**:

```sh
npm install -g ajv-cli ajv-formats
ajv validate -s evalengine.schema.json -d evals.yaml
```

#### CLI tool

Install the `evalvalidate` command to validate YAML files structurally (required fields, duplicate writes, cache_ttl format, precondition expressions):

```sh
go install github.com/laenen-partners/evalengine/cmd/evalvalidate@latest
evalvalidate evals.yaml
```

Accepts multiple files:

```sh
evalvalidate evals/*.yaml
```

#### Go API

**Structural validation** — no proto message needed, suitable for CI pipelines:

```go
cfg, err := evalengine.LoadDefinitionsFromFile("evals.yaml")
if err != nil {
    log.Fatal(err)
}
if err := evalengine.ValidateConfig(cfg); err != nil {
    log.Fatal(err) // *evalengine.ValidationError with all issues
}
```

**Full validation** — includes CEL compilation and dependency graph checks:

```go
if err := evalengine.Validate(cfg, &mypb.MyMessage{}); err != nil {
    log.Fatal(err)
}
```

### Dependency graph

The graph is auto-derived from CEL expressions and validated at engine creation:

```go
graph := eng.Graph()
graph.ExecutionOrder()               // topologically sorted evaluator names
graph.DependenciesMet(name, results) // true if all upstream deps passed
graph.BlockedBy(name, results)       // which upstream deps failed
graph.Blocks(name)                   // which evaluators depend on this one (reverse lookup)
graph.Issues()                       // validation issues (circular deps, missing producers, etc.)
```

### Input field extraction

Query what proto fields an evaluator's expression accesses:

```go
eng.InputFields("score_sufficient") // ["input.score"]
eng.InputFields("is_active")        // ["input.nested_object.is_active"]
eng.InputFields("eligible")         // [] (only reads upstream outputs)
```
