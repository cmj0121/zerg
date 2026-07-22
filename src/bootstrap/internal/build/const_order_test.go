package build

import (
	"strings"
	"testing"
)

// TestModuleConstantOrder is the Phase 1g S3 guard that module constants are
// evaluated in DEPENDENCY order, not declaration order: a forward reference must
// read its dependency's assigned value (not a zero) and infer its real type (not
// void). Each case compiles and runs; the output is the printed constants.
func TestModuleConstantOrder(t *testing.T) {
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
			// The false-green repro: `a` references the later-declared `b`. Declaration
			// order would compute a = 0 + 1 = 1; dependency order computes a = 10 + 1 = 11.
			name: "typed forward reference",
			src:  "a: int = b + 1\nb: int = 10\nfn main() {\n\tprint a\n\tprint b\n}\n",
			want: "11\n10\n",
		},
		{
			// The untyped forward reference: `a`'s type must infer to int from `b`, not
			// void; declaration-order typing would leave `a` void and fail to compile.
			name: "untyped forward reference",
			src:  "a := b + 1\nb := 10\nfn main() {\n\tprint a\n\tprint b\n}\n",
			want: "11\n10\n",
		},
		{
			name: "declaration order equals dependency order",
			src:  "b := 10\na := b + 1\nfn main() {\n\tprint b\n\tprint a\n}\n",
			want: "10\n11\n",
		},
		{
			// A constant may reference a later-declared function (functions are collected
			// before constants are typed), so there is no constant-to-constant edge here.
			name: "constant references a later function",
			src:  "a := f()\nfn f() -> int {\n\treturn 7\n}\nfn main() {\n\tprint a\n}\n",
			want: "7\n",
		},
		{
			// A three-constant chain a -> b -> c evaluated back to front.
			name: "transitive chain",
			src:  "a := b + 1\nb := c + 1\nc := 10\nfn main() {\n\tprint a\n\tprint b\n\tprint c\n}\n",
			want: "12\n11\n10\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, diags := Compile(tc.src)
			if len(diags) != 0 {
				t.Fatalf("should compile, got diagnostics: %v", diags)
			}
			if got := compileAndRun(t, cc, code); got != tc.want {
				t.Fatalf("run: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestModuleConstantCycle guards that a dependency cycle among module constants is a
// clean, span-anchored compile error — not a leaked `void` global that fails at the
// C compiler.
func TestModuleConstantCycle(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "two-constant cycle",
			src:  "p := q + 1\nq := p + 1\nfn main() {\n\tprint p\n}\n",
		},
		{
			name: "self cycle",
			src:  "x := x + 1\nfn main() {\n\tprint x\n}\n",
		},
		{
			name: "three-constant cycle",
			src:  "a := b + 1\nb := c + 1\nc := a + 1\nfn main() {\n\tprint a\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, diags := Compile(tc.src)
			if len(diags) == 0 {
				t.Fatalf("a constant cycle should be a compile error")
			}
			found := false
			for _, d := range diags {
				if strings.Contains(d.Msg, "module-constant cycle") {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected a 'module-constant cycle' diagnostic, got: %v", diags)
			}
		})
	}
}
