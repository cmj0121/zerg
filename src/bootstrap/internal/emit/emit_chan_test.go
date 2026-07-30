package emit

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
)

// The seed's answer to concurrency is one sentence and no C. Tier 2 of the contract in
// ../../README.md: the self-host chain contains no `spawn`, no `chan[T]`, no `select` and
// no `<-`, so the seed has nothing to build with them and carries no opinion about them.
//
// What used to be here was a lowering oracle — nine tests pinning the shape of the C for a
// send, a receive, a select's descriptor array. Those were the SECOND opinion: every gap
// closed on the concurrency chapter was the two compilers disagreeing, and the way to stop
// disagreeing is to stop having an opinion. The oracle now lives where the feature does,
// in the self-hosting compiler's corpus.

// emitDiags parses, checks, and emits src, returning the emitted C and the emitter's own
// diagnostics. Unlike emitWithManifest it does NOT fail on an emit diagnostic — it is how
// the refusal cases below assert both the message and that no C came with it.
func emitDiags(t *testing.T, src string) (string, []string) {
	t.Helper()
	file, pdiags := parser.Parse(src)
	if len(pdiags) != 0 {
		t.Fatalf("parse errors: %v", pdiags)
	}
	info, sdiags := sema.Check(file)
	if len(sdiags) != 0 {
		t.Fatalf("sema errors: %v", sdiags)
	}
	code, _, ediags := Emit(mono.Build(file, info))
	msgs := make([]string, 0, len(ediags))
	for _, d := range ediags {
		msgs = append(msgs, d.Msg)
	}
	return code, msgs
}

// wantRefused asserts that src is refused with a diagnostic containing want, and that NO C
// was emitted for it. Emitting C the refusal does not cover would hand cc a program the
// seed already knows it cannot lower, which is the failure mode the refusals exist to stop.
func wantRefused(t *testing.T, src, want string) {
	t.Helper()
	code, msgs := emitDiags(t, src)
	found := false
	for _, m := range msgs {
		if strings.Contains(m, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a diagnostic containing %q, got %v", want, msgs)
	}
	if code != "" {
		t.Fatalf("a refused program must emit no C, got %d bytes\n%s", len(code), code)
	}
}

// TestConcurrencyIsRefused covers every form that reaches the seed's concurrency door. Each
// must be named the way the source writes it — a reader who typed `spawn` should be told
// about `spawn` — and each must come back with no C behind it.
func TestConcurrencyIsRefused(t *testing.T) {
	const want = "the bootstrap seed does not lower"
	cases := []struct{ name, src string }{
		{"send", "fn main() {\n  ch := chan[int](1)\n  ch <- 1\n}"},
		{"receive", "fn main() {\n  ch := chan[int](1)\n  print (<-ch)!\n}"},
		{"spawn", "fn work() {\n  print 1\n}\nfn main() {\n  ch := chan[int](1)\n  spawn work()\n  print (<-ch)!\n}"},
		{"select", "fn main() {\n  ch := chan[int](1)\n  select {\n    v := <-ch => { print v }\n  }\n}"},
		{"for-select", "fn main() {\n  ch := chan[int](1)\n  for select {\n    v := <-ch => { print v }\n  }\n}"},
		{"for-in-a-channel", "fn main() {\n  ch := chan[int](1)\n  for v in ch {\n    print v\n  }\n}"},
		{"close", "fn main() {\n  ch := chan[int](1)\n  close(ch)\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { wantRefused(t, tc.src, want) })
	}
}

// TestNoConcurrencyStaysClean is the other half of the gate: a program that touches no
// channel and no `spawn` reports no Concurrency and names none of the scheduler, so it
// links none of it.
func TestNoConcurrencyStaysClean(t *testing.T) {
	code, manifest := emitWithManifest(t, "fn main() {\n  print 1 + 2\n}")
	if manifest.Concurrency {
		t.Fatalf("a value-only program must not report Concurrency\n%s", code)
	}
	for _, absent := range []string{"zrt_chan", "zrt_spawn", "zrt_sched_main", "zrt_select"} {
		if strings.Contains(code, absent) {
			t.Fatalf("a value-only program must not name %q\n%s", absent, code)
		}
	}
}
