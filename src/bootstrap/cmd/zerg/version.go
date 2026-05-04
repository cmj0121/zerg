package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// toolchainVersion is the (major, minor) advertised by this binary. The
// example-gating check uses it to refuse files whose `# requires:` marker
// exceeds what we ship. Kept as a const pair — not parsed from the version
// string — so a typo in the version string can't silently relax the gate.
const (
	toolchainMajor = 0
	toolchainMinor = 4
)

// requiresPattern matches a `# requires: vMAJOR.MINOR` example header.
// Anchored at the start; trailing whitespace tolerated. The grammar treats
// the whole line as a normal `#` comment, so the lexer never inspects it —
// the CLI is the only consumer.
var requiresPattern = regexp.MustCompile(`^#\s*requires:\s*v(\d+)\.(\d+)\s*$`)

// scanRequires returns the (major, minor) of the first `# requires:` line
// in src, or ok=false if no such marker exists. We stop at the first line
// that is neither blank nor a `#` comment, so a stray "# requires:" buried
// mid-program cannot retroactively gate a file. The shebang `#! …` counts
// as a comment — that's why an example with a shebang at line 1 can put
// `# requires:` on line 2.
func scanRequires(src []byte) (major, minor int, ok bool) {
	scanner := bufio.NewScanner(bytes.NewReader(src))
	for scanner.Scan() {
		line := trimLeadingSpaceTab(scanner.Text())
		if line == "" {
			continue
		}
		if line[0] != '#' {
			return 0, 0, false
		}
		m := requiresPattern.FindStringSubmatch(line)
		if m == nil {
			// Plain `#` comment (including the shebang) — keep looking.
			continue
		}
		maj, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		return maj, min, true
	}
	return 0, 0, false
}

// versionLess reports whether (aMajor, aMinor) < (bMajor, bMinor) under the
// natural lexicographic order on (major, minor).
func versionLess(aMajor, aMinor, bMajor, bMinor int) bool {
	if aMajor != bMajor {
		return aMajor < bMajor
	}
	return aMinor < bMinor
}

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

// trimLeadingSpaceTab strips spaces and tabs at the head of s. We want
// trailing whitespace preserved so the requiresPattern regex can reject
// anything malformed at the tail.
func trimLeadingSpaceTab(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[i:]
}
