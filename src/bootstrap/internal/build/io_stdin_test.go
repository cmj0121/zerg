package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	runtime "github.com/cmj0121/zerg/src/runtime"
)

// runProgramStdin compiles src, links it against the runtime under ASan+UBSan, runs the
// binary with stdin fed from input, and returns its stdout. It mirrors runProgramRT but
// pipes input — the one thing read_stdin needs to exercise.
func runProgramStdin(t *testing.T, src, input string) string {
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
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s\n--- generated C ---\n%s", err, out, code)
	}
	return string(out)
}

// TestIoReadStdin exercises io.read_stdin as a filter: read all of stdin and write it
// back uppercased. Covers a multi-line body and confirms the whole stream is consumed.
func TestIoReadStdin(t *testing.T) {
	got := runProgramStdin(t, "import \"io\"\n"+
		"import \"strings\"\n"+
		"fn main() {\n"+
		"\tdata := io.read_stdin()\n"+
		"\tio.write(strings.to_upper(str(data)))\n"+
		"}\n", "hello world\nsecond line\n")
	if want := "HELLO WORLD\nSECOND LINE\n"; got != want {
		t.Fatalf("io.read_stdin filter: got %q, want %q", got, want)
	}
}

// TestIoReadStdinEmpty pins that empty input yields an empty read (no hang, no error).
func TestIoReadStdinEmpty(t *testing.T) {
	got := runProgramStdin(t, "import \"io\"\n"+
		"fn main() {\n"+
		"\tdata := io.read_stdin()\n"+
		"\tprint data.len()\n"+
		"}\n", "")
	if want := "0\n"; got != want {
		t.Fatalf("io.read_stdin empty: got %q, want %q", got, want)
	}
}
