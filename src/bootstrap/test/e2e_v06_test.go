// v0.6 parity corpus harness — generics + null-safety edition.
//
// Mirrors `e2e_v05_test.go` exactly; the only differences are paths
// (v0_5 → v0_6) and the corpus-skip lists. Every program is a
// directory:
//
//   test/v0_6/<NN_name>/
//       main.zg           — entry file
//       <sibling>.zg ...  — imported modules (zero or more)
//       expected.txt      — golden stdout for both interpret and build
//
//   test/v0_6/rejects/<NN_name>/
//       main.zg           — entry file
//       <sibling>.zg ...  — imported modules (zero or more)
//       error.txt         — diagnostic substring expected on stderr
//
// `TestE2EV06Corpus` walks every entry under v0_6/ (excluding rejects/)
// and asserts:
//
//   1. `zerg run main.zg`           — exits 0, stdout == expected.txt
//   2. `zerg build main.zg && exec` — exits 0, stdout == expected.txt
//   3. run-half == build-half       — direct parity assertion
//
// `TestE2EV06Rejects` walks every entry under v0_6/rejects/ and asserts
// both halves exit 1 with stderr containing the substring stored in
// error.txt.
//
// The earlier per-version harnesses (v0.0–v0.5) are NOT modified by
// this file — every harness runs in parallel.
//
// Build artifact cleanup is automatic: each `zerg build` runs in its
// own `t.TempDir()` so the produced binary is collected with the temp
// dir at test-end. No artifacts are left in the corpus directories.
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

// v06CorpusDir resolves to src/bootstrap/test/v0_6/ from this test
// file's directory.
func v06CorpusDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testDir(t), "v0_6")
}

// v06RejectsDir resolves to src/bootstrap/test/v0_6/rejects/.
func v06RejectsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(v06CorpusDir(t), "rejects")
}

// v06BuildHalfSkip lists program names whose build half is known-broken
// at v0.6. Empty by design — the v0.6 ship gate disallows allow-list
// entries without explicit ellis-and-page sign-off (mirrors v0.4 / v0.5
// precedent). Kept here so a future codegen escape has a documented
// place to land without reshaping the harness.
var v06BuildHalfSkip = map[string]string{}

// listV06Programs returns the absolute paths to every program directory
// under v0_6/, excluding the rejects/ subdirectory. A program directory
// must contain a `main.zg` and an `expected.txt` — failing either rule
// is a hard test failure (catches forgotten goldens / forgotten entry
// files).
func listV06Programs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read corpus root %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			// Top-level files are not part of the v0.6 corpus shape.
			t.Fatalf("unexpected non-directory entry in %s: %q (every v0.6 program is a directory)", root, e.Name())
		}
		if e.Name() == "rejects" {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "main.zg")); err != nil {
			t.Fatalf("program %s missing main.zg: %v", dir, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "expected.txt")); err != nil {
			t.Fatalf("program %s missing expected.txt: %v", dir, err)
		}
		out = append(out, dir)
	}
	if len(out) == 0 {
		t.Fatalf("no program directories found under %s", root)
	}
	return out
}

// listV06Rejects mirrors listV06Programs for the rejects sub-corpus.
// Every reject directory must contain a `main.zg` and an `error.txt`.
func listV06Rejects(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read rejects root %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("unexpected non-directory entry in %s: %q (every reject program is a directory)", root, e.Name())
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "main.zg")); err != nil {
			t.Fatalf("reject %s missing main.zg: %v", dir, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "error.txt")); err != nil {
			t.Fatalf("reject %s missing error.txt: %v", dir, err)
		}
		out = append(out, dir)
	}
	if len(out) == 0 {
		t.Fatalf("no reject directories found under %s", root)
	}
	return out
}

