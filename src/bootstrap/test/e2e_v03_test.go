// v0.3 parity corpus harness.
//
// For every `<name>.zg` under `test/v0_3/` the harness asserts one of two
// contracts based on the sibling file:
//
//   1. `<name>.txt`  — SUCCESS contract. `zerg run` exits 0 and stdout equals
//      the golden; `zerg build` produces a binary that exits 0 with stdout
//      equal to the same golden. Run-vs-build parity is also asserted
//      directly so a regression points at the parity edge, not at one half.
//
//   2. `<name>.err`  — REJECT contract. Both `zerg run` and `zerg build`
//      exit 1 with stderr containing the substring stored in the .err file.
//      The borrow checker (and a couple of typeck rules feeding into it) are
//      the v0.3 ship gate; programs that aliasing-violate must reject on
//      both halves with the same diagnostic.
//
// A .zg file MUST have exactly one of {.txt, .err}. The harness fails fast
// otherwise so a forgotten golden / .err is loud rather than silent.
//
// The corpus is the v0.3 ship gate: this file failing is a hard merge block.
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

// v03CorpusDir resolves to src/bootstrap/test/v0_3/ from this test file's
// directory.
func v03CorpusDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testDir(t), "v0_3")
}

// classifyV03Programs walks the v0.3 corpus directory, partitions every .zg
// into success / reject, and rejects any .zg without a sibling .txt or .err
// (or with both — the categories are disjoint). Returned slices hold the
// absolute path to each .zg.
func classifyV03Programs(t *testing.T, dir string) (success, reject []string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.zg"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no .zg files found in %s", dir)
	}
	for _, src := range matches {
		base := strings.TrimSuffix(src, ".zg")
		_, txtErr := os.Stat(base + ".txt")
		_, errErr := os.Stat(base + ".err")
		hasTxt := txtErr == nil
		hasErr := errErr == nil
		switch {
		case hasTxt && hasErr:
			t.Fatalf("%s.zg has BOTH .txt and .err siblings; categories are disjoint", base)
		case hasTxt:
			success = append(success, src)
		case hasErr:
			reject = append(reject, src)
		default:
			t.Fatalf("%s.zg has neither .txt (success) nor .err (reject) sibling", base)
		}
	}
	return success, reject
}

// TestE2EV03Corpus runs every "success" v0.3 program through both `zerg run`
// and `zerg build`-then-exec and checks parity against the .txt golden.
func TestE2EV03Corpus(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v03CorpusDir(t)
	success, _ := classifyV03Programs(t, corpus)

	// Resolve cc once. If the toolchain isn't installed we skip the build
	// half — the run half still exercises the parity reference and is
	// worth running on minimal CI images.
	ccAvailable := true
	if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
		ccAvailable = false
	}

	for _, src := range success {
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

// TestE2EV03Rejects runs every "reject" v0.3 program through both `zerg run`
// and `zerg build` and checks each half exits 1 with stderr containing the
// substring stored in the sibling .err file.
//
// The build half is NOT skipped on missing cc: the borrow checker fires
// before the C compiler is invoked, so cc availability is irrelevant here —
// we never reach the C toolchain on a rejected program.
func TestE2EV03Rejects(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v03CorpusDir(t)
	_, reject := classifyV03Programs(t, corpus)

	for _, src := range reject {
		src := src
		base := strings.TrimSuffix(filepath.Base(src), ".zg")
		t.Run(base, func(t *testing.T) {
			errPath := filepath.Join(corpus, base+".err")
			wantBytes, err := os.ReadFile(errPath)
			if err != nil {
				t.Fatalf("read .err file %s: %v", errPath, err)
			}
			// .err files end with a trailing newline for editor friendliness;
			// the substring we want to match is everything before that.
			want := strings.TrimRight(string(wantBytes), "\n")
			if want == "" {
				t.Fatalf(".err file %s is empty", errPath)
			}

			// 1. zerg run half.
			_, stderr, code, err := captureCmdBoth(binPath, []string{"run", src}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg run: %v", err)
			}
			if code != 1 {
				t.Fatalf("zerg run exit code = %d, want 1\nstderr: %s", code, stderr)
			}
			if !strings.Contains(string(stderr), want) {
				t.Fatalf("zerg run stderr missing substring %q\nstderr: %s", want, stderr)
			}

			// 2. zerg build half. cc availability is irrelevant — the borrow
			// checker rejects before we reach the C compiler.
			_, stderr, code, err = captureCmdBoth(binPath, []string{"build", src}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg build: %v", err)
			}
			if code != 1 {
				t.Fatalf("zerg build exit code = %d, want 1\nstderr: %s", code, stderr)
			}
			if !strings.Contains(string(stderr), want) {
				t.Fatalf("zerg build stderr missing substring %q\nstderr: %s", want, stderr)
			}
		})
	}
}
