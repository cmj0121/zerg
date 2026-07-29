package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
)

// TestExamplesCompile guards the developer-facing sample programs: every
// numbered examples/NN_*.zg at the repo root must compile to C and link with cc.
// These are the first code a newcomer reads, so they must never rot. Skips when
// the repo root or a C compiler cannot be found.
func TestExamplesCompile(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("repo root (go.work) not found")
	}
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	sources, err := filepath.Glob(filepath.Join(root, "examples", "[0-9][0-9]_*.zg"))
	if err != nil || len(sources) == 0 {
		t.Skip("no numbered examples found")
	}

	for _, zg := range sources {
		name := strings.TrimSuffix(filepath.Base(zg), ".zg")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(zg)
			if err != nil {
				t.Fatalf("read %s: %v", zg, err)
			}
			if beyondSeed(string(src)) {
				checkSeedRefuses(t, name, string(src))
				return
			}
			code, _, diags := Compile(string(src))
			if len(diags) != 0 {
				t.Fatalf("%s should compile, got diagnostics: %v", name, diags)
			}
			// link (and run) to confirm the emitted C is valid
			compileAndRun(t, cc, code)
		})
	}
}

// beyondSeedMarker is the line an example carries when it is written in the LANGUAGE
// rather than in the subset the bootstrap seed lowers — a directional channel end, a
// `spawn` on a method, anything the seed refuses by design (PLAN Decision #8). The
// examples are built and run by `zerg` (`make examples`), which implements the whole
// language; the tests below compile them with the SEED, so they need to be told where
// the seed's line falls.
//
// The marker lives in the example rather than in a list here, and that is the point: a
// list in this file and the Makefile's own view of the examples would be two statements
// of the same fact, free to disagree. The Makefile has no list to disagree with — it
// builds every numbered example with `zerg` — and a marked example is a fact about the
// file, so it travels with the file when one is added, renamed, or rewritten.
//
// Being marked is not an exemption from testing. checkSeedRefuses holds a marked example
// to a STRICTER contract than an unmarked one: the seed must answer it with its own named
// diagnostic and no C at all, so an example past the seed's line still proves the seed
// stops cleanly instead of crashing or emitting C that cc would reject. And the marker
// cannot rot in either direction — an unmarked example the seed refuses fails the test
// below, and a marked example the seed happily compiles fails checkSeedRefuses.
const beyondSeedMarker = "# beyond-the-seed:"

// beyondSeed reports whether an example declares itself past the seed's line.
func beyondSeed(src string) bool { return strings.Contains(src, beyondSeedMarker) }

// seedDiagPrefix is what every refusal the seed writes begins with. It is the difference
// between "the seed stopped on purpose" and "the seed fell over": a parse error, a type
// error, or an internal failure says something else, and would fail this check.
const seedDiagPrefix = "the bootstrap seed"

// checkSeedRefuses asserts that the seed's answer to an example past its line is the
// clean, named refusal — at least one diagnostic, EVERY diagnostic one the seed writes
// about itself, and no C emitted. It returns the diagnostics so a caller can compare two
// entry points' answers.
func checkSeedRefuses(t *testing.T, name, src string) []diag.Diagnostic {
	t.Helper()
	code, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatalf("%s carries %q but the seed compiled it; drop the marker", name, beyondSeedMarker)
	}
	for _, d := range diags {
		if !strings.Contains(d.Msg, seedDiagPrefix) {
			t.Fatalf("%s is past the seed's line, so every diagnostic must be a seed refusal; got %v", name, d)
		}
	}
	if code != "" {
		t.Fatalf("%s was refused but %d bytes of C came back; a refusal must emit none", name, len(code))
	}
	return diags
}

// repoRoot walks up from the working directory to the module/workspace root,
// identified by the go.work file.
func repoRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
