// v0.13 corpus harness — platform-suffix file resolution at U1.
//
// The U1 corpus is intentionally small: one positive program that exercises
// the host-suffix sibling lookup, and two rejects (ambiguity at sibling
// resolution; wrong-platform at the entry file). Later units (U2–U5) will
// add `asm`-bearing programs once the parser and cgen exist; this harness
// stays generic so future tests slot in without churn.
//
// Layout (matches v0.5):
//
//	test/v0_13/<NN_name>/
//	    main.zg           — entry file
//	    <sibling>.zg ...  — imported modules (zero or more)
//	    expected.txt      — golden stdout for both interpret and build
//
//	test/v0_13/rejects/<NN_name>/
//	    <entry>.zg        — entry file (usually main.zg; reject 07's entry
//	                        is intentionally main_linux.zg to trigger the
//	                        wrong-platform diagnostic)
//	    <sibling>.zg ...  — imported modules (zero or more)
//	    error.txt         — diagnostic substring expected on stderr
//
// The whole file skips on non-darwin hosts. v0.13 is macOS-only by design;
// running these tests under linux would either trip the host-suffix gate
// (helper.zg vs helper_linux.zg flips) or pass uniformly (the rejects'
// diagnostics include host-platform strings that change). Re-enabling on
// linux is a v0.14 task.
package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/build"
)

// v13CCAvailable reports whether the host has the expected C compiler. The
// per-version harnesses each inline this check (every prior corpus does the
// same against build.DefaultCC) — kept local so v0.13 cases stay aligned
// with the rest of the harness fleet.
func v13CCAvailable() bool {
	_, err := exec.LookPath(build.DefaultCC())
	return err == nil
}

// v13CorpusDir resolves to src/bootstrap/test/v0_13/.
func v13CorpusDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testDir(t), "v0_13")
}

// v13RejectsDir resolves to src/bootstrap/test/v0_13/rejects/.
func v13RejectsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(v13CorpusDir(t), "rejects")
}

// listV13Programs returns absolute paths to every program directory under
// v0_13/, excluding rejects/. Each program must contain main.zg and at
// least one golden file — either expected.txt (byte-exact stdout match)
// or expected_sorted.txt (sort stdout lines before comparing, so spawn-
// interleaved programs can pin their output multiset without imposing a
// specific schedule). U5's spawn × asm corpus uses expected_sorted.txt.
func listV13Programs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read corpus root %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("unexpected non-directory entry in %s: %q", root, e.Name())
		}
		if e.Name() == "rejects" {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "main.zg")); err != nil {
			t.Fatalf("program %s missing main.zg: %v", dir, err)
		}
		_, exactErr := os.Stat(filepath.Join(dir, "expected.txt"))
		_, sortedErr := os.Stat(filepath.Join(dir, "expected_sorted.txt"))
		if exactErr != nil && sortedErr != nil {
			t.Fatalf("program %s missing expected.txt or expected_sorted.txt: %v / %v",
				dir, exactErr, sortedErr)
		}
		if exactErr == nil && sortedErr == nil {
			t.Fatalf("program %s has both expected.txt and expected_sorted.txt; pick one", dir)
		}
		out = append(out, dir)
	}
	if len(out) == 0 {
		t.Fatalf("no program directories found under %s", root)
	}
	return out
}

// listV13Rejects returns absolute paths to every reject directory under
// v0_13/rejects/. Each must contain error.txt plus exactly one of: main.zg,
// main_macos.zg, or main_linux.zg (the entry). Reject 07's entry is
// main_linux.zg by design; that's the file whose host-suffix mismatch the
// loader must surface, so the harness has to be flexible about the entry
// filename.
func listV13Rejects(t *testing.T, root string) []rejectCase {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read rejects root %s: %v", root, err)
	}
	var out []rejectCase
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("unexpected non-directory entry in %s: %q", root, e.Name())
		}
		dir := filepath.Join(root, e.Name())
		entry := v13RejectEntry(t, dir)
		if _, err := os.Stat(filepath.Join(dir, "error.txt")); err != nil {
			t.Fatalf("reject %s missing error.txt: %v", dir, err)
		}
		out = append(out, rejectCase{dir: dir, entry: entry})
	}
	if len(out) == 0 {
		t.Fatalf("no reject directories found under %s", root)
	}
	return out
}