// TestE2EV06Corpus runs every multi-file v0.6 success program through
// both `zerg run` and `zerg build`-then-exec and checks parity against
// the directory's expected.txt golden.
//
// Programs in v06BuildHalfSkip have their build-half assertion skipped
// with an explicit t.Logf — the run half still runs against the golden
// so a known codegen bug doesn't mask interpret-half regressions on the
// same surface area.
func TestE2EV06Corpus(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v06CorpusDir(t)
	programs := listV06Programs(t, corpus)

	// Resolve cc once. If the toolchain isn't installed we skip the
	// build half — the run half still exercises the parity reference
	// and is worth running on minimal CI images. Mirrors v0.4 / v0.5.
	ccAvailable := true
	if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
		ccAvailable = false
	}

	for _, prog := range programs {
		prog := prog
		name := filepath.Base(prog)
		t.Run(name, func(t *testing.T) {
			entry := filepath.Join(prog, "main.zg")
			goldenPath := filepath.Join(prog, "expected.txt")
			golden, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v", goldenPath, err)
			}

			// 1. zerg run main.zg. Run cwd is a fresh temp dir so the
			// run half has no chance of incidentally finding files
			// from the corpus tree — the loader uses the entry file's
			// own directory for sibling resolution, not the cwd.
			runOut, runCode, err := captureCmd(binPath, []string{"run", entry}, t.TempDir())
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
			if reason, skip := v06BuildHalfSkip[name]; skip {
				t.Logf("build half skipped: %s", reason)
				return
			}

			// 2. zerg build main.zg → exec. The build artifact is
			// dropped in the build cwd (a temp dir), so it's cleaned
			// up automatically when the test finishes. The artifact
			// basename derives from the entry file's basename
			// ("main"), per Build's filename rule.
			buildDir := t.TempDir()
			_, buildCode, err := captureCmd(binPath, []string{"build", entry}, buildDir)
			if err != nil {
				t.Fatalf("zerg build: %v", err)
			}
			if buildCode != 0 {
				t.Fatalf("zerg build exit code = %d, want 0", buildCode)
			}
			outBin := filepath.Join(buildDir, "main")
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

			// 3. Parity assertions. Both halves vs golden separately,
			// plus run-vs-build directly so a regression is
			// unambiguous about which leg drifted.
			if !bytes.Equal(binOut, golden) {
				t.Errorf("build stdout vs golden mismatch\nbuild:  %q\ngolden: %q", binOut, golden)
			}
			if !bytes.Equal(runOut, binOut) {
				t.Errorf("run vs build stdout mismatch\nrun:   %q\nbuild: %q", runOut, binOut)
			}
		})
	}
}

// TestE2EV06Rejects runs every reject program through both `zerg run`
// and `zerg build`. Both halves must exit 1 with stderr containing the
// substring stored in `error.txt`.
//
// The build half is NOT skipped on missing cc: every v0.6 reject
// diagnostic fires at parse / module-load / typeck time, all of which
// precede the C compiler invocation. Mirrors the v0.4 / v0.5 rejects
// rationale.
func TestE2EV06Rejects(t *testing.T) {
	binPath := buildToolchain(t)
	dir := v06RejectsDir(t)
	rejects := listV06Rejects(t, dir)

	for _, prog := range rejects {
		prog := prog
		name := filepath.Base(prog)
		t.Run(name, func(t *testing.T) {
			entry := filepath.Join(prog, "main.zg")
			errPath := filepath.Join(prog, "error.txt")
			wantBytes, err := os.ReadFile(errPath)
			if err != nil {
				t.Fatalf("read error.txt %s: %v", errPath, err)
			}
			// error.txt files end with a trailing newline for editor
			// friendliness; the substring we want is everything before
			// that.
			want := strings.TrimRight(string(wantBytes), "\n")
			if want == "" {
				t.Fatalf("error.txt %s is empty", errPath)
			}

			// 1. zerg run half.
			_, stderr, code, err := captureCmdBoth(binPath, []string{"run", entry}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg run: %v", err)
			}
			if code != 1 {
				t.Fatalf("zerg run exit code = %d, want 1\nstderr: %s", code, stderr)
			}
			if !strings.Contains(string(stderr), want) {
				t.Fatalf("zerg run stderr missing substring %q\nstderr: %s", want, stderr)
			}

			// 2. zerg build half.
			_, stderr, code, err = captureCmdBoth(binPath, []string{"build", entry}, t.TempDir())
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
