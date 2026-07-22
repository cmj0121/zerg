package emit

import (
	"strings"
	"testing"
)

// TestDynDispatchEmit checks the '#[dyn]' backend shape: a witness struct type, a
// concrete witness table, an erased shared body dispatching through the witness,
// and a call site that erases its argument and passes the witness.
func TestDynDispatchEmit(t *testing.T) {
	code := emitC(t, "spec Show {\n  fn value() -> int\n}\n"+
		"struct Wrap {\n  n: int\n}\n"+
		"impl Show for Wrap {\n  fn value() -> int {\n    return this.n\n  }\n}\n"+
		"#[dyn]\nfn total[T: Show](x: T) -> int {\n  return x.value()\n}\n"+
		"fn main() {\n  print total(Wrap(5))\n}")

	for _, want := range []string{
		"} zg_witness_Show;",
		"int64_t (*value)(const void*);",
		"static const zg_witness_Show zg_witness_Show__Wrap = { zg_Wrap__value };",
		"int64_t zg_total__dyn(const void* zg_x, const zg_witness_Show* w) {",
		"return w->value((const void*)zg_x);",
		"&zg_witness_Show__Wrap",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q\n---\n%s", want, code)
		}
	}
}

// TestGenericEnumEmit checks that a generic enum lowers to a specialized tagged
// union and that a variant construction and a match test reference its tag/union.
func TestGenericEnumEmit(t *testing.T) {
	code := emitC(t, "enum Box[T] {\n  Full(T)\n  Empty\n}\n"+
		"fn unwrap[T](b: Box[T], d: T) -> T {\n  return match b {\n    Full(v) => v\n    Empty => d\n  }\n}\n"+
		"fn main() {\n  print unwrap(Full(7), 0)\n}")

	for _, want := range []string{
		"int32_t tag;",
		"} zg_Box__i;",
		".tag = 0, .u.Full = {.f0 = 7}",
		".tag == 0",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q\n---\n%s", want, code)
		}
	}
}
