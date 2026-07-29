package build

import (
	"os"
	"os/exec"
	"path/filepath"
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

// TestRecvCleanCloseIsStopIterationByKind is the Receive table's first row: a channel that
// has closed cleanly answers `Right(StopIteration)`, and a receiver tells that from a crash
// by KIND — never by comparing the message. When the Right was built from a message string
// this printed `false`, which is the string-matching the spec forbids.
//
// The close here is the EXPLICIT one, which is the only close a seed program can arrange
// deliberately: an auto-close needs the LAST send-capable handle to go, and the seed refuses
// the directional binding that would let main stop being a sender while it is still reading.
// The kind on the Right is the same either way — it is the channel's, not the statement's.
func TestRecvCleanCloseIsStopIterationByKind(t *testing.T) {
	outs := runConcProgram(t, "fn produce(ch: chan[int]) {\n\tnop\n}\n"+
		"fn main() {\n"+
		"\tch := chan[int](2)\n"+
		"\tspawn produce(ch)\n"+
		"\tch <- 1\n"+
		"\tclose(ch)\n"+
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

// TestCloseDrainsThenEnds pins the other half of what `close` means: it is a flag on the
// channel and moves no count, so the handle stays perfectly usable — a receive after it
// hands over everything already buffered, IN ORDER, and only then answers the Right. A
// close that discarded the buffer, or that freed the handle, would fail here rather than in
// a later program that quietly lost values.
func TestCloseDrainsThenEnds(t *testing.T) {
	outs := runConcProgram(t, "fn produce(ch: chan[int]) {\n\tnop\n}\n"+
		"fn main() {\n"+
		"\tch := chan[int](4)\n"+
		"\tspawn produce(ch)\n"+
		"\tch <- 1\n"+
		"\tch <- 2\n"+
		"\tclose(ch)\n"+
		"\tclose(ch)\n"+
		"\tfor v in ch {\n\t\tprint v\n\t}\n"+
		"\tprint 9\n}\n", false)
	for _, got := range outs {
		if want := "1\n2\n9\n"; got != want {
			t.Fatalf("close drains then ends: got %q, want %q", got, want)
		}
	}
}

// TestRecvCrashCloseCarriesTheCrashsOwnErr is the Receive table's second row: a channel
// closed by a CRASHING last sender answers that coroutine's OWN Err — kind, message and
// cause intact — so the reason it died reaches the receiver rather than a stand-in string.
//
// It cannot be written for the seed any more, and the reason is worth stating rather than
// deleting the test over. The crash has to reach the receiver through the AUTO-close, which
// fires when the last send-capable handle goes; so the receiver must not itself be a sender.
// Saying that needs a receive-only binding (`rx: <-chan[int] = ch`, or a `-> <-chan[int]`
// return), and rejectDirectionalChans refuses every directional type in the seed — a
// deliberate line drawn in Unit 3. `close(ch)` is no substitute: it is a flag on the
// channel, first close wins, and an explicit one records StopIteration BEFORE the producer
// dies, which is exactly the trade being pinned here in reverse.
//
// The property itself is not untested: test-data/codegen/conc_crash.zg is this program,
// under the self-hosted compiler, which does lower a directional end.
func TestRecvCrashCloseCarriesTheCrashsOwnErr(t *testing.T) {
	t.Skip("needs a receive-only binding, which the seed refuses; covered by test-data/codegen/conc_crash.zg")
}
