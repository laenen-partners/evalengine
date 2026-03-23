// evalvalidate validates evalengine YAML configuration files.
//
// It performs structural validation (required fields, duplicate writes,
// cache_ttl format, precondition expressions) without requiring a proto
// message or CEL compilation.
//
// Usage:
//
//	evalvalidate <file.yaml> [file2.yaml ...]
package main

import (
	"fmt"
	"os"

	"github.com/laenen-partners/evalengine"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: evalvalidate <file.yaml> [file2.yaml ...]\n")
		os.Exit(2)
	}

	failed := false
	for _, path := range os.Args[1:] {
		if err := validateFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed = true
		} else {
			fmt.Printf("%s: ok\n", path)
		}
	}
	if failed {
		os.Exit(1)
	}
}

func validateFile(path string) error {
	cfg, err := evalengine.LoadDefinitionsFromFile(path)
	if err != nil {
		return err
	}
	return evalengine.ValidateConfig(cfg)
}
