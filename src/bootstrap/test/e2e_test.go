// Package e2e is the v0.0 phase-ship harness.
//
// At v0.0 these tests exercise trivially identical paths between the interpreter
// and the C codegen. The harness exists to lock in the run/build parity rule so
// that v0.1 nontrivial code is checked from day one.
package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/build"
)

// testDir returns the directory holding this test file. We use it to anchor
// every other path so the tests are insensitive to the cwd `go test` happened
// to run from (`go test ./...` from the module root, or directly from this
// directory — both must work).
func testDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(thisFile)
}

// bootstrapRoot is the parent of testDir — i.e. the Go module root for the
// bootstrap toolchain (src/bootstrap/).
func bootstrapRoot(t *testing.T) string {
	t.Helper()
	return filepath.Clean(filepath.Join(testDir(t), ".."))
}

// examplesDir resolves the repo's examples/ directory from this test file's
// location: src/bootstrap/test/ → ../../../examples/.
func examplesDir(t *testing.T) string {
	t.Helper()
	return filepath.Clean(filepath.Join(testDir(t), "..", "..", "..", "examples"))
}

// privateCorpusDir resolves the repo's ./test-data/ submodule from this
// test file's location: src/bootstrap/test/ → ../../../test-data/. The
// submodule (cmj0121/zerg-testdata) ships the v0_9 and v0_13 corpora and
// the requires_future.zg gate fixture.
//
// Behaviour when the corpus is missing or empty depends on
// ZERG_SKIP_PRIVATE_CORPUS:
//
//   - unset → t.Fatal with an actionable hint. This is the developer-mode
//     default: a forgotten `git submodule update --init` should be loud,
//     not silent-green.
//   - "1"   → t.Skip. CI workflows (and external contributors who cannot
//     clone the private repo) set this so the public test surface still
//     runs cleanly and reports SKIP rather than FAIL.
func privateCorpusDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Clean(filepath.Join(testDir(t), "..", "..", "..", "test-data"))
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		if os.Getenv("ZERG_SKIP_PRIVATE_CORPUS") == "1" {
			t.Skipf("private corpus %q missing; skipping (ZERG_SKIP_PRIVATE_CORPUS=1)", dir)
		}
		t.Fatalf("private corpus %q missing — run `git submodule update --init` "+
			"(or set ZERG_SKIP_PRIVATE_CORPUS=1 to skip)", dir)
	}
	return dir
}

// buildToolchain compiles cmd/zerg into a fresh temp dir and returns the
// absolute path to the resulting binary. We build into a temp dir so the
// harness leaves no artifacts in the source tree.
func buildToolchain(t *testing.T) string {
	t.Helper()
	tmpBin := filepath.Join(t.TempDir(), "zerg")
	cmd := exec.Command("go", "build", "-o", tmpBin, "./cmd/zerg")
	cmd.Dir = bootstrapRoot(t)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return tmpBin
}

func TestE2E(t *testing.T) {
	binPath := buildToolchain(t)
	examples := examplesDir(t)
	goldenRoot := filepath.Join(testDir(t), "golden")

	cases := []struct {
		name string
		base string // example basename minus .zg, also golden file basename
	}{
		{"00_nop", "00_nop"},
		{"01_hello", "01_hello"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srcPath := filepath.Join(examples, tc.base+".zg")
			goldenPath := filepath.Join(goldenRoot, tc.base+".txt")
			golden, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			// 1. zerg run — interpret the source.
			runOut, runCode, err := captureCmd(binPath, []string{"run", srcPath}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg run: %v", err)
			}
			if runCode != 0 {
				t.Fatalf("zerg run exit code = %d, want 0", runCode)
			}

			// 2. zerg build — compile to a native binary in a fresh per-case
			// temp working dir so the output binary doesn't pollute the repo.
			// Skip (don't fail) if cc isn't available; the parity rule still
			// has full meaning when both halves run, and CI without a C
			// toolchain shouldn't be a hard failure here.
			if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
				t.Skip("cc not available")
			}
			buildDir := t.TempDir()
			_, buildCode, err := captureCmd(binPath, []string{"build", srcPath}, buildDir)
			if err != nil {
				t.Fatalf("zerg build: %v", err)
			}
			if buildCode != 0 {
				t.Fatalf("zerg build exit code = %d, want 0", buildCode)
			}
			outBin := filepath.Join(buildDir, tc.base)
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

			// 3. Parity assertions — the entire reason this harness exists.
			if !bytes.Equal(runOut, binOut) {
				t.Errorf("run vs build stdout mismatch\nrun:   %q\nbuild: %q", runOut, binOut)
			}
			if !bytes.Equal(runOut, golden) {
				t.Errorf("run stdout vs golden mismatch\nrun:    %q\ngolden: %q", runOut, golden)
			}
		})
	}
}

