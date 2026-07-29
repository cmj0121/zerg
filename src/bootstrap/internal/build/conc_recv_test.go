package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	runtime "github.com/cmj0121/zerg/src/runtime"
)

// RUN-based tests for what a receive's Right actually CARRIES (docs/code/coroutine.md, the
// Receive table). The lowering oracle in internal/emit pins the shape of the emitted C;
// these compile, link and RUN the program, so a pass asserts the kind survives the whole
// way from the runtime's channel to a Zerg `is` test.

// runConcProgram compiles src, links it against the runtime INCLUDING the concurrency
// units (runProgramRT links only the core set, so a channel program does not resolve),
// and runs the binary once per worker mode — the single-worker scheduler and the default
// M:N one — returning the combined output of each. wantAbort inverts the exit
// expectation, for a program that ends by re-raising what it received.
func runConcProgram(t *testing.T, src string, wantAbort bool) []string {
	t.Helper()
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	code, manifest, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !manifest.Concurrency {
		t.Fatalf("a channel program must report Concurrency\n%s", code)
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	cfiles = append(cfiles, runtime.ConcurrencyCUnits(dir, runtime.HostArch())...)
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
	var outs []string
	for _, workers := range []string{"1", ""} {
		cmd := exec.Command(bin)
		cmd.Env = os.Environ()
		if workers != "" {
			cmd.Env = append(cmd.Env, "ZRT_WORKERS="+workers)
		}
		out, err := cmd.CombinedOutput()
		switch {
		case wantAbort && err == nil:
			t.Fatalf("ZRT_WORKERS=%q: expected a non-zero exit (abort), got a clean run\n%s", workers, out)
		case !wantAbort && err != nil:
			t.Fatalf("ZRT_WORKERS=%q: run failed: %v\n%s\n--- generated C ---\n%s", workers, err, out, code)
		}
		outs = append(outs, string(out))
	}
	return outs
}

// TestRecvCleanCloseIsStopIterationByKind is the Receive table's first row: a channel
// whose last sender left normally answers `Right(StopIteration)`, and a receiver tells
// that from a crash by KIND — never by comparing the message. When the Right was built
// from a message string this printed `false`, which is the string-matching the spec
// forbids.
func TestRecvCleanCloseIsStopIterationByKind(t *testing.T) {
	outs := runConcProgram(t, "fn produce(ch: chan[int]) {\n\tch <- 1\n}\n"+
		"fn main() {\n"+
		"\tch := chan[int](2)\n"+
		"\tspawn produce(ch)\n"+
		"\tdel ch\n"+
		"\tprint (<-ch)!\n"+
		"\tmatch <-ch {\n"+
		"\t\tLeft(v) => { print v }\n"+
		"\t\tRight(e) => { print e is StopIteration }\n"+
		"\t}\n}\n", false)
	for _, got := range outs {
		if want := "1\ntrue\n"; got != want {
			t.Fatalf("clean close: got %q, want %q", got, want)
		}
	}
}

// TestRecvCrashCloseCarriesTheCrashsOwnErr is the table's second row: a channel closed by
// a crashing last sender answers that coroutine's OWN Err. Its kind reaches the receiver
// (`e is ValueError`, so the close is NOT StopIteration), and re-raising it reproduces the
// crash's own message rather than a stand-in string.
func TestRecvCrashCloseCarriesTheCrashsOwnErr(t *testing.T) {
	outs := runConcProgram(t, "fn produce(ch: chan[int]) {\n"+
		"\tch <- 7\n"+
		"\traise ValueError(\"producer died\")\n}\n"+
		"fn main() {\n"+
		"\tch := chan[int](2)\n"+
		"\tspawn produce(ch)\n"+
		"\tdel ch\n"+
		"\tprint (<-ch)!\n"+
		"\tmatch <-ch {\n"+
		"\t\tLeft(v) => { print v }\n"+
		"\t\tRight(e) => {\n"+
		"\t\t\tprint e is StopIteration\n"+
		"\t\t\tprint e is ValueError\n"+
		"\t\t\traise e\n"+
		"\t\t}\n"+
		"\t}\n}\n", true)
	for _, got := range outs {
		for _, want := range []string{"7\n", "false\ntrue\n", "producer died"} {
			if !strings.Contains(got, want) {
				t.Fatalf("crash close: output missing %q\n%s", want, got)
			}
		}
	}
}
