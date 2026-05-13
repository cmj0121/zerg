package loader

import (
	"os"
	"path/filepath"
	"sync"
)

// stdlibRoot returns the on-disk directory the loader should read stdlib
// modules from, or "" to defer to the embedded fallback (the canonical
// case for shipped binaries — see internal/loader/stdlib_fallback.go
// and src/bootstrap/std/).
//
// Resolution: only the $ZERG_STDLIB env override. Auto-discovery of a
// source-tree `src/std/` is gone — the stdlib was moved into the
// bootstrap Go module and is embedded via `//go:embed`, so no disk
// search is needed in the common case. Developers iterating on stdlib
// source can set $ZERG_STDLIB to an alternate tree (e.g. a working copy
// of `src/bootstrap/std/`) and the loader will read from disk in
// preference to the embed.
//
// Cached after the first resolution; tests that need to override use
// SetStdlibRootForTest rather than mutating the env.
func stdlibRoot() string {
	stdlibRootOnce.Do(func() {
		stdlibRootValue = resolveStdlibRoot()
	})
	return stdlibRootValue
}

var (
	stdlibRootOnce  sync.Once
	stdlibRootValue string
)

// SetStdlibRootForTest overrides stdlibRoot() for the duration of the
// returned restore func. Forces the once-cache to populate first so the
// captured `prev` is the real default, not the zero value — without
// this a test that's the first caller in the process would poison the
// cache for every subsequent caller (production or test) in the run.
func SetStdlibRootForTest(path string) func() {
	stdlibRootOnce.Do(func() { stdlibRootValue = resolveStdlibRoot() })
	prev := stdlibRootValue
	stdlibRootValue = path
	return func() { stdlibRootValue = prev }
}

func resolveStdlibRoot() string {
	if v := os.Getenv("ZERG_STDLIB"); v != "" && isDir(v) {
		return filepath.Clean(v)
	}
	return ""
}

// stdlibModulePath returns the on-disk path for a `std/<name>` import.
// Layout when $ZERG_STDLIB is set: `<root>/<name>.zg`. When stdlibRoot
// is empty (the default), returns a virtual path (`std/<name>.zg`) that
// will fail os.ReadFile and trigger the embedded-fallback chain — the
// virtual form then serves as the user-facing diagnostic anchor.
func stdlibModulePath(name string) string {
	if root := stdlibRoot(); root != "" {
		return filepath.Join(root, name+".zg")
	}
	return "std/" + name + ".zg"
}

// sysModulePath returns the on-disk path for a `sys/<name>` import.
// Same disk/virtual duality as stdlibModulePath. Layout selection is
// per-module: the loader probes three forms and picks whichever exists.
//
// Resolution order:
//
//  1. `sys/<name>.zg`                                — flat single-file
//                                                      layout. Right for
//                                                      platform-neutral
//                                                      modules with no
//                                                      per-host shape
//                                                      (sys/path is the
//                                                      first example).
//  2. `sys/<name>/mod_<goos>_<goarch>.zg`           — per-host variant.
//                                                      Right for modules
//                                                      with one file per
//                                                      target tuple
//                                                      (sys/syscall).
//  3. `sys/<name>/mod.zg`                            — directory with a
//                                                      generic body.
//                                                      Right for modules
//                                                      that want a
//                                                      common base plus
//                                                      optional per-host
//                                                      overrides; the
//                                                      generic body is
//                                                      the no-variant
//                                                      fall-through.
//
// A module that has BOTH flat (`sys/<name>.zg`) AND a directory
// (`sys/<name>/`) is an authoring error — this resolver silently
// prefers the flat form, but a follow-up sanity pass at module-graph
// load time could reject it explicitly.
//
// The virtual-path branch (ZERG_STDLIB unset) returns the generic
// `sys/<name>/mod.zg` sentinel that os.ReadFile will miss; the
// embedded fallback chain (defaultStdlibFallback) runs its own three-
// way probe over the embedded FS.
func sysModulePath(name string) string {
	if root := stdlibRoot(); root != "" {
		flat := filepath.Join(root, "sys", name+".zg")
		if fileExists(flat) {
			return flat
		}
		dir := filepath.Join(root, "sys", name)
		if variant := sysPlatformVariantName(); variant != "" {
			specific := filepath.Join(dir, "mod_"+variant+".zg")
			if fileExists(specific) {
				return specific
			}
		}
		return filepath.Join(dir, "mod.zg")
	}
	// ZERG_STDLIB unset: return the virtual path whose shape matches
	// the embed's layout for this module, so Target.Path reads as the
	// actual layout the loader will pull from.
	if _, ok := readEmbeddedRoots("sys/" + name + ".zg"); ok {
		return "sys/" + name + ".zg"
	}
	if variant := sysPlatformVariantName(); variant != "" {
		specific := "sys/" + name + "/mod_" + variant + ".zg"
		if _, ok := readEmbeddedRoots(specific); ok {
			return specific
		}
	}
	return "sys/" + name + "/mod.zg"
}

// isDir reports whether path resolves to a directory on disk. Tolerant
// of stat errors — any error is treated as "not a directory" so the
// $ZERG_STDLIB override silently falls back to the embed when set to a
// non-existent path.
func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
