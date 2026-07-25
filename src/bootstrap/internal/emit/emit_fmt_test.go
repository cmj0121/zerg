package emit

import (
	"strings"
	"testing"
)

// TestFStrLowering checks the Phase 1f U3 f-string lowering: a `{x}` hole renders
// through the value's display(), a `{x:spec}` hole through its Format impl (by static
// type), a `!s`/`!r` conversion picks the view, and the parts join through
// zrt_str_concat. An f-string with a hole sets NeedsFormat (hence NeedsRuntime).
func TestFStrLowering(t *testing.T) {
	cases := []struct {
		name         string
		src          string
		wantContains []string
	}{
		{
			name:         "int-display",
			src:          "fn main() -> Result[nil] {\n  n := 7\n  print f\"n={n}\"\n}",
			wantContains: []string{"zrt_str_concat(", "zrt_display_int((int64_t)(zg_n))"},
		},
		{
			name:         "int-spec",
			src:          "fn main() -> Result[nil] {\n  n := 7\n  print f\"{n:05}\"\n}",
			wantContains: []string{`zrt_fmt_int((int64_t)(zg_n), "05")`},
		},
		{
			name:         "str-identity-and-spec",
			src:          "fn main() -> Result[nil] {\n  s := \"x\"\n  print f\"{s} {s:>4}\"\n}",
			wantContains: []string{`zrt_fmt_str(zg_s, ">4")`, "zrt_str_concat("},
		},
		{
			name:         "float-precision",
			src:          "fn main() -> Result[nil] {\n  p := 1.5\n  print f\"{p:.2f}\"\n}",
			wantContains: []string{`zrt_fmt_float((double)(zg_p), ".2f")`},
		},
		{
			name:         "conversion-s",
			src:          "fn main() -> Result[nil] {\n  n := 3\n  print f\"{n!s}\"\n}",
			wantContains: []string{"zrt_display_int((int64_t)(zg_n))"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, manifest := emitWithManifest(t, tc.src)
			if !manifest.NeedsFormat || !manifest.NeedsRuntime {
				t.Fatalf("NeedsFormat/NeedsRuntime = %v/%v, want true/true\n%s", manifest.NeedsFormat, manifest.NeedsRuntime, code)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(code, want) {
					t.Fatalf("emitted C missing %q\n%s", want, code)
				}
			}
		})
	}
}

// TestFStrByteIdenticalWhenAbsent guards the additive contract: a program with no
// f-string leaves NeedsFormat false and references none of the fmt.c helpers, so its
// C stays byte-identical to the pre-1f backend.
func TestFStrByteIdenticalWhenAbsent(t *testing.T) {
	code, manifest := emitWithManifest(t, "fn main() {\n  print 1 + 2\n}")
	if manifest.NeedsFormat {
		t.Fatalf("a program with no f-string must leave NeedsFormat false\n%s", code)
	}
	for _, helper := range []string{"zrt_str_concat", "zrt_display_", "zrt_fmt_"} {
		if strings.Contains(code, helper) {
			t.Fatalf("emitted C unexpectedly references %q\n%s", helper, code)
		}
	}
}
