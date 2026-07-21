package fmt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/corpus"
)

// TestExamplesRoundTrip runs the fmt oracle over every examples/*.zg shipped in
// the repository: parse(fmt(parse(src))) must equal parse(src) and fmt must be
// byte-idempotent.
func TestExamplesRoundTrip(t *testing.T) {
	dir, ok := examplesDir()
	if !ok {
		t.Skip("examples directory not found above the working directory")
	}
	sources, err := filepath.Glob(filepath.Join(dir, "*.zg"))
	if err != nil || len(sources) == 0 {
		t.Skipf("no examples in %s", dir)
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

// TestFmtCorpusRoundTrip runs the oracle over the shared trivia corpus: every
// fmt/*.zg in the test-data submodule (comment placement, doc blocks, blank-line
// runs, integer bases, pub visibility) must survive the round-trip.
//
// Unlike the lexer/parser golden tests, this one FAILS (not skips) when the
// corpus is absent: the fmt oracle is this iteration's deliverable, so a missing
// corpus must not masquerade as green on CI. It only skips when the whole
// test-data submodule is uninitialized (a clean checkout without submodules),
// which is a distinct, visible condition.
func TestFmtCorpusRoundTrip(t *testing.T) {
	dir, ok := corpus.Path("fmt")
	if !ok {
		// distinguish "no submodule at all" (skip) from "submodule present but
		// the fmt corpus is missing" (fail): if a sibling corpus dir exists, the
		// submodule is initialized and the fmt corpus should be there too.
		if _, sibling := corpus.Path("lexer"); sibling {
			t.Fatal("test-data submodule is present but test-data/fmt is missing; the fmt corpus must be committed inside the submodule")
		}
		t.Skip("test-data submodule not initialized (run: git submodule update --init)")
	}
	sources, err := filepath.Glob(filepath.Join(dir, "*.zg"))
	if err != nil || len(sources) == 0 {
		t.Fatalf("no fmt cases in %s; the fmt corpus must be committed inside the submodule", dir)
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

// examplesDir finds the repository's examples/ directory by walking up from the
// current working directory, mirroring how corpus.Path locates test-data.
func examplesDir() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(cwd, "examples")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			return "", false
		}
		cwd = parent
	}
}
