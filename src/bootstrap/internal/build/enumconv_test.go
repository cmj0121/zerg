package build

import "testing"

// TestEnumDiscriminantRoundTrip is the end-to-end oracle for the enum discriminant
// surface (docs/core/types.md, GRAMMAR group 7): `int(v)` reads a C-style enum's stored
// discriminant, `E.of(n)` reverses it — `Some(variant)` on a match, `None` otherwise —
// and `E.of(int(v))` round-trips. The enum `Red = 1; Green; Blue = 10` exercises the
// explicit + continued discriminants.
func TestEnumDiscriminantRoundTrip(t *testing.T) {
	src := "enum Color {\n\tRed = 1\n\tGreen\n\tBlue = 10\n}\n" +
		"fn main() {\n" +
		"\tprint int(Color.Green)\n" + // 2 (continued from Red = 1)
		"\tprint int(Color.Blue)\n" + // 10 (explicit)
		"\tprint int(Color.of(10) ?? Color.Red)\n" + // of(10) = Some(Blue) -> 10
		"\tprint int(Color.of(99) ?? Color.Red)\n" + // of(99) = None -> Red = 1
		"\tprint int(Color.of(int(Color.Green)) ?? Color.Red)\n" + // round-trip -> 2
		"}\n"
	got := runProgramRT(t, src)
	if want := "2\n10\n10\n1\n2\n"; got != want {
		t.Fatalf("enum discriminant round-trip: got %q, want %q", got, want)
	}
}
