package fmt

import "testing"

// TestGroup9Concurrency round-trips the group 9 surface — spawn, the send
// statement, the chan constructor expression, and a select with recv (bound and
// unbound), send, 'done', and '_' arms — pinning their canonical layout.
func TestGroup9Concurrency(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "spawn",
			src:  "fn f() {\nspawn work(1)\n}",
			want: "fn f() {\n\tspawn work(1)\n}\n",
		},
		{
			name: "send statement",
			src:  "fn f() {\nch <- v\n}",
			want: "fn f() {\n\tch <- v\n}\n",
		},
		{
			name: "chan constructor with capacity",
			src:  "fn f() {\nc := chan[int](8)\n}",
			want: "fn f() {\n\tc := chan[int](8)\n}\n",
		},
		{
			name: "chan constructor unbuffered",
			src:  "fn f() {\nc := chan[str]()\n}",
			want: "fn f() {\n\tc := chan[str]()\n}\n",
		},
		{
			name: "select with recv/send/done/default and a bound recv",
			src: "fn f() {\nselect {\nx := <-ch => use(x)\n<-quit => stop()\n" +
				"out <- v => 0\ndone => 1\n_ => 2\n}\n}",
			want: "fn f() {\n\tselect {\n\t\tx := <-ch => use(x)\n\t\t<-quit => stop()\n" +
				"\t\tout <- v => 0\n\t\tdone => 1\n\t\t_ => 2\n\t}\n}\n",
		},
		{
			name: "select wild-bound recv",
			src:  "fn f() {\nselect {\n_ := <-ch => 0\n}\n}",
			want: "fn f() {\n\tselect {\n\t\t_ := <-ch => 0\n\t}\n}\n",
		},
	}
	runRoundTrips(t, cases)
}

// TestGroup10Modules round-trips the group 10 surface — a single import with
// 'as', the grouped 'import ( … )' one-spec-per-line form, and a top-level
// module binding and import interleaved with declarations (the finalized
// top-level stmt-list).
func TestGroup10Modules(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "single import with as",
			src:  "import \"a/text\" as at",
			want: "import \"a/text\" as at\n",
		},
		{
			name: "single import re-export",
			src:  "import pub \"util/text\"",
			want: "import pub \"util/text\"\n",
		},
		{
			name: "grouped import one spec per line",
			src:  "import ( pub \"a/b\"; \"c/d\" as e )",
			want: "import (\n\tpub \"a/b\"\n\t\"c/d\" as e\n)\n",
		},
		{
			name: "top-level binding and import interleaved with declarations",
			src:  "import \"util/text\"\nversion := 3\nfn main() {\nnop\n}",
			want: "import \"util/text\"\nversion := 3\nfn main() {\n\tnop\n}\n",
		},
		{
			name: "top-level init decl round-trips",
			src:  "init() {\nsetup()\n}",
			want: "init() {\n\tsetup()\n}\n",
		},
	}
	runRoundTrips(t, cases)
}

// TestGroup11Cleanup round-trips the group 11 surface — the defer and del
// statements.
func TestGroup11Cleanup(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "defer",
			src:  "fn f() {\ndefer close(ch)\n}",
			want: "fn f() {\n\tdefer close(ch)\n}\n",
		},
		{
			name: "del",
			src:  "fn f() {\ndel ch\n}",
			want: "fn f() {\n\tdel ch\n}\n",
		},
	}
	runRoundTrips(t, cases)
}

// TestGroup12Unsafe round-trips the group 12 surface — the function-body unsafe
// block-expression, the module-level unsafe declaration group (a 'mut' global
// and a fn), and inline assembly with in/out/clobber operands.
func TestGroup12Unsafe(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "unsafe block-expression in a function",
			src:  "fn f() -> int {\nx := unsafe {\naddr(y)\n}\nreturn x\n}",
			want: "fn f() -> int {\n\tx := unsafe {\n\t\taddr(y)\n\t}\n\treturn x\n}\n",
		},
		{
			name: "module-level unsafe group with a mut global and a fn",
			src:  "unsafe {\nmut counter := 0\nfn bump() {\nnop\n}\n}",
			want: "unsafe {\n\tmut counter := 0\n\tfn bump() {\n\t\tnop\n\t}\n}\n",
		},
		{
			name: "asm with in/out/clobber operands",
			src: "fn f() {\nunsafe {\nasm(\"syscall\", in(\"rax\") x, out(\"rbx\") y, " +
				"clobber(\"rcx\"))\n}\n}",
			want: "fn f() {\n\tunsafe {\n\t\tasm(\"syscall\", in(\"rax\") x, out(\"rbx\") y, " +
				"clobber(\"rcx\"))\n\t}\n}\n",
		},
		{
			name: "asm with no operands",
			src:  "fn f() {\nunsafe {\nasm(\"nop\")\n}\n}",
			want: "fn f() {\n\tunsafe {\n\t\tasm(\"nop\")\n\t}\n}\n",
		},
	}
	runRoundTrips(t, cases)
}

// runRoundTrips feeds each case through the fmt oracle and checks the exact
// canonical output.
func runRoundTrips(t *testing.T, cases []struct {
	name string
	src  string
	want string
}) {
	t.Helper()
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
