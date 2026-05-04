package syntax

import (
	"testing"
)

// v0.7 Unit 4 — typeck for the `select` statement. Coverage:
//
//   - Each of the four arm shapes typechecks (recv-bind, recv-discard, send,
//     default).
//   - Recv-bind binds the inner element type (T, not Option[T]) and the
//     binding is scoped to the arm body only.
//   - Send arm enforces value/element type matching with the v0.6 lift.
//   - Multiple default arms are rejected.
//   - Non-channel operands are rejected (recv and send).
//   - Send value type mismatch is rejected.
//   - Bound name is not visible outside the arm.
//   - A nested select inside an arm body works.

// --- recv-bind ----------------------------------------------------------

func TestV07SelectRecvBindOK(t *testing.T) {
	src := `fn run(ch: chan[int]) {
	select {
		v := <- ch -> { print v }
	}
}
`
	checkSrc(t, src)
}

func TestV07SelectRecvBindBindsElementType(t *testing.T) {
	src := `fn run(ch: chan[int]) {
	select {
		v := <- ch -> { let x: int = v }
	}
}
`
	checkSrc(t, src)
}

func TestV07SelectRecvBindStringChan(t *testing.T) {
	src := `fn run(ch: chan[str]) {
	select {
		s := <- ch -> { print s }
	}
}
`
	checkSrc(t, src)
}

func TestV07SelectRecvBindNonChanRejects(t *testing.T) {
	src := `fn run() {
	select {
		v := <- 42 -> { print v }
	}
}
`
	checkErr(t, src, "select recv requires chan")
}

func TestV07SelectRecvBindNotVisibleAfterArm(t *testing.T) {
	src := `fn run(ch: chan[int]) {
	select {
		v := <- ch -> { print v }
	}
	print v
}
`
	checkErr(t, src, "undefined")
}

// --- recv-discard -------------------------------------------------------

func TestV07SelectRecvDiscardOK(t *testing.T) {
	src := `fn run(ch: chan[int]) {
	select {
		<- ch -> { print 1 }
	}
}
`
	checkSrc(t, src)
}

func TestV07SelectRecvDiscardNonChanRejects(t *testing.T) {
	src := `fn run() {
	select {
		<- 42 -> { print 1 }
	}
}
`
	checkErr(t, src, "select recv requires chan")
}

// --- send ---------------------------------------------------------------

func TestV07SelectSendOK(t *testing.T) {
	src := `fn run(ch: chan[int]) {
	select {
		ch <- 5 -> { print 1 }
	}
}
`
	checkSrc(t, src)
}

func TestV07SelectSendStringChan(t *testing.T) {
	src := `fn run(ch: chan[str]) {
	select {
		ch <- "hi" -> { print 1 }
	}
}
`
	checkSrc(t, src)
}

func TestV07SelectSendNonChanRejects(t *testing.T) {
	src := `fn run(x: int) {
	select {
		x <- 5 -> { print 1 }
	}
}
`
	checkErr(t, src, "select send requires chan")
}

func TestV07SelectSendTypeMismatchRejects(t *testing.T) {
	src := `fn run(ch: chan[int]) {
	select {
		ch <- "x" -> { print 1 }
	}
}
`
	checkErr(t, src, "cannot send value of type str on chan[int]")
}

func TestV07SelectSendOptionLifts(t *testing.T) {
	// v0.6 T → T? lift fires on send arm same as plain SendStmt.
	src := `fn run(ch: chan[int?]) {
	select {
		ch <- 5 -> { print 1 }
	}
}
`
	checkSrc(t, src)
}

// --- default ------------------------------------------------------------

func TestV07SelectDefaultOnly(t *testing.T) {
	src := `fn run() {
	select {
		_ -> { print 1 }
	}
}
`
	checkSrc(t, src)
}

func TestV07SelectMixedAllFourArms(t *testing.T) {
	src := `fn run(a: chan[int], b: chan[int], c: chan[int]) {
	select {
		v := <- a -> { print v }
		<- b -> { print 2 }
		c <- 5 -> { print 3 }
		_ -> { print 4 }
	}
}
`
	checkSrc(t, src)
}

func TestV07SelectMultipleDefaultRejects(t *testing.T) {
	src := `fn run() {
	select {
		_ -> { print 1 }
		_ -> { print 2 }
	}
}
`
	checkErr(t, src, "select can have at most one default arm")
}

// --- arm body shadowing / visibility ------------------------------------

func TestV07SelectRecvBindArmBodyUsesBinding(t *testing.T) {
	// The bound name is the unwrapped element type, not Option.
	src := `fn run(ch: chan[int]) {
	select {
		v := <- ch -> {
			let doubled: int = v + 1
			print doubled
		}
	}
}
`
	checkSrc(t, src)
}

func TestV07SelectRecvBindReusesNameAcrossArms(t *testing.T) {
	// The same name in two recv-bind arms is fine — each arm has its own
	// scope rooted at the parent.
	src := `fn run(a: chan[int], b: chan[int]) {
	select {
		v := <- a -> { print v }
		v := <- b -> { print v }
	}
}
`
	checkSrc(t, src)
}

// --- nesting ------------------------------------------------------------

func TestV07SelectNestedInArmBody(t *testing.T) {
	src := `fn run(a: chan[int], b: chan[int]) {
	select {
		v := <- a -> {
			select {
				w := <- b -> { print v + w }
			}
		}
	}
}
`
	checkSrc(t, src)
}

// --- reservation --------------------------------------------------------

func TestV07SelectRecvBindReservedNameRejects(t *testing.T) {
	src := `fn run(ch: chan[int]) {
	select {
		chan := <- ch -> { print 1 }
	}
}
`
	checkErr(t, src, "reserved")
}

// --- recv-bind body sees outer immutables -------------------------------

func TestV07SelectArmBodyClosesOverOuterLet(t *testing.T) {
	src := `fn run(ch: chan[int]) {
	let outer := 100
	select {
		v := <- ch -> { print v + outer }
	}
}
`
	checkSrc(t, src)
}
