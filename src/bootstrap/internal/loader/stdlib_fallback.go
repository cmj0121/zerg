package loader

// stdlibFallback returns the toolchain's built-in source for an embedded-
// family module when the on-disk lookup misses. Family is the family label
// ("stdlib" / "sys"); name is the post-prefix module name. A return of
// (nil, false) means no fallback is registered and the loader surfaces
// the standard "<family> module not found" diagnostic.
//
// The fallback is the bootstrap's (later: the self-hosted runtime's)
// provided implementation of a stdlib module: when src/std/<name>.zg is
// not on disk — pruned install, sandboxed build, corrupted source tree —
// the toolchain can still resolve the import from its built-in copy.
//
// The hook is a function variable so the content mechanism (//go:embed,
// inline Go strings, generated tables, hand-maintained registry) is a
// separate decision from the dispatch chain. The default lookup returns
// no entries, matching today's behaviour where the canonical stdlib lives
// only at src/std/; future work plugs in an actual fallback source.
var stdlibFallback = func(family, name string) ([]byte, bool) {
	return nil, false
}

// SetStdlibFallbackForTest swaps the fallback lookup for the duration of
// the returned restore func. Tests inject a synthetic fallback entry to
// exercise the disk-miss → fallback-hit path without coupling to whatever
// mechanism production eventually uses.
func SetStdlibFallbackForTest(lookup func(family, name string) ([]byte, bool)) func() {
	prev := stdlibFallback
	stdlibFallback = lookup
	return func() { stdlibFallback = prev }
}
