package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file is the Phase 1c coherence and orphan check (DESIGN-1c §2, U2), run over
// the collected registry within a single module: at most one 'impl Spec for Type'
// across the program, and every impl must own the spec or the target type. It also
// enforces a spec impl's super-spec requirement ('impl Ord' needs 'impl Eq').

// headID canonicalizes a target type to a coherence head: a nominal type is keyed
// by its *TypeDef, any other type by its top-level Kind. This is the conservative
// head-key of DESIGN-1c §2.1 (FORK-F): 'impl Ord for list[int]' and 'impl Ord for
// list[str]' share a head and so conflict — a sound over-approximation for 1c,
// which needs no overlapping generic impls.
type headID struct {
	def  *types.TypeDef
	kind types.Kind
}

// implKey is the coherence key of a spec impl: the spec it implements and its
// target head. Two impls with the same key conflict.
type implKey struct {
	spec *types.SpecDef
	head headID
}

// targetHead reduces a target type to its coherence head.
func targetHead(t types.Type) headID {
	switch x := t.(type) {
	case *types.Struct:
		return headID{def: x.Def}
	case *types.Enum:
		return headID{def: x.Def}
	}
	return headID{kind: t.Kind()}
}

// headLocal reports whether a target type is declared in this compilation unit.
// A single module (1c) treats every user nominal type as local and every primitive
// or composite (a foreign built-in) as foreign.
func headLocal(t types.Type) bool {
	switch t.(type) {
	case *types.Struct, *types.Enum:
		return true
	}
	return false
}

// implKeyOf is a spec impl's coherence key.
func implKeyOf(im *types.ImplDef) implKey {
	return implKey{spec: im.Spec, head: targetHead(im.Target)}
}

// orphanOK reports whether a spec impl owns its spec or its target type, the
// single-module orphan rule (DESIGN-1c §2.2): a local spec or a local target type
// makes the impl legitimate.
func orphanOK(im *types.ImplDef) bool {
	return (im.Spec != nil && im.Spec.Local) || headLocal(im.Target)
}

// checkCoherence builds the impl index and reports the U2 violations: a conflicting
// duplicate impl, an orphan impl, and a spec impl missing a required super-spec impl
// (DESIGN-1c §2). It runs after collection, over the registry alone.
func (c *checker) checkCoherence(reg *SpecRegistry) {
	reg.byKey = map[implKey]*types.ImplDef{}
	for _, im := range reg.Impls {
		if im.Spec == nil {
			continue // inherent impls do not participate in coherence
		}
		k := implKeyOf(im)
		if prev, dup := reg.byKey[k]; dup {
			c.errorf(implSpan(im),
				"conflicting impl: spec %s is already implemented for type %s (previous impl at line %d)",
				im.Spec.Name, im.Target, implSpan(prev).Start.Line)
			continue
		}
		reg.byKey[k] = im
	}

	for _, im := range reg.Impls {
		if im.Spec == nil {
			if !headLocal(im.Target) {
				c.errorf(implSpan(im),
					"orphan impl: inherent impl target %s is not declared in this module", im.Target)
			}
			continue
		}
		if reg.byKey[implKeyOf(im)] != im {
			continue // a conflicting duplicate, already reported
		}
		if !orphanOK(im) {
			c.errorf(implSpan(im),
				"orphan impl: neither spec %s nor type %s is declared in this module", im.Spec.Name, im.Target)
		}
		reg.satisfies(c, im.Target, closeSupers(im.Spec), implSpan(im))
	}
}

// implSpan is an impl's source span, for anchoring a diagnostic.
func implSpan(im *types.ImplDef) token.Span {
	if d, ok := im.Decl.(*ast.ImplDecl); ok {
		return d.Span()
	}
	return token.Span{}
}
