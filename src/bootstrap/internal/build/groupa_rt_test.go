package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	runtime "github.com/cmj0121/zerg/src/runtime"
)

// runProgramRT compiles src to C, links it against the materialized Zerg runtime
// (and the concurrency units when the Manifest reports them), runs the binary under
// ASan+UBSan, and returns its stdout. It fails on any diagnostic or a non-zero exit,
// so a passing test asserts both a clean exit and the exact stdout. Use this for the
// pattern/carrier/guard/chan run-tests whose emitted C references zergrt.h; the plain
// compileAndRun links only a single translation unit and cannot resolve the runtime.
func runProgramRT(t *testing.T, src string) string {
	t.Helper()
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	code, manifest, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	if manifest.Concurrency {
		cfiles = append(cfiles, runtime.ConcurrencyCUnits(dir, runtime.HostArch())...)
	}
	cpath := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	bin := filepath.Join(dir, "prog.bin")
	args := append([]string{
		"-std=c11", "-fsanitize=address,undefined", "-fno-sanitize-recover=all",
		"-I", dir, "-o", bin, cpath,
	}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- generated C ---\n%s", err, out, code)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s\n--- generated C ---\n%s", err, out, code)
	}
	return string(out)
}
