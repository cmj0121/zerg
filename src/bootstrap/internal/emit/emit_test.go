package emit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
)

// emitC parses, checks, and emits C for src, failing on any diagnostic.
func emitC(t *testing.T, src string) string {
	t.Helper()
	file, pdiags := parser.Parse(src)
	if len(pdiags) != 0 {
		t.Fatalf("parse errors: %v", pdiags)
	}
	info, sdiags := sema.Check(file)
	if len(sdiags) != 0 {
		t.Fatalf("sema errors: %v", sdiags)
	}
	code, ediags := Emit(file, info)
	if len(ediags) != 0 {
		t.Fatalf("emit errors: %v", ediags)
	}
	return code
}

func TestEmitShape(t *testing.T) {
	code := emitC(t, "fn main() {\n  x := 1 + 2\n  print x\n}")
	for _, want := range []string{
		"#include <stdio.h>",
		"void zg_main(void)",
		"int64_t zg_x = (1 + 2);",
		"printf(\"%lld\\n\", (long long)(zg_x));",
		"int main(void) {",
		"zg_main();",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("generated C missing %q\n---\n%s", want, code)
		}
	}
}

func TestEmitMainReturnsInt(t *testing.T) {
	code := emitC(t, "fn main() -> int { return 0 }")
	if !strings.Contains(code, "int64_t zg_main(void)") {
		t.Fatalf("want int-returning zg_main:\n%s", code)
	}
	if !strings.Contains(code, "return (int)zg_main();") {
		t.Fatalf("want C main to forward zg_main's status:\n%s", code)
	}
}

func TestNoMain(t *testing.T) {
	file, _ := parser.Parse("fn helper() { nop }")
	info, _ := sema.Check(file)
	_, diags := Emit(file, info)
	if len(diags) == 0 {
		t.Fatalf("expected a diagnostic for a program with no main")
	}
}

// TestCompileAndRun exercises the full backend: emit C, compile with cc, run, and
// compare stdout. Skipped when no C compiler is available.
func TestCompileAndRun(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "arithmetic",
			src:  "fn main() {\n  print 6 * 7\n}",
			want: "42\n",
		},
		{
			name: "call-and-if",
			src: "fn max(a: int, b: int) -> int {\n" +
				"  if a > b { return a }\n  return b\n}\n" +
				"fn main() {\n  print max(3, 9)\n}",
			want: "9\n",
		},
		{
			name: "while-loop",
			src: "fn main() {\n  mut i := 0\n  mut s := 0\n" +
				"  for i < 5 { s = s + i\n i = i + 1 }\n  print s\n}",
			want: "10\n",
		},
		{
			name: "float-and-bool",
			src:  "fn main() {\n  x: float = 1\n  print x + 0.5\n  print 2 > 1\n}",
			want: "1.5\ntrue\n",
		},
		{
			name: "string",
			src:  "fn main() {\n  print \"hello, world\"\n}",
			want: "hello, world\n",
		},
		{
			name: "match-int-and-str",
			src: "fn sign(n: int) -> int {\n  return match n {\n    0 => 0\n    x if x < 0 => -1\n    _ => 1\n  }\n}\n" +
				"fn kind(s: str) -> str {\n  return match s {\n    \"hi\" => \"greeting\"\n    _ => \"other\"\n  }\n}\n" +
				"fn main() {\n  print sign(-3)\n  print sign(7)\n  print kind(\"hi\")\n  print kind(\"x\")\n}",
			want: "-1\n1\ngreeting\nother\n",
		},
		{
			name: "return-if-guard",
			src: "fn clamp(v: int) -> int {\n  return 0 if v < 0\n  return 100 if v > 100\n  return v\n}\n" +
				"fn main() {\n  print clamp(-5)\n  print clamp(150)\n  print clamp(42)\n}",
			want: "0\n100\n42\n",
		},
		{
			// 'mut n := n' shadows the parameter; C needs distinct names.
			name: "shadow-parameter",
			src: "fn bump(n: int) -> int {\n  mut n := n\n  n = n + 1\n  return n\n}\n" +
				"fn main() {\n  print bump(41)\n}",
			want: "42\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := emitC(t, tc.src)
			got := compileAndRun(t, cc, code)
			if got != tc.want {
				t.Fatalf("output = %q, want %q\n--- C ---\n%s", got, tc.want, code)
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
	dir := t.TempDir()
	cpath := filepath.Join(dir, "out.c")
	bpath := filepath.Join(dir, "out.bin")
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
