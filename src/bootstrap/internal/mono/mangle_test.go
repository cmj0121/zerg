package mono

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// TestTypeCodeInjective checks that the type-code encoding distinguishes a bare
// nominal name containing '__' from a genuinely applied generic type (B3/B4): a
// user 'struct Box__i' and 'Box[int]' must not encode to the same fragment.
func TestTypeCodeInjective(t *testing.T) {
	boxT := &types.TypeDef{Name: "Box", Params: []*types.Param{{Name: "T"}}}
	boxInt := &types.Struct{Def: boxT, Args: []types.Type{types.Int}}
	boxUnderscore := &types.Struct{Def: &types.TypeDef{Name: "Box__i"}}

	if a, b := typeCode(boxInt), typeCode(boxUnderscore); a == b {
		t.Fatalf("Box[int] and struct Box__i must encode differently, both = %q", a)
	}
	// distinct type arguments must also encode distinctly
	boxBool := &types.Struct{Def: boxT, Args: []types.Type{types.Bool}}
	if a, b := typeCode(boxInt), typeCode(boxBool); a == b {
		t.Fatalf("Box[int] and Box[bool] must encode differently, both = %q", a)
	}
}

// TestInstanceNamePrefix checks the reserved-prefix scheme: a non-generic type
// keeps 'zg_<name>', a specialized type takes 'zgt_', so no user 'zg_<ident>' can
// alias a synthesized name.
func TestInstanceNamePrefix(t *testing.T) {
	def := &types.TypeDef{Name: "Box", Params: []*types.Param{{Name: "T"}}}
	if got := typeInstanceName(def, nil); got != "zg_Box" {
		t.Fatalf("non-generic type name = %q, want zg_Box", got)
	}
	got := typeInstanceName(def, []types.Type{types.Int})
	if got[:4] != "zgt_" {
		t.Fatalf("specialized type name = %q, want a zgt_ prefix", got)
	}
}
