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

// stdlibProvider is the embedded-stdlib source root: it resolves an import by the
// LAST path segment onto a flat stdlib module (`import "io"` and `import "std/io"`
// both load io.zg), matching the pre-1g bundle so the stdlib keeps working while
// its migration onto true directory modules is a later slice. Its canonical
// identity is that last segment, so the mangle tag stays `io` and `io__println`
// is byte-identical.
type stdlibProvider struct{}

// Resolve loads a flat stdlib module by the import path's last segment.
func (stdlibProvider) Resolve(importPath string) (string, []module.ModuleFile, bool) {
	seg := lastSegment(importPath)
	src, ok := stdlib.Source(seg)
	if !ok {
		return "", nil, false
	}
	return seg, []module.ModuleFile{{Name: seg + ".zg", Src: src}}, true
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