// captureCmd runs the given command in dir and returns stdout, exit code,
// and any execution error. Non-zero exit codes are NOT execution errors —
// the caller decides whether they are expected.
func captureCmd(name string, args []string, dir string) ([]byte, int, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return stdout.Bytes(), ee.ExitCode(), nil
		}
		return stdout.Bytes(), -1, err
	}
	return stdout.Bytes(), 0, nil
}

// captureCmdBoth is like captureCmd but also returns stderr — used by the
// version-gating tests, which assert on the rejection message.
func captureCmdBoth(name string, args []string, dir string) (stdout, stderr []byte, code int, err error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	runErr := cmd.Run()
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			return so.Bytes(), se.Bytes(), ee.ExitCode(), nil
		}
		return so.Bytes(), se.Bytes(), -1, runErr
	}
	return so.Bytes(), se.Bytes(), 0, nil
}

// TestExamplesBuild gates every example in examples/ on a successful
// `zerg build` so the documentation cannot drift out of sync with the
// shipped grammar / typeck / codegen surface.
//
// The check stops at "the binary was produced" (the existing TestE2E
// path covers full run/build parity for the small set of examples that
// have golden files); the goal here is breadth, not stdout fidelity.
func TestExamplesBuild(t *testing.T) {
	if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
		t.Skip("cc not available")
	}
	binPath := buildToolchain(t)
	examples := examplesDir(t)

	entries, err := os.ReadDir(examples)
	if err != nil {
		t.Fatalf("read examples dir: %v", err)
	}

	var picked []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".zg") {
			continue
		}
		picked = append(picked, name)
	}
	if len(picked) == 0 {
		t.Fatal("no examples picked up — examplesDir resolution likely broken")
	}

	for _, name := range picked {
		name := name
		t.Run(name, func(t *testing.T) {
			srcPath := filepath.Join(examples, name)
			buildDir := t.TempDir()
			out, code, err := captureCmdBothMerged(binPath, []string{"build", srcPath}, buildDir)
			if err != nil {
				t.Fatalf("zerg build: %v", err)
			}
			if code != 0 {
				t.Fatalf("zerg build %s exited %d, want 0\noutput:\n%s", name, code, out)
			}
			outBin := filepath.Join(buildDir, strings.TrimSuffix(name, ".zg"))
			if _, err := os.Stat(outBin); err != nil {
				t.Fatalf("expected binary at %s: %v", outBin, err)
			}
		})
	}
}

// captureCmdBothMerged runs name in dir and returns stdout+stderr merged
// (stderr first then stdout, separated by a marker) so a build failure
// log is fully visible in the t.Fatalf message. Used by TestExamplesBuild
// where the full diagnostic is what makes a failing example actionable.
func captureCmdBothMerged(name string, args []string, dir string) ([]byte, int, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return combined.Bytes(), ee.ExitCode(), nil
		}
		return combined.Bytes(), -1, err
	}
	return combined.Bytes(), 0, nil
}

// TestRequiresGate verifies that a program carrying a future-version
// `# requires:` marker is rejected with the standard message, and that
// programs without a marker still run cleanly.
//
// The future-version fixture lives in the private ./test-data/ submodule
// rather than examples/ because it is a test artifact, not a user-facing
// sample. Bump the `# requires:` line inside testdata/requires_future.zg
// each time a new minor version lands; update `wantRejection` here to match.
func TestRequiresGate(t *testing.T) {
	binPath := buildToolchain(t)
	gateSrc := filepath.Join(privateCorpusDir(t), "testdata", "requires_future.zg")
	const wantRejection = "requires v0.16 (current is v0.15)"

	t.Run("rejects future version on run", func(t *testing.T) {
		_, stderr, code, err := captureCmdBoth(binPath, []string{"run", gateSrc}, t.TempDir())
		if err != nil {
			t.Fatalf("zerg run: %v", err)
		}
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(string(stderr), wantRejection) {
			t.Fatalf("stderr does not contain %q\nstderr: %s", wantRejection, stderr)
		}
	})

	t.Run("rejects future version on build", func(t *testing.T) {
		_, stderr, code, err := captureCmdBoth(binPath, []string{"build", gateSrc}, t.TempDir())
		if err != nil {
			t.Fatalf("zerg build: %v", err)
		}
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(string(stderr), wantRejection) {
			t.Fatalf("stderr does not contain %q\nstderr: %s", wantRejection, stderr)
		}
	})

	t.Run("unmarked example still runs", func(t *testing.T) {
		src := filepath.Join(examplesDir(t), "01_hello.zg")
		_, _, code, err := captureCmdBoth(binPath, []string{"run", src}, t.TempDir())
		if err != nil {
			t.Fatalf("zerg run: %v", err)
		}
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	})
}
