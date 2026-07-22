// Package build ties the front-end and back-end into one call: it runs the Phase
// 0 pipeline lex -> parse -> sema -> emit and returns C source. Compiling that C to
// a binary with a C compiler is the driver's job (it is host I/O, not language
// processing), so this package stays pure and easy to test.
package build

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/emit"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/stdlib"
)

// Compile lowers Zerg source to C. It stops at the first stage that reports
// diagnostics, returning them with an empty string and an empty Manifest; on
// success it runs the pipeline parse -> bundle -> sema -> mono -> emit and returns
// the C translation unit, an emit.Manifest describing which runtime features the
// program uses (empty for a value-only program), and no diagnostics.
func Compile(src string) (string, emit.Manifest, []diag.Diagnostic) {
	file, diags := parser.Parse(src)
	if len(diags) > 0 {
		return "", emit.Manifest{}, diags
	}
	// Bundle any `import`ed stdlib module into the same compilation unit (Decision C,
	// the single-unit bundle MVP). A program that imports nothing leaves the file
	// untouched and takes the exact pre-1f single-file path, so its C is byte-identical.
	if diags := bundle(file); len(diags) > 0 {
		return "", emit.Manifest{}, diags
	}
	info, diags := sema.Check(file)
	if len(diags) > 0 {
		return "", emit.Manifest{}, diags
	}
	return emit.Emit(mono.Build(file, info))
}

// bundle implements the stdlib bundle importer (DESIGN-1f §10, Decision C). For each
// `import "<path>"` in the file it loads the embedded stdlib module named by the
// path's LAST segment (`import "io"` and `import "std/io"` both load io.zg), parses
// it, and MERGES its public functions into the same compilation unit under a
// namespace-prefixed name (`println` -> `io__println`, sema.ModuleMember) so a member
// call `io.println(...)` resolves to it while the bundled internals neither leak into
// the user's unqualified scope nor collide with a user name. This is the forward-
// compatible subset of the 1g module graph: only the resolver changes then, not the
// stdlib sources or the language.
//
// MVP scope: single level (no transitive graph, no re-export), functions only, and a
// bundled module must not reference a sibling top-level name by its bare (unmangled)
// spelling — the stdlib io module observes this. The import statement stays in the
// file so the resolver still binds the namespace.
func bundle(file *ast.File) []diag.Diagnostic {
	var diags diag.List
	var merged []ast.Stmt
	for _, item := range file.Items {
		imp, ok := item.(*ast.ImportStmt)
		if !ok {
			continue
		}
		for _, spec := range imp.Specs {
			merged = append(merged, bundleSpec(spec, &diags)...)
		}
	}
	file.Items = append(file.Items, merged...)
	return diags.Items()
}

// bundleSpec loads and mangles one import spec's module, returning its merged
// declarations and recording any diagnostic against the spec.
func bundleSpec(spec *ast.ImportSpec, diags *diag.List) []ast.Stmt {
	namespace := importBinding(spec)
	src, ok := stdlib.Source(lastSegment(spec.Path))
	if !ok {
		diags.Add(spec.Span(), "no stdlib module for import %q", spec.Path)
		return nil
	}
	modFile, modDiags := parser.Parse(src)
	if len(modDiags) > 0 {
		diags.Add(spec.Span(), "stdlib module %q failed to parse", spec.Path)
		return nil
	}
	var out []ast.Stmt
	for _, mit := range modFile.Items {
		fn, ok := mit.(*ast.FuncDecl)
		if !ok || !fn.Pub {
			continue // MVP: only a module's public functions are bundled
		}
		fn.Name = sema.ModuleMember(namespace, fn.Name)
		out = append(out, fn)
	}
	return out
}

// importBinding is the namespace an import spec binds: its `as` alias, else the last
// '/'-separated segment of its path (mirrors the resolver's importName).
func importBinding(spec *ast.ImportSpec) string {
	if spec.Alias != "" {
		return spec.Alias
	}
	return lastSegment(spec.Path)
}

// lastSegment returns the final '/'-separated segment of a module path.
func lastSegment(path string) string {
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			start = i + 1
		}
	}
	return path[start:]
}
