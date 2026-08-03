package sema

import (
	"math"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// The folder answers the SAME arithmetic the emitted program performs
// (docs/core/types.md). Nothing else pins that: a fold and a runtime evaluation of one
// expression are two code paths, and while they disagreed `-7 % 3` was -1 in an enum
// discriminant and 2 everywhere else — one language rule with two answers inside one
// compiler, on the path no gate could see.

func TestFoldDivAndModAreEuclidean(t *testing.T) {
	cases := []struct {
		name    string
		op      token.Kind
		a, b, w int64
	}{
		// the remainder is never negative, whatever the sign of either operand
		{"-7 / 3", token.Slash, -7, 3, -3},
		{"-7 % 3", token.Percent, -7, 3, 2},
		{"7 / -3", token.Slash, 7, -3, -2},
		{"7 % -3", token.Percent, 7, -3, 1},
		{"-7 / -3", token.Slash, -7, -3, 3},
		{"-7 % -3", token.Percent, -7, -3, 2},
		// a non-negative pair is C's answer too, and must not have moved
		{"7 / 3", token.Slash, 7, 3, 2},
		{"7 % 3", token.Percent, 7, 3, 1},
		// the one division that overflows has no quotient, but does have a remainder
		{"INT64_MIN % -1", token.Percent, math.MinInt64, -1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := foldBinary(tc.op, intConst(tc.a), intConst(tc.b))
			if !ok {
				t.Fatalf("%s did not fold", tc.name)
			}
			if got.I != tc.w {
				t.Errorf("%s = %d, want %d", tc.name, got.I, tc.w)
			}
		})
	}
}

// TestFoldEuclideanIdentity is the property the sign rule exists to give: the quotient
// and remainder reconstruct the dividend, for every combination of signs.
func TestFoldEuclideanIdentity(t *testing.T) {
	for _, a := range []int64{-9, -7, -1, 0, 1, 7, 9} {
		for _, b := range []int64{-3, -2, -1, 1, 2, 3} {
			q, okq := foldBinary(token.Slash, intConst(a), intConst(b))
			r, okr := foldBinary(token.Percent, intConst(a), intConst(b))
			if !okq || !okr {
				continue // INT64_MIN / -1 is the only non-folding pair, and it is not here
			}
			if q.I*b+r.I != a {
				t.Errorf("(%d / %d) * %d + %d %% %d = %d, want %d", a, b, b, a, b, q.I*b+r.I, a)
			}
			if r.I < 0 || (b > 0 && r.I >= b) || (b < 0 && r.I >= -b) {
				t.Errorf("%d %% %d = %d, outside [0, |%d|)", a, b, r.I, b)
			}
		}
	}
}

// TestFoldRefusesWhatTheProgramWouldRaise: an operation that aborts at runtime has no
// constant value either. Not folding is the right refusal rather than a diagnostic here
// — the caller reports at the use site, which is where the reader can see what the
// constant was wanted for.
func TestFoldRefusesWhatTheProgramWouldRaise(t *testing.T) {
	cases := []struct {
		name string
		op   token.Kind
		a, b int64
	}{
		{"addition overflow", token.Plus, math.MaxInt64, 1},
		{"subtraction overflow", token.Minus, math.MinInt64, 1},
		{"multiplication overflow", token.Star, math.MaxInt64, 2},
		{"division by zero", token.Slash, 7, 0},
		{"remainder by zero", token.Percent, 7, 0},
		{"the quotient with no representation", token.Slash, math.MinInt64, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if v, ok := foldBinary(tc.op, intConst(tc.a), intConst(tc.b)); ok {
				t.Errorf("%s folded to %d; it raises at runtime", tc.name, v.I)
			}
		})
	}
	if v, ok := foldUnary(token.Minus, intConst(math.MinInt64)); ok {
		t.Errorf("negating the minimum folded to %d; it raises at runtime", v.I)
	}
}

// TestFoldWrappingFormsStillWrap is the other half of the pair: `+%`, `-%` and `*%` are
// the forms that ASK for roll-over, so they fold where the checked three refuse.
func TestFoldWrappingFormsStillWrap(t *testing.T) {
	cases := []struct {
		name    string
		op      token.Kind
		a, b, w int64
	}{
		{"+%", token.PlusMod, math.MaxInt64, 1, math.MinInt64},
		{"-%", token.MinusMod, math.MinInt64, 1, math.MaxInt64},
		{"*%", token.StarMod, math.MinInt64, -1, math.MinInt64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := foldBinary(tc.op, intConst(tc.a), intConst(tc.b))
			if !ok {
				t.Fatalf("%s did not fold; the wrapping forms are the ones that may", tc.name)
			}
			if got.I != tc.w {
				t.Errorf("%s = %d, want %d", tc.name, got.I, tc.w)
			}
		})
	}
	if got, ok := foldUnary(token.MinusMod, intConst(math.MinInt64)); !ok || got.I != math.MinInt64 {
		t.Errorf("-%% of the minimum = %d, %v; want %d, true", got.I, ok, int64(math.MinInt64))
	}
}

// TestFoldedConstantReachesTheDiscriminant is the end-to-end half: an enum discriminant
// is one of the few places a folded constant is OBSERVABLE, and it was where the two
// answers diverged.
func TestFoldedConstantReachesTheDiscriminant(t *testing.T) {
	wantOK(t, "enum E {\n  A = -7 % 3\n  B = 1\n}\n\nfn main() {\n  print int(E.B)\n}")
	// and an overflowing one is refused where it is used, rather than wrapping in silence
	wantErr(t, "enum E {\n  A = 9223372036854775807 + 1\n  B = 1\n}\n\nfn main() {\n  print int(E.B)\n}",
		"constant integer")
}
