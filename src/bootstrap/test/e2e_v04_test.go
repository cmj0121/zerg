// v0.4 parity corpus harness.
//
// For every `<name>.zg` under `test/v0_4/` the harness asserts one of three
// contracts based on the sibling file:
//
//  1. `<name>.txt`     — SUCCESS contract. `zerg run` exits 0 and stdout
//     equals the golden; `zerg build` produces a binary that exits 0 with
//     stdout equal to the same golden. Run-vs-build parity is also asserted
//     directly so a regression points at the parity edge, not at one half.
//
//  2. `<name>.notimpl` — RUNTIME-PANIC contract for the v0.4 NotImplemented
//     stub. Both halves must exit non-zero with stderr containing the
//     substring stored in the .notimpl file. Exact position digits diverge
//     between halves (the interpreter reports the call site; the compiled
//     stub embeds the spec-decl site), so substring match is the right
//     parity rule.
//
//  3. `rejects/<name>.zg` paired with `rejects/<name>.error` — TYPECK-REJECT
//     contract. Both halves exit 1 with stderr containing the substring
//     stored in the .error file (mirrors the v0.2 / v0.3 reject corpora).
//
// A .zg in the top-level v0_4/ MUST have exactly one of {.txt, .notimpl}.
// The harness fails fast on a forgotten/duplicated sibling so silent gaps
// can't ship.
//
// Known surfaced bug, NOT a regression: program 25 (`list[int]` as an enum
// payload) hits a codegen forward-declaration ordering issue — the build
// half is allow-listed below with `buildHalfSkip`. The interpret half still
// passes parity vs the golden. Surface and fix tracking happens outside
// Unit 8.
//
// The corpus is the v0.4 ship gate: this file failing is a hard merge block.
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

// v04CorpusDir resolves to src/bootstrap/test/v0_4/ from this test file's
// directory.
func v04CorpusDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testDir(t), "v0_4")
}

// v04RejectsDir resolves to src/bootstrap/test/v0_4/rejects/ — the typeck
// reject sub-corpus, kept beside the success programs so a single tree
// describes the whole v0.4 surface.
func v04RejectsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(v04CorpusDir(t), "rejects")
}

// v04BuildHalfSkip lists programs whose build half is known-broken at v0.4.
// The interpret half still runs and is asserted normally; the build-half
// assertion is skipped with an explicit log so the skip is visible. Empty
// today — kept in place so future codegen escapes have a documented
// allow-list to land in without reshaping the harness.
var v04BuildHalfSkip = map[string]string{}

// classifyV04Programs walks the v0.4 success corpus directory, partitions
// every .zg into success / notimpl, and rejects any .zg without a sibling
// .txt or .notimpl (or with both — the categories are disjoint). Returned
// slices hold the absolute path to each .zg.
//
// The rejects/ subdirectory is handled separately by classifyV04Rejects —
// keeping the two trees distinct mirrors the v0.2/v0.3 reject convention
// and makes the harness wiring self-documenting.
func classifyV04Programs(t *testing.T, dir string) (success, notimpl []string) {
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
		_, niErr := os.Stat(base + ".notimpl")
		hasTxt := txtErr == nil
		hasNi := niErr == nil
		switch {
		case hasTxt && hasNi:
			t.Fatalf("%s.zg has BOTH .txt and .notimpl siblings; categories are disjoint", base)
		case hasTxt:
			success = append(success, src)
		case hasNi:
			notimpl = append(notimpl, src)
		default:
			t.Fatalf("%s.zg has neither .txt (success) nor .notimpl (NotImplemented) sibling", base)
		}
	}
	return success, notimpl
}

// classifyV04Rejects walks rejects/ and pairs each .zg with its .error
// sibling. A missing sibling (in either direction) is a hard failure.
func classifyV04Rejects(t *testing.T, dir string) []string {
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
		if _, err := os.Stat(base + ".error"); err != nil {
			t.Fatalf("%s.zg missing sibling .error: %v", base, err)
		}
	}
	return matches
}

