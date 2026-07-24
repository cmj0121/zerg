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

// RUN-based tests for `fn main(args: list[str])` — the command-line arguments the
// program receives (docs/package.md). The program name (argv[0]) is NOT included, so
// `args` is the program's own interface; the first real argument is `args[0]`.

// runWithArgs compiles src, links it against the runtime with the counting allocator
// swapped in, runs the binary WITH argv (so the args list is actually populated and
// then freed), and asserts a clean exit with a zero alloc/free balance. It mirrors
// runProgramRTBalanced but passes command-line arguments, which the plain runners
// cannot. It returns the program's stdout.
func runWithArgs(t *testing.T, src string, args ...string) string {
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
	if err := os.WriteFile(filepath.Join(dir, "alloc.c"), []byte(countingAllocC), 0o644); err != nil {
		t.Fatalf("write counting alloc.c: %v", err)
	}
	cpath := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	bin := filepath.Join(dir, "prog.bin")
	cargs := append([]string{
		"-std=c11", "-fsanitize=address,undefined", "-fno-sanitize-recover=all",
		"-I", dir, "-o", bin, cpath,
	}, cfiles...)
	if out, err := exec.Command(cc, cargs...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- generated C ---\n%s", err, out, code)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "ZRT_ALLOC_BALANCE=0\n") {
		t.Fatalf("alloc/free NOT balanced (leak): stderr=%q\n--- generated C ---\n%s", stderr.String(), code)
	}
	return stdout.String()
}

// TestMainArgsRuns is the core: main receives the arguments, argv[0] excluded, and the
// list frees cleanly (the balance assertion inside runWithArgs).
func TestMainArgsRuns(t *testing.T) {
	got := runWithArgs(t,
		"fn main(args: list[str]) -> Result[nil] {\n"+
			"\tprint args.len()\n"+
			"\tfor a in args {\n\t\tprint a\n\t}\n"+
			"\treturn nil\n}\n",
		"build", "a.zg", "--flag")
	if want := "3\nbuild\na.zg\n--flag\n"; got != want {
		t.Fatalf("main(args): got %q, want %q", got, want)
	}
}

// TestMainArgsEmptyRuns covers the no-argument invocation: argv holds only the program
// name, so args is empty. The plain RT runner is used rather than the balanced one —
// an empty argv makes zrt_os_args allocate nothing, so there is no buffer to balance.
func TestMainArgsEmptyRuns(t *testing.T) {
	got := runProgramRT(t, "fn main(args: list[str]) -> Result[nil] {\n\tprint args.len()\n\treturn nil\n}\n")
	if want := "0\n"; got != want {
		t.Fatalf("main(args) with no args: got %q, want %q", got, want)
	}
}

// TestMainArgsIndexRuns pins argv[0] exclusion: the FIRST real argument is args[0].
func TestMainArgsIndexRuns(t *testing.T) {
	got := runWithArgs(t,
		"fn main(args: list[str]) -> Result[nil] {\n\tprint args[0]\n\treturn nil\n}\n",
		"first", "second")
	if want := "first\n"; got != want {
		t.Fatalf("args[0] should be the first real argument, got %q, want %q", got, want)
	}
}

// TestMainArgsIntReturn covers the int-returning shape: the argument count becomes the
// exit code, so the entry passes the list through and maps the return.
func TestMainArgsIntReturnRuns(t *testing.T) {
	got := runWithArgs(t, "fn main(args: list[str]) -> int {\n\tprint args.len()\n\treturn 0\n}\n", "x", "y")
	if want := "2\n"; got != want {
		t.Fatalf("int-returning main(args): got %q, want %q", got, want)
	}
}

// TestMainArgsLowering pins the C entry: it grows argc/argv, builds the list with
// zrt_os_args, and routes a Result[nil] main through zrt_run_args.
func TestMainArgsLowering(t *testing.T) {
	code, manifest, diags := Compile("fn main(args: list[str]) -> Result[nil] {\n\treturn nil\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"int main(int argc, char **argv)",
		"zrt_list zg_args = zrt_os_args(argc, argv);",
		"return zrt_run_args(zg_main, zg_args);",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("main(args) should need the runtime, got %+v", manifest)
	}
}

// TestMainZeroArgUnchanged is the control: a no-parameter main keeps the Phase 0
// `int main(void)` entry byte-for-byte, so existing programs are untouched.
func TestMainZeroArgUnchanged(t *testing.T) {
	code, _, diags := Compile("fn main() {\n\tprint 42\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !strings.Contains(code, "int main(void)") || strings.Contains(code, "zrt_os_args") {
		t.Fatalf("a no-arg main must keep int main(void):\n%s", code)
	}
}

// TestMainBadSignatureRejected keeps the signature honest: the one allowed parameter is
// list[str], and a concurrent program cannot yet thread args through the scheduler.
func TestMainBadSignatureRejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"a non-list[str] parameter",
			"fn main(n: int) {\n\tprint n\n}\n",
			"must be 'list[str]'",
		},
		{
			"two parameters",
			"fn main(a: list[str], b: int) {\n\tprint b\n}\n",
			"either no parameters or one",
		},
		{
			"a list of the wrong element",
			"fn main(xs: list[int]) {\n\tprint xs.len()\n}\n",
			"must be 'list[str]'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, diags := Compile(tc.src)
			if len(diags) == 0 {
				t.Fatalf("expected a diagnostic for %s", tc.name)
			}
			var joined string
			for _, d := range diags {
				joined += d.Msg + "\n"
			}
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("expected a diagnostic mentioning %q, got:\n%s", tc.want, joined)
			}
		})
	}
}

// TestMainArgsConcurrentGated holds the gate: threading the args list through the
// scheduler entry is not wired, so a concurrent main(args) fails loudly.
func TestMainArgsConcurrentGated(t *testing.T) {
	src := "fn work() {\n\tprint 1\n}\n" +
		"fn main(args: list[str]) {\n\tspawn work()\n\tprint args.len()\n}\n"
	_, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatalf("a concurrent main(args) should be gated")
	}
}
