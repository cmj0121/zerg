package mono

// This file is the Phase 1 completeness-pass S1 addition: one-shot auto-boxing of
// recursive (self-referential) types. A user writes a recursive type directly —
// `enum Expr { Num(int); Add(Expr, Expr) }` or `struct Node { val: int; next: Node? }`
// — with no explicit Ref; the compiler detects the type-graph cycle and marks the
// minimal cut of self-referential slots `Boxed`, so the emitter heap-indirects each
// through a refcounted box (a Ref[payload] cell). A boxed value has reference
// (refcount-shared) copy semantics: passing/binding/returning bumps a refcount rather
// than deep-copying.
//
// The pass is a DFS over the already-monomorphized type graph (every field/payload
// type is concrete). Edges are BY-VALUE containment only — struct fields and enum
// variant payload slots, descending through the by-value wrappers Opt/Tuple/Array to
// reach a nominal target; it does NOT traverse Ref/List/Map/Chan, which are already
// indirect and break any cycle on their own. A slot whose target is currently on the
// DFS stack is a BACK EDGE: marking exactly the back edges is the minimal cut, so
// every cycle is broken while a non-cyclic use of a recursive type stays inline (no
// spurious boxing, no emit churn for acyclic types).
//
// Runtime cycles (built later via `mut`, e.g. `a.next = a`) still leak — the refcount
// never reaches zero — which is accepted for the MVP (documented in memory.md and
// DESIGN-refcount §7 risk 1); there is no cycle collector this phase. Freeing a long
// recursive chain recurses one C stack frame per node (§7 risk 5), an O(depth) limit
// that is a depth bound, not a correctness bug.

import "github.com/cmj0121/zerg/src/bootstrap/internal/types"

// MarkBoxing detects every type-graph cycle over prog.Types and marks the minimal cut
// of back-edge slots Boxed, recording each cycle's members in prog.CyclicTypes. It runs
// at the end of Build/BuildWithInit, after prog.Types is fully populated. A program that
// defines no recursive type yields zero marks and an empty CyclicTypes, so the emitter
// sees no delta and its C stays byte-identical.
func MarkBoxing(prog *Program) {
	prog.CyclicTypes = map[string]bool{}
	m := &boxer{prog: prog, state: map[*TypeInstance]int{}}
	for _, ti := range prog.Types {
		if m.state[ti] == white {
			m.visit(ti)
		}
	}
	orderTypes(prog)
}

// orderTypes reorders prog.Types so a type is emitted after every type it embeds BY
// VALUE (inline, non-boxed) — the constraint C typedefs and the generated copy/drop
// helpers need. Boxing broke the cycles, so the inline-containment graph is now a DAG;
// a stable topological sort keeps the registration order wherever it is already valid
// (so an acyclic program's type order — and its emitted C — is byte-identical) and only
// moves an inline-contained type ahead of its container (the mutual-recursion case,
// where boxing made one edge a `void*` that no longer forces its target first).
func orderTypes(prog *Program) {
	n := len(prog.Types)
	deps := make([]map[*TypeInstance]bool, n)
	for i, ti := range prog.Types {
		deps[i] = inlineDeps(prog, ti)
	}
	placed := make([]bool, n)
	placedSet := map[*TypeInstance]bool{}
	out := make([]*TypeInstance, 0, n)
	for len(out) < n {
		progressed := false
		for i, ti := range prog.Types {
			if placed[i] {
				continue
			}
			ready := true
			for d := range deps[i] {
				if !placedSet[d] {
					ready = false
					break
				}
			}
			if ready {
				placed[i], placedSet[ti] = true, true
				out = append(out, ti)
				progressed = true
				break // restart from the lowest index so the order stays stable
			}
		}
		if !progressed {
			// A residual inline cycle (should not happen post-boxing): append the rest as-is
			// rather than loop forever — the emit typedef gate then reports it loudly.
			for i, ti := range prog.Types {
				if !placed[i] {
					out = append(out, ti)
				}
			}
			break
		}
	}
	prog.Types = out
}

