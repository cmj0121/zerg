package emit

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
)

// emitWithManifest parses, checks, and emits src, returning both the C and the
// Manifest, failing on any diagnostic.
func emitWithManifest(t *testing.T, src string) (string, Manifest) {
	t.Helper()
	file, pdiags := parser.Parse(src)
	if len(pdiags) != 0 {
		t.Fatalf("parse errors: %v", pdiags)
	}
	info, sdiags := sema.Check(file)
	if len(sdiags) != 0 {
		t.Fatalf("sema errors: %v", sdiags)
	}
	code, manifest, ediags := Emit(mono.Build(file, info))
	if len(ediags) != 0 {
		t.Fatalf("emit errors: %v", ediags)
	}
	return code, manifest
}

// TestManifest is the plumbing oracle: a value-only program yields an empty
// Manifest and emitted C that neither includes nor references the runtime (the
// byte-identical guarantee); a runtime-feature program (a Result[nil] main)
// yields a non-empty Manifest and pulls the runtime header and entry shim in.
func TestManifest(t *testing.T) {
	cases := []struct {
		name         string
		src          string
		wantRuntime  bool
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "value-only-nil-main",
			src:          "fn main() {\n  print 3\n}",
			wantRuntime:  false,
			wantContains: []string{"void zg_main(void)", "zg_main();"},
			wantAbsent:   []string{"zergrt.h", "zrt_"},
		},
		{
			// checked arithmetic raises through zrt_abort, so `1 + 2` — which used to be
			// the value-only case above — now needs the runtime. A wrapping `+%` does not:
			// that is the difference the suffix buys.
			name:         "checked-arithmetic-needs-runtime",
			src:          "fn main() {\n  print 1 + 2\n}",
			wantRuntime:  true,
			wantContains: []string{"zrt_add_i64"},
		},
		{
			name:         "wrapping-arithmetic-does-not",
			src:          "fn main() {\n  print 1 +% 2\n}",
			wantRuntime:  false,
			wantContains: []string{"(1 + 2)"},
			wantAbsent:   []string{"zergrt.h", "zrt_"},
		},
		{
			name:         "value-only-int-main",
			src:          "fn main() -> int {\n  return 0\n}",
			wantRuntime:  false,
			wantContains: []string{"return (int)zg_main();"},
			wantAbsent:   []string{"zergrt.h", "zrt_"},
		},
		{
			name:         "result-nil-main",
			src:          "fn main() -> Result[nil] {\n  nop\n}",
			wantRuntime:  true,
			wantContains: []string{"#include \"zergrt.h\"", "zrt_result_nil zg_main(void)", "static zrt_result_nil __zerg_entry(void)", "return zrt_run(__zerg_entry);", "return zrt_result_ok();"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, manifest := emitWithManifest(t, tc.src)
			if manifest.NeedsRuntime != tc.wantRuntime {
				t.Fatalf("NeedsRuntime = %v, want %v\n%s", manifest.NeedsRuntime, tc.wantRuntime, code)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(code, want) {
					t.Fatalf("emitted C missing %q\n%s", want, code)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(code, absent) {
					t.Fatalf("value-only C must not reference %q\n%s", absent, code)
				}
			}
		})
	}
}
