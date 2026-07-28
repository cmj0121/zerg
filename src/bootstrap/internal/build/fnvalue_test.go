package build

import "testing"

// First-class function VALUES, end to end (docs/code/functions.md). The front end always
// typed these; the backend used to refuse them, so each case here is a spelling that
// returned a diagnostic before and now runs.
//
// The four are separate failure modes, not four flavours of one: a bare name is an
// Ident, a namespace member is a Field, a parameter is a call the callee cannot see
// through, and a struct field is storage that has to survive a typedef being emitted
// before the value ever exists.

// TestNamedFunctionAsValue binds a bare top-level function and calls through it.
func TestNamedFunctionAsValue(t *testing.T) {
	src := "fn f(x: int) -> int {\n\treturn x + 1\n}\n" +
		"fn main() {\n\tg := f\n\tprint g(1)\n}\n"
	if got := runProgramRT(t, src); got != "2\n" {
		t.Fatalf("got %q, want %q", got, "2\n")
	}
}

// TestNamespaceFunctionAsValue is the Field spelling: `mod.f` taken, not called. A
// module flattens into one program, so the value is the same symbol a direct call names.
func TestNamespaceFunctionAsValue(t *testing.T) {
	src := "import \"strconv\"\n\n" +
		"fn main() {\n\tg := strconv.to_string\n\tprint g(255, 16)\n}\n"
	if got := runProgramRT(t, src); got != "ff\n" {
		t.Fatalf("got %q, want %q", got, "ff\n")
	}
}

// TestFunctionValueAsParameter passes a function in and calls it back. This is the
// case a callback is: the callee cannot know which function it got, so the call has to
// go through the value's own type rather than through a name.
func TestFunctionValueAsParameter(t *testing.T) {
	src := "fn double(x: int) -> int {\n\treturn x * 2\n}\n" +
		"fn square(x: int) -> int {\n\treturn x * x\n}\n" +
		"fn apply(f: fn(int) -> int, v: int) -> int {\n\treturn f(v)\n}\n" +
		"fn main() {\n\tprint apply(double, 21)\n\tprint apply(square, 7)\n}\n"
	if got := runProgramRT(t, src); got != "42\n49\n" {
		t.Fatalf("got %q, want %q", got, "42\n49\n")
	}
}

// TestFunctionValueInStructField stores a function in a field and dispatches on it —
// two values of one type behaving differently, which is what a command carrying an
// action is. It also pins the typedef ORDER: the struct's declaration names the shared
// function pointer, and declarations are written before any body, so a typedef decided
// while lowering a body would arrive too late.
func TestFunctionValueInStructField(t *testing.T) {
	src := "struct Cmd {\n\tname: str\n\taction: fn(int) -> int\n}\n\n" +
		"impl Cmd {\n\tfn run(x: int) -> int {\n\t\treturn this.action(x)\n\t}\n}\n\n" +
		"fn double(x: int) -> int {\n\treturn x * 2\n}\n" +
		"fn square(x: int) -> int {\n\treturn x * x\n}\n" +
		"fn main() {\n\tc := Cmd(\"dbl\", double)\n\td := Cmd(\"sq\", square)\n" +
		"\tprint c.run(5)\n\tprint d.run(5)\n}\n"
	if got := runProgramRT(t, src); got != "10\n25\n" {
		t.Fatalf("got %q, want %q", got, "10\n25\n")
	}
}
