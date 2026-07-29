package emit

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
)

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

// TestChannelLowering is the lowering oracle for the happy path: a program using a channel
// reports Concurrency, runs main under the scheduler, and lowers the constructor, the
// spawn capture, the send, and the receive to their runtime shapes. The receive lands on
// the GENERAL Result carrier (emit_result.go), not a channel-private one, which is what
// makes the group-8 operators work on it.
func TestChannelLowering(t *testing.T) {
	const src = "fn produce(ch: chan[int]) {\n" +
		"  ch <- 1\n" +
		"}\n" +
		"fn main() {\n" +
		"  ch := chan[int](2)\n" +
		"  spawn produce(ch)\n" +
		"  x := (<-ch)!\n" +
		"  print x\n" +
		"}"
	code, manifest := emitWithManifest(t, src)
	if !manifest.Concurrency {
		t.Fatalf("a channel program must report Concurrency\n%s", code)
	}
	for _, want := range []string{
		"zrt_chan_new(sizeof(int64_t), 2)",
		"zrt_chan_sender_copy(", // the spawn copies a send-capable handle
		"zrt_chan_send(",        // ch <- 1
		"typedef struct { int32_t tag; int64_t ok; zrt_err err; } zg_result_0;", // the general carrier
		"static zg_result_0 zg_chanrecv_0(zrt_chan *ch) {",
		"r.tag = (int32_t)zrt_chan_recv(ch, &r.ok);",
		"r.err = zrt_err_new(m == NULL ? \"StopIteration\" : m);", // the close carries its reason
		"zg_chanrecv_0(",                 // <-ch
		"zg_force_zg_result_0(",          // (<-ch)! goes through the general carrier's force helper
		"zrt_defer(zg_chan_sender_drop,", // the handle's scope-exit drop
		"return zrt_sched_main_nil(",     // main runs as the first coroutine
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q\n%s", want, code)
		}
	}
}

// TestChannelUnbufferedCap checks the omitted-capacity constructor lowers to capacity 0,
// the unbuffered rendezvous form.
func TestChannelUnbufferedCap(t *testing.T) {
	const src = "fn main() {\n" +
		"  ch := chan[int]()\n" +
		"  ch <- 5\n" +
		"}"
	code, _ := emitWithManifest(t, src)
	if !strings.Contains(code, "zrt_chan_new(sizeof(int64_t), 0)") {
		t.Fatalf("an omitted capacity must lower to 0\n%s", code)
	}
}

// TestDelChannelKeepsHold pins what `del ch` means: it gives up the SEND end and keeps an
// ordinary hold, so the name stays usable. The runtime has no sender-only release, so the
// hold is taken again first and moved to a slot of its own — and the original slot is
// nulled so its scope-exit sender drop does not release the hold a second time.
func TestDelChannelKeepsHold(t *testing.T) {
	const src = "fn produce(ch: chan[int]) {\n" +
		"  ch <- 1\n" +
		"}\n" +
		"fn main() {\n" +
		"  ch := chan[int](1)\n" +
		"  spawn produce(ch)\n" +
		"  del ch\n" +
		"  print (<-ch)!\n" +
		"}"
	code, _ := emitWithManifest(t, src)
	for _, want := range []string{
		"zrt_chan *zg_chk = zrt_chan_copy(zg_ch);",
		"zrt_chan_sender_release(zg_ch);",
		"zg_ch = NULL;",
		"zrt_defer(zg_chan_drop, &zg_chk);",
		"zg_chanrecv_0(zg_chk)", // the receive afterwards reads the surviving hold
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q\n%s", want, code)
		}
	}
}

// TestSelectLowering is the select oracle: a recv arm (with a bind), a send arm, and a
// `done` arm lower to a zrt_sel_case descriptor array, one zrt_select call carrying the
// has_default / has_done flags, and a dispatch that binds the recv arm's Result[T] before
// its body. The bind is assembled from the value the runtime already received and the
// descriptor's closed flag — calling the recv helper again would take a SECOND value off
// the channel.
func TestSelectLowering(t *testing.T) {
	const src = "fn main() {\n" +
		"  c1 := chan[int](1)\n" +
		"  c2 := chan[int](1)\n" +
		"  mut total := 0\n" +
		"  select {\n" +
		"    x := <-c1 => { total = x! }\n" +
		"    c2 <- 7 => { total = 7 }\n" +
		"    done => { total = 0 }\n" +
		"  }\n" +
		"  print total\n" +
		"}"
	code, manifest := emitWithManifest(t, src)
	if !manifest.Concurrency {
		t.Fatalf("a select program must report Concurrency\n%s", code)
	}
	for _, want := range []string{
		"zrt_sel_case",    // the descriptor array
		"ZRT_SEL_RECV",    // the recv case descriptor
		"ZRT_SEL_SEND",    // the send case descriptor
		"= zrt_select(",   // the one runtime call
		", false, true);", // has_default=false, has_done=true
		"== 0) {",         // dispatch keyed on the picked index
		"ZRT_SEL_DONE) {", // the done arm branch
		"zg_result_0 zg_x = {0};",
		".tag = (int32_t)zg_selcs[0].closed;",
		"zg_force_zg_result_0(", // x! in the recv arm body
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q\n%s", want, code)
		}
	}
	// The dispatch must NOT be a C switch: a Zerg `break` in an arm would be captured by the
	// switch instead of the enclosing loop, which inside `for { select { … } }` hangs.
	if strings.Contains(code, "switch (") {
		t.Fatalf("select dispatch must be an if-chain, not a C switch\n%s", code)
	}
}

