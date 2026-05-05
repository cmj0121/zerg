// Package docs_test extracts every tagged Zerg example block from the
// repo's docs/ tree and validates each one against the live toolchain.
//
// At v0.10 only docs/STDLIB.md is exercised here. If Unit 5's
// docs/LANGUAGE.md test lands at the same path, this package extends to
// cover it; otherwise the language-reference doc has its own sibling
// test file in this directory.
//
// Validation per block: write the block's source to a temp main.zg,
// call loader.Load + syntax.CheckBundle. Both lex / parse errors and
// typeck errors fail the test. Block tag format:
//
//	<!-- example: program -->
//
//	```zerg
//	# requires: vX.Y
//	... runnable source ...
//	```
//
// Only blocks immediately following an `example: program` HTML comment
// are extracted — narrative-only fenced blocks (e.g. expected-output
// snippets) are skipped.
package docs_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/loader"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// repoRoot walks up from this test file until it finds a directory
// containing docs/STDLIB.md. The walk fails the test if no such
// ancestor exists — guards against running in a stripped checkout.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "docs", "STDLIB.md")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate docs/STDLIB.md walking up from %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

// docExample is one extracted code block plus its source-line for the
// failure diagnostic.
type docExample struct {
	Tag    string // "program", future tags reserved
	Source string
	Line   int // 1-based start line of the fenced block in the source doc
}

// extractTaggedZergBlocks parses Markdown body and returns every
// `<!-- example: <tag> -->`-tagged ```zerg block. The tag comment must
// appear on its own line; one or more blank lines may separate the
// comment from the opening fence.
func extractTaggedZergBlocks(body string) []docExample {
	lines := strings.Split(body, "\n")
	var out []docExample
	pendingTag := ""
	pendingTagLine := 0
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<!-- example:") && strings.HasSuffix(trimmed, "-->") {
			// "<!-- example: program -->" → "program"
			inner := strings.TrimSuffix(strings.TrimPrefix(trimmed, "<!-- example:"), "-->")
			pendingTag = strings.TrimSpace(inner)
			pendingTagLine = i + 1
			continue
		}
		if pendingTag == "" {
			continue
		}
		if trimmed == "" {
			continue
		}
		// First non-blank line after a tag must be the opening fence.
		if !strings.HasPrefix(trimmed, "```zerg") {
			// Tag was not consumed by a zerg block — drop it. The doc
			// may have an out-of-order narrative comment; we don't
			// silently swallow following blocks.
			pendingTag = ""
			continue
		}
		// Walk to closing fence.
		start := i + 1
		end := -1
		for j := start; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				end = j
				break
			}
		}
		if end < 0 {
			// Unterminated block — let the caller see this as zero
			// extracted blocks; the test that asserts a positive count
			// will catch it.
			break
		}
		src := strings.Join(lines[start:end], "\n")
		if !strings.HasSuffix(src, "\n") {
			src += "\n"
		}
		out = append(out, docExample{
			Tag:    pendingTag,
			Source: src,
			Line:   pendingTagLine,
		})
		pendingTag = ""
		i = end
	}
	return out
}

// validateProgram writes src to a temp main.zg under t.TempDir() and
// runs the public loader + typeck pipeline. Returns the error verbatim
// so the test can compare or report.
func validateProgram(t *testing.T, src string) error {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.zg")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("write main.zg: %v", err)
	}
	bundle, err := loader.Load(entry)
	if err != nil {
		return err
	}
	return syntax.CheckBundle(bundle)
}

// TestStdlibMdExamplesParseAndTypecheck loads docs/STDLIB.md, extracts
// every tagged Zerg block, and validates each one through the live
// loader + typecheck pipeline. Any extraction-zero or per-block error
// fails the test.
func TestStdlibMdExamplesParseAndTypecheck(t *testing.T) {
	root := repoRoot(t)
	docPath := filepath.Join(root, "docs", "STDLIB.md")
	body, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	examples := extractTaggedZergBlocks(string(body))
	if len(examples) == 0 {
		t.Fatalf("no tagged ```zerg examples in %s", docPath)
	}
	// At v0.10 we expect a healthy floor — the doc covers 5 modules and
	// many fns, so anything under ~10 examples means the extractor is
	// missing tags.
	if len(examples) < 10 {
		t.Fatalf("only %d examples extracted from %s; expected >=10", len(examples), docPath)
	}
	for _, ex := range examples {
		ex := ex
		name := docPath + ":" + lineLabel(ex.Line)
		t.Run(name, func(t *testing.T) {
			if ex.Tag != "program" {
				t.Fatalf("unsupported tag %q (only 'program' validated at v0.10)", ex.Tag)
			}
			if err := validateProgram(t, ex.Source); err != nil {
				t.Fatalf("example at %s failed:\n--- source ---\n%s---\n%v",
					name, ex.Source, err)
			}
		})
	}
}

// lineLabel renders a 1-based line into a short subtest name. We avoid
// fmt.Sprintf to keep the test's import set minimal.
func lineLabel(line int) string {
	if line <= 0 {
		return "L?"
	}
	digits := []byte{}
	for line > 0 {
		digits = append([]byte{byte('0' + line%10)}, digits...)
		line /= 10
	}
	return "L" + string(digits)
}
