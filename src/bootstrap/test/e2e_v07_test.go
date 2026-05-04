// v0.7 parity corpus harness — concurrency edition.
//
// Mirrors `e2e_v06_test.go` exactly for the deterministic and reject
// halves; the only difference is paths (v0_6 → v0_7). The third routine
// `TestE2EV07Scheduling` walks `test/v0_7/scheduling/` and validates
// programs against an `invariants.txt` instead of `expected.txt`.
//
// Layout:
//
//   test/v0_7/<NN_name>/
//       main.zg           — entry file
//       <sibling>.zg ...  — imported modules (zero or more)
//       expected.txt      — golden stdout for both interpret and build
//
//   test/v0_7/scheduling/<NN_name>/
//       main.zg           — entry file
//       invariants.txt    — invariant-rule set (see Invariant grammar)
//
//   test/v0_7/rejects/<NN_name>/
//       main.zg           — entry file
//       error.txt         — diagnostic substring expected on stderr
//
// Invariant grammar (one rule per line, blank/`#` lines ignored):
//
//   len: N                — stdout has exactly N non-empty lines
//   contains: <substring> — stdout contains the substring verbatim
//   set: a,b,c            — distinct stdout lines == {a,b,c} (order-free)
//   sorted_set: a,b,c     — sorted stdout lines == [a,b,c]
//
// The reject harness asserts non-zero exit (not strictly 1) so that
// runtime panics (e.g. "send on closed channel") which abort with signal
// 6 / exit 134 are still acceptable. The substring match against stderr
// is the load-bearing assertion.
package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/build"
)

// v07CorpusDir resolves to src/bootstrap/test/v0_7/.
func v07CorpusDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testDir(t), "v0_7")
}

// v07RejectsDir resolves to src/bootstrap/test/v0_7/rejects/.
func v07RejectsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(v07CorpusDir(t), "rejects")
}

// v07SchedulingDir resolves to src/bootstrap/test/v0_7/scheduling/.
func v07SchedulingDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(v07CorpusDir(t), "scheduling")
}

// v07BuildHalfSkip lists program names whose build half is known-broken
// at v0.7. Empty by design.
var v07BuildHalfSkip = map[string]string{}

// listV07Programs returns absolute paths to every deterministic program
// directory under v0_7/, excluding the rejects/ and scheduling/
// subdirectories. Each must contain main.zg + expected.txt.
func listV07Programs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read corpus root %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("unexpected non-directory entry in %s: %q (every v0.7 program is a directory)", root, e.Name())
		}
		if e.Name() == "rejects" || e.Name() == "scheduling" {
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

// listV07Rejects mirrors listV07Programs for the rejects sub-corpus.
func listV07Rejects(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read rejects root %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("unexpected non-directory entry in %s: %q", root, e.Name())
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

// listV07Scheduling lists the scheduling-corpus directories. Each must
// contain main.zg + invariants.txt.
func listV07Scheduling(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read scheduling root %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("unexpected non-directory entry in %s: %q", root, e.Name())
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "main.zg")); err != nil {
			t.Fatalf("scheduling program %s missing main.zg: %v", dir, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "invariants.txt")); err != nil {
			t.Fatalf("scheduling program %s missing invariants.txt: %v", dir, err)
		}
		out = append(out, dir)
	}
	if len(out) == 0 {
		t.Fatalf("no scheduling directories found under %s", root)
	}
	return out
}

// TestE2EV07Corpus runs every deterministic v0.7 program through both
// `zerg run` and `zerg build`-then-exec and checks parity against the
// directory's expected.txt golden.
func TestE2EV07Corpus(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v07CorpusDir(t)
	programs := listV07Programs(t, corpus)

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
			if reason, skip := v07BuildHalfSkip[name]; skip {
				t.Logf("build half skipped: %s", reason)
				return
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

			if !bytes.Equal(binOut, golden) {
				t.Errorf("build stdout vs golden mismatch\nbuild:  %q\ngolden: %q", binOut, golden)
			}
			if !bytes.Equal(runOut, binOut) {
				t.Errorf("run vs build stdout mismatch\nrun:   %q\nbuild: %q", runOut, binOut)
			}
		})
	}
}

