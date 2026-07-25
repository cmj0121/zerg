package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	runtime "github.com/cmj0121/zerg/src/runtime"
)

// RUN-based tests for the self-host lexer INPUT primitives: `int(s)` parses a decimal
// string, and `io.read_file(path)` reads a file's bytes. Together with string scanning
// they let a Zerg-in-Zerg lexer read and tokenize source text.

// TestIntParseRuns covers int(str): a decimal parse with an optional sign, and the
// checked failure (a malformed string or an out-of-range value) demoted by guard.
func TestIntParseRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tprint int(\"42\")\n"+
		"\tprint int(\"-17\")\n"+
		"\tprint int(\"0\")\n"+
		"\tprint int(\"100\") + int(\"23\")\n"+
		"\tprint guard { int(\"nope\") } ?? -1\n"+
		"\tprint guard { int(\"99999999999999999999\") } ?? -2\n"+
		"\tprint guard { int(\"\") } ?? -3\n}\n")
	if want := "42\n-17\n0\n123\n-1\n-2\n-3\n"; got != want {
		t.Fatalf("int(str): got %q, want %q", got, want)
	}
}

// TestIntParseLowering pins the emitted C: the str parse goes through the runtime
// (raising on a bad string), not a scalar cast.
func TestIntParseLowering(t *testing.T) {
	code, manifest, diags := Compile("fn main() {\n\tprint int(\"42\")\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !strings.Contains(code, "zrt_parse_int(") {
		t.Fatalf("int(str) should lower to zrt_parse_int:\n%s", code)
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("int(str) should need the runtime, got %+v", manifest)
	}
}

// TestStrParseRejected keeps the surface narrow: only int(s) parses a string; a str to
// another scalar is not yet a parse, and a scalar int(x) still converts.
func TestStrParseRejected(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n\tprint uint(\"5\")\n}\n",
		"fn main() {\n\tprint byte(\"5\")\n}\n",
	} {
		if _, _, diags := Compile(src); len(diags) == 0 {
			t.Fatalf("parsing a str to a non-int should be rejected: %s", src)
		}
	}
}

// runReadingFile compiles src, writes `content` to a temp file, and runs the binary with
// that path as its sole argument, returning stdout. It is the read_file counterpart to
// runProgramRT — the plain runners pass no argv and create no file.
func runReadingFile(t *testing.T, src, content string) string {
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
	input := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(input, []byte(content), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	bin := filepath.Join(dir, "prog.bin")
	args := append([]string{
		"-std=c11", "-fsanitize=address,undefined", "-fno-sanitize-recover=all",
		"-I", dir, "-o", bin, cpath,
	}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- C ---\n%s", err, out, code)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, input)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v\nstderr:\n%s", err, stderr.String())
	}
	return stdout.String()
}

// TestReadFileRuns reads a real file's bytes, decodes them with str, and scans them —
// the self-host source-input path.
func TestReadFileRuns(t *testing.T) {
	got := runReadingFile(t,
		"import \"io\"\n"+
			"fn main(args: list[str]) -> Result[nil] {\n"+
			"\tbytes := io.read_file(args[0])\n"+
			"\tprint bytes.len()\n"+
			"\tprint str(bytes)\n"+
			"\tprint bytes[0]\n"+
			"\treturn nil\n}\n",
		"hello\n42")
	if want := "8\nhello\n42\n104\n"; got != want {
		t.Fatalf("read_file: got %q, want %q", got, want)
	}
}

// TestReadFileMissingGuarded covers the failure path: a missing file raises IOError,
// which guard demotes to a Result so a default takes over.
func TestReadFileMissingGuarded(t *testing.T) {
	got := runProgramRT(t, "import \"io\"\n"+
		"fn main() -> Result[nil] {\n"+
		"\tbytes := guard { io.read_file(\"/no/such/zerg/file\") } ?? [byte(88)]\n"+
		"\tprint bytes.len()\n"+
		"\tprint str(bytes)\n"+
		"\treturn nil\n}\n")
	if want := "1\nX\n"; got != want {
		t.Fatalf("missing file guard: got %q, want %q", got, want)
	}
}

// TestReadFileLowering pins the emitted C: io.read_file's pure-Zerg loop lowers through
// the open/read/close floor intrinsics to their runtime leaves.
func TestReadFileLowering(t *testing.T) {
	code, manifest, diags := CompileProgram(writeReadProg(t))
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{"zrt_open(", "zrt_read_fd(", "zrt_close("} {
		if !strings.Contains(code, want) {
			t.Fatalf("io.read_file should lower to %s:\n%s", want, code)
		}
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("read_file should need the runtime, got %+v", manifest)
	}
}

// writeReadProg writes a tiny program that imports io and reads a file, returning its
// entry path (CompileProgram resolves the io import from the bundled stdlib).
func writeReadProg(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "main.zg")
	src := "import \"io\"\n" +
		"fn main(args: list[str]) -> Result[nil] {\n" +
		"\tbytes := io.read_file(args[0])\n" +
		"\tprint bytes.len()\n" +
		"\treturn nil\n}\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	return p
}
