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
//go:embed csrc/zergrt.h csrc/alloc.c csrc/ref.c csrc/list.c csrc/map.c csrc/unwind.c csrc/entry.c csrc/sys.c csrc/fmt.c csrc/conv.c csrc/str.c
//go:embed csrc/sched.c csrc/chan.c csrc/ctx_arm64.S csrc/ctx_x86_64.S csrc/ctx_ucontext.c
//go:embed csrc/thread_pthread.c csrc/thread_win32.c csrc/thread_none.c
//go:embed csrc/zrt_test.h csrc/zrt_test.c
var Files embed.FS

// coreCUnits are the Phase 1d core C translation units, always linked when the
// runtime is needed (fmt.c carries the Phase 1f display/Format helpers). The
// concurrency units (sched.c + a context switch) are added separately by
// ConcurrencyCUnits.
// map.c is back: it was dropped while no compiler could emit a map value, which put
// ~9KB of unreachable code in every binary for nothing. The self-hosted compiler emits
// one now — `map[K,V]`, `{k: v}` and `{:}` all lower to the zrt_map family — so the
// unit has a caller again.
var coreCUnits = []string{"alloc.c", "ref.c", "list.c", "map.c", "unwind.c", "entry.c", "sys.c", "fmt.c", "conv.c", "str.c"}

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

// TestCUnits returns the extra translation units for a `zerg test` binary: the
// test-runner harness (zrt_test.c), on top of the core units Materialize returns. The
// file was already written by Materialize; dir must be the same directory. A normal
// `zerg build` never adds it, so its link line and binary are unchanged.
func TestCUnits(dir string) []string {
	return []string{filepath.Join(dir, "zrt_test.c")}
}

// HostArch is the GOARCH of the machine the driver runs on, used to pick the
// context-switch implementation for a concurrent program.
func HostArch() string { return goruntime.GOARCH }

// ConcurrencyCUnits returns the extra translation units to compile for a program
// that uses concurrency: the scheduler and the channels, plus the context switch
// selected by arch (a hand-written .S for arm64/amd64, else the portable ucontext
// floor). The files were already written by Materialize; dir must be the same
// directory.
func ConcurrencyCUnits(dir, arch string) []string {
	ctx := "ctx_ucontext.c"
	switch arch {
	case "arm64":
		ctx = "ctx_arm64.S"
	case "amd64":
		ctx = "ctx_x86_64.S"
	}
	return []string{
		filepath.Join(dir, "sched.c"),
		filepath.Join(dir, "chan.c"),
		filepath.Join(dir, ctx),
		filepath.Join(dir, ThreadCUnit(goruntime.GOOS)),
	}
}

// ThreadCUnit is the zrt_thread backend for an OS: pthreads on anything POSIX, the
// Win32 set on Windows, and the no-thread floor otherwise. The floor is a correct
// build rather than a degraded one — with no threads the scheduler runs every
// coroutine on the calling thread, which is what it did before M:N, so what is lost
// is CPU parallelism and never behaviour.
func ThreadCUnit(goos string) string {
	switch goos {
	case "windows":
		return "thread_win32.c"
	case "js", "wasip1", "plan9":
		return "thread_none.c"
	default:
		return "thread_pthread.c"
	}
}