// TestE2EV07Rejects runs every reject program through both halves and
// asserts non-zero exit + stderr substring match. Non-zero (not strictly
// 1) so runtime panics that abort with signal 6 are admitted alongside
// parser/typeck rejections.
func TestE2EV07Rejects(t *testing.T) {
	binPath := buildToolchain(t)
	dir := v07RejectsDir(t)
	rejects := listV07Rejects(t, dir)

	ccAvailable := true
	if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
		ccAvailable = false
	}

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
			want := strings.TrimRight(string(wantBytes), "\n")
			if want == "" {
				t.Fatalf("error.txt %s is empty", errPath)
			}

			// 1. zerg run half.
			_, stderr, code, err := captureCmdBoth(binPath, []string{"run", entry}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg run: %v", err)
			}
			if code == 0 {
				t.Fatalf("zerg run exit code = 0, want non-zero\nstderr: %s", stderr)
			}
			if !strings.Contains(string(stderr), want) {
				t.Fatalf("zerg run stderr missing substring %q\nstderr: %s", want, stderr)
			}

			// 2. zerg build half. For runtime-panic rejects the panic
			// fires at exec time, so we may need to execute the produced
			// binary and observe its stderr.
			if !ccAvailable {
				t.Skip("cc not available; build half skipped")
			}
			buildDir := t.TempDir()
			_, buildStderr, buildCode, err := captureCmdBoth(binPath, []string{"build", entry}, buildDir)
			if err != nil {
				t.Fatalf("zerg build: %v", err)
			}
			if buildCode != 0 {
				// Build itself rejected — substring must match stderr.
				if !strings.Contains(string(buildStderr), want) {
					t.Fatalf("zerg build stderr missing substring %q\nstderr: %s", want, buildStderr)
				}
				return
			}
			// Build succeeded — the program is a runtime-panic reject.
			outBin := filepath.Join(buildDir, "main")
			if _, err := os.Stat(outBin); err != nil {
				t.Fatalf("expected binary at %s: %v", outBin, err)
			}
			_, execStderr, execCode, err := captureCmdBoth(outBin, nil, buildDir)
			if err != nil {
				t.Fatalf("execute %s: %v", outBin, err)
			}
			if execCode == 0 {
				t.Fatalf("compiled binary exit code = 0, want non-zero\nstderr: %s", execStderr)
			}
			if !strings.Contains(string(execStderr), want) {
				t.Fatalf("binary stderr missing substring %q\nstderr: %s", want, execStderr)
			}
		})
	}
}

// invariantRule is one parsed line from invariants.txt.
type invariantRule struct {
	kind string // "len" | "contains" | "set" | "sorted_set"
	arg  string // raw RHS, parsed per-kind at apply time
}

// parseInvariants reads invariants.txt and returns the rule set.
// Comments (`#`-prefix) and blank lines are skipped.
func parseInvariants(t *testing.T, path string) []invariantRule {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read invariants %s: %v", path, err)
	}
	var rules []invariantRule
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			t.Fatalf("%s:%d: missing ':' in rule %q", path, i+1, line)
		}
		kind := strings.TrimSpace(line[:colon])
		arg := strings.TrimSpace(line[colon+1:])
		switch kind {
		case "len", "contains", "set", "sorted_set":
			rules = append(rules, invariantRule{kind: kind, arg: arg})
		default:
			t.Fatalf("%s:%d: unknown rule kind %q", path, i+1, kind)
		}
	}
	if len(rules) == 0 {
		t.Fatalf("%s: no rules found", path)
	}
	return rules
}

