// Package runtime carries the Zerg C runtime tree (the src/runtime/*.h and
// *.c sources) embedded into the Go toolchain via go:embed, so the single
// `zerg` binary stays self-contained. The compiler driver materializes this
// tree next to the emitted C and links it only when a program's Manifest
// reports it needs the runtime; value-only programs never see it.
//
// The C tree lives under this same module (in csrc/) precisely so it is
// embeddable: Go's go:embed cannot reach outside a package directory, so the
// runtime is a small sibling module (github.com/cmj0121/zerg/src/runtime) at the
// design's src/runtime/ location, wired into the workspace and imported by the
// driver. The sources sit in the csrc/ subdirectory rather than beside this file
// because Go rejects .c files in a package directory that does not use cgo.
package runtime

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// csrcDir is the embedded subdirectory holding the C runtime sources.
const csrcDir = "csrc"

// Files holds the embedded C runtime sources (headers and translation units).
//
//go:embed csrc/zergrt.h csrc/alloc.c csrc/ref.c csrc/unwind.c csrc/entry.c csrc/sys.c
var Files embed.FS

// Materialize writes the embedded runtime tree into dir and returns the paths
// of the C translation units to compile (sorted for a stable command line).
// The include directory is dir itself, where zergrt.h is written. It is used by
// the driver's link step when the Manifest reports the runtime is needed.
func Materialize(dir string) (cfiles []string, err error) {
	entries, err := fs.ReadDir(Files, csrcDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		data, rerr := Files.ReadFile(csrcDir + "/" + e.Name())
		if rerr != nil {
			return nil, rerr
		}
		out := filepath.Join(dir, e.Name())
		if werr := os.WriteFile(out, data, 0o644); werr != nil { //nolint:gosec // runtime source, not a secret
			return nil, werr
		}
		if strings.HasSuffix(e.Name(), ".c") {
			cfiles = append(cfiles, out)
		}
	}
	sort.Strings(cfiles)
	return cfiles, nil
}
