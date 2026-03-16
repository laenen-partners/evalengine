package evalengine

import (
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// FieldRef is a dependency reference — either an evaluator output (bare name
// like "score_sufficient") or an input field path (like "input.email_verified").
// Reads prefixed with "input." refer to the proto passed to Engine.Run.
type FieldRef string

// String returns the string representation.
func (f FieldRef) String() string {
	return string(f)
}

// EvalDefinition is a single evaluator loaded from YAML.
type EvalDefinition struct {
	Name               string        `yaml:"name"`
	Description        string        `yaml:"description"`
	Expression         string        `yaml:"expression"`
	Reads              []FieldRef    `yaml:"reads"`
	Writes             FieldRef      `yaml:"writes"`
	ResolutionWorkflow string        `yaml:"resolution_workflow"`
	Resolution         string        `yaml:"resolution"`
	Severity           string        `yaml:"severity"`
	Category           string        `yaml:"category"`
	CacheTTL           string        `yaml:"cache_ttl"`
	CacheTTLDuration   time.Duration `yaml:"-"`
}

// EvalConfig is the top-level YAML structure.
type EvalConfig struct {
	Evaluations []EvalDefinition `yaml:"evaluations"`
}

// LoadDefinitions parses evaluation definitions from a reader.
func LoadDefinitions(r io.Reader) (*EvalConfig, error) {
	var cfg EvalConfig
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode evaluations yaml: %w", err)
	}
	for i := range cfg.Evaluations {
		if err := cfg.Evaluations[i].parseCacheTTL(); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

// LoadDefinitionsFromFile loads evaluation definitions from a YAML file.
func LoadDefinitionsFromFile(path string) (*EvalConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open evaluations file %q: %w", path, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "error closing file %q: %v\n", path, err)
		}
	}()
	return LoadDefinitions(f)
}

func (d *EvalDefinition) parseCacheTTL() error {
	if d.CacheTTL == "" {
		return nil
	}
	dur, err := time.ParseDuration(d.CacheTTL)
	if err != nil {
		return fmt.Errorf("eval %q: invalid cache_ttl %q: %w", d.Name, d.CacheTTL, err)
	}
	d.CacheTTLDuration = dur
	return nil
}
