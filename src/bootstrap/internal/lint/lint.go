// Package lint implements the lint-only checks behind `zerg lint` (Phase 1g U4).
// It runs on top of a program the compiler already accepts (the driver reports real
// compile diagnostics first), so it reports only findings the compiler itself does
// not: an import whose namespace is never used, and a module-private top-level
// declaration that nothing references. Style and layout are `zerg fmt`'s job, so
// lint deliberately does not overlap with it, and it does no dead-code flow or
// complexity analysis — the MVP scope is exactly these two unused-name findings.
package lint

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// Check scans a single parsed module (the entry file) and returns its lint
// findings in source order: unused imports first discovered by binding name, then
// unused module-private declarations. It walks every reference position in the
// file to decide what is used, so a name referenced anywhere in the module — even
// only from another unused declaration — counts as used (the MVP does no
// transitive dead-code analysis, which keeps it free of false positives).
func Check(file *ast.File) []diag.Diagnostic {
	var diags diag.List
	if file == nil {
		return nil
	}

	used := map[string]bool{}
	c := &collector{used: used}
	for _, it := range file.Items {
		c.item(it)
	}

	reportUnusedImports(file, used, &diags)
	reportUnusedPrivate(file, used, &diags)
	return diags.Items()
}

// reportUnusedImports flags every non-pub import whose binding name is never
// referenced. A `pub` import is a re-export (its binding is meant to be unused
// locally), so it is exempt.
func reportUnusedImports(file *ast.File, used map[string]bool, diags *diag.List) {
	for _, it := range file.Items {
		imp, ok := it.(*ast.ImportStmt)
		if !ok {
			continue
		}
		for _, spec := range imp.Specs {
			if spec.Pub {
				continue
			}
			name := bindingName(spec)
			if name == "" || used[name] {
				continue
			}
			diags.Add(spec.Span(), "unused import %q (the %q namespace is never used)", spec.Path, name)
		}
	}
}

// reportUnusedPrivate flags every module-private (non-pub) top-level declaration
// whose name is never referenced. `main` is the program entry point, so it is
// exempt even though it is not `pub`.
func reportUnusedPrivate(file *ast.File, used map[string]bool, diags *diag.List) {
	for _, it := range file.Items {
		name, kind, pub, span, ok := privateDecl(it)
		if !ok || pub || name == "main" || used[name] {
			continue
		}
		diags.Add(span, "unused %s %q (module-private and never used)", kind, name)
	}
}

// privateDecl reports a top-level declaration's name, a human label for its kind,
// whether it is `pub`, and its span. ok is false for items that are not
// name-bearing declarations (imports, impls, init blocks, unsafe groups).
func privateDecl(it ast.Stmt) (name, kind string, pub bool, span token.Span, ok bool) {
	switch n := it.(type) {
	case *ast.FuncDecl:
		return n.Name, "function", n.Pub, n.Span(), true
	case *ast.StructDecl:
		return n.Name, "struct", n.Pub, n.Span(), true
	case *ast.EnumDecl:
		return n.Name, "enum", n.Pub, n.Span(), true
	case *ast.TypeDecl:
		return n.Name, "type", n.Pub, n.Span(), true
	case *ast.SpecDecl:
		return n.Name, "spec", n.Pub, n.Span(), true
	case *ast.BindStmt:
		// A top-level binding is a module constant; it is always module-private.
		return n.Name, "constant", false, n.Span(), true
	}
	return "", "", false, token.Span{}, false
}

// bindingName is the local namespace name an import spec introduces: its `as`
// alias, else the last '/'-separated segment of its path.
func bindingName(spec *ast.ImportSpec) string {
	if spec.Alias != "" {
		return spec.Alias
	}
	path := spec.Path
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			start = i + 1
		}
	}
	return path[start:]
}
