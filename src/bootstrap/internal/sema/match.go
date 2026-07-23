package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file types a match expression and checks its exhaustiveness (DESIGN-1b §5).
// Every arm binds its pattern into a fresh scope, every body must yield the common
// result type, and — over the subject type — every case must be covered: an enum
// by all its variants (or a catch-all), a bool by true and false (or a catch-all),
// and any other subject by a catch-all. A guarded arm and a range arm never count
// toward coverage (the compiler cannot prove a guard always holds, GRAMMAR group
// 6); Phase 1b does not attempt range-union coverage, so an integer subject always
// needs a catch-all (the conservative FORK-5 path).

// inferMatch types a match expression and returns its result type — the common
// type of every arm body — after checking exhaustiveness and unreachable arms.
func (c *checker) inferMatch(n *ast.MatchExpr) Type {
	subjT := c.synth(n.Subject)
	if len(n.Arms) == 0 {
		c.errorf(n.Span(), "match must have at least one arm")
		return Invalid
	}

	result := Invalid
	for i, arm := range n.Arms {
		c.pushScope()
		c.checkPattern(arm.Pat, subjT)
		if arm.Guard != nil {
			c.checkCond(arm.Guard)
		}
		if i == 0 {
			result = c.synth(arm.Body)
		} else if bt := c.check(arm.Body, result); !bad(bt) && !bad(result) && !c.assignable(result, arm.Body, bt) {
			c.errorf(arm.Body.Span(), "all match arms must yield the same type; found %s and %s", result, bt)
		}
		c.popScope()
	}

	c.checkUnreachable(n.Arms)
	if !bad(subjT) {
		if missing, ok := c.exhaustive(subjT, n.Arms); !ok {
			c.errorf(n.Span(), "non-exhaustive match: missing %s", missing)
		}
	}
	return result
}

// checkUnreachable reports the first unguarded catch-all arm that is not the last,
// since every arm after it can never be reached (DESIGN-1b §5.4).
func (c *checker) checkUnreachable(arms []ast.MatchArm) {
	for i := 0; i < len(arms)-1; i++ {
		if arms[i].Guard == nil && c.isCatchAll(arms[i].Pat) {
			c.errorf(arms[i].Pat.Span(), "this catch-all arm makes the following arms unreachable")
			return
		}
	}
}

// --- exhaustiveness -----------------------------------------------------------

// exhaustive reports whether the arms cover every case of the subject type; when
// not, it returns a description of one missing case (DESIGN-1b §5.1). An unguarded
// catch-all covers any subject; otherwise an enum needs every variant and a bool
// needs both truth values, while any other subject needs a catch-all.
func (c *checker) exhaustive(subjT Type, arms []ast.MatchArm) (string, bool) {
	if c.hasCatchAll(arms) {
		return "", true
	}
	switch s := subjT.(type) {
	case *types.Enum:
		return c.enumExhaustive(s, arms)
	case *types.Primitive:
		if s.Kind() == types.KBool {
			return c.boolExhaustive(arms)
		}
	}
	return "a catch-all '_' arm", false
}

// hasCatchAll reports whether any unguarded arm matches every value (a wildcard or
// a bare-binding pattern). A guarded arm never counts (DESIGN-1b §5.2).
func (c *checker) hasCatchAll(arms []ast.MatchArm) bool {
	for _, a := range arms {
		if a.Guard == nil && c.isCatchAll(a.Pat) {
			return true
		}
	}
	return false
}

// enumExhaustive reports whether the arms cover every variant of an enum subject.
// Coverage is at variant granularity: any unguarded variant pattern for a variant
// covers it (Phase 1b does not check nested payload exhaustiveness).
func (c *checker) enumExhaustive(e *types.Enum, arms []ast.MatchArm) (string, bool) {
	if e.Def == nil || e.Def.Enum == nil {
		return "", true
	}
	covered := map[string]bool{}
	for _, a := range arms {
		if a.Guard == nil {
			c.collectVariants(a.Pat, covered)
		}
	}
	for _, v := range e.Def.Enum.Variants {
		if !covered[v.Name] {
			return "variant " + e.Def.Name + "." + v.Name, false
		}
	}
	return "", true
}

// collectVariants records into covered the enum variants an unguarded pattern
// matches, descending through or- and as-patterns.
func (c *checker) collectVariants(pat ast.Pattern, covered map[string]bool) {
	switch p := pat.(type) {
	case *ast.NamePattern:
		if res, ok := c.info.Patterns[p]; ok && res.Kind == NameVariant && res.Variant != nil {
			covered[res.Variant.Name] = true
		}
	case *ast.VariantPattern:
		covered[p.Name] = true
	case *ast.OrPattern:
		for _, alt := range p.Alts {
			c.collectVariants(alt, covered)
		}
	case *ast.AsPattern:
		c.collectVariants(p.Inner, covered)
	}
}

