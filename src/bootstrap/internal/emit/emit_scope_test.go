package emit

import (
	"strings"
	"testing"
)

// TestScopeEmit is the Phase 1d iteration-3 oracle for drop order & `defer`/`del`/
// `with` (U4/U5): it checks that a program owning teardown records a cleanup mark,
// schedules its releases and defers on the runtime stack, and unwinds them at every
// exit — and that a value-only program records none of it (the byte-identical
// guarantee).
func TestScopeEmit(t *testing.T) {
	cases := []struct {
		name         string
		src          string
		wantRuntime  bool
		wantContains []string
		wantAbsent   []string
	}{
		{
			// a value-only program owns no teardown: no mark, no defer, no unwind, no
			// runtime include — byte-identical to Phase 0.
			name:        "value-only-no-teardown",
			src:         "fn main() {\n x := 1\n print x\n}",
			wantRuntime: false,
			wantAbsent:  []string{"zergrt.h", "zrt_scope_mark", "zrt_unwind_to", "zrt_defer"},
		},
		{
			// `defer f()` with no args: one thunk, scheduled with a NULL env; the scope
			// records a mark and unwinds it at exit.
			name:        "defer-no-args",
			src:         "fn f() {\n print 1\n}\nfn main() {\n defer f()\n print 2\n}",
			wantRuntime: true,
			wantContains: []string{
				"static void zg_deferfn_0(void *p) { (void)p; zg_f(); }",
				"= zrt_scope_mark();",
				"zrt_defer(zg_deferfn_0, NULL);",
				"zrt_unwind_to(",
			},
		},
		{
			// `defer f(a)` captures the argument value into a per-site env struct and
			// the thunk replays the call.
			name:        "defer-captures-arg",
			src:         "fn f(n: int) {\n print n\n}\nfn main() {\n defer f(5)\n print 2\n}",
			wantRuntime: true,
			wantContains: []string{
				"typedef struct { int64_t f0; } zg_deferenv_0;",
				"zg_deferenv_0 *zg_c = (zg_deferenv_0 *)p;",
				"zg_f(zg_c->f0);",
				"zg_deferenv_0 zg_denv = { 5 };",
				"zrt_defer(zg_deferfn_0, &zg_denv);",
			},
		},
		{
			// an early `return` copies its value out, unwinds the function mark, then
			// returns the temporary — so a Ref returned early is retained before release.
			name:        "return-unwinds-before-return",
			src:         "fn pick(flag: bool) -> int {\n r := Ref(7)\n if flag {\n  return deref(r)\n }\n return 0\n}\nfn main() {\n print pick(true)\n}",
			wantRuntime: true,
			wantContains: []string{
				"int64_t zg_ret = (*(int64_t*)zrt_ref_payload(zg_r));",
				"zrt_unwind_to(",
				"return zg_ret;",
			},
		},
		{
			// reassigning a Ref binding retains the new value into a temp, releases the
			// old value, then stores the temp — so the target does not leak or double-free
			// and a self-referential RHS keeps the old cell live across the release
			// (review R3; temp-first ordering fixes the `s = s + x` use-after-free).
			name:        "reassign-ref-binding-releases-old",
			src:         "fn main() {\n a := Ref(1)\n mut b := Ref(2)\n b = a\n print deref(b)\n}",
			wantRuntime: true,
			wantContains: []string{
				"void* zg_as = zrt_ref_copy(zg_a);", // retain the aliased new value into a temp first
				"zrt_release(zg_b);",                // then release old Ref(2)
				"zg_b = zg_as;",                     // then store the temp
			},
		},
		{
			// reassigning a Ref-typed field materializes the new box into a temp, releases
			// the old field value in place, then stores the temp (review R8: a sub-place not
			// tracked as a binding; temp-first ordering avoids a self-referential UAF).
			name:        "reassign-ref-field-releases-old",
			src:         "struct Box { v: Ref[int] }\nfn main() {\n mut a := Box(Ref(1))\n a.v = Ref(2)\n print deref(a.v)\n}",
			wantRuntime: true,
			wantContains: []string{
				"void* zg_as = zg_refnew_0(2);", // materialize the new box into a temp first
				"zrt_release(zg_a.zg_v);",       // then release old field Ref(1)
				"zg_a.zg_v = zg_as;",            // then store the temp
			},
		},
		{
			// `with e as y { }` desugars to a scoped binding whose Ref drop is scheduled
			// and unwound at the block's exit — no explicit defer node needed.
			name:        "with-desugars-to-scoped-drop",
			src:         "fn main() {\n with Ref(7) as f {\n  print deref(f)\n }\n}",
			wantRuntime: true,
			wantContains: []string{
				"void* zg_f = zg_refnew_0(7);",
				"zrt_defer(zg_ref_drop, &zg_f);",
				"zrt_unwind_to(",
			},
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
