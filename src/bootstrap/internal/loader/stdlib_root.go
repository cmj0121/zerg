package loader

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// stdlibRoot returns the absolute path to the Zerg stdlib source tree
// (the `src/std/` directory in the development tree). The stdlib lives
// outside the bootstrap Go module as pure Zerg code; the toolchain
// resolves imports under `std/*` and `sys/*` by reading files from this
// directory at runtime rather than embedding them in the binary.
//
// Resolution order:
//
//  1. $ZERG_STDLIB environment override. Wins unconditionally so
//     packagers / users with non-standard layouts have an escape hatch.
//  2. runtime.Caller(0) on this source file. The bootstrap is built
//     from <repo>/src/bootstrap/; this source file lives at
//     <repo>/src/bootstrap/internal/loader/stdlib_root.go, so walking
//     up four levels (loader/ -> internal/ -> bootstrap/ -> src/)
//     and then descending into std/ yields the canonical tree.
//     Works for `go test` and for binaries built and run on the same
//     machine; fails on cross-machine deploys (the embedded source
//     path no longer exists on disk).
//  3. os.Executable()/../std/. Works for binaries packaged alongside
//     a sibling `std/` directory — the conventional install layout
//     (`/usr/local/bin/zerg` next to `/usr/local/share/zerg/std`,
//     adjusted for the install prefix).
//
// Returns "" if no candidate resolves to an actual directory. Stdlib
// and sys imports then miss with the same "stdlib module not found" /
// "sys module not found" wording as before — the user-facing surface
// is unchanged.
//
// The result is cached after the first successful resolution; the
// discovery walk does enough syscalls that re-running on every import
// during a large bundle load would be wasteful.
func stdlibRoot() string {
	stdlibRootOnce.Do(func() {
		stdlibRootValue = resolveStdlibRoot()
	})
	return stdlibRootValue
}

// stdlibRootOnce guards the lazy resolution. We must defer the
// resolution rather than initialise at package-init time because tests
// override $ZERG_STDLIB via t.Setenv after package init.
var (
	stdlibRootOnce  sync.Once
	stdlibRootValue string
)

// SetStdlibRootForTest overrides stdlibRoot() for the duration of the
// returned restore func. Tests that need a hermetic stdlib tree (a
// fixture directory under t.TempDir, say) use this seam rather than
// $ZERG_STDLIB so they remain insensitive to the host's env vars.
//
// Force the production resolver to populate the cached value FIRST so
// the captured `prev` is the real root, not the empty default. Without
// this, a test that is the first caller in the process would capture
// `prev == ""` and the restore would poison the cache for every
// subsequent caller (production or test) in the same `go test` run.
func SetStdlibRootForTest(path string) func() {
	stdlibRootOnce.Do(func() { stdlibRootValue = resolveStdlibRoot() })
	prev := stdlibRootValue
	stdlibRootValue = path
	return func() { stdlibRootValue = prev }
}

// resolveStdlibRoot runs the discovery chain documented on stdlibRoot.
// Separated from stdlibRoot so the once-cache is the only difference.
func resolveStdlibRoot() string {
	// $ZERG_STDLIB wins unconditionally but is still validated as a
	// directory so a misconfigured env var fails fast at discovery
	// time instead of producing "module not found" errors for every
	// stdlib import.
	if v := os.Getenv("ZERG_STDLIB"); v != "" && isDir(v) {
		return filepath.Clean(v)
	}
	// runtime.Caller(0) for go test / go run from the source tree.
	if _, thisFile, _, ok := runtime.Caller(0); ok {
		// thisFile = .../src/bootstrap/internal/loader/stdlib_root.go
		// loader -> internal -> bootstrap -> src -> src/std.
		candidate := filepath.Join(
			filepath.Dir(thisFile), // loader/
			"..", "..", "..",       // internal/ bootstrap/ src/
			"std",
		)
		if isDir(candidate) {
			return filepath.Clean(candidate)
		}
	}
	// os.Executable() for installed binaries with a sibling std/ tree.
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "std")
		if isDir(candidate) {
			return filepath.Clean(candidate)
		}
	}
	return ""
}

// stdlibModulePath returns the on-disk path for a `std/<name>` import.
// Layout: `<stdlib-root>/<name>.zg`. The std/* family is the implicit
// default — its files live directly at the stdlib-tree root rather
// than in a redundant `std/` subdirectory. The flat-file form is kept
// because v0.5–v0.12 ships one file per module and the modules are
// small enough that the directory-with-mod.zg form would just add
// indirection.
func stdlibModulePath(name string) string {
	return filepath.Join(stdlibRoot(), name+".zg")
}

// sysModulePath returns the on-disk path for a `sys/<name>` import.
// Layout: `<stdlib-root>/sys/<name>/mod.zg`. The sys/* family sits in
// its own subdirectory under the stdlib root (parallel families like
// usr/* would follow the same pattern) and uses the
// directory-with-mod.zg convention (Rust's mod.rs) so platform-suffix
// variants `<name>_macos.zg` / `<name>_linux.zg` can sit alongside
// mod.zg inside the module's directory without crowding the top level.
func sysModulePath(name string) string {
	return filepath.Join(stdlibRoot(), "sys", name, "mod.zg")
}

// isDir reports whether path resolves to a directory on disk. Tolerant
// of stat errors — any error is treated as "not a directory" so the
// discovery chain falls through to the next candidate.
func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
