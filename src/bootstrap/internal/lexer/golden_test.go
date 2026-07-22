package lexer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/corpus"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// TestLexerGolden checks the scanner against the shared corpus: each lexer/*.zg in
// the test-data submodule is tokenized and its dump compared to the sibling
// *.tokens golden (the 'zerg build --emit tokens' format: line:col<TAB>token).
func TestLexerGolden(t *testing.T) {
	dir, ok := corpus.Path("lexer")
	if !ok {
		t.Skip("test-data submodule not initialized (run: git submodule update --init)")
	}
	sources, err := filepath.Glob(filepath.Join(dir, "*.zg"))
	if err != nil || len(sources) == 0 {
		t.Skipf("no lexer cases in %s", dir)
	}

	for _, zg := range sources {
		name := strings.TrimSuffix(filepath.Base(zg), ".zg")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(zg)
			if err != nil {
				t.Fatalf("read %s: %v", zg, err)
			}
			want, err := os.ReadFile(strings.TrimSuffix(zg, ".zg") + ".tokens")
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if got := dumpTokens(string(src)); got != string(want) {
				t.Fatalf("token dump mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
			}
		})
	}
}

// dumpTokens renders the token stream exactly as 'zerg build --emit tokens' does.
func dumpTokens(src string) string {
	toks, _ := Tokenize(src)
	var b strings.Builder
	for _, tok := range toks {
		if tok.Kind == token.EOF {
			break
		}
		fmt.Fprintf(&b, "%s\t%s\n", tok.Span.Start, tok)
	}
	return b.String()
}