// TestE2EV04Corpus runs every "success" v0.4 program through both `zerg run`
// and `zerg build`-then-exec and checks parity against the .txt golden.
//
// Programs in v04BuildHalfSkip have their build-half assertion skipped with
// an explicit t.Logf — the run half still runs against the golden so the
// known codegen bug doesn't mask interpret-half regressions on the same
// surface area.
func TestE2EV04Corpus(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v04CorpusDir(t)
	success, _ := classifyV04Programs(t, corpus)

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
			if reason, skip := v04BuildHalfSkip[base]; skip {
				t.Logf("build half skipped: %s", reason)
				return
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

// TestE2EV04Rejects runs every "reject" v0.4 program (rejects/) through both
// `zerg run` and `zerg build` and checks each half exits 1 with stderr
// containing the substring stored in the sibling .error file.
//
// The build half is NOT skipped on missing cc: the typeck recursive-enum
// check fires before the C compiler is invoked, so cc availability is
// irrelevant — a rejected program never reaches the C toolchain.
func TestE2EV04Rejects(t *testing.T) {
	binPath := buildToolchain(t)
	dir := v04RejectsDir(t)
	rejects := classifyV04Rejects(t, dir)

	for _, src := range rejects {
		src := src
		base := strings.TrimSuffix(filepath.Base(src), ".zg")
		t.Run(base, func(t *testing.T) {
			errPath := filepath.Join(dir, base+".error")
			wantBytes, err := os.ReadFile(errPath)
			if err != nil {
				t.Fatalf("read .error file %s: %v", errPath, err)
			}
			// .error files end with a trailing newline for editor friendliness;
			// the substring we want is everything before that.
			want := strings.TrimRight(string(wantBytes), "\n")
			if want == "" {
				t.Fatalf(".error file %s is empty", errPath)
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

			// 2. zerg build half.
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

// TestE2EV04NotImplemented exercises the NotImplemented runtime panic stub
// emitted for spec methods that lack both an override and a default. Both
// halves must exit non-zero with stderr containing the .notimpl substring.
//
// Position digits diverge by design between halves (the interpreter reports
// the offending call site; the compiled stub embeds the spec-decl site at
// codegen time), so the .notimpl file holds the position-free prefix only —
// substring match suffices for parity.
func TestE2EV04NotImplemented(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v04CorpusDir(t)
	_, notimpl := classifyV04Programs(t, corpus)

	// Resolve cc once. Without cc the run half still asserts the panic;
	// the build half is skipped per-case so the skip message is visible.
	ccAvailable := true
	if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
		ccAvailable = false
	}

	for _, src := range notimpl {
		src := src
		base := strings.TrimSuffix(filepath.Base(src), ".zg")
		t.Run(base, func(t *testing.T) {
			niPath := filepath.Join(corpus, base+".notimpl")
			wantBytes, err := os.ReadFile(niPath)
			if err != nil {
				t.Fatalf("read .notimpl file %s: %v", niPath, err)
			}
			want := strings.TrimRight(string(wantBytes), "\n")
			if want == "" {
				t.Fatalf(".notimpl file %s is empty", niPath)
			}

			// 1. zerg run half — interpret panics.
			_, stderr, code, err := captureCmdBoth(binPath, []string{"run", src}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg run: %v", err)
			}
			if code == 0 {
				t.Fatalf("zerg run exit code = 0, want non-zero (NotImplemented panic)\nstderr: %s", stderr)
			}
			if !strings.Contains(string(stderr), want) {
				t.Fatalf("zerg run stderr missing substring %q\nstderr: %s", want, stderr)
			}

			if !ccAvailable {
				t.Skip("cc not available; build half skipped")
			}

			// 2. zerg build half — compiled stub panics at runtime.
			buildDir := t.TempDir()
			_, _, buildCode, err := captureCmdBoth(binPath, []string{"build", src}, buildDir)
			if err != nil {
				t.Fatalf("zerg build: %v", err)
			}
			if buildCode != 0 {
				t.Fatalf("zerg build exit code = %d, want 0 (the panic is at runtime, not at build time)", buildCode)
			}
			outBin := filepath.Join(buildDir, base)
			if _, err := os.Stat(outBin); err != nil {
				t.Fatalf("expected binary at %s: %v", outBin, err)
			}
			_, binStderr, binCode, err := captureCmdBoth(outBin, nil, buildDir)
			if err != nil {
				t.Fatalf("execute %s: %v", outBin, err)
			}
			if binCode == 0 {
				t.Fatalf("compiled binary exit code = 0, want non-zero (NotImplemented panic)\nstderr: %s", binStderr)
			}
			if !strings.Contains(string(binStderr), want) {
				t.Fatalf("compiled binary stderr missing substring %q\nstderr: %s", want, binStderr)
			}
		})
	}
}