// rejectCase carries a v0.13 reject directory and the chosen entry filename.
type rejectCase struct {
	dir   string
	entry string // absolute path to the entry file (main.zg or main_<platform>.zg)
}

// v13RejectEntry picks the entry file for a v0.13 reject. Preference order
// is main.zg, main_macos.zg, main_linux.zg — only one should exist; a
// future contributor adding multiple would surface here as an explicit
// failure rather than silently picking the "first" alphabetic candidate.
func v13RejectEntry(t *testing.T, dir string) string {
	t.Helper()
	candidates := []string{"main.zg", "main_macos.zg", "main_linux.zg"}
	var found []string
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(dir, c)); err == nil {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		t.Fatalf("reject %s has no entry file (looked for %v)", dir, candidates)
	case 1:
		return filepath.Join(dir, found[0])
	default:
		t.Fatalf("reject %s has multiple entry candidates %v; pick one", dir, found)
	}
	return "" // unreachable
}

// TestE2EV13Corpus runs every v0.13 corpus program through `zerg run` and
// `zerg build`, comparing both halves to expected.txt.
func TestE2EV13Corpus(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("v0.13 corpus is macOS-only (Linux corpus deferred to v0.14+)")
	}
	binPath := buildToolchain(t)
	programs := listV13Programs(t, v13CorpusDir(t))

	for _, prog := range programs {
		prog := prog
		name := filepath.Base(prog)
		t.Run(name, func(t *testing.T) {
			entry := filepath.Join(prog, "main.zg")
			golden, sortedCompare, err := readV13Golden(prog)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			// `zerg run` half. v0.13 admits `asm { … }` blocks that the
			// interpreter cannot execute (pin 6); programs that use asm
			// are build-only. Detect by scanning the source — the
			// alternative (a per-program manifest flag) adds a moving
			// part without buying clarity.
			srcBytes, err := os.ReadFile(entry)
			if err != nil {
				t.Fatalf("read entry %s: %v", entry, err)
			}
			// asm-bearing programs are build-only because the interpreter
			// rejects every asm body (v0.13 pin 6). The check walks the
			// entry source directly AND treats imports of known
			// asm-bearing stdlib modules (currently sys/syscall) as
			// transitive build-only triggers — a fixture that calls into
			// sys.syscall.write inherits the same "no zerg run" rule
			// even though its own entry has no `asm {` keyword.
			asmBearing := bytes.Contains(srcBytes, []byte("asm {")) ||
				bytes.Contains(srcBytes, []byte("asm{")) ||
				bytes.Contains(srcBytes, []byte(`"sys/syscall"`))
			var runOut []byte
			if asmBearing {
				// `zerg run` cannot execute the asm body (pin 6). For
				// programs whose asm sits at top-level the rejection
				// fires immediately; for programs whose asm sits inside
				// a spawn body, the interpreter may finish main without
				// ever stepping into the spawn, so the exit code is 0
				// with no diagnostic. Both shapes are acceptable here —
				// the build half is where these programs actually run.
				// TestRunRejectsAsmBlock (internal/run/run_v13_asm_test.go)
				// pins pin 6's wording end-to-end against the live
				// interpreter so the run-half doesn't have to.
			} else {
				runCode := 0
				runOut, runCode, err = captureCmd(binPath, []string{"run", entry}, t.TempDir())
				if err != nil {
					t.Fatalf("zerg run: %v", err)
				}
				if runCode != 0 {
					t.Fatalf("zerg run exit code = %d, want 0\nstdout: %s", runCode, runOut)
				}
				if !v13StdoutEqual(runOut, golden, sortedCompare) {
					t.Errorf("run stdout vs golden mismatch\nrun:    %q\ngolden: %q", runOut, golden)
				}
			}

			// `zerg build` half. Skipped if cc is missing on the host —
			// matches every other E2E harness's policy. v0.13 is macOS-only
			// so `cc` is essentially always available on the gating CI;
			// the skip keeps developer machines without a toolchain happy.
			if !v13CCAvailable() {
				t.Skip("cc not available; build half skipped")
			}
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
			if !v13StdoutEqual(binOut, golden, sortedCompare) {
				t.Errorf("build stdout vs golden mismatch\nbuild:  %q\ngolden: %q", binOut, golden)
			}
			// run-vs-build parity is only meaningful for programs the
			// interpreter actually runs. For asm-bearing programs the
			// build output IS the golden surface.
			if !asmBearing && !v13StdoutEqual(runOut, binOut, sortedCompare) {
				t.Errorf("run vs build stdout mismatch\nrun:   %q\nbuild: %q", runOut, binOut)
			}
		})
	}
}

