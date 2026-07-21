package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExamplesCompile guards the developer-facing sample programs: every
// numbered examples/NN_*.zg at the repo root must compile to C and link with cc.
// These are the first code a newcomer reads, so they must never rot. Skips when
// the repo root or a C compiler cannot be found.
func TestExamplesCompile(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("repo root (go.work) not found")
	}
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	sources, err := filepath.Glob(filepath.Join(root, "examples", "[0-9][0-9]_*.zg"))
	if err != nil || len(sources) == 0 {
		t.Skip("no numbered examples found")
	}

	for _, zg := range sources {
		name := strings.TrimSuffix(filepath.Base(zg), ".zg")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(zg)
			if err != nil {
				t.Fatalf("read %s: %v", zg, err)
			}
			code, diags := Compile(string(src))
			if len(diags) != 0 {
				t.Fatalf("%s should compile, got diagnostics: %v", name, diags)
			}
			// link (and run) to confirm the emitted C is valid
			compileAndRun(t, cc, code)
		})
	}
}

// repoRoot walks up from the working directory to the module/workspace root,
// identified by the go.work file.
func repoRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
