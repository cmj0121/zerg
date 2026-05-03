// v0.1 parity corpus harness.
//
// For every `<name>.zg` under `test/v0_1/`, this test asserts:
//
//   1. `zerg run <file>` exits 0 and stdout == `<name>.txt` golden.
//   2. `zerg build <file>` exits 0, the produced binary exits 0, and its stdout
//      matches the same golden.
//
// (1) and (2) together imply run-vs-build parity; the harness still asserts it
// directly so a regression points at the parity edge, not at one half of it.
//
// The corpus is the v0.1 ship gate: this file failing is a hard merge block.
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

// v01CorpusDir resolves to src/bootstrap/test/v0_1/ from this test file's
// directory.
func v01CorpusDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testDir(t), "v0_1")
}

// TestE2EV01Corpus discovers every .zg under test/v0_1/ and exercises both
// `zerg run` and `zerg build`-then-exec against the sibling .txt golden.
//
// We discover the corpus with a glob rather than enumerating cases by name so
// adding a program to the corpus only requires dropping in the .zg + .txt
// pair. CI catches a missing golden as a hard failure rather than silently
// skipping the case.
func TestE2EV01Corpus(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v01CorpusDir(t)

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
