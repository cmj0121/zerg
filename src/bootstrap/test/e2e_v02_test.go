// v0.2 parity corpus harness.
//
// For every `<name>.zg` under `test/v0_2/`, this test asserts:
//
//   1. `zerg run <file>` exits 0 and stdout == `<name>.txt` golden.
//   2. `zerg build <file>` exits 0, the produced binary exits 0, and its stdout
//      matches the same golden.
//
// (1) and (2) together imply run-vs-build parity; the harness still asserts it
// directly so a regression points at the parity edge, not at one half of it.
//
// The corpus is the v0.2 ship gate: this file failing is a hard merge block.
//
// Special-case: 22_no_match_panics.zg is excluded from the standard glob loop
// because both halves are expected to exit 1 with a "no arm matched" stderr.
// TestE2EV02NoMatchPanic exercises that program directly.
package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/build"
)

// noMatchPanicProgram is the basename (sans `.zg`) of the corpus program that
// is expected to panic at runtime. Excluded from the standard parity loop.
const noMatchPanicProgram = "22_no_match_panics"

// v02CorpusDir resolves to src/bootstrap/test/v0_2/ from this test file's
// directory.
func v02CorpusDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testDir(t), "v0_2")
}

// TestE2EV02Corpus discovers every .zg under test/v0_2/ (except the panic
// program) and exercises both `zerg run` and `zerg build`-then-exec against
// the sibling .txt golden.
func TestE2EV02Corpus(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v02CorpusDir(t)

	matches, err := filepath.Glob(filepath.Join(corpus, "*.zg"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no .zg files found in %s", corpus)
	}

	// Resolve cc once. If the toolchain isn't installed we skip only the
	// build half — the run half still exercises the parity reference and is
	// worth running on minimal CI images.
	ccAvailable := true
	if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
		ccAvailable = false
	}

	for _, src := range matches {
		src := src
		base := strings.TrimSuffix(filepath.Base(src), ".zg")
		if base == noMatchPanicProgram {
			// Exercised separately by TestE2EV02NoMatchPanic.
			continue
		}
		t.Run(base, func(t *testing.T) {
			goldenPath := filepath.Join(corpus, base+".txt")
			golden, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v", goldenPath, err)
			}

			// 1. zerg run.
			runOut, runCode, err := captureCmd(binPath, []string{"run", src}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg run: %v", err)
			}
			if runCode != 0 {
				t.Fatalf("zerg run exit code = %d, want 0\nstdout: %s", runCode, runOut)
			}
			if !bytes.Equal(runOut, golden) {
				t.Errorf("run stdout vs golden mismatch\nrun:    %q\ngolden: %q", runOut, golden)
			}

			if !ccAvailable {
				t.Skip("cc not available; build half skipped")
			}

			// 2. zerg build → exec.
			buildDir := t.TempDir()
			_, buildCode, err := captureCmd(binPath, []string{"build", src}, buildDir)
			if err != nil {
				t.Fatalf("zerg build: %v", err)
			}
			if buildCode != 0 {
				t.Fatalf("zerg build exit code = %d, want 0", buildCode)
			}
			outBin := filepath.Join(buildDir, base)
			if _, err := os.Stat(outBin); err != nil {
				t.Fatalf("expected binary at %s: %v", outBin, err)
			}
			binOut, binCode, err := captureCmd(outBin, nil, buildDir)
			if err != nil {
				t.Fatalf("execute %s: %v", outBin, err)
			}
			if binCode != 0 {
				t.Fatalf("compiled binary exit code = %d, want 0", binCode)
			}

			// 3. Parity assertions. Both halves vs golden separately, plus
			// run-vs-build directly so a regression is unambiguous about
			// which leg drifted.
			if !bytes.Equal(binOut, golden) {
				t.Errorf("build stdout vs golden mismatch\nbuild:  %q\ngolden: %q", binOut, golden)
			}
			if !bytes.Equal(runOut, binOut) {
				t.Errorf("run vs build stdout mismatch\nrun:   %q\nbuild: %q", runOut, binOut)
			}
		})
	}
}

// TestE2EV02NoMatchPanic verifies the panic program (22_no_match_panics.zg)
// matches PLAN's "no arm matched" contract on both halves: exit code 1 plus
// a stderr that mentions the diagnostic. The interpreter wraps the message
// in zerolog format and the codegen runtime prints the bare line, so the
// assertion is on a substring shared by both — `match: no arm matched at`.
func TestE2EV02NoMatchPanic(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v02CorpusDir(t)
	src := filepath.Join(corpus, noMatchPanicProgram+".zg")
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("missing panic program %s: %v", src, err)
	}

	const wantSubstr = "match: no arm matched at"

	t.Run("run half", func(t *testing.T) {
		_, stderr, code, err := captureCmdBoth(binPath, []string{"run", src}, t.TempDir())
		if err != nil {
			t.Fatalf("zerg run: %v", err)
		}
		if code != 1 {
			t.Fatalf("zerg run exit code = %d, want 1\nstderr: %s", code, stderr)
		}
		if !strings.Contains(string(stderr), wantSubstr) {
			t.Fatalf("zerg run stderr missing %q\nstderr: %s", wantSubstr, stderr)
		}
	})

	t.Run("build half", func(t *testing.T) {
		if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
			t.Skip("cc not available; build half skipped")
		}
		buildDir := t.TempDir()
		_, _, buildCode, err := captureCmdBoth(binPath, []string{"build", src}, buildDir)
		if err != nil {
			t.Fatalf("zerg build: %v", err)
		}
		if buildCode != 0 {
			t.Fatalf("zerg build exit code = %d, want 0", buildCode)
		}
		outBin := filepath.Join(buildDir, noMatchPanicProgram)
		if _, err := os.Stat(outBin); err != nil {
			t.Fatalf("expected binary at %s: %v", outBin, err)
		}
		_, stderr, runCode, err := captureCmdBoth(outBin, nil, buildDir)
		if err != nil {
			t.Fatalf("execute %s: %v", outBin, err)
		}
		if runCode != 1 {
			t.Fatalf("compiled binary exit code = %d, want 1\nstderr: %s", runCode, stderr)
		}
		if !strings.Contains(string(stderr), wantSubstr) {
			t.Fatalf("compiled binary stderr missing %q\nstderr: %s", wantSubstr, stderr)
		}
	})
}
