package sema

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
)

// checkInfo parses and checks src, returning the Info side tables plus the
// diagnostics as strings.
func checkInfo(t *testing.T, src string) (*Info, []string) {
	t.Helper()
	file, diags := parser.Parse(src)
	if len(diags) != 0 {
		t.Fatalf("parse errors for %q: %v", src, diags)
	}
	info, sdiags := Check(file)
	msgs := make([]string, len(sdiags))
	for i, d := range sdiags {
		msgs[i] = d.Msg
	}
	return info, msgs
}

// onlyBracket returns the single settled Bracket resolution, failing otherwise.
func onlyBracket(t *testing.T, info *Info) BracketRes {
	t.Helper()
	if len(info.Brackets) != 1 {
		t.Fatalf("expected exactly one settled bracket, got %d", len(info.Brackets))
	}
	for _, r := range info.Brackets {
		return r
	}
	return BracketRes{}
}

// onlyPattern returns the single settled NamePattern resolution, failing otherwise.
func onlyPattern(t *testing.T, info *Info) NameRes {
	t.Helper()
	if len(info.Patterns) != 1 {
		t.Fatalf("expected exactly one settled name pattern, got %d", len(info.Patterns))
	}
	for _, r := range info.Patterns {
		return r
	}
	return NameRes{}
}

// TestBracketResolution settles the provisional '[ … ]' postfix: a value base is
// an index, a type/generic base is type arguments, and a comma is unambiguously
// type arguments regardless of the base.
func TestBracketResolution(t *testing.T) {
	t.Run("value base is an index", func(t *testing.T) {
		info, msgs := checkInfo(t, "fn f() {\n  xs := 1\n  y := xs[0]\n}")
		if len(msgs) != 0 {
			t.Fatalf("unexpected diags: %v", msgs)
		}
		if got := onlyBracket(t, info).Kind; got != BracketIndex {
			t.Fatalf("bracket kind = %v, want BracketIndex", got)
		}
	})

	t.Run("type-constructor base is type args", func(t *testing.T) {
		info, _ := checkInfo(t, "fn f() {\n  y := list[int]\n}")
		if got := onlyBracket(t, info).Kind; got != BracketTypeArg {
			t.Fatalf("bracket kind = %v, want BracketTypeArg", got)
		}
	})

	t.Run("named-type base is type args", func(t *testing.T) {
		info, _ := checkInfo(t, "struct Box {}\nfn f() {\n  y := Box[int]\n}")
		if got := onlyBracket(t, info).Kind; got != BracketTypeArg {
			t.Fatalf("bracket kind = %v, want BracketTypeArg", got)
		}
	})

	t.Run("a comma is always type args", func(t *testing.T) {
		info, _ := checkInfo(t, "fn f() {\n  xs := 1\n  y := xs[0, 1]\n}")
		if got := onlyBracket(t, info).Kind; got != BracketTypeArg {
			t.Fatalf("bracket kind = %v, want BracketTypeArg (comma wins over a value base)", got)
		}
	})
}

// TestNamePatternResolution settles a name pattern: a QUALIFIED one is a nullary
// variant pattern, and a BARE one is a fresh binding — whatever the name means in
// scope, and whatever letter it starts with (GRAMMAR#pattern).
func TestNamePatternResolution(t *testing.T) {
	t.Run("a qualified name is a variant pattern", func(t *testing.T) {
		// a trailing '_' keeps the match exhaustive; the resolver settles 'Color.Red'
		// as a variant because the pattern named the enum it belongs to.
		info, msgs := checkInfo(t, "enum Color {\n  Red\n  Green\n  Blue\n}\n"+
			"fn f(c: Color) -> int {\n  return match c {\n    Color.Red => 0\n    _ => 2\n  }\n}")
		if len(msgs) != 0 {
			t.Fatalf("unexpected diags: %v", msgs)
		}
		res := onlyPattern(t, info)
		if res.Kind != NameVariant {
			t.Fatalf("pattern kind = %v, want NameVariant", res.Kind)
		}
		if res.Variant == nil || res.Variant.Name != "Red" {
			t.Fatalf("variant = %v, want Red", res.Variant)
		}
	})

	t.Run("a bare name binds even when a variant answers to it", func(t *testing.T) {
		// 'Red' names a variant in scope and STILL binds: were it resolved, declaring a
		// variant in another file would change what this arm matched. It is the only arm,
		// so binding is also what keeps the match exhaustive.
		info, msgs := checkInfo(t, "enum Color {\n  Red\n  Green\n  Blue\n}\n"+
			"fn f(c: Color) -> int {\n  return match c {\n    Red => 0\n  }\n}")
		if len(msgs) != 0 {
			t.Fatalf("unexpected diags: %v", msgs)
		}
		res := onlyPattern(t, info)
		if res.Kind != NameBinding {
			t.Fatalf("pattern kind = %v, want NameBinding", res.Kind)
		}
		if res.Sym == nil || res.Sym.Name != "Red" {
			t.Fatalf("binding symbol = %v, want Red", res.Sym)
		}
	})

	t.Run("an unknown name is a fresh binding", func(t *testing.T) {
		info, _ := checkInfo(t, "fn f(n: int) -> int {\n  return match n {\n    x => x\n  }\n}")
		res := onlyPattern(t, info)
		if res.Kind != NameBinding {
			t.Fatalf("pattern kind = %v, want NameBinding", res.Kind)
		}
		if res.Sym == nil || res.Sym.Name != "x" {
			t.Fatalf("binding symbol = %v, want x", res.Sym)
		}
	})
}

// TestImportNamespaceBinding checks that an import binds a namespace into the one
// value namespace: a colliding name (with another import or a top-level
// declaration) is an error, and an 'as' rename avoids it.
func TestImportNamespaceBinding(t *testing.T) {
	t.Run("two imports sharing a last segment collide", func(t *testing.T) {
		wantErr(t, "import \"util/text\"\nimport \"other/text\"\nfn f() { nop }", "collides")
	})

	t.Run("an 'as' rename avoids the collision", func(t *testing.T) {
		wantOK(t, "import \"util/text\"\nimport \"other/text\" as t2\nfn f() { nop }")
	})

	t.Run("a namespace collides with a top-level name", func(t *testing.T) {
		wantErr(t, "fn text() { nop }\nimport \"util/text\"\nfn f() { nop }", "collides")
	})
}
