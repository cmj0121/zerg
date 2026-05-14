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
	// Path is the import-form path the user types, e.g. "io" (bare,
	// implicit std/* default) or "sys/path" (explicit sys/* prefix
	// always required for platform-specific modules). The display
	// mirrors the bare-import fall-through: std/* modules show their
	// short name; sys/* modules show the required prefix.
	Path string
	// Description is one short sentence (target ≤ 70 chars) suitable for
	// display in a single terminal line next to Path.
	Description string
}

// Catalog returns the supported stdlib modules in stable display order:
// std/* (bare-name) entries first, alphabetical; then sys/* entries
// (always prefixed), alphabetical. The returned slice is a fresh copy
// each call so callers may sort or filter without mutating shared state.
//
// New stdlib modules add an entry here. The catalog is the single source
// of truth for `zerg stdlib`.
func Catalog() []CatalogEntry {
	return []CatalogEntry{
		{Path: "io", Description: "File reading and writing with typed IoError variants."},
		{Path: "math", Description: "Integer math primitives: abs, min, max, gcd."},
		{Path: "os", Description: "Process surface: environment, argv, exit."},
		{Path: "strings", Description: "String search, transform, and parse helpers."},
		{Path: "time", Description: "Wall-clock time (now_ms) and sleep_ms."},
		{Path: "sys/path", Description: "Path-string manipulation as a Path struct."},
		{Path: "sys/syscall", Description: "Raw BSD syscall traps for macOS arm64."},
	}
}
