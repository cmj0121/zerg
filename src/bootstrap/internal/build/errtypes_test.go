package build

import "testing"

// TestErrorRaiseGuardMatch is the end-to-end oracle for the fixed built-in error set
// (docs/errors.md, GRAMMAR group 8): a function raises a named error, the caller's
// `guard` demotes the abort to a Result, a `match` destructures it, and `is` distinguishes
// the kind while `.message()` reads the message. It confirms the kind survives the abort
// round-trip.
func TestErrorRaiseGuardMatch(t *testing.T) {
	src := "fn risky(which: int) -> int {\n" +
		"\tif which == 1 {\n\t\traise ValueError(\"bad value\")\n\t}\n" +
		"\tif which == 2 {\n\t\traise IOError(\"io down\")\n\t}\n" +
		"\treturn 42\n}\n" +
		"fn kind_of(e: Err) -> int {\n" +
		"\tprint e.message()\n" +
		"\tif e is ValueError { return 1 }\n" +
		"\tif e is IOError { return 2 }\n" +
		"\treturn 0\n}\n" +
		"fn classify(which: int) -> int {\n" +
		"\tr := guard { risky(which) }\n" +
		"\treturn match r {\n\t\tLeft(v) => v\n\t\tRight(e) => kind_of(e)\n\t}\n}\n" +
		"fn main() {\n\tprint classify(1)\n\tprint classify(2)\n\tprint classify(0)\n}\n"
	got := runProgramRT(t, src)
	if want := "bad value\n1\nio down\n2\n42\n"; got != want {
		t.Fatalf("error raise/guard/match: got %q, want %q", got, want)
	}
}

// TestErrorGuardDemotesIntrinsic covers a guard-demoted INTRINSIC abort: the runtime's
// own `int(s)` ValueError (and a checked-conversion OverflowError) reify to the built-in
// kinds, so a `guard { … } ?? default` recovers a value rather than crashing.
func TestErrorGuardDemotesIntrinsic(t *testing.T) {
	src := "fn main() {\n" +
		"\tprint guard { int(\"xx\") } ?? -1\n" + // ValueError -> -1
		"\tprint guard { int(\"42\") } ?? -1\n" + // parses -> 42
		"\tprint guard { byte(300) } ?? -1\n" + // OverflowError -> -1
		"}\n"
	got := runProgramRT(t, src)
	if want := "-1\n42\n-1\n"; got != want {
		t.Fatalf("guard-demoted intrinsic: got %q, want %q", got, want)
	}
}

// TestErrorKindDistinguished pins that the built-in kinds are DISTINGUISHABLE by `is`
// (not merely constructible): a directly built ValueError is not an IOError, and the
// kind rides through a raise+guard so a recovered error reports the same kind.
func TestErrorKindDistinguished(t *testing.T) {
	src := "fn label(e: Err) -> int {\n" +
		"\tif e is ValueError { return 1 }\n" +
		"\tif e is OverflowError { return 2 }\n" +
		"\tif e is IOError { return 3 }\n" +
		"\treturn 0\n}\n" +
		"fn main() {\n" +
		"\tprint label(ValueError(\"a\"))\n" +
		"\tprint label(OverflowError(\"b\"))\n" +
		"\tprint label(IOError(\"c\"))\n" +
		"\tprint label(KeyError(\"d\"))\n" + // an unlisted kind falls through
		"}\n"
	got := runProgramRT(t, src)
	if want := "1\n2\n3\n0\n"; got != want {
		t.Fatalf("error kind discrimination: got %q, want %q", got, want)
	}
}
