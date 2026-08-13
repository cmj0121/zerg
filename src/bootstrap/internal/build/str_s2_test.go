package build

import "testing"

// RUN-based tests for S2 (refcounted runtime strings): a program that PRODUCES a heap
// string — a concat, an f-string, or a str(bytes|runes) conversion — manages every str
// value as a refcounted cell. Each program is compiled to C, linked against the runtime
// under ASan+UBSan with the counting allocator swapped in (runProgramRTBalanced), and
// run: a pass asserts a clean exit, the exact stdout, AND a zero alloc/free balance — so
// no produced string leaks and none is freed twice. macOS ships no LeakSanitizer, so the
// alloc-balance assertion is the leak proof; ASan+UBSan catch double-free / UAF.

// TestStrConcatBalanced covers string concatenation: a bound result is dropped at scope
// exit, a nested concat releases its intermediate, a print of a temporary releases it,
// and a comparison of a producer releases it — all balanced.
func TestStrConcatBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\ta := \"foo\"\n"+
			"\tb := \"bar\"\n"+
			"\ts := a + b\n"+ // bound producer: dropped at scope exit
			"\tprint s\n"+
			"\tprint a + b + \"!\"\n"+ // nested + printed temporary: intermediate + result freed
			"\tif a + b == \"foobar\" { print \"eq\" }\n"+ // producer compared: freed
			"\treturn nil\n}\n")
	if want := "foobar\nfoobar!\neq\n"; got != want {
		t.Fatalf("concat: got %q, want %q", got, want)
	}
}

// TestStrConcatRetainBalanced covers the copy path: binding a str VARIABLE retains the
// cell, so both holders drop it and the count returns to zero.
func TestStrConcatRetainBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\ts := \"x\" + \"y\"\n"+ // a heap cell, rc=1
			"\tt := s\n"+ // retain: rc=2
			"\tprint s\n"+
			"\tprint t\n"+
			"\treturn nil\n}\n")
	if want := "xy\nxy\n"; got != want {
		t.Fatalf("concat retain: got %q, want %q", got, want)
	}
}

// TestFStringBalanced covers f-string lowering: holes render through display()/Format,
// text parts are cells, and every intermediate join plus the escaping result is freed.
func TestFStringBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\tn := 42\n"+
			"\ts := \"hi\"\n"+
			"\tprint f\"{s} count={n} done\"\n"+ // borrowed hole + text + int hole
			"\tr := f\"[{n:04}]\"\n"+ // bound f-string: dropped at scope exit
			"\tprint r\n"+
			"\tprint f\"{s}\"\n"+ // lone borrowed hole: retained to an owned value, then freed
			"\treturn nil\n}\n")
	if want := "hi count=42 done\n[0042]\nhi\n"; got != want {
		t.Fatalf("f-string: got %q, want %q", got, want)
	}
}

// TestStrBridgeRoundTripBalanced covers str(bytes)/str(runes): the list->str conversion
// is a heap producer, and the round trip through byte and rune lists frees cleanly.
func TestStrBridgeRoundTripBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\tbs := list[byte](\"cafe\")\n"+
			"\ta := str(bs)\n"+ // bytes -> str cell
			"\tprint a\n"+
			"\trs := list[rune](\"cafe\")\n"+
			"\tb := str(rs)\n"+ // runes -> str cell
			"\tprint b\n"+
			"\treturn nil\n}\n")
	if want := "cafe\ncafe\n"; got != want {
		t.Fatalf("str bridge: got %q, want %q", got, want)
	}
}

// TestStrListBalanced covers list[str] in a managed program: the element vtable retains
// on the list copy and releases on the list drop, so a list of concat results (rc=1
// cells) plus a retained copy of the whole list frees to zero.
func TestStrListBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\txs := [\"a\" + \"1\", \"b\" + \"2\"]\n"+ // list of two heap cells
			"\tys := xs\n"+ // deep copy: each element retained
			"\tprint xs[0]\n"+
			"\tprint ys[1]\n"+
			"\treturn nil\n}\n")
	if want := "a1\nb2\n"; got != want {
		t.Fatalf("list[str]: got %q, want %q", got, want)
	}
}

// TestStrReassignBalanced covers reassigning a managed str local: the old cell is
// released before the new one is bound, so an independent producer on each side balances.
func TestStrReassignBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\tmut s := \"a\" + \"b\"\n"+ // rc=1 cell
			"\ts = \"c\" + \"d\"\n"+ // release old, bind new
			"\tprint s\n"+
			"\treturn nil\n}\n")
	if want := "cd\n"; got != want {
		t.Fatalf("str reassign: got %q, want %q", got, want)
	}
}

// TestStrStructBalanced covers a struct holding str fields in a managed program: the
// generated struct copy retains each str field and the drop releases it, so constructing
// (with owned producer/literal field values), copying, and dropping balances.
func TestStrStructBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct Pair {\n\tpub a: str\n\tpub b: str\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\tp := Pair(\"x\" + \"y\", \"z\")\n"+ // a: heap cell, b: immortal literal
			"\tq := p\n"+ // struct copy: fields retained
			"\tprint q.a\n"+
			"\tprint q.b\n"+
			"\treturn nil\n}\n")
	if want := "xy\nz\n"; got != want {
		t.Fatalf("struct[str]: got %q, want %q", got, want)
	}
}
