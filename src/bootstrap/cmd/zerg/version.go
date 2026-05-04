package main

import (
	"fmt"
	"os"

	"github.com/cmj/zerg/src/bootstrap/internal/version"
)

// scanRequires is the cmd-package-local alias for version.ScanRequires. The
// helper kept its lower-case name so cmd-level tests (version_test.go) can
// continue to drive the same code path; the implementation lives in
// internal/version so the loader's per-module gate shares it.
func scanRequires(src []byte) (major, minor int, ok bool) {
	return version.ScanRequires(src)
}

// versionLess is the cmd-package-local alias for version.Less. Same reason
// as scanRequires: the cmd-level tests call into this entry, while the
// loader uses the shared helper.
func versionLess(aMajor, aMinor, bMajor, bMinor int) bool {
	return version.Less(aMajor, aMinor, bMajor, bMinor)
}

// toolchainMajor / toolchainMinor mirror the shared toolchain constants so
// existing cmd-level call sites and tests don't churn. The single source of
// truth is internal/version.
const (
	toolchainMajor = version.Major
	toolchainMinor = version.Minor
)

// errRequiresFutureVersion is the sentinel checkRequiresFile returns when
// a file's `# requires:` marker exceeds the toolchain version. main()
// detects this sentinel and exits 1 without re-logging — the user-facing
// message has already been written to stderr by checkRequiresFile.
var errRequiresFutureVersion = fmt.Errorf("requires future toolchain version")

// checkRequiresFile reads path and, if it carries a future-version
// `# requires:` marker, prints the standard rejection message to stderr
// and returns errRequiresFutureVersion. A nil return means the file is
// either unmarked or marked for a version we can ship.
func checkRequiresFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	maj, min, ok := scanRequires(src)
	if !ok {
		return nil
	}
	if versionLess(toolchainMajor, toolchainMinor, maj, min) {
		fmt.Fprintf(os.Stderr, "zerg: %s requires v%d.%d (current is v%d.%d)\n",
			path, maj, min, toolchainMajor, toolchainMinor)
		return errRequiresFutureVersion
	}
	return nil
}
