package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// runBounded runs bin with a hard timeout, so a lowering regression that busy-loops (the
// slice-C3 break-in-select hang) fails the test instead of wedging the whole suite.
func runBounded(t *testing.T, bin string, d time.Duration) (stdout, stderr string, err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("program did not terminate within %s (select break regression?)\nstdout so far: %q", d, out.String())
	}
	return out.String(), errb.String(), err
}

// TestSelectDoneOverProducers compiles and runs the slice-C3 acceptance program: two
// producer coroutines each send two values on their own UNBUFFERED channel, and main
// loops a `select` over both receive-only handles, summing every value, until `done`
// fires once both channels have auto-closed (each producer was made the sole sender by
// narrowing main's handle to `<-chan[int]` and `del`-ing the bidirectional original).
// It proves fair multi-way recv, park-on-many across two channels, and the all-closed
// `done` arm end to end.
func TestSelectDoneOverProducers(t *testing.T) {
	const src = "fn produce(ch: chan[int], a: int, b: int) {\n" +
		"  ch <- a\n" +
		"  ch <- b\n" +
		"}\n" +
		"fn main() {\n" +
		"  c1 := chan[int]()\n" +
		"  c2 := chan[int]()\n" +
		"  spawn produce(c1, 1, 2)\n" +
		"  spawn produce(c2, 3, 4)\n" +
		"  r1: <-chan[int] = c1\n" +
		"  r2: <-chan[int] = c2\n" +
		"  del c1\n" +
		"  del c2\n" +
		"  mut total := 0\n" +
		"  mut open := 2\n" +
		"  for open > 0 {\n" +
		"    select {\n" +
		"      x := <-r1 => { total = total + x! }\n" +
		"      y := <-r2 => { total = total + y! }\n" +
		"      done => { open = 0 }\n" +
		"    }\n" +
		"  }\n" +
		"  print total\n" +
		"}"
	bin := buildConcurrent(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "10\n" {
		t.Fatalf("stdout = %q, want %q (1+2+3+4 received before done fired)", stdout, "10\n")
	}
}

// TestSelectBreakInArm is the slice-C3 regression for a `break` inside a select arm: the
// arm dispatch must be an if-chain, NOT a C switch, so a Zerg `break` binds to the
// enclosing `for` and terminates the loop once `done` fires. Lowered as a switch this
// program busy-loops forever (the `break` breaks the switch, not the loop). It must
// print the sum and exit.
func TestSelectBreakInArm(t *testing.T) {
	const src = "fn prod(ch: chan[int], a: int, b: int) {\n" +
		"  ch <- a\n" +
		"  ch <- b\n" +
		"}\n" +
		"fn main() {\n" +
		"  c1 := chan[int]()\n" +
		"  c2 := chan[int]()\n" +
		"  spawn prod(c1, 1, 2)\n" +
		"  spawn prod(c2, 3, 4)\n" +
		"  r1: <-chan[int] = c1\n" +
		"  r2: <-chan[int] = c2\n" +
		"  del c1\n" +
		"  del c2\n" +
		"  mut sum := 0\n" +
		"  for {\n" +
		"    select {\n" +
		"      x := <-r1 => { sum = sum + x! }\n" +
		"      y := <-r2 => { sum = sum + y! }\n" +
		"      done => { break }\n" +
		"    }\n" +
		"  }\n" +
		"  print sum\n" +
		"}"
	bin := buildConcurrent(t, src)
	stdout, stderr, err := runBounded(t, bin, 10*time.Second)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "10\n" {
		t.Fatalf("stdout = %q, want %q (break in the done arm exits the select loop)", stdout, "10\n")
	}
}

// TestSelectNonBlockingDefault checks the `_` arm: nothing is ready on the channel (no
// sender is parked and it is not closed), so `select` takes the non-blocking else
// without parking.
func TestSelectNonBlockingDefault(t *testing.T) {
	const src = "fn main() {\n" +
		"  c := chan[int]()\n" +
		"  select {\n" +
		"    x := <-c => { print x! }\n" +
		"    _ => { print 99 }\n" +
		"  }\n" +
		"}"
	bin := buildConcurrent(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "99\n" {
		t.Fatalf("stdout = %q, want %q (the non-blocking `_` arm fired)", stdout, "99\n")
	}
}

// TestSelectSendArm checks a send arm fires: main `select`s a send of 5 on a buffered
// channel (which has room, so the send arm is ready) against a `_` else; the spawned
// consumer then receives the buffered value.
func TestSelectSendArm(t *testing.T) {
	const src = "fn consume(ch: chan[int]) {\n" +
		"  x := (<-ch)!\n" +
		"  print x\n" +
		"}\n" +
		"fn main() {\n" +
		"  c := chan[int](1)\n" +
		"  spawn consume(c)\n" +
		"  select {\n" +
		"    c <- 5 => { print 1 }\n" +
		"    _ => { print 0 }\n" +
		"  }\n" +
		"}"
	bin := buildConcurrent(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "1\n5\n" {
		t.Fatalf("stdout = %q, want %q (send arm buffered 5, consumer received it)", stdout, "1\n5\n")
	}
}