// applyInvariants reports any rule violations against stdout. Returns
// nil if every rule holds.
func applyInvariants(stdout []byte, rules []invariantRule) []string {
	text := strings.TrimRight(string(stdout), "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	var fails []string
	for _, r := range rules {
		switch r.kind {
		case "len":
			var n int
			if _, err := fmtSscan(r.arg, &n); err != nil || n < 0 {
				fails = append(fails, "len: invalid arg "+r.arg)
				continue
			}
			if len(lines) != n {
				fails = append(fails, "len: got "+itoa(len(lines))+" want "+r.arg)
			}
		case "contains":
			if !strings.Contains(string(stdout), r.arg) {
				fails = append(fails, "contains: substring "+strconvQuote(r.arg)+" not in stdout")
			}
		case "set":
			want := splitCSV(r.arg)
			gotSet := dedupeSorted(lines)
			wantSet := dedupeSorted(want)
			if !equalStringSlice(gotSet, wantSet) {
				fails = append(fails, "set: got "+joinCSV(gotSet)+" want "+joinCSV(wantSet))
			}
		case "sorted_set":
			want := splitCSV(r.arg)
			gotSorted := append([]string(nil), lines...)
			sort.Strings(gotSorted)
			if !equalStringSlice(gotSorted, want) {
				fails = append(fails, "sorted_set: got "+joinCSV(gotSorted)+" want "+joinCSV(want))
			}
		}
	}
	return fails
}

// fmtSscan / itoa / strconvQuote / splitCSV / dedupeSorted /
// equalStringSlice / joinCSV are tiny helpers kept inline so the harness
// has zero new package dependencies beyond what the v0.6 harness uses.
func fmtSscan(s string, out *int) (int, error) {
	n := 0
	if s == "" {
		return 0, &parseErr{"empty"}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, &parseErr{"non-digit"}
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return 1, nil
}

type parseErr struct{ msg string }

func (e *parseErr) Error() string { return e.msg }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func strconvQuote(s string) string { return "\"" + s + "\"" }

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	cp := append([]string(nil), in...)
	sort.Strings(cp)
	out := cp[:0]
	for i, s := range cp {
		if i == 0 || s != cp[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func joinCSV(parts []string) string { return "[" + strings.Join(parts, ",") + "]" }

// TestE2EV07Scheduling runs every program under test/v0_7/scheduling/
// through both halves; both must exit 0 and satisfy every invariant
// rule from the directory's invariants.txt.
//
// The harness asserts invariants on each half independently — there is
// no run-vs-build byte equality requirement for scheduling programs,
// since their stdout is intentionally non-deterministic.
func TestE2EV07Scheduling(t *testing.T) {
	binPath := buildToolchain(t)
	root := v07SchedulingDir(t)
	progs := listV07Scheduling(t, root)

	ccAvailable := true
	if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
		ccAvailable = false
	}

	for _, prog := range progs {
		prog := prog
		name := filepath.Base(prog)
		t.Run(name, func(t *testing.T) {
			entry := filepath.Join(prog, "main.zg")
			rules := parseInvariants(t, filepath.Join(prog, "invariants.txt"))

			runOut, runCode, err := captureCmd(binPath, []string{"run", entry}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg run: %v", err)
			}
			if runCode != 0 {
				t.Fatalf("zerg run exit code = %d, want 0\nstdout: %s", runCode, runOut)
			}
			if fails := applyInvariants(runOut, rules); len(fails) > 0 {
				t.Errorf("run-half invariant failures:\n  %s\nstdout:\n%s", strings.Join(fails, "\n  "), runOut)
			}

			if !ccAvailable {
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
			binOut, binCode, err := captureCmd(outBin, nil, buildDir)
			if err != nil {
				t.Fatalf("execute %s: %v", outBin, err)
			}
			if binCode != 0 {
				t.Fatalf("compiled binary exit code = %d, want 0", binCode)
			}
			if fails := applyInvariants(binOut, rules); len(fails) > 0 {
				t.Errorf("build-half invariant failures:\n  %s\nstdout:\n%s", strings.Join(fails, "\n  "), binOut)
			}
		})
	}
}
