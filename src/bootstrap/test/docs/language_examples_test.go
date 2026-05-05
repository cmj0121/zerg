// Package docs_test extracts every fenced code block from docs/LANGUAGE.md
// and confirms it parses cleanly under the v0.10 toolchain. v0.10 Unit 5.
//
// Each fenced ```zerg ... ``` block in LANGUAGE.md is preceded by an HTML
// comment of the form
//
//	<!-- example: <tag> -->
//
// where <tag> is one of:
//
//   - program     — block is a complete program; parsed verbatim.
//   - fn-body     — wrap as `fn main() -> int { ...block... ; return 0 }`.
//   - expression  — wrap as `let __ := <expr>` inside the same fn shell.
//
// Blocks without a tag are rejected by this test as a doc-author bug — the
// reader can't tell from the page what shape the snippet expects, and the
// validator can't reliably parse it. Blocks tagged with an unknown name
// fail the same way.
//
// The test runs Lex+Parse only — no typecheck, no resolve, no run. The
// goal is to keep LANGUAGE.md in sync with the parser surface; semantics
// are exercised by the v0_X corpora elsewhere.
package docs_test

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// languageMdPath resolves docs/LANGUAGE.md relative to this test file's
// location. test file lives at src/bootstrap/test/docs/...; the doc lives
// at the repo root under docs/.
func languageMdPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = .../src/bootstrap/test/docs/language_examples_test.go
	// repo root = .../  (four parents up)
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
	return filepath.Join(root, "docs", "LANGUAGE.md")
}

// extractedBlock is one tagged code block pulled from LANGUAGE.md.
type extractedBlock struct {
	Tag      string // "program" | "fn-body" | "expression"
	Code     string // raw block contents, no fence markers
	StartLn  int    // 1-based line number of the opening fence in LANGUAGE.md
	TagLn    int    // 1-based line number of the example: tag comment
}

// extractBlocks walks the markdown source line-by-line and pulls every
// fenced code block whose immediately-preceding non-blank line is the
// example tag comment. The pairing is strict: a code block without a
// preceding tag is reported as a doc-author bug.
//
// A fence is the literal `\x60\x60\x60` (three backticks) at column 1; the
// optional language hint after the fence (e.g. `\x60\x60\x60zerg`) is
// permitted but not required. We only walk blocks whose preceding tag was
// recognised — this lets unrelated fenced blocks (e.g. shell snippets in
// a section we don't want to validate) coexist without being parsed as
// Zerg, by simply omitting their tag.
func extractBlocks(t *testing.T, mdPath string) []extractedBlock {
	t.Helper()
	f, err := os.Open(mdPath)
	if err != nil {
		t.Fatalf("open %s: %v", mdPath, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// Allow long lines — markdown tables can run wide and Scanner's default
	// 64 KiB buffer is plenty, but stay defensive.
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)

	var blocks []extractedBlock
	var pendingTag string
	var pendingTagLn int
	lineNo := 0
	inFence := false
	var fenceStart int
	var buf bytes.Buffer

	for sc.Scan() {
		lineNo++
		line := sc.Text()
		switch {
		case inFence:
			if strings.HasPrefix(line, "```") {
				// End of fence. Only collect when a tag was attached.
				if pendingTag != "" {
					blocks = append(blocks, extractedBlock{
						Tag:     pendingTag,
						Code:    buf.String(),
						StartLn: fenceStart,
						TagLn:   pendingTagLn,
					})
					pendingTag = ""
					pendingTagLn = 0
				}
				inFence = false
				buf.Reset()
				continue
			}
			buf.WriteString(line)
			buf.WriteByte('\n')
		case strings.HasPrefix(line, "```"):
			inFence = true
			fenceStart = lineNo
			buf.Reset()
		case strings.HasPrefix(strings.TrimSpace(line), "<!-- example:"):
			tag := parseTagLine(line)
			if tag == "" {
				t.Fatalf("%s:%d: malformed example tag: %q", mdPath, lineNo, line)
			}
			pendingTag = tag
			pendingTagLn = lineNo
		case strings.TrimSpace(line) == "":
			// Blank lines preserve a pending tag — markdown puts a blank
			// between the comment and the fence.
		default:
			// Any other content cancels a pending tag (the tag must
			// immediately precede its fence).
			pendingTag = ""
			pendingTagLn = 0
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", mdPath, err)
	}
	if inFence {
		t.Fatalf("%s: unterminated fenced code block (started at line %d)", mdPath, fenceStart)
	}
	return blocks
}

// parseTagLine extracts the tag from a comment of the form
// `<!-- example: TAG -->` (with arbitrary surrounding whitespace inside
// the comment). Returns "" on any shape mismatch.
func parseTagLine(line string) string {
	s := strings.TrimSpace(line)
	const prefix = "<!-- example:"
	const suffix = "-->"
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, suffix) {
		return ""
	}
	inner := s[len(prefix) : len(s)-len(suffix)]
	return strings.TrimSpace(inner)
}

// wrap shapes the extracted block into a parseable program based on its
// tag. The wrappers carry a leading newline so the block's reported line
// numbers in any parse error stay close to the doc's.
func wrap(tag, code string) (string, error) {
	switch tag {
	case "program":
		return code, nil
	case "fn-body":
		return "fn main() -> int {\n" + code + "\nreturn 0\n}\n", nil
	case "expression":
		// Strip a trailing newline so the synthesized let stays on one
		// logical line if the user wrote a single-line expression.
		c := strings.TrimRight(code, "\n")
		return "fn main() -> int {\nlet __ := " + c + "\nreturn 0\n}\n", nil
	}
	return "", fmt.Errorf("unknown example tag %q", tag)
}

// TestLanguageMdExamples is the parser-validation harness for LANGUAGE.md.
// Every fenced + tagged code block must lex and parse without error. Errors
// are reported with file:line context anchored at the doc's tag line so the
// author can navigate directly to the broken example.
func TestLanguageMdExamples(t *testing.T) {
	mdPath := languageMdPath(t)
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("LANGUAGE.md not found at %s: %v", mdPath, err)
	}
	blocks := extractBlocks(t, mdPath)
	if len(blocks) == 0 {
		t.Fatalf("no tagged example blocks found in %s — did the doc lose its example tags?", mdPath)
	}

	for i, b := range blocks {
		i, b := i, b
		name := fmt.Sprintf("block_%02d_%s_L%d", i+1, b.Tag, b.TagLn)
		t.Run(name, func(t *testing.T) {
			src, err := wrap(b.Tag, b.Code)
			if err != nil {
				t.Fatalf("LANGUAGE.md:%d: %v", b.TagLn, err)
			}
			toks, err := syntax.Lex([]byte(src))
			if err != nil {
				t.Fatalf("LANGUAGE.md:%d (tag %q): lex error: %v\n--- wrapped source ---\n%s",
					b.StartLn, b.Tag, err, src)
			}
			if _, err := syntax.Parse(toks); err != nil {
				t.Fatalf("LANGUAGE.md:%d (tag %q): parse error: %v\n--- wrapped source ---\n%s",
					b.StartLn, b.Tag, err, src)
			}
		})
	}
}
