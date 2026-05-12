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
// bootstrap-provided shims. Each family uses its on-disk layout
// convention (flat <name>.zg for stdlib, directory-with-mod.zg for
// sys). An unrecognised family returns (nil, false).
//
// For the sys/ family, v0.14 adds a per-host variant lookup before the
// generic mod.zg fall-through: when the host's (GOOS, GOARCH) pair is
// recognised, the loader first tries `sys/<name>/mod_<goos>_<goarch>.zg`
// in each embedded root, then falls back to `sys/<name>/mod.zg`. A
// module like sys/syscall ships only platform-specific variants (no
// mod.zg) so the lookup either finds the right variant or fails per
// host; sys/path ships only mod.zg and remains platform-neutral. The
// fall-back path keeps both styles working from a single loader rule.
func defaultStdlibFallback(family, name string) ([]byte, bool) {
	switch family {
	case "stdlib":
		path := name + ".zg"
		return readEmbeddedRoots(path)
	case "sys":
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
