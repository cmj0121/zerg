package sema

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
)

// TestChannelDirectionNarrowing checks the slice-C2 directional rules (GRAMMAR group
// 9): a send on a receive-only channel and a receive from a send-only channel are each
// rejected, while both operations are allowed on a bidirectional channel.
func TestChannelDirectionNarrowing(t *testing.T) {
	reject := []struct {
		name   string
		src    string
		substr string
	}{
		{
			name:   "send on a receive-only channel",
			src:    "fn f(ch: <-chan[int]) {\n  ch <- 1\n}",
			substr: "cannot send on a receive-only channel",
		},
		{
			name:   "receive from a send-only channel",
			src:    "fn f(ch: chan[int]<-) -> int {\n  x := <-ch\n  return 0\n}",
			substr: "cannot receive from a send-only channel",
		},
	}
	for _, tc := range reject {
		t.Run(tc.name, func(t *testing.T) {
			file, pdiags := parser.Parse(tc.src)
			if len(pdiags) != 0 {
				t.Fatalf("unexpected parse errors: %v", pdiags)
			}
			_, diags := Check(file)
			found := false
			for _, d := range diags {
				if strings.Contains(d.Msg, tc.substr) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want a diagnostic containing %q, got %v", tc.substr, diags)
			}
		})
	}

	// a bidirectional channel accepts both a send and a receive with no diagnostic.
	t.Run("bidirectional allows send and receive", func(t *testing.T) {
		const src = "fn f(ch: chan[int]) -> int {\n  ch <- 1\n  x := <-ch\n  return 0\n}"
		file, pdiags := parser.Parse(src)
		if len(pdiags) != 0 {
			t.Fatalf("unexpected parse errors: %v", pdiags)
		}
		if _, diags := Check(file); len(diags) != 0 {
			t.Fatalf("bidirectional channel send/recv must be clean, got %v", diags)
		}
	})
}

// TestCloseBuiltin covers `close(ch)` — the channel's one built-in (docs/code/coroutine.md).
// It says THIS HOLDER IS DONE SENDING, so it wants a channel that has a send end to give up,
// and it leaves the name usable: giving up your send end and then draining what the producer
// sends is the shape every concurrent program is written in.
func TestCloseBuiltin(t *testing.T) {
	// a send-capable end closes, and the name stays live afterwards.
	wantOK(t, "fn produce(ch: chan[int]) {\n ch <- 1\n}\nfn main() {\n ch := chan[int](1)\n spawn produce(ch)\n close(ch)\n print (<-ch)!\n}")

	// anything that is not a channel names the type it got.
	wantErr(t, "fn main() {\n x := 3\n close(x)\n}", "close requires a channel, found int")

	// a receive-only end never had a send capability to give up, so asking is a bug rather
	// than a no-op — the one place `close` is stricter than the old `del ch` was.
	wantErr(t, "fn f(rx: <-chan[int]) {\n close(rx)\n}",
		"cannot close a receive-only channel <-chan[int]")

	// exactly one argument: the channel.
	wantErr(t, "fn main() {\n ch := chan[int](1)\n close(ch, 1)\n}", "close takes exactly one argument")
}

// TestDelRevokesAChannelToo pins the other half of the split: with `close` carrying "done
// sending", `del` means what it means everywhere else — this name is gone. It used to be a
// channel-shaped exception, which let `ch <- v` typecheck on a handle whose send end had
// already been given up.
func TestDelRevokesAChannelToo(t *testing.T) {
	wantErr(t, "fn main() {\n ch := chan[int](1)\n del ch\n print (<-ch)!\n}", "used after del")
	wantErr(t, "fn main() {\n ch := chan[int](1)\n del ch\n ch <- 1\n}", "used after del")

	// a `del` with nothing after it is still fine.
	wantOK(t, "fn main() {\n ch := chan[int](1)\n ch <- 1\n del ch\n}")
}
