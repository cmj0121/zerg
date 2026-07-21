package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/corpus"
)

// TestParserGolden runs the parser over the shared corpus: every parser/*.zg in
// the test-data submodule must parse with no diagnostics. It skips when the
// submodule is absent.
func TestParserGolden(t *testing.T) {
	dir, ok := corpus.Path("parser")
	if !ok {
		t.Skip("test-data submodule not initialized (run: git submodule update --init)")
	}
	sources, err := filepath.Glob(filepath.Join(dir, "*.zg"))
	if err != nil || len(sources) == 0 {
		t.Skipf("no parser cases in %s", dir)
	}

	for _, zg := range sources {
		name := strings.TrimSuffix(filepath.Base(zg), ".zg")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(zg)
			if err != nil {
				t.Fatalf("read %s: %v", zg, err)
			}
			if _, diags := Parse(string(src)); len(diags) != 0 {
				t.Fatalf("%s should parse cleanly, got: %v", name, diags)
			}
		})
	}
}
