package fmt

import "testing"

// TestGroup6ControlFlow round-trips the group 6 control-flow surface — the if
// expression and binding head, the three 'for' loops (including the
// parenthesized membership while), 'with', and 'break'/'continue if' — through
// the oracle and pins their canonical layout.
func TestGroup6ControlFlow(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "if-expr yields a value",
			src:  "fn f() -> int {\nx := if true {\n1\n} else {\n2\n}\nreturn x\n}",
			want: "fn f() -> int {\n\tx := if true {\n\t\t1\n\t} else {\n\t\t2\n\t}\n\treturn x\n}\n",
		},
		{
			name: "if binding head",
			src:  "fn f() {\nif y := g() {\nprint y\n}\n}",
			want: "fn f() {\n\tif y := g() {\n\t\tprint y\n\t}\n}\n",
		},
		{
			name: "for infinite",
			src:  "fn f() {\nfor {\nbreak\n}\n}",
			want: "fn f() {\n\tfor {\n\t\tbreak\n\t}\n}\n",
		},
		{
			name: "for while",
			src:  "fn f() {\nfor i < 5 {\nnop\n}\n}",
			want: "fn f() {\n\tfor i < 5 {\n\t\tnop\n\t}\n}\n",
		},
		{
			name: "for iterate",
			src:  "fn f() {\nfor x in items {\nprint x\n}\n}",
			want: "fn f() {\n\tfor x in items {\n\t\tprint x\n\t}\n}\n",
		},
		{
			name: "for iterate mut",
			src:  "fn f() {\nfor mut x in items {\nnop\n}\n}",
			want: "fn f() {\n\tfor mut x in items {\n\t\tnop\n\t}\n}\n",
		},
		{
			name: "for parenthesized membership while",
			src:  "fn f() {\nfor (v in r) {\nnop\n}\n}",
			want: "fn f() {\n\tfor (v in r) {\n\t\tnop\n\t}\n}\n",
		},
		{
			name: "with as",
			src:  "fn f() {\nwith acquire() as y {\nprint y\n}\n}",
			want: "fn f() {\n\twith acquire() as y {\n\t\tprint y\n\t}\n}\n",
		},
		{
			name: "with no binding",
			src:  "fn f() {\nwith lock() {\nnop\n}\n}",
			want: "fn f() {\n\twith lock() {\n\t\tnop\n\t}\n}\n",
		},
		{
			name: "break if and continue if",
			src:  "fn f() {\nfor {\ncontinue if skip\nbreak if done\n}\n}",
			want: "fn f() {\n\tfor {\n\t\tcontinue if skip\n\t\tbreak if done\n\t}\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RoundTrip(tc.src)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// TestGroup6Patterns round-trips the full pattern grammar through a match — or-
// patterns, 'as' bindings, struct/tuple/list patterns with rest, negative
// literals, range arms, guards, the bare-name NamePattern, and the wildcard.
func TestGroup6Patterns(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "or, variant with as, guard, name, wild",
			src: "fn f(n: int) -> int {\nreturn match n {\nA | B => 1\n" +
				"Some(inner as v) => 2\nx if x < 0 => 3\n_ => 4\n}\n}",
			want: "fn f(n: int) -> int {\n\treturn match n {\n\t\tA | B => 1\n" +
				"\t\tSome(inner as v) => 2\n\t\tx if x < 0 => 3\n\t\t_ => 4\n\t}\n}\n",
		},
		{
			name: "struct pattern with as and rest",
			src: "fn f(m: int) -> int {\nreturn match m {\nMove{x, y} as p => 1\n" +
				"Div{q, ..} => 2\n_ => 3\n}\n}",
			want: "fn f(m: int) -> int {\n\treturn match m {\n\t\tMove{x, y} as p => 1\n" +
				"\t\tDiv{q, ..} => 2\n\t\t_ => 3\n\t}\n}\n",
		},
		{
			name: "tuple, list rest, negative literal",
			src: "fn f(t: int) -> int {\nreturn match t {\n(a, b) => 1\n" +
				"[h, ..t] => 2\n-1 => 3\n_ => 4\n}\n}",
			want: "fn f(t: int) -> int {\n\treturn match t {\n\t\t(a, b) => 1\n" +
				"\t\t[h, ..t] => 2\n\t\t-1 => 3\n\t\t_ => 4\n\t}\n}\n",
		},
		{
			name: "range arms",
			src: "fn f(code: int) -> str {\nreturn match code {\n200..300 => \"ok\"\n" +
				"400..=499 => \"client\"\n500.. => \"server\"\n_ => \"other\"\n}\n}",
			want: "fn f(code: int) -> str {\n\treturn match code {\n\t\t200..300 => \"ok\"\n" +
				"\t\t400..=499 => \"client\"\n\t\t500.. => \"server\"\n\t\t_ => \"other\"\n\t}\n}\n",
		},
		{
			name: "field-pattern with sub-pattern and struct rest only",
			src:  "fn f(d: int) -> int {\nreturn match d {\nP{x: 0, y: v} => v\n_ => 0\n}\n}",
			want: "fn f(d: int) -> int {\n\treturn match d {\n\t\tP{x: 0, y: v} => v\n\t\t_ => 0\n\t}\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RoundTrip(tc.src)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// TestGroup8Errors round-trips the remaining group 8 surface — the guard block
// expression and the 'raise e (from c)?' statement.
func TestGroup8Errors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "guard expression",
			src:  "fn f() {\nr := guard {\nrisky()\n}\nprint r\n}",
			want: "fn f() {\n\tr := guard {\n\t\trisky()\n\t}\n\tprint r\n}\n",
		},
		{
			name: "raise",
			src:  "fn f() {\nraise Err(\"bad\")\n}",
			want: "fn f() {\n\traise Err(\"bad\")\n}\n",
		},
		{
			name: "raise from",
			src:  "fn f() {\nraise Wrap(e) from e\n}",
			want: "fn f() {\n\traise Wrap(e) from e\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RoundTrip(tc.src)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// TestF1DocBetweenDecorator pins follow-up F1: a doc-comment sitting between a
// decorator and its declaration attaches to the declaration (so it round-trips)
// and is reprinted above the decorator in canonical form, rather than being
// dropped with a stale diagnostic.
func TestF1DocBetweenDecorator(t *testing.T) {
	src := "#[derive(Encode)]\n## a documented type\nstruct S {\nx: int\n}"
	want := "## a documented type\n#[derive(Encode)]\nstruct S {\n\tx: int\n}\n"
	got, err := RoundTrip(src)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}