// boolExhaustive reports whether the unguarded arms cover both true and false.
func (c *checker) boolExhaustive(arms []ast.MatchArm) (string, bool) {
	var sawTrue, sawFalse bool
	for _, a := range arms {
		if a.Guard != nil {
			continue
		}
		if lit, ok := a.Pat.(*ast.LitPattern); ok {
			if b, ok := lit.Lit.(*ast.BoolLit); ok {
				sawTrue = sawTrue || b.Value
				sawFalse = sawFalse || !b.Value
			}
		}
	}
	switch {
	case !sawTrue:
		return "the 'true' case", false
	case !sawFalse:
		return "the 'false' case", false
	}
	return "", true
}

// isCatchAll reports whether a pattern matches every value of its subject type: a
// wildcard, a bare-binding name (not a variant), an as-pattern wrapping one, or a
// structural pattern all of whose components are themselves irrefutable — a tuple
// whose every element is a catch-all, or a struct all of whose field patterns are
// (a shorthand `{x}` binds and so is a catch-all field). A range arm NEVER counts
// (its containment is a partial cover), and a list pattern only when it is a single
// bare rest `[..]`/`[..rest]` (any fixed head imposes a length/element test).
func (c *checker) isCatchAll(pat ast.Pattern) bool {
	switch p := pat.(type) {
	case *ast.WildPattern:
		return true
	case *ast.NamePattern:
		res, ok := c.info.Patterns[p]
		return ok && res.Kind == NameBinding
	case *ast.AsPattern:
		return c.isCatchAll(p.Inner)
	case *ast.TuplePattern:
		for _, el := range p.Elems {
			if !c.isCatchAll(el) {
				return false
			}
		}
		return true
	case *ast.StructPattern:
		for _, f := range p.Fields {
			if f.Pat != nil && !c.isCatchAll(f.Pat) {
				return false
			}
		}
		return true
	case *ast.ListPattern:
		return len(p.Elems) == 1 && p.Elems[0].Rest
	}
	return false
}

// --- pattern binding ----------------------------------------------------------

// checkPattern binds a pattern's names into the current arm scope, typed against
// the subject type it destructures (DESIGN-1b §5.4). A literal pattern is checked
// for compatibility; a structured pattern recurses into the subject's components.
func (c *checker) checkPattern(pat ast.Pattern, subjT Type) {
	switch p := pat.(type) {
	case *ast.WildPattern:
		// matches anything, binds nothing
	case *ast.NamePattern:
		if res, ok := c.info.Patterns[p]; ok && res.Kind == NameVariant {
			c.checkVariantMember(p.Span(), p.Name, subjT, 0, false)
			return // a nullary variant binds nothing
		}
		c.declare(p.Span(), p.Name, subjT, false)
	case *ast.LitPattern:
		c.checkLitPattern(p, subjT)
	case *ast.VariantPattern:
		c.checkVariantPattern(p, subjT)
	case *ast.StructPattern:
		c.checkStructPattern(p, subjT)
	case *ast.TuplePattern:
		c.checkTuplePattern(p, subjT)
	case *ast.ListPattern:
		c.checkListPattern(p, subjT)
	case *ast.AsPattern:
		c.checkPattern(p.Inner, subjT)
		c.declare(p.Span(), p.Name, subjT, false)
	case *ast.OrPattern:
		for _, alt := range p.Alts {
			c.checkPattern(alt, subjT)
		}
	case *ast.RangeArm:
		c.synth(p.Lo)
		if p.Hi != nil {
			c.synth(p.Hi)
		}
	}
}

func (c *checker) checkLitPattern(p *ast.LitPattern, subjT Type) {
	lt := c.synth(p.Lit)
	if p.Neg && !isNumeric(lt) {
		c.errorf(p.Span(), "'-' pattern requires a number, found %s", lt)
	}
	if !bad(lt) && !bad(subjT) && !patternMatches(subjT, p.Lit, lt) {
		c.errorf(p.Span(), "a %s literal cannot match a subject of type %s", lt, subjT)
	}
}

