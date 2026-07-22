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
