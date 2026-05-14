// Package version centralises the toolchain `(major, minor)` constants and
// the `# requires: vX.Y` comment scanner. v0.0–v0.4 only needed the gate at
// the entry file, so the helpers lived in `cmd/zerg`. v0.5's loader checks
// every imported module's `requires:` line as well — pulling the helpers
// here keeps the toolchain version one source of truth and lets both the CLI
// and the loader call into the same code path.
package version
//
// The scanner deliberately runs ahead of the lexer: a `# requires:` marker
// is plain comment to the language but a load-time gate to the driver.
// Keeping it as a byte-level pre-scan means a corrupted lex doesn't rob us
// of the user-facing rejection.

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
)

// Toolchain is the (major, minor) advertised by this binary. The example-
// gating check uses it to refuse files whose `# requires:` marker exceeds
// what we ship. Kept as a const pair (not parsed from a version string) so a
// typo in the version string can't silently relax the gate.
const (
	Major = 0
	// Minor advances to 19 with v0.19 — self-rehydrating multi-assign
	// for composite types. The borrow checker now treats an RHS
	// tuple-literal element that is a bare ident naming one of the
	// multi-assign's own targets as a read, not a move. The LHS write
	// rebinds the slot immediately after the temp tuple is built, so
	// the binding's state matches before and after the statement —
	// even inside a loop body. This unblocks the canonical Fibonacci
	// step `a, b = b, a + b` over struct types (math.BigInt and
	// friends), not just primitive ints.
	//
	// Inherited from v0.18 cycle: `pub import` flat re-export.
	// Inherited from v0.17 cycle: operator-spec wiring (Arithmetic /
	// Comparable / From), math/big arbitrary-precision arithmetic,
	// and the math directory-module reorganization.
	Minor = 19
)

// requiresPattern matches a `# requires: vMAJOR.MINOR` example header.
// Anchored at the start; trailing whitespace tolerated. The grammar treats
// the whole line as a normal `#` comment, so the lexer never inspects it —
// the driver and loader are the only consumers.
var requiresPattern = regexp.MustCompile(`^#\s*requires:\s*v(\d+)\.(\d+)\s*$`)

// ScanRequires returns the (major, minor) of the first `# requires:` line
// in src, or ok=false if no such marker exists. We stop at the first line
// that is neither blank nor a `#` comment, so a stray "# requires:" buried
// mid-program cannot retroactively gate a file. The shebang `#! …` counts
// as a comment — that's why an example with a shebang at line 1 can put
// `# requires:` on line 2.
func ScanRequires(src []byte) (major, minor int, ok bool) {
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

// Less reports whether (aMajor, aMinor) < (bMajor, bMinor) under the natural
// lexicographic order on (major, minor).
func Less(aMajor, aMinor, bMajor, bMinor int) bool {
	if aMajor != bMajor {
		return aMajor < bMajor
	}
	return aMinor < bMinor
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
