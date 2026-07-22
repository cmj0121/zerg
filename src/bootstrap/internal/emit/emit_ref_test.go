package emit

import (
	"strings"
	"testing"
)

// TestRefEmit is the Phase 1d iteration-2 oracle for value copy (U2) and Ref[T]
// (U3): it checks, per program, that the emitted C carries the right retain/
// release/alloc calls at the right sites — and that a value-only program stays free
// of any runtime reference (the byte-identical guarantee).
func TestRefEmit(t *testing.T) {
	cases := []struct {
		name         string
		src          string
		wantRuntime  bool
		wantContains []string
		wantAbsent   []string
	}{
		{
			// construct = move (rc stays 1, no retain); copy of an lvalue = retain;
			// deref reads the payload; del releases; scope exit releases the survivor.
			name:        "ref-construct-copy-deref-del",
			src:         "fn main() {\n r := Ref(7)\n s := r\n print deref(s)\n del s\n}",
			wantRuntime: true,
			wantContains: []string{
				"#include \"zergrt.h\"",
				"static void *zg_refnew_0(int64_t v)",
				"zrt_ref_alloc(sizeof(int64_t), NULL)",
				"zg_r = zg_refnew_0(7)",     // construct: moved, not retained
				"zg_s = zrt_ref_copy(zg_r)", // copy an lvalue: retain
				"zrt_ref_payload(zg_s)",     // deref
				"zrt_release(zg_s);",        // del s
				"zrt_release(zg_r);",        // scope-exit release of the survivor
			},
		},
		{
			// a construction bound directly is a move: no retain, exactly one release.
			name:         "ref-move-no-retain",
			src:          "fn main() {\n r := Ref(1)\n}",
			wantRuntime:  true,
			wantContains: []string{"zg_r = zg_refnew_0(1)", "zrt_release(zg_r);"},
			wantAbsent:   []string{"zrt_ref_copy"},
		},
		{
			// a struct holding a Ref gets copy/drop helpers; copying the struct retains
			// the inner Ref, dropping it releases (reverse field order).
			name:        "struct-with-ref-copy-drop",
			src:         "struct Box { value: Ref[int] }\nfn main() {\n b := Box(Ref(7))\n c := b\n print deref(c.value)\n}",
			wantRuntime: true,
			wantContains: []string{
				"static zg_Box zg_copy_zg_Box(zg_Box x)",
				"r.zg_value = zrt_ref_copy(x.zg_value);",
				"static void zg_drop_zg_Box(zg_Box *x)",
				"zrt_release(x->zg_value);",
				"zg_c = zg_copy_zg_Box(zg_b);", // copy an lvalue struct: deep copy
				"zg_drop_zg_Box(&zg_c);",
				"zg_drop_zg_Box(&zg_b);",
			},
		},
		{
			// a POD struct (no Ref) generates NO copy/drop helpers and no runtime refs.
			name:        "pod-struct-no-helpers",
			src:         "struct Point { x: int }\nfn main() {\n p := Point(3)\n q := p\n print q.x\n}",
			wantRuntime: false,
			wantAbsent:  []string{"zergrt.h", "zrt_", "zg_copy_", "zg_drop_"},
		},
		{
			// explicit element type 'Ref[int](v)' lowers the same as the inferred form.
			name:         "ref-explicit-elem",
			src:          "fn main() {\n r := Ref[int](9)\n print deref(r)\n}",
			wantRuntime:  true,
			wantContains: []string{"zg_refnew_0(9)", "zrt_release(zg_r);"},
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
					t.Fatalf("emitted C must not contain %q\n%s", absent, code)
				}
			}
		})
	}
}
