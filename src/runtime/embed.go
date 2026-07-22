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
	goruntime "runtime"
	"sort"
)

// csrcDir is the embedded subdirectory holding the C runtime sources.
const csrcDir = "csrc"

// Files holds the embedded C runtime sources: the Phase 1d core (header + core
// translation units) plus the 1e concurrency sources (the scheduler and the
// per-arch context switch). The concurrency sources are materialized alongside the
// core but linked ONLY when the Manifest reports Concurrency, so a non-concurrent
// program's link line — and its binary — is unchanged.
//
//go:embed csrc/zergrt.h csrc/alloc.c csrc/ref.c csrc/unwind.c csrc/entry.c csrc/sys.c
//go:embed csrc/sched.c csrc/ctx_arm64.S csrc/ctx_x86_64.S csrc/ctx_ucontext.c
var Files embed.FS

// coreCUnits are the Phase 1d core C translation units, always linked when the
// runtime is needed. The concurrency units (sched.c + a context switch) are added
// separately by ConcurrencyCUnits.
var coreCUnits = []string{"alloc.c", "ref.c", "unwind.c", "entry.c", "sys.c"}

// Materialize writes the whole embedded runtime tree (header, core units, and the
// concurrency sources) into dir and returns the paths of the CORE C translation
// units to compile (sorted for a stable command line). The include directory is dir
// itself, where zergrt.h is written. Concurrency units are written too but returned
// only via ConcurrencyCUnits, so a program that does not need concurrency links
// exactly the Phase 1d set and stays byte-identical.
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
	}
	for _, name := range coreCUnits {
		cfiles = append(cfiles, filepath.Join(dir, name))
	}
	sort.Strings(cfiles)
	return cfiles, nil
}

// HostArch is the GOARCH of the machine the driver runs on, used to pick the
// context-switch implementation for a concurrent program.
func HostArch() string { return goruntime.GOARCH }

// ConcurrencyCUnits returns the extra translation units to compile for a program
// that uses concurrency: the scheduler plus the context switch selected by arch
// (a hand-written .S for arm64/amd64, else the portable ucontext floor). The files
// were already written by Materialize; dir must be the same directory.
func ConcurrencyCUnits(dir, arch string) []string {
	ctx := "ctx_ucontext.c"
	switch arch {
	case "arm64":
		ctx = "ctx_arm64.S"
	case "amd64":
		ctx = "ctx_x86_64.S"
	}
	return []string{filepath.Join(dir, "sched.c"), filepath.Join(dir, ctx)}
}
