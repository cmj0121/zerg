package syntax

import (
	"strings"
	"testing"
)

// v0.8 Unit 2 — typeck-side tests for the __builtin fn-decl registry.
//
// The registry is closed at compile time; these tests pin the validation
// behaviour for both the happy path (known name + matching signature) and
// the two reject paths (unknown name, signature mismatch). Borrow check is
// exercised implicitly through CheckBundle: a __builtin fn with a non-nil
// body would crash the body walker, so a passing CheckBundle on a __builtin
// fn confirms the body-skip path.

// checkStdlibSrc lexes + parses src as a v0.8 stdlib file (so __builtin is
// admitted) and runs the full single-program type check. Returns the typeck
// error if any.
func checkStdlibSrc(t *testing.T, src string) error {
	t.Helper()
	tokens, err := lexWithVersion([]byte(src), 0, 8)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := ParseWithOptions(tokens, ParseOptions{InStdlibFile: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Check(prog)
}

// --- happy path -----------------------------------------------------------

func TestTypeckBuiltinKnownNameMatchingSig(t *testing.T) {
	// Pick a builtin that doesn't depend on Result/IoError synth so the test
	// stays self-contained: math_abs is (int) -> int.
	src := "fn abs(x: int) -> int __builtin math_abs\n"
	if err := checkStdlibSrc(t, src); err != nil {
		t.Fatalf("typeck: %v", err)
	}
}

func TestTypeckBuiltinAllMathEntries(t *testing.T) {
	// Pin every std/math entry — these are the registry entries with shapes
	// that don't reach for Option/Result so we can validate them without
	// pulling in user-declared error enums.
	cases := []struct {
		name string
		src  string
	}{
		{"abs", "fn abs(x: int) -> int __builtin math_abs\n"},
		{"min", "fn min(a: int, b: int) -> int __builtin math_min\n"},
		{"max", "fn max(a: int, b: int) -> int __builtin math_max\n"},
		{"gcd", "fn gcd(a: int, b: int) -> int __builtin math_gcd\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkStdlibSrc(t, tc.src); err != nil {
				t.Fatalf("typeck: %v", err)
			}
		})
	}
}

func TestTypeckBuiltinStringsNonResult(t *testing.T) {
	// Pin the std/strings entries that don't return Result so they can be
	// validated without user-declared error enums.
	cases := []struct {
		name string
		src  string
	}{
		{"trim", "fn trim(s: str) -> str __builtin strings_trim\n"},
		{"starts_with", "fn starts_with(s: str, prefix: str) -> bool __builtin strings_starts_with\n"},
		{"ends_with", "fn ends_with(s: str, suffix: str) -> bool __builtin strings_ends_with\n"},
		{"contains", "fn contains(s: str, needle: str) -> bool __builtin strings_contains\n"},
		{"replace", "fn replace(s: str, old: str, new: str) -> str __builtin strings_replace\n"},
		{"to_upper", "fn to_upper(s: str) -> str __builtin strings_to_upper\n"},
		{"to_lower", "fn to_lower(s: str) -> str __builtin strings_to_lower\n"},
		{"split", "fn split(s: str, sep: str) -> list[str] __builtin strings_split\n"},
		{"join", "fn join(parts: list[str], sep: str) -> str __builtin strings_join\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkStdlibSrc(t, tc.src); err != nil {
				t.Fatalf("typeck: %v", err)
			}
		})
	}
}

// --- reject paths ---------------------------------------------------------

func TestTypeckBuiltinUnknownName(t *testing.T) {
	src := "fn foo() -> int __builtin not_a_real_builtin\n"
	err := checkStdlibSrc(t, src)
	if err == nil {
		t.Fatalf("expected typeck error for unknown builtin")
	}
	if !strings.Contains(err.Error(), "unknown builtin") {
		t.Errorf("error %q does not mention 'unknown builtin'", err.Error())
	}
	if !strings.Contains(err.Error(), "not_a_real_builtin") {
		t.Errorf("error %q does not name the offending bareword", err.Error())
	}
}

func TestTypeckBuiltinSignatureMismatchParamType(t *testing.T) {
	// math_abs takes (int) -> int; pass a string instead.
	src := "fn abs(x: str) -> int __builtin math_abs\n"
	err := checkStdlibSrc(t, src)
	if err == nil {
		t.Fatalf("expected typeck error for signature mismatch")
	}
	if !strings.Contains(err.Error(), "signature mismatch") {
		t.Errorf("error %q does not mention 'signature mismatch'", err.Error())
	}
	if !strings.Contains(err.Error(), "math_abs") {
		t.Errorf("error %q does not name the offending bareword", err.Error())
	}
}

func TestTypeckBuiltinSignatureMismatchReturnType(t *testing.T) {
	// math_abs returns int; declare bool instead.
	src := "fn abs(x: int) -> bool __builtin math_abs\n"
	err := checkStdlibSrc(t, src)
	if err == nil {
		t.Fatalf("expected typeck error for return-type mismatch")
	}
	if !strings.Contains(err.Error(), "signature mismatch") {
		t.Errorf("error %q does not mention 'signature mismatch'", err.Error())
	}
}

func TestTypeckBuiltinSignatureMismatchArity(t *testing.T) {
	// math_min takes (int, int); pass a single int.
	src := "fn min(a: int) -> int __builtin math_min\n"
	err := checkStdlibSrc(t, src)
	if err == nil {
		t.Fatalf("expected typeck error for arity mismatch")
	}
	if !strings.Contains(err.Error(), "signature mismatch") {
		t.Errorf("error %q does not mention 'signature mismatch'", err.Error())
	}
}

// --- borrow-check guard ---------------------------------------------------

func TestTypeckBuiltinBorrowSkipsBody(t *testing.T) {
	// The body-skip is implicit: if the borrow walker tried to walk a
	// __builtin fn's nil body it would NPE. A passing typeck on a __builtin
	// confirms the borrow walker skipped the body.
	src := "fn abs(x: int) -> int __builtin math_abs\n"
	if err := checkStdlibSrc(t, src); err != nil {
		t.Fatalf("typeck: %v", err)
	}
}
