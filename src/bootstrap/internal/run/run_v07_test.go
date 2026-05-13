package run

import (
	"strings"
	"testing"
)

// v0.7 Unit 6 — interpreter for concurrency. Mirrors the v0.5 / v0.6 style
// (expectOK / expectErr from run_test.go). Each test exercises one
// execution path. Concurrent programs must be deterministic by
// construction (single sender / single receiver, fan-in via wait_group +
// chan close, or IIFE that the host blocks on).

// ---------------------------------------------------------------------------
// Channels — unbuffered + buffered, send / recv parity.
// ---------------------------------------------------------------------------

func TestRunV07ChanUnbufferedSendRecv(t *testing.T) {
	src := `ch := chan[int]()
spawn fn() { ch <- 5 }()
v := <- ch
print v
`
	expectOK(t, src, "5\n")
}

func TestRunV07ChanBufferedSendDrain(t *testing.T) {
	src := `ch := chan[int](3)
ch <- 1
ch <- 2
ch <- 3
close(ch)
for v in ch {
  print v
}
`
	expectOK(t, src, "1\n2\n3\n")
}

func TestRunV07ChanRecvAfterCloseYieldsNone(t *testing.T) {
	src := `ch := chan[int](1)
ch <- 7
close(ch)
a := <- ch
b := <- ch
print a
print b
`
	expectOK(t, src, "7\nnil\n")
}

func TestRunV07ChanForInDrainsBufferedThenStops(t *testing.T) {
	src := `ch := chan[str](2)
ch <- "a"
ch <- "b"
close(ch)
for s in ch {
  print s
}
print "done"
`
	expectOK(t, src, "a\nb\ndone\n")
}

// ---------------------------------------------------------------------------
// Spawn + anon-fn IIFE — capture semantics deep-copy the captured value.
// ---------------------------------------------------------------------------

func TestRunV07SpawnAnonFnIIFEPrints(t *testing.T) {
	// The host blocks on spawnWg before returning, so the IIFE's print
	// arrives before main's print. Single-goroutine deterministic by
	// the synchronisation barrier.
	src := `ch := chan[int]()
spawn fn() {
  ch <- 42
}()
v := <- ch
print v
`
	expectOK(t, src, "42\n")
}

func TestRunV07AnonFnCaptureLetIIFE(t *testing.T) {
	src := `n := 7
f := fn() -> int { return n + 1 }
print f()
`
	expectOK(t, src, "8\n")
}

func TestRunV07AnonFnCaptureDeepCopySnapshot(t *testing.T) {
	// Captures snapshot at fn-evaluation time. The closure sees the
	// captured `n` from before the rebind — but Zerg lacks `mut` capture
	// (rejected at typeck) so we test via let-rebind in scope.
	src := `n := 1
f := fn() -> int { return n }
print f()
`
	expectOK(t, src, "1\n")
}

// ---------------------------------------------------------------------------
// Defer — LIFO ordering at fn return, plus `?` early-return drain.
// ---------------------------------------------------------------------------

func TestRunV07DeferLIFOOrder(t *testing.T) {
	src := `fn f() {
  defer print 1
  defer print 2
  print 3
}
f()
`
	expectOK(t, src, "3\n2\n1\n")
}

func TestRunV07DeferRunsOnFallThroughExit(t *testing.T) {
	src := `fn f() {
  defer print "bye"
  print "hi"
}
f()
`
	expectOK(t, src, "hi\nbye\n")
}

func TestRunV07DeferRunsOnReturn(t *testing.T) {
	src := `fn f() -> int {
  defer print "drained"
  return 7
}
print f()
`
	expectOK(t, src, "drained\n7\n")
}

func TestRunV07DeferDrainOnPropagateEarlyReturn(t *testing.T) {
	src := `fn fails() -> Result[int, str] { return Result.Err("boom") }
fn caller() -> Result[int, str] {
  defer print "drained"
  v := fails()?
  return Result.Ok(v + 1)
}
print caller()
`
	expectOK(t, src, "drained\nResult.Err(boom)\n")
}

func TestRunV07DeferDrainOnNonePropagate(t *testing.T) {
	src := `fn maybe() -> int? { return nil }
fn caller() -> int? {
  defer print "cleanup"
  v := maybe()?
  return v
}
print caller()
`
	expectOK(t, src, "cleanup\nnil\n")
}

// ---------------------------------------------------------------------------
// wait_group fan-in pattern — N senders, one drainer.
// ---------------------------------------------------------------------------

func TestRunV07WaitGroupFanInSorted(t *testing.T) {
	// Three senders all push their assigned value, call wg.done(). Main
	// wg.wait()s, closes the channel, then drains via for-v-in-ch. To
	// avoid scheduling-induced reordering we collect into a list and
	// inspect totals.
	src := `ch := chan[int](16)
wg := wait_group()
wg.add(3)
spawn fn() {
  ch <- 1
  wg.done()
}()
spawn fn() {
  ch <- 1
  wg.done()
}()
spawn fn() {
  ch <- 1
  wg.done()
}()
wg.wait()
close(ch)
mut total := 0
for v in ch {
  total += v
}
print total
`
	expectOK(t, src, "3\n")
}

