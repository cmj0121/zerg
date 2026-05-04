package syntax

// v0.7 Unit 5 — borrow check for the concurrency surface (send / recv /
// spawn / defer / select / anon_fn).
//
// PLAN.md §Borrow rules for concurrency pins:
//
//   * Send moves the value into the channel; the channel handle is read.
//   * Recv yields a fresh Option[T]; the channel handle is read.
//   * Spawn closure capture deep-copies composite captures at the spawn
//     site — each capture is a clone-read on the source binding, so the
//     spawning fn keeps full ownership of the originals.
//   * Anon-fn evaluation outside a spawn shares the same shape — the
//     closure value carries a deep-copy, so building the closure observes
//     (does not move) the source bindings.
//   * Defer body is checked as if it ran at the defer-statement site.
//   * Select arms are branch-merged like match arms — every outer
//     binding's end-state must agree across non-diverged arms.
//
// Helpers (checkSrc / checkErr / borrowErrSrc) come from typeck_test.go and
// borrow_test.go.

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// (A) Send semantics.
// ---------------------------------------------------------------------------

// Send of a composite value moves the source binding — using it after the
// send rejects with use-after-move.
func TestBorrowV07SendMovesComposite(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let ch := chan[list[int]]()\n" +
		"ch <- xs\n" +
		"print xs[0]\n" +
		"}\n"
	msg := borrowErrSrc(t, src, "use of moved value")
	if !strings.Contains(msg, `"xs"`) {
		t.Errorf("error %q does not name xs", msg)
	}
}

// Send of a primitive value never reports a borrow error — primitives copy
// freely.
func TestBorrowV07SendPrimitiveOK(t *testing.T) {
	src := "fn run() {\n" +
		"let ch := chan[int]()\n" +
		"ch <- 5\n" +
		"print 5\n" +
		"}\n"
	checkSrc(t, src)
}

// The channel handle itself is not moved by a send — sending twice on the
// same channel is fine.
func TestBorrowV07SendChannelNotMoved(t *testing.T) {
	src := "fn run() {\n" +
		"let ch := chan[int]()\n" +
		"ch <- 1\n" +
		"ch <- 2\n" +
		"}\n"
	checkSrc(t, src)
}

// Send of a fresh literal does not register against any source binding.
func TestBorrowV07SendLiteralOK(t *testing.T) {
	src := "fn run() {\n" +
		"let ch := chan[list[int]]()\n" +
		"ch <- [1, 2, 3]\n" +
		"}\n"
	checkSrc(t, src)
}

// Sending a moved binding reports use-after-move at the send site (the
// binding was already consumed earlier).
func TestBorrowV07SendOfMovedBindingRejects(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let ys := xs\n" +
		"let ch := chan[list[int]]()\n" +
		"ch <- xs\n" +
		"print ys[0]\n" +
		"}\n"
	borrowErrSrc(t, src, "use of moved value")
}

// ---------------------------------------------------------------------------
// (B) Recv semantics.
// ---------------------------------------------------------------------------

// Recv yields a fresh Option[T] value; the channel handle is read-only and
// remains usable after a receive.
func TestBorrowV07RecvDoesNotMoveChannel(t *testing.T) {
	src := "fn run() {\n" +
		"let ch := chan[int]()\n" +
		"let v1 := <- ch\n" +
		"let v2 := <- ch\n" +
		"print v1\n" +
		"print v2\n" +
		"}\n"
	checkSrc(t, src)
}

