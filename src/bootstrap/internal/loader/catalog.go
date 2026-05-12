package loader

// CatalogEntry describes a toolchain-supported stdlib module — the unit
// surfaced by `zerg stdlib` to the user. Curated by hand rather than
// derived from disk: the catalog is the authoritative set of modules
// the toolchain *promises* to resolve, independent of whether
// src/std/<name>.zg happens to exist on the current filesystem.
//
// This mirrors the bootstrap-fallback concept: even on a pruned install
// with no src/std/ tree, `zerg stdlib` should report the same set of
// modules the toolchain can resolve via the fallback path.
type CatalogEntry struct {
	// Path is the import-form path, e.g. "std/io" or "sys/path".
	Path string
	// Description is one short sentence (target ≤ 70 chars) suitable for
	// display in a single terminal line next to Path.
	Description string
}

// Catalog returns the supported stdlib modules in stable display order:
// family-grouped (std/* then sys/*), alphabetical within family. The
// returned slice is a fresh copy each call so callers may sort or filter
// without mutating shared state.
//
// New stdlib modules add an entry here. The catalog is the single source
// of truth for `zerg stdlib`; LANGUAGE.md / STDLIB.md document the same
// set in more detail.
func Catalog() []CatalogEntry {
	return []CatalogEntry{
		{Path: "std/io", Description: "File reading and writing with typed IoError variants."},
		{Path: "std/math", Description: "Integer math primitives: abs, min, max, gcd."},
		{Path: "std/os", Description: "Process surface: environment, argv, exit."},
		{Path: "std/strings", Description: "String search, transform, and parse helpers."},
		{Path: "std/time", Description: "Wall-clock time (now_ms) and sleep_ms."},
		{Path: "sys/path", Description: "Path-string manipulation as a Path struct."},
	}
}
