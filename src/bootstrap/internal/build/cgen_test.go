package build

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

func TestCQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", `""`},
		{"plain", "hello", `"hello"`},
		{"escaped quote", `say "hi"`, `"say \"hi\""`},
		{"backslash", `a\b`, `"a\\b"`},
		{"newline", "a\nb", `"a\nb"`},
		{"tab", "a\tb", `"a\tb"`},
		{"carriage return", "a\rb", `"a\rb"`},
		{"nul is three-digit octal", "a\x00b", `"a\000b"`},
		{"nul followed by digit stays three-digit", "\x001", `"\0001"`},
		{"low control byte", "\x01", `"\001"`},
		{"del", "\x7f", `"\177"`},
		{"high bit byte passes through", "\xff", "\"\xff\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cQuote(c.in)
			if got != c.want {
				t.Fatalf("cQuote(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestEmitUsesFwriteForParity(t *testing.T) {
	// puts() truncates at NUL; fwrite() does not. The interpreter writes the
	// full byte sequence via fmt.Fprintln, so the codegen must too.
	prog := &syntax.Program{
		Statements: []syntax.Stmt{
			&syntax.PrintStmt{Value: "hi"},
		},
	}
	var buf bytes.Buffer
	if err := Emit(prog, &buf); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "puts(") {
		t.Errorf("Emit still uses puts() — switch to fwrite for NUL-clean parity:\n%s", out)
	}
	if !strings.Contains(out, `fwrite("hi", 2, 1, stdout);`) {
		t.Errorf("Emit must call fwrite with the byte length; got:\n%s", out)
	}
	if !strings.Contains(out, `putchar('\n');`) {
		t.Errorf("Emit must follow fwrite with putchar('\\n'); got:\n%s", out)
	}
}

func TestEmitNop(t *testing.T) {
	prog := &syntax.Program{
		Statements: []syntax.Stmt{&syntax.NopStmt{}},
	}
	var buf bytes.Buffer
	if err := Emit(prog, &buf); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !strings.Contains(buf.String(), "(void)0;") {
		t.Errorf("nop should lower to (void)0;, got:\n%s", buf.String())
	}
}
