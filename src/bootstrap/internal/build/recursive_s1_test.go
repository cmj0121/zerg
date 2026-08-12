package build

import "testing"

// RUN-based tests for S1 (one-shot auto-boxing of recursive/self-referential types): the
// compiler detects a type-graph cycle and heap-indirects the self-referential slot
// through a refcounted box, so the user writes the recursive type directly with no
// explicit Ref. Each program is compiled to C, linked against the runtime under
// ASan+UBSan with the counting allocator swapped in (runProgramRTBalanced), and run: a
// pass asserts a clean exit, the exact stdout, AND a zero alloc/free balance — so no box
// leaks and none is freed twice.
//
// Two limits are accepted for the MVP (DESIGN-refcount §7 risk 1 and 5, documented in
// memory.md): a runtime cycle built via `mut` (e.g. `a.next = a`) leaks because the
// refcount never reaches zero, and freeing a long recursive chain recurses one C stack
// frame per node (O(depth)). Neither is exercised here; every list/tree below is acyclic
// and shallow.

// TestRecursiveEnumListBalanced covers a singly-linked list as a direct-nominal recursive
// enum (Cons's tail is a boxed List cell): built, traversed by a recursive match, summed,
// and freed. The tail box of each node is released exactly once at scope exit.
func TestRecursiveEnumListBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"enum List { Nil; Cons(int, List) }\n"+
			"fn sum(l: List) -> int {\n"+
			"\treturn match l {\n"+
			"\t\tList.Nil => 0\n"+
			"\t\tCons(h, t) => h + sum(t)\n"+
			"\t}\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\txs := Cons(1, Cons(2, Cons(3, Nil)))\n"+
			"\tprint sum(xs)\n"+
			"\treturn nil\n}\n")
	if want := "6\n"; got != want {
		t.Fatalf("enum list: got %q, want %q", got, want)
	}
}

// TestRecursiveExprTreeBalanced covers a recursive expression tree (Add's two payloads are
// boxed Expr cells): built with nested Add(Add(_,_), _), matched with a nested variant
// pattern, evaluated, and freed. Exercises the box deref on match binding and the deep
// copy/drop of a non-POD enum.
func TestRecursiveExprTreeBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"enum Expr { Num(int); Add(Expr, Expr) }\n"+
			"fn eval(e: Expr) -> int {\n"+
			"\treturn match e {\n"+
			"\t\tNum(n) => n\n"+
			"\t\tAdd(a, b) => eval(a) + eval(b)\n"+
			"\t}\n}\n"+
			"fn depth(e: Expr) -> int {\n"+ // a nested variant pattern Add(Add(_,_), _)
			"\treturn match e {\n"+
			"\t\tAdd(Add(_, _), _) => 2\n"+
			"\t\tAdd(_, _) => 1\n"+
			"\t\tNum(_) => 0\n"+
			"\t}\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\te := Add(Add(Num(1), Num(2)), Num(3))\n"+
			"\tprint eval(e)\n"+
			"\tprint depth(e)\n"+
			"\treturn nil\n}\n")
	if want := "6\n2\n"; got != want {
		t.Fatalf("expr tree: got %q, want %q", got, want)
	}
}

// TestRecursiveMutualEnumBalanced covers mutual recursion: A holds B and B holds A, a
// two-node cycle in the type graph. MarkBoxing cuts one back edge, so one direction is
// boxed and the other stays inline; both types free cleanly.
func TestRecursiveMutualEnumBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"enum A { AEnd; AB(B) }\n"+
			"enum B { BEnd; BA(A) }\n"+
			"fn count(a: A) -> int {\n"+
			"\treturn match a {\n"+
			"\t\tA.AEnd => 0\n"+
			"\t\tAB(b) => match b {\n"+
			"\t\t\tB.BEnd => 1\n"+
			"\t\t\tBA(inner) => 1 + count(inner)\n"+
			"\t\t}\n"+
			"\t}\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\tx := AB(BA(AB(BEnd)))\n"+
			"\tprint count(x)\n"+
			"\treturn nil\n}\n")
	if want := "2\n"; got != want {
		t.Fatalf("mutual recursion: got %q, want %q", got, want)
	}
}

