package build

import "testing"

// These tests cover copy/drop of a NON-boxed Optional carrier whose element is non-POD
// (a `str?`/`Ref[T]?`/`list[T]?`, or an optional field on a struct). Before this fix such
// a carrier fell through to the "copying a str? is not supported" gate. Each program is
// compiled to C, linked under ASan+UBSan with the counting allocator, and asserted to
// exit cleanly with a ZERO alloc/free balance (no leak, no double-free) — the if-let
// unwrap binds a BORROW of the payload, and only the Opt owner releases it once.

// TestNonPODOptIfLetStr: an if-let over a str-producing optional binds the unwrapped
// non-POD string on the present path and skips it on the absent path, leak-balanced.
func TestNonPODOptIfLetStr(t *testing.T) {
	src := "fn find(k: int) -> str? {\n" +
		"\treturn (\"h\" + \"i\") if k == 1\n" +
		"\treturn nil\n" +
		"}\n" +
		"fn main() {\n" +
		"\tif s := find(1) {\n\t\tprint s\n\t}\n" +
		"\tif s := find(2) {\n\t\tprint s\n\t} else {\n\t\tprint (\"no\" + \"ne\")\n\t}\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "hi\nnone\n" {
		t.Fatalf("non-POD opt if-let = %q, want %q", got, "hi\nnone\n")
	}
}

// TestNonPODOptStrBoundPassedReturned: a str? is returned from a function, bound to a
// local (a non-POD Opt owner scheduled for drop), passed by value into another function
// (retained/released across the call), and unwrapped there — all refcount-balanced.
func TestNonPODOptStrBoundPassedReturned(t *testing.T) {
	src := "fn get(k: int) -> str? {\n" +
		"\treturn (\"a\" + \"b\") if k == 1\n" +
		"\treturn nil\n" +
		"}\n" +
		"fn use(o: str?) {\n" +
		"\tif s := o {\n\t\tprint s\n\t} else {\n\t\tprint (\"x\" + \"y\")\n\t}\n" +
		"}\n" +
		"fn main() {\n" +
		"\to := get(1)\n" +
		"\tuse(o)\n" +
		"\tuse(o)\n" +
		"\to2 := get(2)\n" +
		"\tuse(o2)\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "ab\nab\nxy\n" {
		t.Fatalf("str? bound/passed/returned = %q, want %q", got, "ab\nab\nxy\n")
	}
}

// TestNonPODOptRef: a Ref[int]? — an optional holding a refcounted heap box. The if-let
// binds a borrow of the Ref; the Opt owner releases the box exactly once at scope exit.
func TestNonPODOptRef(t *testing.T) {
	src := "fn get(k: int) -> Ref[int]? {\n" +
		"\treturn Ref(5) if k == 1\n" +
		"\treturn nil\n" +
		"}\n" +
		"fn main() {\n" +
		"\tif r := get(1) {\n\t\tprint deref(r)\n\t} else {\n\t\tprint -1\n\t}\n" +
		"\tif r := get(2) {\n\t\tprint deref(r)\n\t} else {\n\t\tprint -1\n\t}\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "5\n-1\n" {
		t.Fatalf("Ref[int]? if-let = %q, want %q", got, "5\n-1\n")
	}
}

// TestNonPODOptList: a list[int]? — an optional holding a heap buffer. The if-let binds a
// borrow of the list; the Opt owner drops the buffer once at scope exit.
func TestNonPODOptList(t *testing.T) {
	src := "fn get(k: int) -> list[int]? {\n" +
		"\treturn [1, 2, 3] if k == 1\n" +
		"\treturn nil\n" +
		"}\n" +
		"fn main() {\n" +
		"\tif xs := get(1) {\n\t\tprint xs[0]\n\t\tprint xs[2]\n\t}\n" +
		"\tif xs := get(2) {\n\t\tprint xs[0]\n\t} else {\n\t\tprint -1\n\t}\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "1\n3\n-1\n" {
		t.Fatalf("list[int]? if-let = %q, want %q", got, "1\n3\n-1\n")
	}
}

// TestNonPODOptStructField: a struct with an optional non-POD field (`name: str?`). The
// struct's generated copy/drop must deep-copy/release the present payload; the carrier
// typedef must precede the struct typedef. Balanced across a bind and an if-let read.
func TestNonPODOptStructField(t *testing.T) {
	src := "struct Box {\n\tname: str?\n}\n" +
		"fn main() {\n" +
		"\tb := Box(name: \"x\" + \"y\")\n" +
		"\tc := b\n" +
		"\tif s := b.name {\n\t\tprint s\n\t}\n" +
		"\tif s := c.name {\n\t\tprint s\n\t}\n" +
		"\tempty := Box(name: nil)\n" +
		"\tif s := empty.name {\n\t\tprint s\n\t} else {\n\t\tprint (\"no\" + \"ne\")\n\t}\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "xy\nxy\nnone\n" {
		t.Fatalf("optional struct field = %q, want %q", got, "xy\nxy\nnone\n")
	}
}

// TestNonPODOptCoalesceForce: `??` (coalesce) and `!` (force) over a non-POD optional
// route through copyValue — each must yield an OWNED value (retaining a borrowed payload)
// so a bind/print does not double-free the Opt owner's copy. Present and absent both.
func TestNonPODOptCoalesceForce(t *testing.T) {
	src := "fn get(k: int) -> str? {\n" +
		"\treturn (\"a\" + \"b\") if k == 1\n" +
		"\treturn nil\n" +
		"}\n" +
		"fn main() {\n" +
		"\to := get(1)\n" +
		"\ts := o ?? (\"d\" + \"ef\")\n" +
		"\tprint s\n" +
		"\to2 := get(2)\n" +
		"\tprint (o2 ?? (\"d\" + \"ef\"))\n" +
		"\to3 := get(1)\n" +
		"\tf := o3!\n" +
		"\tprint f\n" +
		"\tprint (get(1)!)\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "ab\ndef\nab\nab\n" {
		t.Fatalf("coalesce/force over non-POD opt = %q, want %q", got, "ab\ndef\nab\nab\n")
	}
}
