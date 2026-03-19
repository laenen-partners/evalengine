package evalengine

import (
	"github.com/google/cel-go/cel"
	celast "github.com/google/cel-go/common/ast"
)

// extractInputFieldPaths walks the compiled CEL AST and returns all dotted
// field paths rooted at the "input" identifier (e.g. "input.score",
// "input.nested_object.is_active"). Duplicate paths are deduplicated.
func extractInputFieldPaths(a *cel.Ast) []string {
	if a == nil {
		return nil
	}
	nav := celast.NavigateAST(a.NativeRep())
	selects := celast.MatchDescendants(nav, celast.KindMatcher(celast.SelectKind))

	// Build set of expression IDs that are operands of other selects,
	// so we only emit leaf (outermost) select chains.
	operandIDs := make(map[int64]bool)
	for _, sel := range selects {
		for _, child := range sel.Children() {
			if child.Kind() == celast.SelectKind {
				operandIDs[child.ID()] = true
			}
		}
	}

	seen := make(map[string]bool)
	var paths []string
	for _, sel := range selects {
		if operandIDs[sel.ID()] {
			continue // this select is an intermediate operand, not a leaf
		}
		path := buildSelectChain(sel)
		if path == "" {
			continue
		}
		if hasInputPrefix(path) && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}

// buildSelectChain walks a select expression chain back to its root and
// reconstructs the dotted path. Returns "" if the chain doesn't terminate at
// an identifier.
func buildSelectChain(e celast.NavigableExpr) string {
	sel := e.AsSelect()
	operand := sel.Operand()

	switch operand.Kind() {
	case celast.IdentKind:
		return operand.AsIdent() + "." + sel.FieldName()
	case celast.SelectKind:
		// Navigate to the NavigableExpr for the operand so we can recurse
		// with the correct interface type.
		for _, child := range e.Children() {
			if child.Kind() == celast.SelectKind || child.Kind() == celast.IdentKind {
				prefix := buildSelectChainExpr(child)
				if prefix != "" {
					return prefix + "." + sel.FieldName()
				}
			}
		}
		return ""
	default:
		return ""
	}
}

// buildSelectChainExpr recursively builds a dotted path from a NavigableExpr.
func buildSelectChainExpr(e celast.NavigableExpr) string {
	switch e.Kind() {
	case celast.IdentKind:
		return e.AsIdent()
	case celast.SelectKind:
		sel := e.AsSelect()
		for _, child := range e.Children() {
			prefix := buildSelectChainExpr(child)
			if prefix != "" {
				return prefix + "." + sel.FieldName()
			}
		}
		return ""
	default:
		return ""
	}
}

// extractIdentRefs walks the compiled CEL AST and returns all bare identifier
// references (not "input"). These are potential references to upstream
// evaluator outputs.
func extractIdentRefs(a *cel.Ast) []string {
	if a == nil {
		return nil
	}
	nav := celast.NavigateAST(a.NativeRep())
	idents := celast.MatchDescendants(nav, celast.KindMatcher(celast.IdentKind))

	seen := make(map[string]bool)
	var refs []string
	for _, ident := range idents {
		name := ident.AsIdent()
		if name == "input" || seen[name] {
			continue
		}
		seen[name] = true
		refs = append(refs, name)
	}
	return refs
}
