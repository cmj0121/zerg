package syntax_test

// v0.10 Unit 4 — diagnostic hardening, position-coverage gate.
//
// Walks every program in the v0.0–v0.9 reject corpus and asserts that the
// emitted diagnostic carries `:line:col` somewhere in its text. The reject
// corpus already pins each program's expected message via substring match;
// this test pins the orthogonal property that every diagnostic anchors on
// a source position.
//
// Layout discovered at run time:
//
//   v0_3/<NN_name>.zg                    + sibling .err (single-file rejects)
//   v0_4/rejects/<NN_name>.zg            + sibling .error
//   v0_5..v0_9/rejects/<NN_name>/main.zg + sibling error.txt (per-dir)
//
// Diagnostic origin spans parser → typeck → borrow → loader → runtime; the
// test drives the same pipeline the CLI does — loader.Load + CheckBundle,
// then for runtime-panic rejects (v0_7 send-on-closed, v0_8 split: empty
// separator) RunBundle. The first error returned must contain `:N:M`.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/loader"
	"github.com/cmj/zerg/src/bootstrap/internal/run"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// posRe matches a `<line>:<col>` token with line,col ≥ 1 — the canonical
// position shape every diagnostic in the toolchain prints. Two emission
// styles are in use today:
//
//   - "<file>:<line>:<col>: <message>"     (loader, LoadError)
//   - "<category> error at <line>:<col>: <message>"   (parser, typeck,
//                                                      borrow, runtime)
//
// Both fit `\b<line>:<col>\b` with no leading colon required.
var posRe = regexp.MustCompile(`\b[1-9][0-9]*:[1-9][0-9]*\b`)

// bootstrapTestDir resolves to src/bootstrap/test/ from this test file's
// directory (src/bootstrap/internal/syntax/).
func bootstrapTestDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "test"))
}

// rejectCase identifies one reject-corpus program. Diagnostic-message
// substring matching is the existing reject harness's responsibility (it
// reads .err / .error / error.txt and exercises the toolchain binary's
// stderr); this test layers an orthogonal property — every emitted
// diagnostic carries source position — onto the same corpus.
type rejectCase struct {
	label string // human-readable, e.g. "v0_5/14_cycle_reject"
	main  string // absolute path to entry .zg
}

// collectRejectCorpus returns every reject program across v0_3..v0_9 in
// declaration order. The walk fails fast if any expected layout deviates,
// matching the existing per-version harnesses.
func collectRejectCorpus(t *testing.T) []rejectCase {
	t.Helper()
	root := bootstrapTestDir(t)
	var out []rejectCase

	// v0_3 — sibling .err per .zg in the top-level directory.
	matches, err := filepath.Glob(filepath.Join(root, "v0_3", "*.err"))
	if err != nil {
		t.Fatalf("glob v0_3: %v", err)
	}
	sort.Strings(matches)
	for _, m := range matches {
		base := strings.TrimSuffix(m, ".err")
		out = append(out, rejectCase{
			label: "v0_3/" + filepath.Base(base),
			main:  base + ".zg",
		})
	}

	// v0_4 — rejects/ subdir, sibling .error per .zg.
	matches, err = filepath.Glob(filepath.Join(root, "v0_4", "rejects", "*.error"))
	if err != nil {
		t.Fatalf("glob v0_4/rejects: %v", err)
	}
	sort.Strings(matches)
	for _, m := range matches {
		base := strings.TrimSuffix(m, ".error")
		out = append(out, rejectCase{
			label: "v0_4/" + filepath.Base(base),
			main:  base + ".zg",
		})
	}

	// v0_5..v0_9 — per-directory layout: rejects/<name>/{main.zg,error.txt}.
	for v := 5; v <= 9; v++ {
		dir := filepath.Join(root, fmt.Sprintf("v0_%d", v), "rejects")
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		var names []string
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			progDir := filepath.Join(dir, name)
			mp := filepath.Join(progDir, "main.zg")
			ep := filepath.Join(progDir, "error.txt")
			if _, err := os.Stat(ep); err != nil {
				t.Fatalf("reject %s missing error.txt: %v", progDir, err)
			}
			out = append(out, rejectCase{
				label: fmt.Sprintf("v0_%d/%s", v, name),
				main:  mp,
			})
		}
	}

	if len(out) == 0 {
		t.Fatalf("no reject programs discovered under %s", root)
	}
	return out
}

