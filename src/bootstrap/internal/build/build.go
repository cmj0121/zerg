// Package build ties the front-end and back-end into one call: it runs the Phase
// 0 pipeline lex -> parse -> sema -> emit and returns C source. Compiling that C to
// a binary with a C compiler is the driver's job (it is host I/O, not language
// processing), so this package stays pure and easy to test.
package build

import (
	"os"
	"path/filepath"

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

// CheckProgram runs only the front-end of the whole-program pipeline over the entry
// file — module resolution, name resolution, and type checking — and returns its
// diagnostics (empty on success). It is the shared basis for `zerg lint`, which
// layers its lint-only findings on a program the compiler already accepts; stopping
// before mono/emit means it reports exactly the compile-time errors without lowering.
func CheckProgram(entryPath string) []diag.Diagnostic {
	src, err := os.ReadFile(entryPath) //nolint:gosec // the entry source the user asked to lint
	if err != nil {
		var diags diag.List
		diags.Add(token.Span{}, "cannot read entry file %q: %v", entryPath, err)
		return diags.Items()
	}
	root := module.OSProvider{Root: filepath.Dir(entryPath)}
	loader := module.NewLoader(root, stdlibProvider{})
	file, _, diags := loader.LoadProgram(string(src))
	if len(diags) > 0 {
		return diags
	}
	_, diags = sema.Check(file)
	return diags
}

// compileWith runs the shared inner pipeline over a configured loader.
func compileWith(loader *module.Loader, src string) (string, emit.Manifest, []diag.Diagnostic) {
	file, plan, diags := loader.LoadProgram(src)
	if len(diags) > 0 {
		return "", emit.Manifest{}, diags
	}
	info, diags := sema.Check(file)
	if len(diags) > 0 {
		return "", emit.Manifest{}, diags
	}
	return emit.Emit(mono.BuildWithInit(file, info, plan))
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
func (stdlibProvider) Resolve(importPath string) (string, []module.ModuleFile, bool) {
	fsp := module.FSProvider{FS: stdlib.FS()}
	if canonical, files, ok := fsp.Resolve(importPath); ok {
		return canonical, files, true
	}
	if seg := lastSegment(importPath); seg != importPath {
		return fsp.Resolve(seg)
	}
	return "", nil, false
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