// The bound name from a recv is a fresh local — using it after binding is
// fine, and the source channel survives.
func TestBorrowV07RecvBindsFreshLocal(t *testing.T) {
	src := "fn run() {\n" +
		"let ch := chan[list[int]]()\n" +
		"let v := <- ch\n" +
		"print v\n" +
		"let w := <- ch\n" +
		"print w\n" +
		"}\n"
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// (C) Spawn / anon-fn capture semantics.
// ---------------------------------------------------------------------------

// Spawn of an anon-fn that captures a composite reads the source binding
// (deep-copy at closure construction). The source remains usable after.
func TestBorrowV07SpawnCapturesByClone(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"spawn fn() { print xs[0] }()\n" +
		"print xs[0]\n" +
		"}\n"
	checkSrc(t, src)
}

// Spawn of a named fn with a composite arg follows the regular fn-call
// shared-borrow rule — the source binding survives the call.
func TestBorrowV07SpawnFnArgIsSharedBorrow(t *testing.T) {
	src := "fn worker(ys: list[int]) { print ys[0] }\n" +
		"fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"spawn worker(xs)\n" +
		"print xs[0]\n" +
		"}\n"
	checkSrc(t, src)
}

// An anon-fn evaluated WITHOUT spawn still deep-copies its captures at
// construction; the source binding remains usable.
func TestBorrowV07AnonFnCaptureIsRead(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let f := fn() { print xs[0] }\n" +
		"print xs[0]\n" +
		"f()\n" +
		"}\n"
	checkSrc(t, src)
}

// Inside an anon-fn body, a captured composite is immutable from the
// closure's perspective — the body cannot move it out via a let-rebind.
func TestBorrowV07AnonFnCaptureCannotBeMovedFromBody(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let f := fn() { let ys := xs\nprint ys[0] }\n" +
		"f()\n" +
		"}\n"
	msg := borrowErrSrc(t, src, "cannot move borrowed value")
	if !strings.Contains(msg, `"xs"`) {
		t.Errorf("error %q does not name xs", msg)
	}
}

// Capturing an already-moved binding rejects at closure construction.
func TestBorrowV07AnonFnCaptureOfMovedRejects(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let ys := xs\n" +
		"let f := fn() { print xs[0] }\n" +
		"f()\n" +
		"print ys[0]\n" +
		"}\n"
	borrowErrSrc(t, src, "use of moved value")
}

// Spawn-of-IIFE that captures a composite — the original survives because
// captures are clone-reads.
func TestBorrowV07SpawnIifeCaptureSurvives(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let ys := [4, 5, 6]\n" +
		"spawn fn() { print xs[0]\nprint ys[0] }()\n" +
		"print xs[0]\n" +
		"print ys[0]\n" +
		"}\n"
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// (D) Defer semantics.
// ---------------------------------------------------------------------------

// Defer body sees in-scope bindings — referencing a live composite from a
// defer is a read at the defer site, like any other expression.
func TestBorrowV07DeferReadsInScopeBinding(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"defer print xs[0]\n" +
		"print xs[0]\n" +
		"}\n"
	checkSrc(t, src)
}

// A defer body that moves a binding registers the move against the
// surrounding fn scope — using the binding after the defer rejects.
func TestBorrowV07DeferBodyMoveRegisters(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"defer { let ys := xs\nprint ys[0] }\n" +
		"print xs[0]\n" +
		"}\n"
	borrowErrSrc(t, src, "use of moved value")
}

// Defer of a binding that has already been moved rejects at the defer's
// own walk.
func TestBorrowV07DeferOfMovedBindingRejects(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let ys := xs\n" +
		"defer print xs[0]\n" +
		"print ys[0]\n" +
		"}\n"
	borrowErrSrc(t, src, "use of moved value")
}

// ---------------------------------------------------------------------------
// (E) Select arm branch-merge.
// ---------------------------------------------------------------------------

// Select with arms that read the same outer binding agree on its end-state.
func TestBorrowV07SelectArmsReadOK(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let ch := chan[int]()\n" +
		"select {\n" +
		"v := <- ch -> { print xs[0]\nprint v }\n" +
		"_ -> { print xs[0] }\n" +
		"}\n" +
		"print xs[0]\n" +
		"}\n"
	checkSrc(t, src)
}

// Select-send of a composite moves the source binding on that arm; if the
// other arm reads the binding, branch states disagree and reject.
func TestBorrowV07SelectSendDisagreesWithRead(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let ch := chan[list[int]]()\n" +
		"select {\n" +
		"ch <- xs -> { print 1 }\n" +
		"_ -> { print xs[0] }\n" +
		"}\n" +
		"}\n"
	borrowErrSrc(t, src, "branch states disagree")
}

// Select-send of a composite that every arm consumes is admitted (all
// non-diverged arms agree on the moved end-state).
func TestBorrowV07SelectAllArmsConsumeOK(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let ys := [4, 5, 6]\n" +
		"let ch1 := chan[list[int]]()\n" +
		"let ch2 := chan[list[int]]()\n" +
		"select {\n" +
		"ch1 <- xs -> { print 1 }\n" +
		"ch2 <- xs -> { print 2 }\n" +
		"}\n" +
		"print ys[0]\n" +
		"}\n"
	checkSrc(t, src)
}

// Select-send moves the value; using it after the select rejects.
func TestBorrowV07SelectSendThenUseRejects(t *testing.T) {
	src := "fn run() {\n" +
		"let xs := [1, 2, 3]\n" +
		"let ch := chan[list[int]]()\n" +
		"select {\n" +
		"ch <- xs -> { print 1 }\n" +
		"ch <- xs -> { print 2 }\n" +
		"}\n" +
		"print xs[0]\n" +
		"}\n"
	borrowErrSrc(t, src, "use of moved value")
}

// Select recv-bind binds a fresh local for the arm body; the channel handle
// is not moved.
func TestBorrowV07SelectRecvBindFreshLocal(t *testing.T) {
	src := "fn run() {\n" +
		"let ch := chan[list[int]]()\n" +
		"select {\n" +
		"v := <- ch -> { print v[0] }\n" +
		"_ -> { print 0 }\n" +
		"}\n" +
		"let w := <- ch\n" +
		"print w\n" +
		"}\n"
	checkSrc(t, src)
}

// Select recv-discard does not bind a name; the channel handle survives.
func TestBorrowV07SelectRecvDiscardOK(t *testing.T) {
	src := "fn run() {\n" +
		"let ch := chan[int]()\n" +
		"select {\n" +
		"<- ch -> { print 1 }\n" +
		"_ -> { print 0 }\n" +
		"}\n" +
		"let w := <- ch\n" +
		"print w\n" +
		"}\n"
	checkSrc(t, src)
}

// Select with a diverging arm (return) does not require the diverged arm
// to agree on the move-state — the surviving arm sets the join state.
func TestBorrowV07SelectDivergingArmExempt(t *testing.T) {
	src := "fn run() -> int {\n" +
		"let xs := [1, 2, 3]\n" +
		"let ch := chan[list[int]]()\n" +
		"select {\n" +
		"ch <- xs -> { return 0 }\n" +
		"_ -> { print xs[0] }\n" +
		"}\n" +
		"return xs[0]\n" +
		"}\n"
	checkSrc(t, src)
}
