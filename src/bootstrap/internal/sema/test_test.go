package sema

import (
	"strings"
	"testing"
)

// TestDiscoverTestFunctions checks that a `#[test]` function is collected into
// Info.Tests with a label, an ordinary function is not, and both admissible return
// shapes (`nil` and `Result[nil]`) are accepted (Phase 1i U1).
func TestDiscoverTestFunctions(t *testing.T) {
	src := "#[test]\nfn test_a() {\n  nop\n}\n" +
		"#[test]\nfn test_b() -> Result[nil] {\n  return nil\n}\n" +
		"fn helper() -> int {\n  return 1\n}\n"
	info, msgs := checkInfo(t, src)
	if len(msgs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if len(info.Tests) != 2 {
		t.Fatalf("collected %d tests, want 2", len(info.Tests))
	}
	if info.Tests[0].Name != "test_a" || info.Tests[1].Name != "test_b" {
		t.Fatalf("test order = [%s %s], want [test_a test_b]", info.Tests[0].Name, info.Tests[1].Name)
	}
	// an entry-module test's label is its bare surface name (no module prefix).
	if info.Tests[0].TestLabel != "test_a" {
		t.Fatalf("label = %q, want %q", info.Tests[0].TestLabel, "test_a")
	}
}

// TestMisshapenTestRejected checks the shape rules: a parameter, a generic
// parameter, or a non-nil/Result[nil] return each makes a `#[test]` function a span
// diagnostic and keeps it out of Info.Tests (Phase 1i U1, DESIGN-1i §2.2).
func TestMisshapenTestRejected(t *testing.T) {
	cases := map[string]string{
		"param":   "#[test]\nfn test_p(x: int) {\n  nop\n}\n",
		"generic": "#[test]\nfn test_g[T]() {\n  nop\n}\n",
		"ret":     "#[test]\nfn test_r() -> int {\n  return 1\n}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			info, msgs := checkInfo(t, src)
			if len(info.Tests) != 0 {
				t.Fatalf("a malformed #[test] must not be collected, got %d", len(info.Tests))
			}
			if !hasSubstr(msgs, "#[test] function") {
				t.Fatalf("expected a #[test] shape diagnostic, got %v", msgs)
			}
		})
	}
}

// TestTestLabelModulePrefix checks testLabel turns a loader-mangled cross-module
// name (`<tag>__surface`) into the `module::surface` label the runner prints.
func TestTestLabelModulePrefix(t *testing.T) {
	if got := testLabel("geom__test_area"); got != "geom::test_area" {
		t.Fatalf("testLabel = %q, want %q", got, "geom::test_area")
	}
	if got := testLabel("test_local"); got != "test_local" {
		t.Fatalf("testLabel = %q, want %q (entry module: no prefix)", got, "test_local")
	}
}

func hasSubstr(msgs []string, sub string) bool {
	for _, m := range msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}
