package fmt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// TestCorpusMeasurement walks the deterministic v0.0–v0.9 corpus and
// reports the count of programs whose canonical formatting differs from
// the source. v0.10 Unit 2 uses this measurement to decide style locks;
// Unit 3 will turn the diff into a hard parity gate.
//
// The test logs the count and the first N differing programs; it does NOT
// fail on any diff. Pass `go test -run TestCorpusMeasurement -v` to see
// the full report. Set ZERG_FMT_CORPUS_FAIL=1 to convert the report into
// a hard failure.
func TestCorpusMeasurement(t *testing.T) {
	root := corpusRoot(t)
	versions := []string{"v0_0", "v0_1", "v0_2", "v0_3", "v0_4", "v0_5", "v0_6", "v0_7", "v0_8", "v0_9"}
	type diff struct {
		path     string
		original string
		formatted string
	}
	var (
		scanned int
		differ  int
		samples []diff
	)
	const sampleLimit = 5
	for _, v := range versions {
		dir := filepath.Join(root, v)
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				// Skip rejects/ and scheduling/ subtrees.
				name := info.Name()
				if name == "rejects" || name == "scheduling" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".zg") {
				return nil
			}
			scanned++
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Logf("read %s: %v", path, rerr)
				return nil
			}
			tokens, comments, lerr := syntax.LexWithComments(src)
			if lerr != nil {
				t.Logf("lex %s: %v", path, lerr)
				return nil
			}
			prog, perr := syntax.ParseWithComments(tokens, comments)
			if perr != nil {
				t.Logf("parse %s: %v", path, perr)
				return nil
			}
			out := Format(prog)
			if string(out) == string(src) {
				return nil
			}
			differ++
			if len(samples) < sampleLimit {
				samples = append(samples, diff{
					path:      path,
					original:  string(src),
					formatted: string(out),
				})
			}
			return nil
		})
	}
	t.Logf("corpus measurement: %d programs scanned, %d differ", scanned, differ)
	for _, s := range samples {
		rel, _ := filepath.Rel(root, s.path)
		t.Logf("---- DIFFER: %s ----", rel)
		t.Logf("ORIGINAL:\n%s", s.original)
		t.Logf("FORMATTED:\n%s", s.formatted)
	}
	// Construct-level summary: tally a small set of common reasons the
	// formatted output differs from source.
	if differ > 0 {
		summarise(t, root, versions)
	}
	if os.Getenv("ZERG_FMT_CORPUS_FAIL") == "1" && differ > 0 {
		t.Fatalf("ZERG_FMT_CORPUS_FAIL set: %d corpus programs differ", differ)
	}
}

// summarise classifies each differing program's diff by surface category.
// Categories are approximate — used only to guide the U3 corpus rewrite.
func summarise(t *testing.T, root string, versions []string) {
	categories := map[string]int{}
	for _, v := range versions {
		dir := filepath.Join(root, v)
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				name := info.Name()
				if name == "rejects" || name == "scheduling" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".zg") {
				return nil
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			tokens, comments, lerr := syntax.LexWithComments(src)
			if lerr != nil {
				return nil
			}
			prog, perr := syntax.ParseWithComments(tokens, comments)
			if perr != nil {
				return nil
			}
			out := string(Format(prog))
			orig := string(src)
			if out == orig {
				return nil
			}
			classifyDiff(orig, out, categories)
			return nil
		})
	}
	t.Logf("corpus diff categories:")
	for cat, n := range categories {
		t.Logf("  %s: %d", cat, n)
	}
}

func classifyDiff(orig, formatted string, cat map[string]int) {
	classified := false
	// Indent change: 2-space source vs 4-space output. Detected by looking
	// for `\n  X` (two spaces then a non-space) anywhere in the source —
	// 4-space-indented sources have `\n    ` and never the bare `\n  X`.
	if hasTwoSpaceIndent(orig) {
		cat["indent: 2-space source -> 4-space"]++
		classified = true
	}
	// Grouped imports collapsed to per-line.
	if strings.Contains(orig, "import (") && !strings.Contains(formatted, "import (") {
		cat["grouped-import collapsed to per-line"]++
		classified = true
	}
	// Numeric literal underscore separators dropped.
	if hasUnderscoreInNumber(orig) {
		cat["int/float underscore separator stripped"]++
		classified = true
	}
	// Struct-lit no-space form (`Name{ x: ... }`).
	if hasStructLitNoSpace(orig) {
		cat["struct-lit no-space form `Box{...}`"]++
		classified = true
	}
	// Trailing comma on multi-line enum/struct decl.
	if hasMultilineDeclTrailingComma(orig) {
		cat["multi-line decl trailing comma stripped"]++
		classified = true
	}
	// Column-aligned `:=` (extra spaces inside one stmt).
	if hasColumnAlignment(orig) {
		cat["column-aligned `:=` collapsed to single space"]++
		classified = true
	}
	// Spaces around `+` inside parens compressed in source.
	if strings.Contains(orig, "(i+1)") || strings.Contains(orig, "(i +1)") {
		cat["binary op without surrounding spaces"]++
		classified = true
	}
	if !classified {
		cat["other"]++
	}
	cat["total"]++
}

// hasTwoSpaceIndent looks for any line that begins with exactly 2 spaces
// followed by a non-space character — that's the v0_2 corpus indent style.
func hasTwoSpaceIndent(s string) bool {
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		if len(line) >= 3 && line[0] == ' ' && line[1] == ' ' && line[2] != ' ' {
			return true
		}
	}
	return false
}

// hasMultilineDeclTrailingComma checks for an enum/struct multi-line decl
// whose final variant/field carries a trailing comma. Heuristic: search
// for `<ident>(<types>),\n}` or `<ident>,\n}`.
func hasMultilineDeclTrailingComma(s string) bool {
	return strings.Contains(s, ",\n}")
}

// hasColumnAlignment checks for runs of >1 space between identifiers and
// `:=` — a sign that the source uses column alignment that fmt collapses.
func hasColumnAlignment(s string) bool {
	return strings.Contains(s, "  :=") || strings.Contains(s, "   :=")
}

func hasUnderscoreInNumber(s string) bool {
	// Look for digit_digit pattern.
	for i := 1; i < len(s)-1; i++ {
		if s[i] == '_' && isDigit(s[i-1]) && isDigit(s[i+1]) {
			return true
		}
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func hasStructLitNoSpace(s string) bool {
	// Heuristic: search for `[A-Z]\w*\{` (uppercase ident immediately
	// followed by `{` with no space).
	for i := 0; i < len(s)-1; i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			// Skip the rest of the ident.
			j := i + 1
			for j < len(s) && (isAlpha(s[j]) || isDigit(s[j])) {
				j++
			}
			if j < len(s) && s[j] == '{' {
				// Make sure this isn't a type annotation `let x: T{...}`
				// or `struct T {...}` decl — we want struct-lit value
				// position. Easiest heuristic: preceding non-space was
				// `(` `[` `=` `,` ` `.
				return true
			}
			i = j
		}
	}
	return false
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

// corpusRoot returns the path to src/bootstrap/test, walking up from cwd.
func corpusRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	// We are in src/bootstrap/internal/fmt; corpus is src/bootstrap/test.
	dir := wd
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "test")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			// Confirm this is the corpus by probing for v0_1.
			if _, err := os.Stat(filepath.Join(candidate, "v0_1")); err == nil {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate test/ corpus root from cwd=%s", wd)
	return ""
}
