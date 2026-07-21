package build

import (
	"strings"
	"testing"
)

func TestCompileSuccess(t *testing.T) {
	code, diags := Compile("fn main() {\n  print 1 + 2\n}")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !strings.Contains(code, "int main(void)") {
		t.Fatalf("emitted C missing a C main:\n%s", code)
	}
}

func TestCompileStopsAtParseError(t *testing.T) {
	code, diags := Compile("fn f( {")
	if len(diags) == 0 {
		t.Fatalf("expected a parse diagnostic")
	}
	if code != "" {
		t.Fatalf("code should be empty on failure, got %q", code)
	}
}

func TestCompileStopsAtSemaError(t *testing.T) {
	code, diags := Compile("fn main() { print undefined_name }")
	if len(diags) == 0 {
		t.Fatalf("expected a sema diagnostic")
	}
	if code != "" {
		t.Fatalf("code should be empty on failure, got %q", code)
	}
}
