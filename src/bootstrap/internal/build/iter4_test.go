package build

import (
	"strings"
	"testing"
)

// TestBundleImportMath covers the stdlib math module (Phase 1f U4): the pure
// arithmetic helpers bundle and resolve as ordinary namespace members, while the
// transcendental functions are `#[extern]`-bound foreign calls (U5) that lower to a
// libc thunk, pull in <math.h>, and set NeedsFFI.
func TestBundleImportMath(t *testing.T) {
	src := "import \"math\"\n" +
		"fn main() -> Result[nil] {\n" +
		"\tprint math.abs(-7)\n" +
		"\tprint math.max(3, 9)\n" +
		"\tr := unsafe { math.sqrt(16.0) }\n" +
		"\tprint r\n" +
		"\tp := unsafe { math.pow(2.0, 10.0) }\n" +
		"\tprint p\n" +
		"\treturn nil\n" +
		"}\n"
	code, manifest, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"#include <math.h>",
		"double zg_math__sqrt(double",
		"return (double)sqrt(",
		"double zg_math__pow(double",
		"return (double)pow(",
		"zg_math__abs(",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
	if !manifest.NeedsFFI {
		t.Fatalf("import math (sqrt/pow) should set NeedsFFI, got %+v", manifest)
	}
}

// TestBundleImportAtomic covers the stdlib atomic module (Phase 1f U2): the cell
// constructor is a Ref[int] box and each operation lowers to its sys.c SC primitive
// over the box's payload pointer, pulling in the runtime.
func TestBundleImportAtomic(t *testing.T) {
	src := "import \"atomic\"\n" +
		"fn main() -> Result[nil] {\n" +
		"\ta := atomic.atomic(0)\n" +
		"\tatomic.store(a, 42)\n" +
		"\tatomic.fetch_add(a, 8)\n" +
		"\tok := atomic.compare_swap(a, 50, 99)\n" +
		"\tprint ok\n" +
		"\tprint atomic.load(a)\n" +
		"\treturn nil\n" +
		"}\n"
	code, manifest, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"zg_atomic__atomic(",
		"zrt_atomic_store((int64_t*)zrt_ref_payload(",
		"zrt_atomic_add((int64_t*)zrt_ref_payload(",
		"zrt_atomic_cas((int64_t*)zrt_ref_payload(",
		"zrt_atomic_load((int64_t*)zrt_ref_payload(",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("import atomic should set NeedsRuntime, got %+v", manifest)
	}
}

// TestExternForeignBinding covers the FFI import binder (Phase 1f U5): a user
// `#[extern("c_symbol")] unsafe fn` lowers to a thunk forwarding to the C symbol
// verbatim and is callable from inside an `unsafe { }` block.
func TestExternForeignBinding(t *testing.T) {
	src := "#[extern(\"strlen\")]\n" +
		"unsafe fn c_strlen(s: str) -> int {\n" +
		"}\n" +
		"fn main() -> Result[nil] {\n" +
		"\tprint unsafe { c_strlen(\"hello\") }\n" +
		"\treturn nil\n" +
		"}\n"
	code, manifest, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !strings.Contains(code, "return (int64_t)strlen(") {
		t.Fatalf("emitted C missing the strlen thunk:\n%s", code)
	}
	if !manifest.NeedsFFI {
		t.Fatalf("an #[extern] binding should set NeedsFFI, got %+v", manifest)
	}
}

// TestForeignCallOutsideUnsafeRejected checks docs/ffi.md's rule: a foreign call is
// unsafe, so calling an `#[extern]`-bound symbol outside an unsafe context errors.
func TestForeignCallOutsideUnsafeRejected(t *testing.T) {
	src := "#[extern(\"strlen\")]\n" +
		"unsafe fn c_strlen(s: str) -> int {\n" +
		"}\n" +
		"fn main() -> Result[nil] {\n" +
		"\tprint c_strlen(\"oops\")\n" +
		"\treturn nil\n" +
		"}\n"
	_, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatalf("a foreign call outside `unsafe` should be rejected")
	}
}

// TestExternRequiresUnsafeFn checks that an `#[extern]` foreign binding must be
// declared `unsafe fn` (the binding names an unsafe foreign call).
func TestExternRequiresUnsafeFn(t *testing.T) {
	src := "#[extern(\"strlen\")]\n" +
		"fn c_strlen(s: str) -> int {\n" +
		"}\n" +
		"fn main() -> Result[nil] { return nil }\n"
	_, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatalf("an #[extern] binding on a non-unsafe fn should be rejected")
	}
}
