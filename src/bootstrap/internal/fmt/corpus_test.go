package fmt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// TestFmtCorpusParity is the v0.10 Unit 3 (part 2) parity gate. After the
// chore-rewrite that aligned v0.0–v0.9 corpus programs with the canonical
// style locked in STYLE.md, `Format(Parse(src)) == src` byte-for-byte for
// every deterministic corpus program. Any future drift — either the
// formatter changing emission or a corpus file regaining a non-canonical
// shape — fails this test with the offending paths listed.
//
// Skips: rejects/ (programs that never parse) and scheduling/ (concurrency
// fixtures with their own harness; not part of the parity surface).
func TestFmtCorpusParity(t *testing.T) {
	root := corpusRoot(t)
	versions := []string{"v0_0", "v0_1", "v0_2", "v0_3", "v0_4", "v0_5", "v0_6", "v0_7", "v0_8", "v0_9"}

	var (
		scanned int
		differ  []string
	)
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
			scanned++
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Errorf("read %s: %v", path, rerr)
				return nil
			}
			tokens, comments, lerr := syntax.LexWithComments(src)
			if lerr != nil {
				t.Errorf("lex %s: %v", path, lerr)
				return nil
			}
			prog, perr := syntax.ParseWithComments(tokens, comments)
			if perr != nil {
				t.Errorf("parse %s: %v", path, perr)
				return nil
			}
			out := Format(prog)
			if string(out) != string(src) {
				rel, _ := filepath.Rel(root, path)
				differ = append(differ, rel)
			}
			return nil
		})
	}
	t.Logf("corpus parity: %d programs scanned", scanned)
	if len(differ) > 0 {
		t.Fatalf("%d corpus programs differ from Format(Parse(src)) — fmt drift or non-canonical source:\n  %s",
			len(differ), strings.Join(differ, "\n  "))
	}
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
