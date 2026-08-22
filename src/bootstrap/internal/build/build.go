// Package build ties the front-end and back-end into one call: it runs the Phase
// 0 pipeline lex -> parse -> sema -> emit and returns C source. Compiling that C to
// a binary with a C compiler is the driver's job (it is host I/O, not language
// processing), so this package stays pure and easy to test.
package build

import (
	"os"
	"path/filepath"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/emit"
	"github.com/cmj0121/zerg/src/bootstrap/internal/module"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/stdlib"
)

// Compile lowers Zerg source to C. It stops at the first stage that reports
// diagnostics, returning them with an empty string and an empty Manifest; on
// success it runs the pipeline load -> sema -> mono -> emit and returns the C
// translation unit, an emit.Manifest describing which runtime features the program
// uses (empty for a value-only program), and no diagnostics.
//
// The entry source is loaded as an anonymous entry module against the embedded
// stdlib root only (it has no on-disk directory to root a user tree), so it keeps
// the historic single-string entry point for tests and REPL use. A program that
// imports nothing flattens to exactly its own file, so its C is byte-identical to
// the pre-module compiler; a stdlib import bundles the module exactly as before.
func Compile(src string) (string, emit.Manifest, []diag.Diagnostic) {
	loader := module.NewLoader(stdlibProvider{})
	return compileWith(loader, src)
}

// CompileProgram lowers a multi-module program to C, rooting `import "a/b"` in the
// entry file's directory tree (root 1) with the embedded stdlib as a fallback
// (root 2). It reads the entry file itself, then runs the same inner pipeline as
// Compile — the whole-program flatten means the downstream sema/mono/emit are
// unaware there was ever more than one module.
func CompileProgram(entryPath string) (string, emit.Manifest, []diag.Diagnostic) {
	src, err := os.ReadFile(entryPath) //nolint:gosec // the entry source the user asked to compile
	if err != nil {
		var diags diag.List
		diags.Add(token.Span{}, "cannot read entry file %q: %v", entryPath, err)
		return "", emit.Manifest{}, diags.Items()
	}
	root := module.OSProvider{Root: filepath.Dir(entryPath)}
	loader := module.NewLoader(root, stdlibProvider{})
	return compileWith(loader, string(src))
}

// compileWith runs the shared inner pipeline over a configured loader.
func compileWith(loader *module.Loader, src string) (string, emit.Manifest, []diag.Diagnostic) {
	file, plan, diags := loader.LoadProgram(src)
	if len(diags) > 0 {
		return "", emit.Manifest{}, diags
	}
	// A `#[test]` function is not part of a normal build: strip it before sema so it is
	// neither type-checked nor emitted (Phase 1i U5). Nothing calls a test function, so a
	// program with no `#[test]` is unchanged and its emitted C stays byte-identical; a
	// program that happens to carry one builds exactly as it would without it.
	file.Items = dropTestItems(file.Items)
	info, diags := sema.Check(file)
	if len(diags) > 0 {
		return "", emit.Manifest{}, diags
	}
	return emit.Emit(mono.BuildWithInit(file, info, plan))
}

// dropTestItems returns items with every `#[test]` function removed, descending into
// a module-level `unsafe { }` group so a test declared inside one is stripped too. It
// allocates a new slice only when a test is present, so a program with none keeps its
// exact item list (byte-identical guarantee).
func dropTestItems(items []ast.Stmt) []ast.Stmt {
	hasTest := false
	for _, it := range items {
		if fn, ok := it.(*ast.FuncDecl); ok && sema.HasTestDecorator(fn) {
			hasTest = true
			break
		}
		if g, ok := it.(*ast.UnsafeGroup); ok {
			for _, sub := range g.Items {
				if fn, ok := sub.(*ast.FuncDecl); ok && sema.HasTestDecorator(fn) {
					hasTest = true
					break
				}
			}
		}
	}
	if !hasTest {
		return items
	}
	out := make([]ast.Stmt, 0, len(items))
	for _, it := range items {
		if fn, ok := it.(*ast.FuncDecl); ok && sema.HasTestDecorator(fn) {
			continue
		}
		if g, ok := it.(*ast.UnsafeGroup); ok {
			g.Items = dropTestItems(g.Items)
		}
		out = append(out, it)
	}
	return out
}

// stdlibProvider is the embedded-stdlib source root (Phase 1g S4). It resolves an
// import through the SAME module machinery as a user module — a module.FSProvider
// over the embedded stdlib tree (stdlib.FS()) — rather than the pre-1g out-of-band
// bundle. `import "io"` resolves the file module io.zg with canonical identity "io",
// so its mangle tag stays `io` and `io__println` is byte-identical.
//
// The one behaviour that stays special-cased is the historic last-segment ALIAS:
// the flat embedded layout also answers `import "std/io"` by loading io.zg, so an
// import that does not resolve at its full path retries at its last segment. This
// keeps the documented pre-1g convention working; it disappears once the stdlib is
// laid out as real nested directory modules.
type stdlibProvider struct{}

// Resolve locates a stdlib module through the shared FSProvider, then falls back to
// the last-segment alias for the flat layout.
func (stdlibProvider) Resolve(importPath string) (string, string, []module.ModuleFile, bool) {
	fsp := module.FSProvider{FS: stdlib.FS()}
	if canonical, dir, files, ok := fsp.Resolve(importPath); ok {
		return canonical, dir, files, true
	}
	if seg := lastSegment(importPath); seg != importPath {
		return fsp.Resolve(seg)
	}
	return "", "", nil, false
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