// TestRecursiveStructNodeOptBalanced covers a struct with a `Node?` self-referential
// field: the cyclic Opt lowers to a nullable box (None≡NULL, Some≡a Node cell). Built by
// coercing a Node into the optional field, read back through force `!`, and freed.
func TestRecursiveStructNodeOptBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct Node {\n\tval: int\n\tnext: Node?\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\ttail := Node(2, nil)\n"+
			"\thead := Node(1, tail)\n"+
			"\tprint head.val\n"+
			"\tprint head.next!.val\n"+
			"\treturn nil\n}\n")
	if want := "1\n2\n"; got != want {
		t.Fatalf("struct Node?: got %q, want %q", got, want)
	}
}

// TestRecursiveBoxedFieldReassignBalanced covers reassigning a boxed field: the old cell
// is released before the new one is stored (release-old → move/retain-new → store), so an
// independent producer on each side balances with no leak or double-free.
func TestRecursiveBoxedFieldReassignBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct Node {\n\tval: int\n\tnext: Node?\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\ta := Node(10, nil)\n"+
			"\tb := Node(20, nil)\n"+
			"\tmut n := Node(1, a)\n"+
			"\tn.next = b\n"+ // release old box (holds a), retain/box new (holds b)
			"\tprint n.next!.val\n"+
			"\treturn nil\n}\n")
	if want := "20\n"; got != want {
		t.Fatalf("boxed field reassign: got %q, want %q", got, want)
	}
}

// TestRecursiveSharedBoxBalanced covers a shared box (rc>1): two boxed-Opt holders point
// at the same Node cell, one is `del`'d (dropping its refcount), and the other still reads
// the payload through it — the cell survives until its last holder releases it, then frees
// exactly once.
func TestRecursiveSharedBoxBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct Node {\n\tval: int\n\tnext: Node?\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\tbase := Node(7, nil)\n"+
			"\ta: Node? = base\n"+ // Some-coercion: a fresh box, rc=1
			"\tb := a\n"+ // retain the same box: rc=2
			"\tdel a\n"+ // drop a's hold; rc=1, b's box still alive
			"\tprint b!.val\n"+ // reads the shared cell through b
			"\treturn nil\n}\n")
	if want := "7\n"; got != want {
		t.Fatalf("shared box: got %q, want %q", got, want)
	}
}

// TestRecursiveDerivedCompareBalanced covers derive(Eq, Ord) on a recursive enum: the
// synthesized comparators bind boxed payloads through the box (deref) and recurse, and
// each comparison operand is retained across the (consuming) dispatch so a boxed operand
// is not double-freed. Asserts correct lexicographic results AND a zero alloc/free
// balance under ASan.
func TestRecursiveDerivedCompareBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"#[derive(Eq, Ord)]\nenum List { Nil; Cons(int, List) }\n"+
			"fn main() -> Result[nil] {\n"+
			"\ta := Cons(1, Cons(2, Nil))\n"+
			"\tb := Cons(1, Cons(3, Nil))\n"+
			"\tprint a == a\n"+
			"\tprint a == b\n"+
			"\tprint a < b\n"+
			"\tprint b < a\n"+
			"\treturn nil\n}\n")
	if want := "true\nfalse\ntrue\nfalse\n"; got != want {
		t.Fatalf("derived compare: got %q, want %q", got, want)
	}
}

// TestConstructBorrowedStrFieldBalanced closes the S2/S1 construction move-retain gap for
// a str field: a struct built from a BORROWED managed-str variable must RETAIN the cell,
// so it is alloc/free balanced (no double-free when both the struct and the source drop).
func TestConstructBorrowedStrFieldBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct P {\n\tname: str\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\ts := \"foo\" + \"bar\"\n"+ // a managed heap str, rc=1
			"\tp := P(s)\n"+ // borrowed field: MUST retain, rc=2
			"\tprint p.name\n"+
			"\tprint s\n"+
			"\treturn nil\n}\n")
	if want := "foobar\nfoobar\n"; got != want {
		t.Fatalf("borrowed str field: got %q, want %q", got, want)
	}
}

// TestConstructBorrowedRefFieldBalanced closes the same gap for a Ref field: a struct
// built from a BORROWED Ref variable must retain the box, balanced under ASan.
func TestConstructBorrowedRefFieldBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct Q {\n\tr: Ref[int]\n}\n"+
			"fn main() {\n"+
			"\tr := Ref(42)\n"+ // a box, rc=1
			"\tq := Q(r)\n"+ // borrowed field: MUST retain, rc=2
			"\tprint deref(q.r)\n"+
			"\tprint deref(r)\n"+
			"}\n")
	if want := "42\n42\n"; got != want {
		t.Fatalf("borrowed ref field: got %q, want %q", got, want)
	}
}
