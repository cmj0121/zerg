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

// TestRequiresGate verifies that examples carrying a future-version
// `# requires:` marker are rejected with the standard message, and that
// examples without a marker still run cleanly.
func TestRequiresGate(t *testing.T) {
	binPath := buildToolchain(t)
	examples := examplesDir(t)

	t.Run("rejects future version", func(t *testing.T) {
		src := filepath.Join(examples, "10_specs.zg")
		_, stderr, code, err := captureCmdBoth(binPath, []string{"run", src}, t.TempDir())
		if err != nil {
			t.Fatalf("zerg run: %v", err)
		}
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		want := "requires v0.4 (current is v0.2)"
		if !strings.Contains(string(stderr), want) {
			t.Fatalf("stderr does not contain %q\nstderr: %s", want, stderr)
		}
	})

	t.Run("unmarked example still runs", func(t *testing.T) {
		src := filepath.Join(examples, "01_hello.zg")
		_, _, code, err := captureCmdBoth(binPath, []string{"run", src}, t.TempDir())
		if err != nil {
			t.Fatalf("zerg run: %v", err)
		}
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	})

	t.Run("rejects future version on build too", func(t *testing.T) {
		src := filepath.Join(examples, "13_asm.zg")
		_, stderr, code, err := captureCmdBoth(binPath, []string{"build", src}, t.TempDir())
		if err != nil {
			t.Fatalf("zerg build: %v", err)
		}
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		want := "requires v0.10 (current is v0.2)"
		if !strings.Contains(string(stderr), want) {
			t.Fatalf("stderr does not contain %q\nstderr: %s", want, stderr)
		}
	})
}
