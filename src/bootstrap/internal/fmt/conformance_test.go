package fmt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/corpus"
)

// TestConformanceRoundTrip runs the fmt oracle over the systematic conformance
// corpus (PLAN follow-up F2): test-data/conformance/g01_*.zg … g12_*.zg, one
// fixture per grammar group, each exercising the representative constructs of
// that group. Every fixture must survive the round-trip contract — parse(fmt)
// AST-equivalence, byte idempotence, and trivia conservation — proving the
// parser and printer cover the whole surface grammar, not just the Phase-0
// subset the examples reach.
//
// Like TestFmtCorpusRoundTrip, this FAILS (not skips) when the corpus is absent
// while the submodule is otherwise present: the conformance corpus is a Slice-E
// deliverable, so a missing directory must not masquerade as green. It skips
// only when the whole test-data submodule is uninitialized (a clean checkout
// without submodules), a distinct and visible condition.
func TestConformanceRoundTrip(t *testing.T) {
	dir, ok := corpus.Path("conformance")
	if !ok {
		// Distinguish "no submodule at all" (skip) from "submodule present but
		// the conformance corpus is missing" (fail): a sibling corpus dir proves
		// the submodule is initialized, so the conformance corpus must be there.
		if _, sibling := corpus.Path("lexer"); sibling {
			t.Fatal("test-data submodule is present but test-data/conformance is missing; " +
				"the conformance corpus must be committed inside the submodule")
		}
		t.Skip("test-data submodule not initialized (run: git submodule update --init)")
	}
	sources, err := filepath.Glob(filepath.Join(dir, "*.zg"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	if len(sources) == 0 {
		t.Fatalf("no conformance fixtures in %s; the corpus must be committed inside the submodule", dir)
	}

	for _, zg := range sources {
		name := filepath.Base(zg)
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(zg)
			if err != nil {
				t.Fatalf("read %s: %v", zg, err)
			}
			mustRoundTrip(t, string(src))
		})
	}
}
