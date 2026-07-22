// Package stdlib carries the Zerg standard-library modules (the src/stdlib/*.zg
// sources) embedded into the Go toolchain via go:embed, so the bundle importer
// (internal/build) can load a module's source without a filesystem lookup and the
// single `zerg` binary stays self-contained.
//
// The modules live under this same module (a small sibling module at the design's
// src/stdlib/ location, github.com/cmj0121/zerg/src/stdlib, wired into the
// workspace) precisely so they are embeddable: Go's go:embed cannot reach outside
// a package directory. The importer resolves a module BY ITS LAST PATH SEGMENT —
// `import "io"` and `import "std/io"` both bundle io.zg — which is the forward
// compatible subset of the 1g module graph (only the resolver changes then, not
// these sources).
package stdlib

import "embed"

// files holds the embedded stdlib module sources.
//
//go:embed io.zg testing.zg atomic.zg math.zg
var files embed.FS

// Source returns the embedded Zerg source of the stdlib module named name (a bare
// last-path-segment, e.g. "io"), and whether such a module exists.
func Source(name string) (string, bool) {
	data, err := files.ReadFile(name + ".zg")
	if err != nil {
		return "", false
	}
	return string(data), true
}