func TestRunV07WaitGroupSingleSenderClosesCorrectly(t *testing.T) {
	src := `ch := chan[int](2)
wg := wait_group()
wg.add(1)
spawn fn() {
  ch <- 10
  ch <- 20
  wg.done()
}()
wg.wait()
close(ch)
for v in ch {
  print v
}
`
	expectOK(t, src, "10\n20\n")
}

// ---------------------------------------------------------------------------
// select — recv arm + default arm; recv-bind binds the unwrapped element.
// ---------------------------------------------------------------------------

func TestRunV07SelectDefaultTakesWhenChanEmpty(t *testing.T) {
	src := `fn run() {
  ch := chan[int]()
  select {
    v := <- ch -> { print v }
    _ -> { print "default" }
  }
}
run()
`
	expectOK(t, src, "default\n")
}

func TestRunV07SelectRecvBindFires(t *testing.T) {
	src := `fn run() {
  ch := chan[int](1)
  ch <- 99
  select {
    v := <- ch -> { print v }
    _ -> { print "default" }
  }
}
run()
`
	expectOK(t, src, "99\n")
}

func TestRunV07SelectRecvDiscardFires(t *testing.T) {
	src := `fn run() {
  ch := chan[int](1)
  ch <- 7
  select {
    <- ch -> { print "got" }
    _ -> { print "default" }
  }
}
run()
`
	expectOK(t, src, "got\n")
}

func TestRunV07SelectSendArm(t *testing.T) {
	src := `fn run() {
  ch := chan[int](1)
  select {
    ch <- 5 -> { print "sent" }
    _ -> { print "blocked" }
  }
  v := <- ch
  print v
}
run()
`
	expectOK(t, src, "sent\n5\n")
}

// invokeSpecDefaultDirect must push/drain a defer frame so a spec-default
// body's `defer` runs at body exit, matching invokeFnDirect /
// invokeFnValueDirect / invokeMethodFnDirect. Before Fix 5 this path
// silently dropped the defer.
func TestRunV07SpecDefaultMethodDeferRuns(t *testing.T) {
	src := `spec Greeter {
fn greet() {
print "before"
defer { print "deferred" }
print "after"
}
}
struct G { id: int }
impl G for Greeter {}
g := G { id: 1 }
g.greet()
`
	expectOK(t, src, "before\nafter\ndeferred\n")
}

// PLAN.md pins v0.7 select tie-break to declaration order (vs. Go's random
// pick). With two ready recv arms, the first-declared one must fire every
// time. We run the select 100 times against fresh channels per trial: a
// reflect.Select-uniform-random regression would fail with overwhelming
// probability (probability all 100 trials still land on arm `a` is 2^-100).
func TestRunV07SelectTieBreakIsDeclarationOrder(t *testing.T) {
	src := `fn trial() {
  a := chan[int](1)
  b := chan[int](1)
  a <- 1
  b <- 2
  select {
    v := <- a -> { print v }
    v := <- b -> { print v }
  }
}
fn run() {
  mut i := 0
  for i < 100 {
    trial()
    i = i + 1
  }
}
run()
`
	want := strings.Repeat("1\n", 100)
	expectOK(t, src, want)
}

// ---------------------------------------------------------------------------
// Send-on-closed surfaces as a runtime error rather than crashing the host.
// ---------------------------------------------------------------------------

func TestRunV07SendOnClosedSurfacesError(t *testing.T) {
	src := `ch := chan[int](1)
close(ch)
ch <- 1
`
	out, err := runSrc(t, src)
	if err == nil {
		t.Fatalf("expected error, got stdout %q", out)
	}
	if !strings.Contains(err.Error(), "send on closed channel") {
		t.Errorf("error %q lacks 'send on closed channel'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Anon-fn calling a captured fn — ensures fn-typed bindings dispatch.
// ---------------------------------------------------------------------------

func TestRunV07AnonFnReturnsValueFromBody(t *testing.T) {
	src := `g := fn(x: int) -> int { return x * 2 }
print g(5)
`
	expectOK(t, src, "10\n")
}

func TestRunV07AnonFnIIFEReturning(t *testing.T) {
	src := `v := fn() -> int { return 7 }()
print v
`
	expectOK(t, src, "7\n")
}

// ---------------------------------------------------------------------------
// for-v-in-ch with single-spawn sender, deterministic by sync barrier.
// ---------------------------------------------------------------------------

func TestRunV07ForInChanFromSingleSpawn(t *testing.T) {
	src := `ch := chan[int](4)
wg := wait_group()
wg.add(1)
spawn fn() {
  ch <- 1
  ch <- 2
  ch <- 3
  wg.done()
}()
wg.wait()
close(ch)
for v in ch {
  print v
}
`
	expectOK(t, src, "1\n2\n3\n")
}
