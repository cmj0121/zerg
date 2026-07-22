package sema

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// TestScopeDeclareLookup exercises the retained scope tree: declareLocal reports a
// same-scope duplicate, and lookup walks outward to the first (innermost) match.
func TestScopeDeclareLookup(t *testing.T) {
	root := newScope(ScopeModule, nil)
	child := newScope(ScopeBlock, root)

	a := &Symbol{Name: "a", Kind: SymVar, Type: types.Int}
	if prev := root.declareLocal(a); prev != nil {
		t.Fatalf("first declare of a should succeed, got prev %v", prev)
	}
	if prev := root.declareLocal(&Symbol{Name: "a", Kind: SymVar}); prev != a {
		t.Fatalf("redeclare of a should return the existing symbol, got %v", prev)
	}

	// a is visible from the child through the parent chain.
	if got := child.lookup("a"); got != a {
		t.Fatalf("child.lookup(a) = %v, want the root symbol", got)
	}

	// a child binding shadows the outer one for lookup, but the outer stays visible.
	shadow := &Symbol{Name: "a", Kind: SymVar, Type: types.Str}
	child.declareLocal(shadow)
	if got := child.lookup("a"); got != shadow {
		t.Fatalf("child.lookup(a) after shadow = %v, want the child symbol", got)
	}
	if got := lookupVisible(child.Parent, "a"); got != a {
		t.Fatalf("lookupVisible(child.Parent, a) = %v, want the root symbol", got)
	}
	if got := child.lookup("missing"); got != nil {
		t.Fatalf("lookup of an undeclared name = %v, want nil", got)
	}
}

// TestConstBidirectionalShadow covers the 'const' shadow-proof rule in both
// directions (GRAMMAR group 4): a 'const' may not shadow a visible outer binding,
// and no later binding may shadow a visible outer 'const'; a plain ':=' may still
// shadow a plain ':='.
func TestConstBidirectionalShadow(t *testing.T) {
	// (b) a const may not shadow a visible outer binding
	wantErr(t, "fn f() {\n  x := 1\n  if true {\n    const x := 2\n  }\n}", "shadow")
	// (a) a later binding may not shadow a visible outer const
	wantErr(t, "fn f() {\n  const x := 1\n  if true {\n    x := 2\n  }\n}", "shadow")
	// a plain ':=' shadowing a plain ':=' in a nested scope is allowed
	wantOK(t, "fn f() {\n  x := 1\n  if true {\n    x := 2\n  }\n}")
}