// checkVariantPattern validates a variant pattern against the subject enum and
// binds its payload sub-patterns against the variant's payload types (M1).
func (c *checker) checkVariantPattern(p *ast.VariantPattern, subjT Type) {
	vd := c.checkVariantMember(p.Span(), p.Name, subjT, len(p.Elems), true)
	var payload []Type
	if vd != nil {
		payload = variantPayload(subjT, vd)
	}
	for i, sub := range p.Elems {
		et := types.Type(types.Unknown)
		if i < len(payload) {
			et = payload[i]
		}
		c.checkPattern(sub, et)
	}
}

// checkVariantMember validates a variant pattern against the subject type (M1):
// the subject must be an enum that declares the named variant, and the payload
// arity must match — a nullary variant takes no '(...)', a payload variant takes
// '(...)' with the right count. It returns the matched variant, or nil.
func (c *checker) checkVariantMember(span token.Span, name string, subjT Type, arity int, parens bool) *types.VariantDef {
	e, ok := subjT.(*types.Enum)
	if !ok {
		if !bad(subjT) {
			c.errorf(span, "variant pattern %q cannot match a subject of type %s", name, subjT)
		}
		return nil
	}
	if e.Def == nil || e.Def.Enum == nil {
		return nil
	}
	var vd *types.VariantDef
	for _, v := range e.Def.Enum.Variants {
		if v.Name == name {
			vd = v
			break
		}
	}
	if vd == nil {
		c.errorf(span, "enum %s has no variant %q", e.Def.Name, name)
		return nil
	}
	switch np := len(vd.Payload); {
	case np == 0 && parens:
		c.errorf(span, "variant %s.%s is nullary and takes no payload", e.Def.Name, name)
	case np > 0 && !parens:
		c.errorf(span, "variant %s.%s requires a payload of %d value(s)", e.Def.Name, name, np)
	case parens && arity != np:
		c.errorf(span, "variant %s.%s expects %d payload value(s), got %d", e.Def.Name, name, np, arity)
	}
	return vd
}

// variantPayload returns a variant's payload types specialized to the subject
// enum's type arguments, so a generic enum's payload 'T' reads as the concrete
// argument the subject supplies — 'Full(v)' on a 'Box[T]' subject binds v to that
// T, not the enum's own abstract parameter (generic-enum pattern binding).
func variantPayload(subjT Type, vd *types.VariantDef) []Type {
	e, ok := subjT.(*types.Enum)
	if !ok || len(e.Args) == 0 || len(e.Def.Params) == 0 {
		return vd.Payload
	}
	out := make([]Type, len(vd.Payload))
	for i, pt := range vd.Payload {
		out[i] = substituteArgs(e.Def.Params, e.Args, pt)
	}
	return out
}

// checkStructPattern binds a struct pattern's fields against the subject struct's
// field types; a shorthand 'id' binds the field's value under its own name.
func (c *checker) checkStructPattern(p *ast.StructPattern, subjT Type) {
	st, _ := subjT.(*types.Struct)
	for _, f := range p.Fields {
		ft := types.Type(types.Unknown)
		if st != nil && st.Def.Struct != nil {
			if fd := findField(st.Def, f.Name); fd != nil {
				ft = fd.Type
			}
		}
		if f.Pat == nil {
			c.declare(p.Span(), f.Name, ft, false)
		} else {
			c.checkPattern(f.Pat, ft)
		}
	}
}

// checkTuplePattern binds a tuple pattern's elements against the subject tuple's
// element types.
func (c *checker) checkTuplePattern(p *ast.TuplePattern, subjT Type) {
	tup, _ := subjT.(*types.Tuple)
	for i, sub := range p.Elems {
		et := types.Type(types.Unknown)
		if tup != nil && i < len(tup.Elems) {
			et = tup.Elems[i]
		}
		c.checkPattern(sub, et)
	}
}

// checkListPattern binds a list pattern's element patterns against the subject
// sequence's element type; a rest '..name' binds the remaining run as a list.
func (c *checker) checkListPattern(p *ast.ListPattern, subjT Type) {
	elem := types.Type(types.Unknown)
	switch b := subjT.(type) {
	case *types.List:
		elem = b.Elem
	case *types.Array:
		elem = b.Elem
	}
	for _, el := range p.Elems {
		switch {
		case el.Rest:
			if el.Name != "" {
				c.declare(p.Span(), el.Name, &types.List{Elem: elem}, false)
			}
		case el.Pat != nil:
			c.checkPattern(el.Pat, elem)
		}
	}
}

// patternMatches reports whether a literal pattern of type lt can match a subject
// of type subjT (allowing an untyped int literal against a numeric subject).
func patternMatches(subjT Type, lit ast.Expr, lt Type) bool {
	if types.Identical(subjT, lt) {
		return true
	}
	return isNumeric(subjT) && lt == Int && isIntLit(lit)
}
