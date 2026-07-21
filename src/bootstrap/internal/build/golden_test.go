package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/corpus"
)

// TestCodegenGolden is the end-to-end oracle: every codegen/*.zg in the test-data
// submodule is compiled to C, linked with cc, run, and its stdout compared to the
// sibling *.out golden. It skips when the submodule or a C compiler is absent.
func TestCodegenGolden(t *testing.T) {
	dir, ok := corpus.Path("codegen")
	if !ok {
		t.Skip("test-data submodule not initialized (run: git submodule update --init)")
	}
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	sources, err := filepath.Glob(filepath.Join(dir, "*.zg"))
	if err != nil || len(sources) == 0 {
		t.Skipf("no codegen cases in %s", dir)
	}

	for _, zg := range sources {
		name := strings.TrimSuffix(filepath.Base(zg), ".zg")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(zg)
			if err != nil {
				t.Fatalf("read %s: %v", zg, err)
			}
			want, err := os.ReadFile(strings.TrimSuffix(zg, ".zg") + ".out")
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			code, diags := Compile(string(src))
			if len(diags) != 0 {
				t.Fatalf("compile diagnostics: %v", diags)
			}
			got := compileAndRun(t, cc, code)
			if got != string(want) {
				t.Fatalf("output mismatch\n got: %q\nwant: %q", got, string(want))
			}
		})
	}
}

func findCC() string {
	for _, name := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func compileAndRun(t *testing.T, cc, code string) string {
	t.Helper()
	tmp := t.TempDir()
	cpath := filepath.Join(tmp, "out.c")
	bpath := filepath.Join(tmp, "out.bin")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	if out, err := exec.Command(cc, "-std=c11", "-o", bpath, cpath).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- C ---\n%s", err, out, code)
	}
	out, err := exec.Command(bpath).CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	return string(out)
}