// driveReject runs the reject through the CLI pipeline (load → check → run)
// and returns the first error encountered. Per-stage early-return matches
// the toolchain's no-cascade contract.
func driveReject(main string) error {
	bundle, err := loader.Load(main)
	if err != nil {
		return err
	}
	if err := syntax.CheckBundle(bundle); err != nil {
		return err
	}
	// Runtime-panic rejects (send-on-closed, split: empty separator) only
	// surface during execution. RunBundle returns the runtime error
	// verbatim; we discard stdout into a sink.
	var sink bytes.Buffer
	_, _, err = run.RunBundleWithOptions(bundle, &sink, run.Options{Argv: []string{main}})
	return err
}

func TestRejectCorpusDiagnosticPositions(t *testing.T) {
	cases := collectRejectCorpus(t)
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			err := driveReject(tc.main)
			if err == nil {
				t.Fatalf("%s: pipeline accepted reject program", tc.label)
			}
			msg := err.Error()
			if !posRe.MatchString(msg) {
				t.Fatalf("%s: diagnostic %q lacks line:col position", tc.label, msg)
			}
		})
	}
}

// TestRejectCorpusSingleDiagnostic asserts the no-cascade contract: each
// reject program must emit exactly ONE diagnostic. Today this is structural
// (every checker pass early-returns on first error) but pinning it as a
// test guards against a future regression where someone "helpfully"
// accumulates errors and produces three downstream messages from one
// upstream cause.
//
// "One diagnostic" means: the pipeline returns a single error value whose
// message contains exactly one of the canonical category prefixes
// ("parse error", "type error", "borrow error", "runtime error", or a
// loader-style "<file>:N:M:" prefix appearing once).
func TestRejectCorpusSingleDiagnostic(t *testing.T) {
	categoryRe := regexp.MustCompile(`(parse error|type error|borrow error|runtime error)`)
	cases := collectRejectCorpus(t)
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			err := driveReject(tc.main)
			if err == nil {
				t.Fatalf("%s: pipeline accepted reject program", tc.label)
			}
			msg := err.Error()
			matches := categoryRe.FindAllString(msg, -1)
			// Loader-cycle diagnostics use the "<file>:N:M:" prefix without
			// a category word; in that case len(matches) == 0 is fine.
			if len(matches) > 1 {
				t.Fatalf("%s: cascade detected — %d category-prefixed diagnostics in one error message: %q",
					tc.label, len(matches), msg)
			}
		})
	}
}

// TestSingleSyntaxErrorEmitsOneDiagnostic is the synthetic-fixture half of
// the cascade-suppression contract. A program with one syntactic mistake
// (here: a missing `)` on a fn declaration) must produce exactly one parse
// error, not three downstream errors from continuation parsing.
func TestSingleSyntaxErrorEmitsOneDiagnostic(t *testing.T) {
	src := []byte("fn f(x: int -> int {\n    return x\n}\n")
	tokens, err := syntax.Lex(src)
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	_, err = syntax.Parse(tokens)
	if err == nil {
		t.Fatalf("Parse accepted malformed source")
	}
	msg := err.Error()
	// Exactly one "parse error" prefix: parser early-returns on first err.
	if n := strings.Count(msg, "parse error"); n != 1 {
		t.Fatalf("got %d parse-error prefixes, want 1: %q", n, msg)
	}
	if !posRe.MatchString(msg) {
		t.Fatalf("diagnostic %q lacks :line:col substring", msg)
	}
}
