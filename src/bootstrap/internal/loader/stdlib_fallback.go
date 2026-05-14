package loader

import (
	"embed"
	"io/fs"
)

// The loader serves stdlib modules from a single embedded source:
// stdlib_embed/ — a mirror of src/std/ (pure-Zerg source) populated
// at build time by `make sync-stdlib` (rsync of ../std/ → stdlib_embed/).
//
// v0.14 retired the second embed root (bootstrap_provided/) after the
// last `__builtin`-bearing shim (os.zg) was rewritten in pure Zerg over
// atomic accessor primitives. `__builtin` itself stays as a language
// feature — pure-Zerg stdlib files still declare narrow primitives
// (time_clock_us, os_argv_at, etc.) that the cgen and interpreter
// recognise by name — but the directory of forwarder shims is gone.

//go:embed stdlib_embed
var embeddedStdlib embed.FS

var embeddedStdlibRoot = mustSub(embeddedStdlib, "stdlib_embed")

func mustSub(efs embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(efs, dir)
	if err != nil {
		// fs.Sub only errors on an invalid path component; the
		// constant we pass is always valid. Treat as unrecoverable
		// init failure rather than degrading to a half-loaded
		// toolchain.
		panic(err)
	}
	return sub
}

// stdlibFallback returns the toolchain's built-in source for an embedded-
// family module when the on-disk lookup misses. Family is the family
// label ("stdlib" / "sys"); name is the post-prefix module name. A
// return of (nil, false) means no fallback is registered and the loader
// surfaces the standard "<family> module not found" diagnostic.
//
// The default reads from the embedded source. $ZERG_STDLIB lets
// developers point at an on-disk tree for tight edit loops, but the
// embed is what makes the toolchain self-contained.
//
// The hook is a function variable so tests can swap in a synthetic
// registry without touching production code; production never reassigns
// it.
var stdlibFallback = defaultStdlibFallback

// SetStdlibFallbackForTest swaps the fallback lookup for the duration
// of the returned restore func. Tests inject a synthetic fallback entry
// to exercise the disk-miss → fallback-hit path, or pass a no-op lookup
// to force a total miss for negative tests against the default embed.
func SetStdlibFallbackForTest(lookup func(family, name string) ([]byte, bool)) func() {
	prev := stdlibFallback
	stdlibFallback = lookup
	return func() { stdlibFallback = prev }
}

// defaultStdlibFallback resolves (family, name) against the embedded
// source. An unrecognised family returns (nil, false).
//
// Layout:
//   - std/* family admits two forms, probed in order:
//
//        1. <name>.zg                                  flat single-file
//        2. <name>/mod.zg                              directory module
//                                                       (math at v0.17)
//
//   - sys/* family is per-module — the loader probes three forms in
//     order and uses whichever exists (matches sysModulePath's on-disk
//     rule so disk and embed paths agree):
//
//        1. sys/<name>.zg                              flat single-file
//        2. sys/<name>/mod_<goos>_<goarch>.zg          per-host variant
//        3. sys/<name>/mod.zg                          generic dir body
//
//     A module author picks whichever shape fits — sys/path (platform-
//     neutral, single file) uses form 1; sys/syscall (per-host
//     implementations) uses form 2; a module with shared base + opt-
//     in per-host overrides uses 2+3.
func defaultStdlibFallback(family, name string) ([]byte, bool) {
	switch family {
	case "stdlib":
		if content, ok := readEmbeddedRoots(name + ".zg"); ok {
			return content, true
		}
		return readEmbeddedRoots(name + "/mod.zg")
	case "sys":
		if content, ok := readEmbeddedRoots("sys/" + name + ".zg"); ok {
			return content, true
		}
		if variant := sysPlatformVariantName(); variant != "" {
			specific := "sys/" + name + "/mod_" + variant + ".zg"
			if content, ok := readEmbeddedRoots(specific); ok {
				return content, true
			}
		}
		return readEmbeddedRoots("sys/" + name + "/mod.zg")
	default:
		return nil, false
	}
}

// readEmbeddedRoots reads a single embedded path from the stdlib_embed
// root. Retains the (path, bool) signature because the sys/ family
// calls it three times to probe the layout variants without
// re-implementing the open / errno-check.
func readEmbeddedRoots(path string) ([]byte, bool) {
	if content, err := fs.ReadFile(embeddedStdlibRoot, path); err == nil {
		return content, true
	}
	return nil, false
}
