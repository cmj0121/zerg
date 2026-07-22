package emit

import (
	"strings"
	"testing"
)

// TestConcurrencyGate is the Phase 1e slice-C1 plumbing oracle: a program that uses
// `spawn` sets Manifest.Concurrency (implying NeedsRuntime), lowers each spawn site to
// a generated trampoline + zrt_spawn, and runs main under the scheduler entry; a
// program with no concurrency sets neither flag and references nothing new, so its C
// stays byte-identical to the Phase 1d path.
func TestConcurrencyGate(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantConc    bool
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:       "value-only-no-concurrency",
			src:        "fn main() {\n  print 1\n}",
			wantConc:   false,
			wantAbsent: []string{"zrt_spawn", "zrt_sched_main", "zergrt.h"},
		},
		{
			name:     "spawn-no-args-nil-main",
			src:      "fn worker() {\n  print 42\n}\nfn main() {\n  spawn worker()\n  print 1\n}",
			wantConc: true,
			wantContain: []string{
				"#include \"zergrt.h\"",
				"static void zg_spawnthunk_0(void *p) { (void)p; zg_worker(); }",
				"zrt_spawn(zg_spawnthunk_0, NULL);",
				"return zrt_sched_main_nil(zg_main);",
			},
		},
		{
			name:     "spawn-with-args",
			src:      "fn greet(n: int) {\n  print n\n}\nfn main() {\n  spawn greet(7)\n}",
			wantConc: true,
			wantContain: []string{
				"typedef struct { int64_t f0; } zg_spawnenv_0;",
				"zg_spawnenv_0 *zg_e = (zg_spawnenv_0 *)p;",
				"zg_greet(zg_e->f0);",
				"zrt_free(p);",
				"(zg_spawnenv_0){ 7 };",
				"zrt_spawn(zg_spawnthunk_0, ",
			},
		},
		{
			name:        "spawn-in-int-main-uses-int-entry",
			src:         "fn worker() {\n  print 42\n}\nfn main() -> int {\n  spawn worker()\n  return 0\n}",
			wantConc:    true,
			wantContain: []string{"return zrt_sched_main_int(zg_main);"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, manifest := emitWithManifest(t, tc.src)
			if manifest.Concurrency != tc.wantConc {
				t.Fatalf("Concurrency = %v, want %v\n%s", manifest.Concurrency, tc.wantConc, code)
			}
			if tc.wantConc && !manifest.NeedsRuntime {
				t.Fatalf("Concurrency implies NeedsRuntime, but NeedsRuntime is false\n%s", code)
			}
			for _, want := range tc.wantContain {
				if !strings.Contains(code, want) {
					t.Fatalf("emitted C missing %q\n%s", want, code)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(code, absent) {
					t.Fatalf("non-concurrent C must not reference %q\n%s", absent, code)
				}
			}
		})
	}
}