// TestSelectDefaultFlag checks a `select` with a `_` arm passes has_default=true and
// dispatches it from the ZRT_SEL_DEFAULT sentinel.
func TestSelectDefaultFlag(t *testing.T) {
	const src = "fn main() {\n" +
		"  c := chan[int]()\n" +
		"  select {\n" +
		"    x := <-c => { print x! }\n" +
		"    _ => { print 0 }\n" +
		"  }\n" +
		"}"
	code, _ := emitWithManifest(t, src)
	for _, want := range []string{", true, false);", "ZRT_SEL_DEFAULT) {"} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q\n%s", want, code)
		}
	}
}

// TestForInChannel pins the channel as an iterator: the loop IS the receive, the close is
// what ends it, and the reason is read AFTER the loop — an ordinary close ends it quietly,
// a sender that crashed re-raises, so a crash never turns into a short answer.
func TestForInChannel(t *testing.T) {
	const src = "fn produce(ch: chan[int]) {\n" +
		"  ch <- 1\n" +
		"}\n" +
		"fn main() {\n" +
		"  ch := chan[int](1)\n" +
		"  spawn produce(ch)\n" +
		"  del ch\n" +
		"  for v in ch {\n" +
		"    print v\n" +
		"  }\n" +
		"}"
	code, _ := emitWithManifest(t, src)
	for _, want := range []string{
		"while (zrt_chan_recv(zg_cit, &zg_v) == 0) {",
		"if (zrt_chan_err(zg_cit) != NULL) { zrt_abort(zrt_chan_err(zg_cit)); }",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q\n%s", want, code)
		}
	}
	// The loop binds the ELEMENT, not the Result[T] a bare `<-ch` yields: the Right that
	// ends the stream ends the loop instead of reaching the body.
	if strings.Contains(code, "zg_chanrecv_") {
		t.Fatalf("a for-in over a channel must not build a Result carrier\n%s", code)
	}
}

// TestSpawnEnvFreedOnAbort pins that a spawned coroutine's heap env is freed from the
// coroutine's CLEANUP STACK rather than after the call: a coroutine that aborts never
// returns from the call, and the scheduler unwinds that stack on the abort path too.
func TestSpawnEnvFreedOnAbort(t *testing.T) {
	const src = "fn produce(ch: chan[int]) {\n" +
		"  raise \"died\"\n" +
		"}\n" +
		"fn main() {\n" +
		"  ch := chan[int](1)\n" +
		"  spawn produce(ch)\n" +
		"  del ch\n" +
		"  for v in ch {\n" +
		"    print v\n" +
		"  }\n" +
		"}"
	code, _ := emitWithManifest(t, src)
	for _, want := range []string{
		"static void zg_spawn_free(void *p) { zrt_free(p); }",
		"zrt_defer(zg_spawn_free, p);",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q\n%s", want, code)
		}
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

// TestChannelRefusals covers the line the seed draws. Each of these is checked by sema and
// has no lowering here, and each would otherwise reach cc as C the seed already knew it
// could not write — so each must be a clean Zerg diagnostic with no C behind it.
func TestChannelRefusals(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			// The copy path is keyed on a value's OWN type, so a bidirectional handle passed
			// to a receive-only parameter would be copied as a sender and released as a plain
			// hold — the send count would never reach zero and every receiver would hang.
			name: "directional-parameter",
			src: "fn produce(out: chan[int]<-) {\n  out <- 1\n}\n" +
				"fn main() {\n  ch := chan[int]()\n  spawn produce(ch)\n  del ch\n  print (<-ch)!\n}",
			want: "does not lower a directional channel type chan[int]<-",
		},
		{
			name: "directional-binding",
			src:  "fn main() {\n  ch := chan[int](1)\n  inbox: <-chan[int] = ch\n  print (<-inbox)!\n}",
			want: "does not lower a directional channel type <-chan[int]",
		},
		{
			name: "spawn-method-callee",
			src: "struct Box {\n  n: int\n}\nimpl Box {\n  fn run() {\n    print this.n\n  }\n}\n" +
				"fn main() {\n  b := Box(1)\n  ch := chan[int](1)\n  spawn b.run()\n  del ch\n}",
			want: "lowers 'spawn' only on a direct function call",
		},
		{
			name: "mut-ref-across-spawn",
			src: "fn bump(mut &n: int, ch: chan[int]) {\n  n = n + 1\n  ch <- n\n}\n" +
				"fn main() {\n  mut n := 0\n  ch := chan[int](1)\n  spawn bump(n, ch)\n  del ch\n  print (<-ch)!\n}",
			want: "cannot pass a 'mut &' argument across a 'spawn'",
		},
		{
			// The scheduler entry shims take a zero-argument function pointer.
			name: "main-args-with-concurrency",
			src: "fn produce(ch: chan[int]) {\n  ch <- 1\n}\n" +
				"fn main(args: list[str]) {\n  ch := chan[int](1)\n  spawn produce(ch)\n  del ch\n  print (<-ch)!\n}",
			want: "does not lower a 'main(args)' in a concurrent program",
		},
		{
			// The survivor's release is pushed on the cleanup stack where the `del` runs, so a
			// `del` in an inner block would free the channel at that block's exit while the
			// enclosing scope still names it.
			name: "del-outside-the-binding-block",
			src: "fn produce(ch: chan[int]) {\n  ch <- 1\n}\n" +
				"fn main() {\n  ch := chan[int](1)\n  spawn produce(ch)\n  if true {\n    del ch\n  }\n  print (<-ch)!\n}",
			want: "lowers 'del' on a channel only in the block that binds it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { wantRefused(t, tc.src, tc.want) })
	}
}
