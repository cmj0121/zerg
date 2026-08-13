package build

import "testing"

// RUN-based tests for match-expression teardown, the memory-safety fixes to the
// match/destructure emit lowering:
//
//   - Bug 1: a match arm that yields a value naming still-owned storage (an enum
//     payload binding, a borrowed variable, a field, a list) is retained/deep-copied
//     before it is consumed as owned, so it is not aliased and released twice. Each arm
//     body now flows through copyValue against the match's result type, exactly as the
//     if-/block-expression lowerings do.
//   - Bug 3: a non-trivial subject (a call, a producer) is hoisted into a single temp
//     so it is evaluated exactly once and a non-POD producer subject is owned by the
//     temp and released once rather than re-evaluated and leaked.
//
// Every program is compiled to C, linked under ASan+UBSan with the counting allocator
// swapped in (runProgramRTBalanced), and run: a pass asserts a clean exit, the exact
// stdout, AND a zero alloc/free balance — so the arm value is neither leaked nor freed
// twice. macOS ships no LeakSanitizer, so the balance assertion is the leak proof;
// ASan+UBSan catch the double-free / use-after-free the pre-fix code triggered.

// TestMatchArmEnumPayloadBalanced (Bug 1) covers a match arm yielding a managed-str enum
// payload binding: the arm retains the payload cell so the bound result and the subject's
// own drop each release it exactly once.
func TestMatchArmEnumPayloadBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"enum Msg { Empty; Text(str) }\n"+
			"fn main() -> Result[nil] {\n"+
			"\tm := Text(\"a\" + \"b\")\n"+
			"\ts := match m {\n"+
			"\t\tMsg.Empty => \"none\"\n"+
			"\t\tText(s) => s\n"+
			"\t}\n"+
			"\tprint s\n"+
			"\treturn nil\n}\n")
	if want := "ab\n"; got != want {
		t.Fatalf("enum payload arm: got %q, want %q", got, want)
	}
}

// TestMatchArmBorrowedStrBalanced (Bug 1) covers a match arm yielding a borrowed managed
// str variable: the arm retains the cell so both the variable and the bound result drop it.
func TestMatchArmBorrowedStrBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\ta := \"m\" + \"n\"\n"+
			"\tn := 1\n"+
			"\ts := match n {\n"+
			"\t\t0 => \"z\"\n"+
			"\t\t_ => a\n"+
			"\t}\n"+
			"\tprint s\n"+
			"\treturn nil\n}\n")
	if want := "mn\n"; got != want {
		t.Fatalf("borrowed str arm: got %q, want %q", got, want)
	}
}

// TestMatchArmRefPayloadBalanced (Bug 1) covers a match arm yielding a Ref enum payload:
// the arm retains the box (zrt_ref_copy) while the unrelated producer arm is moved, so the
// bound Ref and the subject's own drop release it once each.
func TestMatchArmRefPayloadBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"enum RefBox { Empty; Full(Ref[int]) }\n"+
			"fn main() -> Result[nil] {\n"+
			"\tm := Full(Ref(5))\n"+
			"\tr := match m {\n"+
			"\t\tRefBox.Empty => Ref(0)\n"+
			"\t\tFull(x) => x\n"+
			"\t}\n"+
			"\tprint deref(r)\n"+
			"\treturn nil\n}\n")
	if want := "5\n"; got != want {
		t.Fatalf("ref payload arm: got %q, want %q", got, want)
	}
}

// TestMatchArmListBalanced (Bug 1) covers a match arm yielding a borrowed list: the arm
// deep-copies the buffer so the bound result owns an independent list — without the copy
// the two holders alias and double-free the buffer.
func TestMatchArmListBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\txs := [1, 2, 3]\n"+
			"\tn := 1\n"+
			"\tys := match n {\n"+
			"\t\t0 => [9]\n"+
			"\t\t_ => xs\n"+
			"\t}\n"+
			"\tprint ys[0]\n"+
			"\treturn nil\n}\n")
	if want := "1\n"; got != want {
		t.Fatalf("list arm: got %q, want %q", got, want)
	}
}

// TestMatchArmGetterReturnBalanced (Bug 1) covers a getter that RETURNS a match whose arm
// yields the by-value param's payload: the arm retains it so the returned value outlives
// the param's drop at function exit.
func TestMatchArmGetterReturnBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"enum Msg { Empty; Text(str) }\n"+
			"fn get(m: Msg) -> str {\n"+
			"\treturn match m {\n"+
			"\t\tMsg.Empty => \"e\"\n"+
			"\t\tText(s) => s\n"+
			"\t}\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\tr := get(Text(\"x\" + \"y\"))\n"+
			"\tprint r\n"+
			"\treturn nil\n}\n")
	if want := "xy\n"; got != want {
		t.Fatalf("getter return arm: got %q, want %q", got, want)
	}
}

// TestMatchArmFunctionArgBalanced (Bug 1) covers a match at a function-argument site: the
// arm-level retain is what rescues the borrowed value, since the call's own copyValue sees
// a MatchExpr (not an lvalue) and moves it.
func TestMatchArmFunctionArgBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn id(s: str) -> str {\n\treturn s\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\ta := \"p\" + \"q\"\n"+
			"\tn := 1\n"+
			"\tr := id(match n {\n"+
			"\t\t0 => \"z\"\n"+
			"\t\t_ => a\n"+
			"\t})\n"+
			"\tprint r\n"+
			"\treturn nil\n}\n")
	if want := "pq\n"; got != want {
		t.Fatalf("function-arg arm: got %q, want %q", got, want)
	}
}

// TestMatchSubjectEvaluatedOnce (Bug 3) covers a side-effecting subject: hoisting it into
// a single temp makes it evaluate exactly once, so the print inside make() fires once
// rather than once per arm tag test / payload extraction.
func TestMatchSubjectEvaluatedOnce(t *testing.T) {
	got := runProgramRT(t,
		"enum OptI { None; Some(int) }\n"+
			"fn make() -> OptI {\n"+
			"\tprint \"called\"\n"+
			"\treturn Some(7)\n}\n"+
			"fn main() {\n"+
			"\tprint match make() {\n"+
			"\t\tOptI.None => 0\n"+
			"\t\tSome(v) => v\n"+
			"\t}\n}\n")
	if want := "called\n7\n"; got != want {
		t.Fatalf("subject eval-once: got %q, want %q", got, want)
	}
}

// TestMatchNonPodProducerSubjectBalanced (Bug 3) covers a non-POD producer subject: the
// hoisted temp owns the produced value and releases it at scope exit, so a Ref-bearing
// subject that no arm keeps is not leaked (and not re-produced per arm).
func TestMatchNonPodProducerSubjectBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"enum RefBox { Empty; Full(Ref[int]) }\n"+
			"fn make() -> RefBox {\n"+
			"\treturn Full(Ref(9))\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\tprint match make() {\n"+
			"\t\tRefBox.Empty => 0\n"+
			"\t\tFull(x) => deref(x)\n"+
			"\t}\n"+
			"\treturn nil\n}\n")
	if want := "9\n"; got != want {
		t.Fatalf("non-POD producer subject: got %q, want %q", got, want)
	}
}
