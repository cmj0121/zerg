package mono

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
)

// Unit tests for the S1 MarkBoxing pass: type-graph cycle detection and the minimal
// back-edge cut. A recursive type gets exactly the self-referential slots marked Boxed
// (heap-indirected through a refcounted cell) and its members recorded in CyclicTypes;
// an acyclic type gets zero marks — the invariant that keeps the example corpus
// byte-identical.

// enumInst returns the collected instance of a named enum, failing if absent.
func enumInst(t *testing.T, prog *Program, name string) *TypeInstance {
	t.Helper()
	for _, ti := range prog.Types {
		if ti.IsEnum && ti.Def != nil && ti.Def.Name == name {
			return ti
		}
	}
	t.Fatalf("enum %q not collected", name)
	return nil
}

// structInst returns the collected instance of a named struct, failing if absent.
func structInst(t *testing.T, prog *Program, name string) *TypeInstance {
	t.Helper()
	for _, ti := range prog.Types {
		if !ti.IsEnum && ti.Def != nil && ti.Def.Name == name {
			return ti
		}
	}
	t.Fatalf("struct %q not collected", name)
	return nil
}

// TestMarkBoxingRecursiveEnum: a direct-nominal recursive enum boxes exactly its
// self-referential payload slots and records itself cyclic.
func TestMarkBoxingRecursiveEnum(t *testing.T) {
	prog := build(t, "enum Expr { Num(int); Add(Expr, Expr) }\n"+
		"fn main() { e := Add(Num(1), Num(2)) }\n")
	ti := enumInst(t, prog, "Expr")
	for _, v := range ti.Variants {
		switch v.Name {
		case "Num":
			if v.Boxed[0] {
				t.Fatalf("Num(int) payload must NOT be boxed")
			}
		case "Add":
			if !v.Boxed[0] || !v.Boxed[1] {
				t.Fatalf("Add(Expr, Expr) both payloads must be boxed, got %v", v.Boxed)
			}
		}
	}
	if !prog.CyclicTypes[ti.Mangled] {
		t.Fatalf("Expr must be recorded cyclic")
	}
}

// TestMarkBoxingRecursiveStructOpt: a struct with a `Self?` field boxes that field and
// records the struct cyclic (the cycle runs through the by-value Opt wrapper).
func TestMarkBoxingRecursiveStructOpt(t *testing.T) {
	prog := build(t, "struct Node { val: int; next: Node? }\n"+
		"fn main() { n := Node(1, nil) }\n")
	ti := structInst(t, prog, "Node")
	for _, f := range ti.Fields {
		switch f.Name {
		case "val":
			if f.Boxed {
				t.Fatalf("val: int must NOT be boxed")
			}
		case "next":
			if !f.Boxed {
				t.Fatalf("next: Node? must be boxed")
			}
		}
	}
	if !prog.CyclicTypes[ti.Mangled] {
		t.Fatalf("Node must be recorded cyclic")
	}
}

// TestMarkBoxingMutualRecursion: a two-type cycle A<->B cuts exactly one back edge, so
// one direction is boxed and the other stays inline, and both types are recorded cyclic.
func TestMarkBoxingMutualRecursion(t *testing.T) {
	prog := build(t, "enum A { AEnd; AB(B) }\nenum B { BEnd; BA(A) }\n"+
		"fn main() { x := AB(BEnd) }\n")
	a := enumInst(t, prog, "A")
	b := enumInst(t, prog, "B")
	boxedCount := 0
	for _, v := range a.Variants {
		for _, bx := range v.Boxed {
			if bx {
				boxedCount++
			}
		}
	}
	for _, v := range b.Variants {
		for _, bx := range v.Boxed {
			if bx {
				boxedCount++
			}
		}
	}
	if boxedCount != 1 {
		t.Fatalf("mutual recursion must cut exactly one back edge, got %d boxed slots", boxedCount)
	}
	if !prog.CyclicTypes[a.Mangled] || !prog.CyclicTypes[b.Mangled] {
		t.Fatalf("both A and B must be recorded cyclic")
	}
}

// TestMarkBoxingAcyclicNoMarks: a non-cyclic use of a (recursive) type is left INLINE —
// `Tree` holds an `Expr` but is not itself in Expr's cycle, so `Tree`'s field is not
// boxed and `Tree` is not recorded cyclic. This is the "no spurious boxing" property.
func TestMarkBoxingAcyclicNoMarks(t *testing.T) {
	prog := build(t, "enum Expr { Num(int); Add(Expr, Expr) }\n"+
		"struct Tree { root: Expr }\n"+
		"fn main() { t := Tree(Num(1)) }\n")
	tree := structInst(t, prog, "Tree")
	if tree.Fields[0].Boxed {
		t.Fatalf("Tree.root (a non-cyclic use of Expr) must NOT be boxed")
	}
	if prog.CyclicTypes[tree.Mangled] {
		t.Fatalf("Tree is not in a cycle and must not be recorded cyclic")
	}
}

// TestMarkBoxingExampleCorpusZeroMarks asserts the HARD INVARIANT source-of-truth: none
// of the numbered examples defines a recursive type, so MarkBoxing yields ZERO marks and
// an empty CyclicTypes — hence zero emit delta and byte-identical C.
func TestMarkBoxingExampleCorpusZeroMarks(t *testing.T) {
	root, ok := repoRootMono()
	if !ok {
		t.Skip("repo root (go.work) not found")
	}
	sources, err := filepath.Glob(filepath.Join(root, "examples", "[0-9][0-9]_*.zg"))
	if err != nil || len(sources) == 0 {
		t.Skip("no numbered examples found")
	}
	for _, zg := range sources {
		name := filepath.Base(zg)
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(zg)
			if err != nil {
				t.Fatalf("read %s: %v", zg, err)
			}
			file, pdiags := parser.Parse(string(src))
			if len(pdiags) != 0 {
				t.Skipf("%s does not parse standalone: %v", name, pdiags)
			}
			info, sdiags := sema.Check(file)
			if len(sdiags) != 0 {
				t.Skipf("%s does not check standalone: %v", name, sdiags)
			}
			prog := Build(file, info)
			if len(prog.CyclicTypes) != 0 {
				t.Fatalf("%s: MarkBoxing marked %d cyclic types, want 0 (byte-identical invariant)", name, len(prog.CyclicTypes))
			}
			for _, ti := range prog.Types {
				for _, f := range ti.Fields {
					if f.Boxed {
						t.Fatalf("%s: field %s.%s boxed, want none", name, ti.Mangled, f.Name)
					}
				}
				for _, v := range ti.Variants {
					for i, bx := range v.Boxed {
						if bx {
							t.Fatalf("%s: variant %s.%s slot %d boxed, want none", name, ti.Mangled, v.Name, i)
						}
					}
				}
			}
		})
	}
}

// repoRootMono walks up from the working directory to the go.work root, so the corpus
// test can locate the examples/ tree.
func repoRootMono() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
