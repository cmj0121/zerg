package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/build"
	runtime "github.com/cmj0121/zerg/src/runtime"
)

// buildTests compiles a `#[test]` program at entryPath into a runnable test binary,
// linking the runtime and the zrt_test.c harness (Phase 1i U2). It is the e2e harness
// for the runner: `zerg test` does exactly this before executing the binary.
func buildTests(t *testing.T, entryPath string) string {
	t.Helper()
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	code, manifest, diags := build.CompileTests(entryPath)
	if len(diags) != 0 {
		t.Fatalf("compile diagnostics: %v", diags)
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("a test binary must report NeedsRuntime")
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	cfiles = append(cfiles, runtime.TestCUnits(dir)...)
	if manifest.Concurrency {
		cfiles = append(cfiles, runtime.ConcurrencyCUnits(dir, runtime.HostArch())...)
	}
	cpath := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	bin := filepath.Join(dir, "prog.bin")
	args := append([]string{"-std=c11", "-I", dir, "-o", bin, cpath}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- generated C ---\n%s", err, out, code)
	}
	return bin
}

// entryAt writes src to name under a fresh temp dir and returns both the dir and the
// entry file path, so a cross-module test can add sibling module directories.
func entryAt(t *testing.T, name, src string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return dir, path
}

// TestZergTestPassAndFailReport is the runner's end-to-end demo: three tests — two
// passing (a `nil` and a `Result[nil]` test) and one failing via testing.assert_eq —
// produce the ok/FAILED report, the failure message, the summary, and exit code 1.
func TestZergTestPassAndFailReport(t *testing.T) {
	src := "import \"testing\"\n\n" +
		"#[test]\nfn test_add() {\n  testing.assert_eq(1 + 1, 2)\n}\n\n" +
		"#[test]\nfn test_bad() {\n  testing.assert_eq(2 + 2, 5)\n}\n\n" +
		"#[test]\nfn test_bool() -> Result[nil] {\n  testing.assert(true)\n  return nil\n}\n"
	_, entry := entryAt(t, "in.zg", src)
	bin := buildTests(t, entry)

	stdout, stderr, err := run(t, bin)
	if err == nil {
		t.Fatalf("a failing test suite must exit non-zero; stdout=%q stderr=%q", stdout, stderr)
	}
	var exit *exec.ExitError
	if !asExit(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("exit code = %v, want 1", err)
	}
	want := "test test_add ... ok\n" +
		"test test_bad ... FAILED\n" +
		"    assert_eq failed: values are not equal\n" +
		"test test_bool ... ok\n" +
		"\ntest result: 3 tests; 2 passed, 1 failed\n"
	if stdout != want {
		t.Fatalf("report =\n%q\nwant\n%q", stdout, want)
	}
}

// TestZergTestAllPass checks a suite with every test passing exits 0 with a clean
// summary.
func TestZergTestAllPass(t *testing.T) {
	src := "import \"testing\"\n\n#[test]\nfn test_ok() {\n  testing.assert_eq(3, 3)\n}\n"
	_, entry := entryAt(t, "in.zg", src)
	bin := buildTests(t, entry)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("an all-pass suite must exit 0: %v\n%s", err, stderr)
	}
	want := "test test_ok ... ok\n\ntest result: 1 tests; 1 passed, 0 failed\n"
	if stdout != want {
		t.Fatalf("report =\n%q\nwant\n%q", stdout, want)
	}
}

// TestZergTestNoTests checks a program with no `#[test]` runs an empty suite and
// exits 0 (not an error).
func TestZergTestNoTests(t *testing.T) {
	_, entry := entryAt(t, "in.zg", "fn main() {\n  print 1\n}\n")
	bin := buildTests(t, entry)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("a no-test suite must exit 0: %v\n%s", err, stderr)
	}
	want := "\ntest result: 0 tests; 0 passed, 0 failed\n"
	if stdout != want {
		t.Fatalf("report = %q, want %q", stdout, want)
	}
}

// TestZergTestCrossModule checks a `#[test]` inside an imported module is discovered
// and run, and its label carries the `module::surface` prefix (Phase 1i U1). The
// entry's own test runs first, then the imported module's, in deterministic order.
func TestZergTestCrossModule(t *testing.T) {
	dir, entry := entryAt(t, "main.zg",
		"import \"testing\"\nimport \"geom\"\n\n"+
			"#[test]\nfn test_local() {\n  testing.assert_eq(geom.area(2, 5), 10)\n}\n\n"+
			"fn main() {\n  print geom.area(1, 1)\n}\n")
	if err := os.Mkdir(filepath.Join(dir, "geom"), 0o755); err != nil {
		t.Fatalf("mkdir geom: %v", err)
	}
	geom := "import \"testing\"\n\npub fn area(w: int, h: int) -> int {\n  return w * h\n}\n\n" +
		"#[test]\nfn test_area() {\n  testing.assert_eq(area(3, 4), 12)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "geom", "geom.zg"), []byte(geom), 0o644); err != nil {
		t.Fatalf("write geom.zg: %v", err)
	}

	bin := buildTests(t, entry)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("cross-module suite must pass: %v\n%s", err, stderr)
	}
	want := "test test_local ... ok\n" +
		"test geom::test_area ... ok\n" +
		"\ntest result: 2 tests; 2 passed, 0 failed\n"
	if stdout != want {
		t.Fatalf("report =\n%q\nwant\n%q", stdout, want)
	}
}

// TestZergTestReportInterleavesWithIO is the M1 fix: the report is unbuffered and
// incremental (the label prints BEFORE the test runs), so a test's own io.println
// output lands between the `test <label> ... ` line and its `ok` verdict, in order —
// not reordered or swallowed by stdio block-buffering when stdout is piped.
func TestZergTestReportInterleavesWithIO(t *testing.T) {
	src := "import \"testing\"\nimport \"io\"\n\n" +
		"#[test]\nfn test_talky() {\n  io.println(\"mid\")\n  testing.assert(true)\n}\n"
	_, entry := entryAt(t, "in.zg", src)
	bin := buildTests(t, entry)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("suite must pass: %v\n%s", err, stderr)
	}
	want := "test test_talky ... mid\nok\n\ntest result: 1 tests; 1 passed, 0 failed\n"
	if stdout != want {
		t.Fatalf("interleaved report =\n%q\nwant\n%q", stdout, want)
	}
}

// TestZergTestSpawnChannel is the M2 fix: a `#[test]` that `spawn`s a coroutine and
// receives on a channel runs UNDER the scheduler (as a normal program does) instead
// of enqueuing a coroutine onto a run queue nobody drains — so it completes rather
// than silently deadlocking.
func TestZergTestSpawnChannel(t *testing.T) {
	src := "import \"testing\"\n\n" +
		"#[test]\nfn test_spawn_chan() {\n  ch := chan[int]()\n  spawn send42(ch)\n  v := <-ch\n  testing.assert_eq(v!, 42)\n}\n\n" +
		"fn send42(ch: chan[int]) {\n  ch <- 42\n}\n"
	_, entry := entryAt(t, "in.zg", src)
	bin := buildTests(t, entry)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("a spawn/channel test must run, not hang: %v\nstdout=%q stderr=%q", err, stdout, stderr)
	}
	want := "test test_spawn_chan ... ok\n\ntest result: 1 tests; 1 passed, 0 failed\n"
	if stdout != want {
		t.Fatalf("spawn-in-test report =\n%q\nwant\n%q", stdout, want)
	}
}

func asExit(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}
