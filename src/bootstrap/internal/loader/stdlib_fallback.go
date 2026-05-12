package loader

import (
	"embed"
	"io/fs"
)

// The loader serves stdlib modules from TWO embedded sources, consulted
// in priority order:
//
//   1. embeddedStdlib  — mirror of src/std/ (pure-Zerg source).
//                        Populated at build time by `make sync-stdlib`
//                        (rsync of ../std/ → stdlib_embed/).
//
//   2. bootstrapProvided — `__builtin`-bearing shims that live inside
//                          the bootstrap module. Authored directly,
//                          committed under bootstrap_provided/, never
//                          synced.
//
// Pure-Zerg implementations in src/std/ shadow the bootstrap shims by
// search order — writing src/std/math.zg in pure Zerg silently retires
// bootstrap_provided/math.zg with no other change. When the last shim
// has a pure-Zerg replacement, bootstrap_provided/ goes away and
// `__builtin` retires with it.

//go:embed stdlib_embed
var embeddedStdlib embed.FS

//go:embed bootstrap_provided
var bootstrapProvided embed.FS

var (
	embeddedStdlibRoot   = mustSub(embeddedStdlib, "stdlib_embed")
	bootstrapProvidedRoot = mustSub(bootstrapProvided, "bootstrap_provided")
)

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
// The default reads from the two embedded sources above. $ZERG_STDLIB
// lets developers point at an on-disk tree for tight edit loops, but
// the embeds are what make the toolchain self-contained.
//
// The hook is a function variable so tests can swap in a synthetic
// registry without touching production code; production never reassigns
// it.
var stdlibFallback = defaultStdlibFallback

// SetStdlibFallbackForTest swaps the fallback lookup for the duration
// of the returned restore func. Tests inject a synthetic fallback entry
// to exercise the disk-miss → fallback-hit path, or pass a no-op lookup
// to force a total miss for negative tests against the default embeds.
func SetStdlibFallbackForTest(lookup func(family, name string) ([]byte, bool)) func() {
	prev := stdlibFallback
	stdlibFallback = lookup
	return func() { stdlibFallback = prev }
}

// defaultStdlibFallback resolves (family, name) against the two
// embedded sources, in priority order: src/std/ mirror first, then the
// bootstrap-provided shims. An unrecognised family returns (nil, false).
//
// Layout:
//   - std/* family is flat: `<name>.zg`.
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
		path := name + ".zg"
		return readEmbeddedRoots(path)
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

// readEmbeddedRoots tries the embedded roots in priority order and
// returns the first hit. Factored out so the sys/ family can call it
// twice (variant lookup then generic) without duplicating the loop.
func readEmbeddedRoots(path string) ([]byte, bool) {
	for _, root := range []fs.FS{embeddedStdlibRoot, bootstrapProvidedRoot} {
		if content, err := fs.ReadFile(root, path); err == nil {
			return content, true
		}
	}
	return nil, false
}
