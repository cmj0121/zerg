package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	runtime "github.com/cmj0121/zerg/src/runtime"
)

// runProgramRTSplit compiles+links src under ASan+UBSan, runs it, and returns its stdout
// and stderr SEPARATELY — the one thing the io error-stream writers need to prove (that
// eprintln/ewrite land on fd 2, not fd 1). It mirrors runProgramRT but keeps the two
// streams apart instead of combining them.
func runProgramRTSplit(t *testing.T, src string) (string, string) {
	t.Helper()
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
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
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v\nstderr:\n%s\n--- generated C ---\n%s", err, stderr.String(), code)
	}
	return stdout.String(), stderr.String()
}

// TestIoStderrWriters pins that eprintln/ewrite write to stderr (fd 2) and stay off
// stdout, while write/println still go to stdout — the two streams are independent.
func TestIoStderrWriters(t *testing.T) {
	stdout, stderr := runProgramRTSplit(t, "import \"io\"\n"+
		"fn main() {\n"+
		"\tio.println(\"out-line\")\n"+
		"\tio.eprintln(\"err-line\")\n"+
		"\tio.ewrite(\"err-bare\")\n"+
		"\tio.write(\"out-bare\")\n"+
		"}\n")
	if want := "out-line\nout-bare"; stdout != want {
		t.Fatalf("stdout: got %q, want %q", stdout, want)
	}
	if want := "err-line\nerr-bare"; stderr != want {
		t.Fatalf("stderr: got %q, want %q", stderr, want)
	}
}
