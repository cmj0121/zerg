package main

import (
	"testing"
)

// TestChannelBufferedProducer compiles and runs a real Zerg program: a producer
// coroutine sends two values on a BUFFERED channel and main receives them, unwrapping
// each recv's Result[T] with `!`. It exercises the whole slice-C2 path end to end —
// chan[T](cap) construction, sender-handle copy into the spawn, `ch <- v` send, `<-ch`
// recv, park/wake across a context switch, and the channel dropping on scope exit.
func TestChannelBufferedProducer(t *testing.T) {
	const src = "fn produce(ch: chan[int]) {\n" +
		"  ch <- 1\n" +
		"  ch <- 2\n" +
		"}\n" +
		"fn main() {\n" +
		"  ch := chan[int](2)\n" +
		"  spawn produce(ch)\n" +
		"  x := (<-ch)!\n" +
		"  y := (<-ch)!\n" +
		"  print x\n" +
		"  print y\n" +
		"}"
	bin := buildConcurrent(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "1\n2\n" {
		t.Fatalf("stdout = %q, want %q (values received through a buffered channel)", stdout, "1\n2\n")
	}
}

// TestChannelUnbufferedRendezvous compiles and runs a program whose channel is
// UNBUFFERED (capacity 0): the producer's send blocks until main's receive pairs with
// it (a rendezvous), proving the parked-sender / parked-receiver hand-off works through
// the real compiler and scheduler.
func TestChannelUnbufferedRendezvous(t *testing.T) {
	const src = "fn produce(ch: chan[int]) {\n" +
		"  ch <- 7\n" +
		"}\n" +
		"fn main() {\n" +
		"  ch := chan[int]()\n" +
		"  spawn produce(ch)\n" +
		"  x := (<-ch)!\n" +
		"  print x\n" +
		"}"
	bin := buildConcurrent(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "7\n" {
		t.Fatalf("stdout = %q, want %q (value rendezvoused through an unbuffered channel)", stdout, "7\n")
	}
}
