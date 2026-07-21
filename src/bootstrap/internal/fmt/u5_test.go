package fmt

import "testing"

// TestGroup7Declarations round-trips the group 7 declarations through the oracle
// and checks the canonical layout: fields/variants/members one per line, 'pub'
// prefixes, generics with bounds, and const-expr literals reprinted verbatim.
func TestGroup7Declarations(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "struct with pub and defaulted fields",
			src:  "struct Point{\npub x:int\npub y : int\nlabel: str = \"origin\"\n}",
			want: "struct Point {\n\tpub x: int\n\tpub y: int\n\tlabel: str = \"origin\"\n}\n",
		},
		{
			name: "generic struct",
			src:  "pub struct Wrapper[T]{\npub value: T\n}",
			want: "pub struct Wrapper[T] {\n\tpub value: T\n}\n",
		},
		{
			name: "generic fn with conjunction bound",
			src:  "fn max[T: Ord + Eq](a: T, b: T) -> T {\n\treturn a\n}",
			want: "fn max[T: Ord + Eq](a: T, b: T) -> T {\n\treturn a\n}\n",
		},
		{
			name: "enum with payload variants",
			src:  "enum Shape{\nCircle(float)\nRect(float, float)\n}",
			want: "enum Shape {\n\tCircle(float)\n\tRect(float, float)\n}\n",
		},
		{
			name: "C-style discriminant enum keeps hex verbatim",
			src:  "enum Color{\nRed = 1\nGreen = 0xFF\nBlue\n}",
			want: "enum Color {\n\tRed = 1\n\tGreen = 0xFF\n\tBlue\n}\n",
		},
		{
			name: "type alias",
			src:  "pub type Meters = int",
			want: "pub type Meters = int\n",
		},
		{
			name: "spec with super, assoc type, assoc val, required and provided",
			src: "pub spec Ord : Eq{\ntype Item\nBITS: int\nfn compare(other: This) -> int\n" +
				"fn top() -> This {\nreturn this\n}\n}",
			want: "pub spec Ord: Eq {\n\ttype Item\n\tBITS: int\n\tfn compare(other: This) -> int\n" +
				"\tfn top() -> This {\n\t\treturn this\n\t}\n}\n",
		},
		{
			name: "spec assoc type with bound",
			src:  "spec Iterable{\ntype Item: Ord\n}",
			want: "spec Iterable {\n\ttype Item: Ord\n}\n",
		},
		{
			name: "impl spec for type",
			src:  "impl Ord for int{\ntype Item = int\nBITS := 32\nfn compare(other: This) -> int {\nreturn 0\n}\n}",
			want: "impl Ord for int {\n\ttype Item = int\n\tBITS := 32\n" +
				"\tfn compare(other: This) -> int {\n\t\treturn 0\n\t}\n}\n",
		},
		{
			name: "inherent impl with generics",
			src:  "impl[T] Wrapper[T]{\nfn get() -> T {\nreturn this.value\n}\n}",
			want: "impl[T] Wrapper[T] {\n\tfn get() -> T {\n\t\treturn this.value\n\t}\n}\n",
		},
		{
			name: "init declaration",
			src:  "init(){\nnop\n}",
			want: "init() {\n\tnop\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRoundTrip(t, tc.src); got != tc.want {
				t.Fatalf("canonical mismatch\n--- got ---\n%q\n--- want ---\n%q", got, tc.want)
			}
		})
	}
}

// TestGroup7Types round-trips every type-expression form in type position,
// including the optional '?', through a function signature.
func TestGroup7Types(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "tuple type",
			src:  "fn f(a: (int, str)) {\n\tnop\n}",
			want: "fn f(a: (int, str)) {\n\tnop\n}\n",
		},
		{
			name: "array type keeps the semicolon",
			src:  "fn f(a: [int; 8]) {\n\tnop\n}",
			want: "fn f(a: [int; 8]) {\n\tnop\n}\n",
		},
		{
			name: "array length keeps a const name",
			src:  "fn f(a: [int; N]) {\n\tnop\n}",
			want: "fn f(a: [int; N]) {\n\tnop\n}\n",
		},
		{
			name: "channel directions",
			src:  "fn f(a: chan[int], b: <-chan[int], c: chan[int]<-) {\n\tnop\n}",
			want: "fn f(a: chan[int], b: <-chan[int], c: chan[int]<-) {\n\tnop\n}\n",
		},
		{
			name: "function type",
			src:  "fn f(g: fn(int, str) -> int) {\n\tnop\n}",
			want: "fn f(g: fn(int, str) -> int) {\n\tnop\n}\n",
		},
		{
			name: "unsafe function type",
			src:  "fn f(g: ptr[unsafe fn(int)]) {\n\tnop\n}",
			want: "fn f(g: ptr[unsafe fn(int)]) {\n\tnop\n}\n",
		},
		{
			name: "pointer types",
			src:  "fn f(a: ptr, b: ptr[int]) {\n\tnop\n}",
			want: "fn f(a: ptr, b: ptr[int]) {\n\tnop\n}\n",
		},
		{
			name: "optional type",
			src:  "fn f(a: int?, b: list[str]?) {\n\tnop\n}",
			want: "fn f(a: int?, b: list[str]?) {\n\tnop\n}\n",
		},
		{
			name: "generic type argument",
			src:  "fn f(a: map[str, int]) {\n\tnop\n}",
			want: "fn f(a: map[str, int]) {\n\tnop\n}\n",
		},
		{
			name: "value generic argument",
			src:  "fn f(a: Matrix[3, 4]) {\n\tnop\n}",
			want: "fn f(a: Matrix[3, 4]) {\n\tnop\n}\n",
		},
		{
			name: "associated-type projection",
			src:  "fn f(a: Iterator.Item) -> Iterator.Item.Sub {\n\tnop\n}",
			want: "fn f(a: Iterator.Item) -> Iterator.Item.Sub {\n\tnop\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRoundTrip(t, tc.src); got != tc.want {
				t.Fatalf("canonical mismatch\n--- got ---\n%q\n--- want ---\n%q", got, tc.want)
			}
		})
	}
}

// TestGroup7Decorators round-trips decorators, including a decorated declaration
// carrying a doc-comment, keeping each decorator on its own line above the decl.
func TestGroup7Decorators(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "derive with multiple items",
			src:  "#[derive(Encode, Decode)]\nstruct Data{\npub id: int\n}",
			want: "#[derive(Encode, Decode)]\nstruct Data {\n\tpub id: int\n}\n",
		},
		{
			name: "align with const argument",
			src:  "#[align(16)]\nstruct Aligned{\npub x: int\n}",
			want: "#[align(16)]\nstruct Aligned {\n\tpub x: int\n}\n",
		},
		{
			name: "doc-comment above a decorated declaration",
			src:  "## a documented type\n#[derive(Encode)]\nstruct Doc{\npub y: int\n}",
			want: "## a documented type\n#[derive(Encode)]\nstruct Doc {\n\tpub y: int\n}\n",
		},
		{
			name: "field doc-comment and trailing comment preserved",
			src:  "struct P{\n## the x coordinate\npub x: int # inline\n}",
			want: "struct P {\n\t## the x coordinate\n\tpub x: int # inline\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRoundTrip(t, tc.src); got != tc.want {
				t.Fatalf("canonical mismatch\n--- got ---\n%q\n--- want ---\n%q", got, tc.want)
			}
		})
	}
}
