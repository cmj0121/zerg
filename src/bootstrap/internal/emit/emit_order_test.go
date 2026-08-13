package emit

import (
	"strings"
	"testing"
)

// The two effectful functions every case below combines. They print their own name, so
// the ORDER the program ran them in is the program's output — which is the only way to
// see a bug whose whole nature is that C picked an order and never said so.
const orderProlog = "fn a() -> int {\n print \"a\"\n return 1\n}\n" +
	"fn b() -> int {\n print \"b\"\n return 2\n}\n" +
	"fn c() -> int {\n print \"c\"\n return 4\n}\n"

// TestOrderSequencesEffectfulOperands is the shape assertion: a combining form whose
// later operands can run code hands each of them to its own temp first, so the C it
// produces has no unspecified order left in it.
func TestOrderSequencesEffectfulOperands(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"binary", "fn main() { print a() + b() }"},
		{"call-args", "fn f(p: int, q: int) -> int { return p + q }\nfn main() { print f(a(), b()) }"},
		{"construct", "struct P {\n pub x: int\n pub y: int\n}\nfn main() { print P(a(), b()).x }"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := emitC(t, orderProlog+tc.src)
			if n := strings.Count(code, "__auto_type"); n != 2 {
				t.Fatalf("want both operands materialized, got %d temps\n---\n%s", n, code)
			}
		})
	}
}

// TestOrderLeavesSettledFormsAlone holds the trigger narrow. A form whose later operands
// cannot run code gains nothing from a temp, and the SHORT-CIRCUIT operators must not get
// one at all: sequencing `and`'s right side into a temp would evaluate it always, which is
// not a slower program but a different one.
func TestOrderLeavesSettledFormsAlone(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"literals", "fn main() { print 1 + 2 }"},
		{"names", "fn main() {\n x := 3\n y := 4\n print x + y\n}"},
		{"effect-first-only", "fn main() { print a() + 1 }"},
		{"and", "fn t() -> bool { return true }\nfn main() { print t() and t() }"},
		{"or", "fn t() -> bool { return true }\nfn main() { print t() or t() }"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := emitC(t, orderProlog+tc.src)
			if strings.Contains(code, "__auto_type") {
				t.Fatalf("form needed no ordering but was sequenced\n---\n%s", code)
			}
		})
	}
}

// TestOrderRunsLeftToRight is the assertion that matters: the program SAYS which order it
// ran its operands in, and it is the source's. A shape test alone would pass on a compiler
// that sequenced the temps in the wrong order.
func TestOrderRunsLeftToRight(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"binary", "fn main() { print a() + b() }", "a\nb\n3\n"},
		{"nested-binary", "fn main() { print a() + b() * c() }", "a\nb\nc\n9\n"},
		{
			"call-args",
			"fn f(p: int, q: int, r: int) -> int { return p + q + r }\nfn main() { print f(a(), b(), c()) }",
			"a\nb\nc\n7\n",
		},
		{
			"construct",
			"struct P {\n pub x: int\n pub y: int\n}\nfn main() {\n p := P(a(), b())\n print p.x + p.y\n}",
			"a\nb\n3\n",
		},
		{
			"method",
			"struct P {\n pub x: int\n}\nimpl P {\n fn add(k: int) -> int { return this.x + k }\n}\n" +
				"fn mk() -> P {\n print \"mk\"\n return P(1)\n}\nfn main() { print mk().add(b()) }",
			"mk\nb\n3\n",
		},
		{
			// the right side of `and` is SKIPPED, not sequenced — the whole point of the
			// operator, and the one thing this change could have quietly taken away.
			"short-circuit",
			"fn no() -> bool {\n print \"no\"\n return false\n}\n" +
				"fn yes() -> bool {\n print \"yes\"\n return true\n}\n" +
				"fn main() { print no() and yes() }",
			"no\nfalse\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := emitC(t, orderProlog+tc.src)
			if got := compileAndRun(t, cc, code); got != tc.want {
				t.Fatalf("output = %q, want %q\n--- C ---\n%s", got, tc.want, code)
			}
		})
	}
}
