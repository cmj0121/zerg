package build

import "testing"

// RUN-based tests for the two shapes the seed used to turn away although GRAMMAR spells
// both of them out: `defer close(ch)` (group 11's one non-expression defer) and a select
// arm whose body is a statement (group 9). A lowering oracle would pin the emitted C;
// these compile, link and RUN, because what is being asserted is an effect — a stream that
// ends at a block's exit, and an arm that does something rather than yielding a value.

// TestDeferClose checks that a deferred close ends the stream at the block's exit. The
// channel cannot auto-close here: `main` holds the only end and it is bidirectional, so
// it counts as a sender for as long as it lives. Without the deferred close the third
// receive would block forever, and the `?? -1` that answers it is the whole observation.
func TestDeferClose(t *testing.T) {
	const src = "fn fill(ch: chan[int]) {\n" +
		"  defer close(ch)\n" +
		"  ch <- 1\n" +
		"  ch <- 2\n" +
		"}\n" +
		"fn main() {\n" +
		"  ch := chan[int](4)\n" +
		"  fill(ch)\n" +
		"  print (<-ch)!\n" +
		"  print (<-ch)!\n" +
		"  print (<-ch) ?? -1\n" +
		"}"
	for _, got := range runConcProgram(t, src, false) {
		if got != "1\n2\n-1\n" {
			t.Fatalf("a deferred close must end the stream at the block's exit, got %q", got)
		}
	}
}

// TestDeferCloseRunsBeforeTheHandleGoes is the ordering the cleanup stack gives for free,
// stated as a test because it is the one way this could be wrong: the binding registers
// its release when the binding is made, which is before the defer, so LIFO runs the close
// first and it never sees a freed channel. A program that got this backwards would not
// print a wrong answer — it would fail under the sanitizers, which is why the buffered
// values are drained AFTER the producer's scope has exited.
func TestDeferCloseRunsBeforeTheHandleGoes(t *testing.T) {
	const src = "fn produce(ch: chan[int]) {\n" +
		"  defer close(ch)\n" +
		"  mut i := 0\n" +
		"  for i < 3 {\n" +
		"    ch <- i\n" +
		"    i = i + 1\n" +
		"  }\n" +
		"}\n" +
		"fn main() {\n" +
		"  ch := chan[int](8)\n" +
		"  produce(ch)\n" +
		"  mut total := 0\n" +
		"  for v in ch {\n" +
		"    total = total + v\n" +
		"  }\n" +
		"  print total\n" +
		"}"
	for _, got := range runConcProgram(t, src, false) {
		if got != "3\n" {
			t.Fatalf("a for-in must drain a deferred-closed channel and end, got %q", got)
		}
	}
}

// TestSelectArmStatementBody covers a select arm whose body is a bare statement rather
// than an expression — `print v!` here, which is what the concurrency chapter writes. The
// seed refused it ("expected an expression, found 'print'") while the shipped compiler
// took it; GRAMMAR now makes the body a statement, because a select yields nothing and an
// arm RUNS rather than yields.
func TestSelectArmStatementBody(t *testing.T) {
	const src = "fn main() {\n" +
		"  a := chan[int](1)\n" +
		"  a <- 5\n" +
		"  select {\n" +
		"    v := <-a => print v!\n" +
		"    _        => print 0\n" +
		"  }\n" +
		"}"
	for _, got := range runConcProgram(t, src, false) {
		if got != "5\n" {
			t.Fatalf("a statement arm body must run, got %q", got)
		}
	}
}

// TestSelectArmSendBody is the same change seen from the other side: a send is a
// statement, so `=> out <- v!` used to parse the arm body as the expression `out` and
// leave the `<- v!` behind.
func TestSelectArmSendBody(t *testing.T) {
	const src = "fn main() {\n" +
		"  a := chan[int](1)\n" +
		"  out := chan[int](1)\n" +
		"  a <- 7\n" +
		"  select {\n" +
		"    v := <-a => out <- v!\n" +
		"  }\n" +
		"  print (<-out)!\n" +
		"}"
	for _, got := range runConcProgram(t, src, false) {
		if got != "7\n" {
			t.Fatalf("a send as an arm body must send, got %q", got)
		}
	}
}

// TestSelectArmDivergeBody covers `done => break`, the spelling the chapter's own select
// example uses. `break` is a statement and no expression, so the arm body had to become a
// statement before this could parse at all.
func TestSelectArmDivergeBody(t *testing.T) {
	const src = "fn fill(ch: chan[int]) {\n" +
		"  defer close(ch)\n" +
		"  ch <- 1\n" +
		"  ch <- 2\n" +
		"}\n" +
		"fn main() {\n" +
		"  ch := chan[int](4)\n" +
		"  fill(ch)\n" +
		"  mut total := 0\n" +
		"  for {\n" +
		"    select {\n" +
		"      v := <-ch => total = total + v!\n" +
		"      done      => break\n" +
		"    }\n" +
		"  }\n" +
		"  print total\n" +
		"}"
	for _, got := range runConcProgram(t, src, false) {
		if got != "3\n" {
			t.Fatalf("`done => break` must end the loop after the stream, got %q", got)
		}
	}
}
