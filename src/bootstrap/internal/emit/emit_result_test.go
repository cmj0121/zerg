package emit

import (
	"strings"
	"testing"
)

// TestResultCarrierLowering is the U0 codegen oracle: each null-safety construct
// lowers to its carrier-based C, and a program using one reports NeedsResult (and
// hence NeedsRuntime) while a value-only program reports neither and shows no
// carrier artifact — the byte-identical gate.
func TestResultCarrierLowering(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantResult  bool
		wantContain []string
		wantAbsent  []string
	}{
		{
			name: "return-ok-wraps",
			src: "fn f(n: int) -> Result[int] {\n  return n\n}\n" +
				"fn main() -> Result[nil] {\n  print f(1)!\n}",
			wantResult: true,
			wantContain: []string{
				"typedef struct { int32_t tag; int64_t ok; zrt_err err; } zg_result_0;",
				"return (zg_result_0){ .tag = 0, .ok = zg_n };",
				"static int64_t zg_force_zg_result_0(zg_result_0 r) {",
			},
		},
		{
			name: "try-propagates",
			src: "fn f() -> Result[int] {\n  return 1\n}\n" +
				"fn g() -> Result[int] {\n  v := f()?\n  return v\n}\n" +
				"fn main() -> Result[nil] {\n  print g()!\n}",
			wantResult:  true,
			wantContain: []string{".tag != 0) { return (zg_result_0){ .tag = 1, .err ="},
		},
		{
			name: "coalesce-default",
			src: "fn f() -> Result[int] {\n  return 1\n}\n" +
				"fn main() -> Result[nil] {\n  print (f() ?? -1)\n}",
			wantResult:  true,
			wantContain: []string{".tag == 0 ? ", ".ok : ((-1))"},
		},
		{
			name: "guard-demotes",
			src: "fn r() -> int {\n  raise \"x\"\n  return 0\n}\n" +
				"fn main() -> Result[nil] {\n  g := guard { r() }\n  print (g ?? -1)\n}",
			wantResult: true,
			wantContain: []string{
				"zrt_handler_push_catch(",
				".tag = 1, .err = zrt_taken_err() }",
			},
		},
		{
			name: "a-body-that-only-raises-gets-no-trailing-return",
			// `raise` DIVERGES, so the trailing return is unreachable. It used to be
			// emitted anyway — as a zeroed carrier here, and as the scalar `0` for a
			// nominal result, which is a C type error. Nothing follows the raise now.
			src: "fn boom() -> Result[int] { raise \"bad\" }\n" +
				"fn main() {\n  g := guard { boom()! }\n  print g ?? -1\n}",
			wantResult:  true,
			wantContain: []string{"zrt_raise_err("},
			wantAbsent:  []string{"return (zg_result_0){0};"},
		},
		{
			name: "raise-carries-err",
			src:  "fn main() -> Result[nil] {\n  raise \"boom\"\n}",
			// raise alone needs the runtime but registers no carrier.
			wantResult:  false,
			wantContain: []string{"zrt_raise_err(zrt_err_new(\"boom\"));"},
			wantAbsent:  []string{"zg_result_", "zrt_abort("},
		},
		{
			name:        "value-only-byte-identical",
			src:         "fn main() {\n  print 1 + 2\n}",
			wantResult:  false,
			wantContain: []string{"void zg_main(void)"},
			wantAbsent:  []string{"zg_result_", "zg_opt_", "zg_either_", "zrt_", "zergrt.h"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, manifest := emitWithManifest(t, tc.src)
			if manifest.NeedsResult != tc.wantResult {
				t.Fatalf("NeedsResult = %v, want %v\n%s", manifest.NeedsResult, tc.wantResult, code)
			}
			if tc.wantResult && !manifest.NeedsRuntime {
				t.Fatalf("NeedsResult implies NeedsRuntime, but it is false\n%s", code)
			}
			for _, want := range tc.wantContain {
				if !strings.Contains(code, want) {
					t.Fatalf("emitted C missing %q\n%s", want, code)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(code, absent) {
					t.Fatalf("emitted C should not contain %q\n%s", absent, code)
				}
			}
		})
	}
}
