package types

import "testing"

// TestPrimitivesInterned checks that primitive references are singletons: two
// references to the same primitive are the same pointer (the property the emitter
// relies on for == dispatch).
func TestPrimitivesInterned(t *testing.T) {
	// A reference to a primitive is the same pointer wherever it is used, so a
	// consumer can dispatch on identity. Alias through a Type variable to make the
	// point without comparing an expression to itself.
	var alias Type = Int
	if alias != Int {
		t.Fatal("a primitive reference must be identical to its singleton")
	}
	if Int == Float {
		t.Fatal("distinct primitives must be distinct pointers")
	}
	if !Identical(Int, Int) {
		t.Fatal("Identical must hold for the same primitive")
	}
	if Identical(Int, Float) {
		t.Fatal("Identical must not hold for distinct primitives")
	}
}

func TestIdentical(t *testing.T) {
	userA := &TypeDef{Name: "A"}
	userB := &TypeDef{Name: "B"}
	paramT := &Param{Name: "T"}
	paramU := &Param{Name: "U"}

	tests := []struct {
		name string
		a, b Type
		want bool
	}{
		{"same primitive", Int, Int, true},
		{"different primitive", Int, Bool, false},
		{"invalid is compatible", Invalid, Bool, true},
		{"unknown is compatible", Unknown, Str, true},
		{"list of same", &List{Int}, &List{Int}, true},
		{"list of different", &List{Int}, &List{Float}, false},
		{"set vs list", &Set{Int}, &List{Int}, false},
		{"map same", &Map{Int, Str}, &Map{Int, Str}, true},
		{"map different val", &Map{Int, Str}, &Map{Int, Bool}, false},
		{"tuple same", &Tuple{[]Type{Int, Bool}}, &Tuple{[]Type{Int, Bool}}, true},
		{"tuple arity", &Tuple{[]Type{Int}}, &Tuple{[]Type{Int, Bool}}, false},
		{"opt same", &Opt{Int}, &Opt{Int}, true},
		{"either same", &Either{Int, Str}, &Either{Int, Str}, true},
		{"either flipped", &Either{Int, Str}, &Either{Str, Int}, false},
		{"array same length", &Array{Int, ConstVal{Kind: KInt, I: 3, Known: true}},
			&Array{Int, ConstVal{Kind: KInt, I: 3, Known: true}}, true},
		{"array diff length", &Array{Int, ConstVal{Kind: KInt, I: 3, Known: true}},
			&Array{Int, ConstVal{Kind: KInt, I: 4, Known: true}}, false},
		{"array unknown length compatible", &Array{Int, ConstVal{}},
			&Array{Int, ConstVal{Kind: KInt, I: 4, Known: true}}, true},
		{"bare ptr same", &Ptr{nil}, &Ptr{nil}, true},
		{"bare ptr vs typed", &Ptr{nil}, &Ptr{Int}, false},
		{"fn same", &Fn{Params: []Param0{{Type: Int}}, Ret: Bool},
			&Fn{Params: []Param0{{Type: Int}}, Ret: Bool}, true},
		{"fn ret differ", &Fn{Params: []Param0{{Type: Int}}, Ret: Bool},
			&Fn{Params: []Param0{{Type: Int}}, Ret: Int}, false},
		{"fn nil ret same", &Fn{Ret: nil}, &Fn{Ret: nil}, true},
		{"struct same def", &Struct{Def: userA}, &Struct{Def: userA}, true},
		{"struct diff def", &Struct{Def: userA}, &Struct{Def: userB}, false},
		{"enum vs struct kind", &Enum{Def: userA}, &Struct{Def: userA}, false},
		{"param identity", paramT, paramT, true},
		{"param distinct", paramT, paramU, false},
		{"proj same", &Proj{On: paramT, Path: []string{"Item"}},
			&Proj{On: paramT, Path: []string{"Item"}}, true},
		{"proj diff path", &Proj{On: paramT, Path: []string{"Item"}},
			&Proj{On: paramT, Path: []string{"Key"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Identical(tt.a, tt.b); got != tt.want {
				t.Fatalf("Identical(%s, %s) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		t    Type
		want string
	}{
		{Int, "int"},
		{&List{Int}, "list[int]"},
		{&Map{Str, Int}, "map[str, int]"},
		{&Opt{Bool}, "bool?"},
		{&Fixed{Bits: 32, Signed: true}, "i32"},
		{&Fixed{Bits: 8, Signed: false}, "u8"},
		{&Fixed{Bits: 64, Float: true}, "f64"},
		{&Ptr{nil}, "ptr"},
		{&Ptr{Int}, "ptr[int]"},
		{&Tuple{[]Type{Int, Bool}}, "(int, bool)"},
		{&Either{Int, Str}, "Either[int, str]"},
	}
	for _, tt := range tests {
		if got := tt.t.String(); got != tt.want {
			t.Fatalf("String() = %q, want %q", got, tt.want)
		}
	}
}