// inlineDeps is the set of type instances a type embeds BY VALUE — the nominal targets
// of its NON-boxed slots reached directly or through Array/Tuple. A boxed slot (a
// `void*` cell) and an Opt slot (a carrier or a nullable box) embed no nominal inline,
// so they impose no definition-order dependency.
func inlineDeps(prog *Program, ti *TypeInstance) map[*TypeInstance]bool {
	out := map[*TypeInstance]bool{}
	add := func(t types.Type) {
		for _, x := range inlineTargets(prog, t) {
			out[x] = true
		}
	}
	if ti.IsEnum {
		for _, v := range ti.Variants {
			for i, pt := range v.Payload {
				if !v.Boxed[i] {
					add(pt)
				}
			}
		}
	} else {
		for _, f := range ti.Fields {
			if !f.Boxed {
				add(f.Type)
			}
		}
	}
	delete(out, ti)
	return out
}

// inlineTargets returns the nominal instances a type embeds by value: a struct/enum
// directly, or one reached through an Array/Tuple. Opt/Ref/List/Map/Chan lower to a
// carrier, pointer, or handle — none embeds a nominal inline — so they contribute none.
func inlineTargets(prog *Program, t types.Type) []*TypeInstance {
	switch x := t.(type) {
	case *types.Struct:
		if ti, ok := prog.typeByKey[typeKey(x.Def, x.Args)]; ok {
			return []*TypeInstance{ti}
		}
	case *types.Enum:
		if ti, ok := prog.typeByKey[typeKey(x.Def, x.Args)]; ok {
			return []*TypeInstance{ti}
		}
	case *types.Array:
		return inlineTargets(prog, x.Elem)
	case *types.Tuple:
		var out []*TypeInstance
		for _, el := range x.Elems {
			out = append(out, inlineTargets(prog, el)...)
		}
		return out
	}
	return nil
}

// DFS colours: white = unvisited, grey = on the recursion stack, black = finished.
const (
	white = iota
	grey
	black
)

// boxer carries the DFS state: the program (for the type-instance lookup) and each
// instance's colour.
type boxer struct {
	prog  *Program
	state map[*TypeInstance]int
}

// visit runs the DFS from ti. For each of ti's slots it inspects the slot's by-value
// nominal targets: a target currently on the stack (grey) is a back edge, so the slot
// is cut (marked Boxed) and both endpoints recorded as cyclic; an unvisited (white)
// target is recursed into. A finished (black) target is a forward/cross edge and is
// left inline, so a non-cyclic use of a recursive type is not boxed.
func (m *boxer) visit(ti *TypeInstance) {
	m.state[ti] = grey
	if ti.IsEnum {
		for vi := range ti.Variants {
			v := &ti.Variants[vi]
			for i, pt := range v.Payload {
				if m.cut(ti, pt) {
					v.Boxed[i] = true
				}
			}
		}
	} else {
		for fi := range ti.Fields {
			f := &ti.Fields[fi]
			if m.cut(ti, f.Type) {
				f.Boxed = true
			}
		}
	}
	m.state[ti] = black
}

// cut inspects one slot of owner and reports whether it must be boxed: it walks the
// slot type's by-value nominal targets, recursing into unvisited ones, and returns true
// as soon as a target is found on the DFS stack (a back edge). When it cuts, it records
// both the owner and the target in CyclicTypes so a type-level Opt bypass can see them.
func (m *boxer) cut(owner *TypeInstance, t types.Type) bool {
	boxed := false
	for _, tgt := range m.nominalTargets(t) {
		switch m.state[tgt] {
		case grey:
			m.prog.CyclicTypes[owner.Mangled] = true
			m.prog.CyclicTypes[tgt.Mangled] = true
			boxed = true
		case white:
			m.visit(tgt)
		}
	}
	return boxed
}

// nominalTargets returns the by-value nominal type instances a slot type reaches: a
// struct/enum directly, descending through the by-value wrappers Opt/Tuple/Array. It
// stops at Ref/List/Map/Chan (already indirect — they break a cycle) and at primitives,
// returning none, so those never form a containment edge.
func (m *boxer) nominalTargets(t types.Type) []*TypeInstance {
	switch x := t.(type) {
	case *types.Struct:
		if ti, ok := m.prog.typeByKey[typeKey(x.Def, x.Args)]; ok {
			return []*TypeInstance{ti}
		}
	case *types.Enum:
		if ti, ok := m.prog.typeByKey[typeKey(x.Def, x.Args)]; ok {
			return []*TypeInstance{ti}
		}
	case *types.Opt:
		return m.nominalTargets(x.Elem)
	case *types.Array:
		return m.nominalTargets(x.Elem)
	case *types.Tuple:
		var out []*TypeInstance
		for _, el := range x.Elems {
			out = append(out, m.nominalTargets(el)...)
		}
		return out
	}
	return nil
}
