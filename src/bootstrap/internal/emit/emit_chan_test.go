package emit

import (
	"strings"
	"testing"
)

// TestChannelLowering is the slice-C2 lowering oracle: a program using a channel sets
// Manifest.Concurrency and lowers the constructor, send, recv, and force to their
// runtime shapes — chan[T](cap) to zrt_chan_new, a sender-handle copy into the spawn,
// `ch <- v` through a temporary into zrt_chan_send, `<-ch` to the per-element Result[T]
// carrier helper, `!` to its force helper, and the send-capable handle's scope-exit drop.
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
		"typedef struct { int32_t tag; int64_t val; } zg_recv_0;",
		"r.tag = (int32_t)zrt_chan_recv(ch, &r.val);",
		"zg_chanrecv_0(",                                // <-ch
		"zg_force_0(",                                   // (<-ch)!
		"static void zg_chan_sender_drop(void *slot) {", // handle drop thunk
		"zrt_chan_sender_release(",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q\n%s", want, code)
		}
	}
}

// TestSelectLowering is the slice-C3 lowering oracle: a `select` with a recv arm (with a
// bind), a send arm, and a `done` arm lowers to a zrt_sel_case descriptor array, one
// zrt_select call carrying the has_default / has_done flags, and a switch that binds the
// recv arm's Result[T] carrier before its body and dispatches `done` from ZRT_SEL_DONE.
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
		"zrt_sel_case",       // the descriptor array
		"ZRT_SEL_RECV",       // the recv case descriptor
		"ZRT_SEL_SEND",       // the send case descriptor
		"= zrt_select(",      // the one runtime call
		", false, true);",    // has_default=false, has_done=true
		"== 0) {",            // dispatch keyed on the picked index (if-chain, not a switch)
		"ZRT_SEL_DONE) {",    // the done arm branch
		"zg_recv_0 zg_x = {", // the recv arm binds its Result[T] carrier
		"zg_force_0(",        // x! in the recv arm body
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q\n%s", want, code)
		}
	}
	// The dispatch must NOT be a C switch: a Zerg `break` in an arm would be captured by
	// the switch instead of the enclosing loop (the slice-C3 break-in-select hang).
	if strings.Contains(code, "switch (") {
		t.Fatalf("select dispatch must be an if-chain, not a C switch\n%s", code)
	}
}

// TestSelectDefaultFlag checks a `select` with a `_` arm passes has_default=true and
// dispatches it from the ZRT_SEL_DEFAULT label.
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

// TestChannelUnbufferedCap checks the omitted-capacity constructor lowers to capacity 0
// (the unbuffered rendezvous form).
func TestChannelUnbufferedCap(t *testing.T) {
	const src = "fn main() {\n" +
		"  ch := chan[int]()\n" +
		"  ch <- 5\n" +
		"}"
	code, _ := emitWithManifest(t, src)
	if !strings.Contains(code, "zrt_chan_new(sizeof(int64_t), 0)") {
		t.Fatalf("omitted capacity must lower to 0\n%s", code)
	}
}
