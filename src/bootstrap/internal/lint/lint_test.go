package lint

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
)

// TestCheck is the table for the two lint-only findings: unused imports and unused
// module-private declarations, and the exemptions (used names, `pub` items, `main`,
// and `import pub` re-exports).
func TestCheck(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string // substrings each expected finding must contain; nil = no findings
	}{
		{
			name: "unused import",
			src:  "import \"io\"\nfn main() {\n\tprint 1\n}\n",
			want: []string{"unused import \"io\""},
		},
		{
			name: "used import",
			src:  "import \"io\"\nfn main() {\n\tprint io.write(\"x\")\n}\n",
			want: nil,
		},
		{
			name: "import pub is exempt",
			src:  "import pub \"deep\"\n",
			want: nil,
		},
		{
			name: "aliased import used by alias",
			src:  "import \"std/text\" as text\nfn main() {\n\tprint text.id(1)\n}\n",
			want: nil,
		},
		{
			name: "aliased import unused",
			src:  "import \"std/text\" as text\nfn main() {\n\tprint 1\n}\n",
			want: []string{"unused import \"std/text\""},
		},
		{
			name: "unused private function",
			src:  "fn helper() -> int {\n\treturn 2\n}\nfn main() {\n\tprint 1\n}\n",
			want: []string{"unused function \"helper\""},
		},
		{
			name: "used private function",
			src:  "fn helper() -> int {\n\treturn 2\n}\nfn main() {\n\tprint helper()\n}\n",
			want: nil,
		},
		{
			name: "pub function is exempt",
			src:  "pub fn api() -> int {\n\treturn 1\n}\nfn main() {\n\tprint 1\n}\n",
			want: nil,
		},
		{
			name: "main is exempt",
			src:  "fn main() {\n\tprint 1\n}\n",
			want: nil,
		},
		{
			name: "unused private struct",
			src:  "struct S {\n\tx: int\n}\nfn main() {\n\tprint 1\n}\n",
			want: []string{"unused struct \"S\""},
		},
		{
			name: "struct used as a type is exempt",
			src:  "struct S {\n\tx: int\n}\nfn get(s: S) -> int {\n\treturn s.x\n}\nfn main() {\n\tprint get(S(x: 1))\n}\n",
			want: nil,
		},
		{
			name: "unused private constant",
			src:  "answer := 42\nfn main() {\n\tprint 1\n}\n",
			want: []string{"unused constant \"answer\""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, diags := parser.Parse(tc.src)
			if len(diags) > 0 {
				t.Fatalf("source should parse, got: %v", diags)
			}
			findings := Check(file)
			if len(findings) != len(tc.want) {
				t.Fatalf("got %d findings %v, want %d %v", len(findings), rendered(findings), len(tc.want), tc.want)
			}
			for i, want := range tc.want {
				if !strings.Contains(findings[i].Msg, want) {
					t.Fatalf("finding %d = %q, want substring %q", i, findings[i].Msg, want)
				}
			}
		})
	}
}

func rendered(diags []diag.Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Msg
	}
	return out
}