// readV13Golden picks the golden file shape for a corpus program. A
// directory carrying expected.txt uses byte-exact compare; expected_sorted.txt
// switches to "sort stdout lines, then compare to (already-sorted) golden",
// which is how U5's spawn × asm corpus pins the output multiset without
// asserting a particular schedule. Exactly one of the two files must
// exist (enforced in listV13Programs).
func readV13Golden(prog string) ([]byte, bool, error) {
	exact := filepath.Join(prog, "expected.txt")
	sorted := filepath.Join(prog, "expected_sorted.txt")
	if b, err := os.ReadFile(exact); err == nil {
		return b, false, nil
	}
	b, err := os.ReadFile(sorted)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// v13StdoutEqual compares the program's stdout against the golden. When
// sortedCompare is true, both sides are split on '\n' and the lines are
// sorted before comparing — the golden file is expected to be authored
// in already-sorted form. Trailing empty lines (from a final '\n') are
// preserved so a program that prints exactly N lines must produce N
// goldens (no off-by-one slip).
func v13StdoutEqual(got, want []byte, sortedCompare bool) bool {
	if !sortedCompare {
		return bytes.Equal(got, want)
	}
	return bytes.Equal(sortLines(got), sortLines(want))
}

// sortLines splits buf on '\n', sorts the lines (stable), and reassembles
// with '\n' separators. A trailing '\n' in the input survives — split-join
// preserves the empty tail element.
func sortLines(buf []byte) []byte {
	lines := strings.Split(string(buf), "\n")
	sort.Strings(lines)
	return []byte(strings.Join(lines, "\n"))
}

// TestE2EV13Rejects runs every reject's entry file through `zerg run` and
// asserts non-zero exit + stderr substring match. The build half is
// asserted only when applicable — reject 06's collision fails inside the
// loader, which both halves invoke; reject 07's wrong-platform fires at
// loadEntry, also before either half diverges. Both halves get the same
// diagnostic in both flows.
func TestE2EV13Rejects(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("v0.13 corpus is macOS-only (Linux corpus deferred to v0.14+)")
	}
	binPath := buildToolchain(t)
	rejects := listV13Rejects(t, v13RejectsDir(t))

	for _, rc := range rejects {
		rc := rc
		name := filepath.Base(rc.dir)
		t.Run(name, func(t *testing.T) {
			wantBytes, err := os.ReadFile(filepath.Join(rc.dir, "error.txt"))
			if err != nil {
				t.Fatalf("read error.txt: %v", err)
			}
			want := strings.TrimRight(string(wantBytes), "\n")
			if want == "" {
				t.Fatalf("error.txt is empty")
			}

			_, stderr, code, err := captureCmdBoth(binPath, []string{"run", rc.entry}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg run: %v", err)
			}
			if code == 0 {
				t.Fatalf("zerg run exit code = 0, want non-zero\nstderr: %s", stderr)
			}
			if !strings.Contains(string(stderr), want) {
				t.Fatalf("zerg run stderr missing substring %q\nstderr: %s", want, stderr)
			}

			if !v13CCAvailable() {
				t.Skip("cc not available; build half skipped")
			}
			_, buildStderr, buildCode, err := captureCmdBoth(binPath, []string{"build", rc.entry}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg build: %v", err)
			}
			if buildCode == 0 {
				t.Fatalf("zerg build exit code = 0, want non-zero\nstderr: %s", buildStderr)
			}
			if !strings.Contains(string(buildStderr), want) {
				t.Fatalf("zerg build stderr missing substring %q\nstderr: %s", want, buildStderr)
			}
		})
	}
}
