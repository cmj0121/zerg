package syntax

import (
	"strings"
)

// v0.6 Unit 2 — built-in Option[T] / Result[T, E] enum synthesis.
//
// Print path must suppress type-args; diagnostic path must show them. Unit 6
// (run.go) and Unit 7 (cgen.go) honour this split: when emitting a value to
// stdout the bare-name form (`Option.Some(7)`, `Result.Err("oops")`) is
// printed; when reporting a diagnostic the bracketed instance name
// (`Option[int]`) is shown to disambiguate generic instances.
//
// The synthetic decls live under the pseudo-module `<builtin>` (see
// builtinModuleTag). Their Pos carries the zero Position{} value so error
// messages don't claim they came from a user file; downstream consumers that
// need to detect a synthetic node can check `decl.Pos == (Position{})` or
// rely on the explicit reservation diagnostic path that fires before any
// position-sensitive lookup.

// builtinModuleTag is the pseudo-module name attributed to the synthetic
// Option / Result enum decls. Unit 7 codegen emits these under the literal
// `zerg_builtin` mangle (no FNV hash) per PLAN.md §Built-in mangle.
const builtinModuleTag = "<builtin>"

// reservedBuiltinTypeNames is the set of type names users may not redeclare
// at the top level. v0.6 reserves Option and Result; v0.7 adds chan; future
// built-ins append.
var reservedBuiltinTypeNames = map[string]bool{
	"Option": true,
	"Result": true,
	"chan":   true,
}

// isReservedBuiltinTypeName reports whether name collides with a v0.6
// built-in type. Used by collectTopLevel to reject user redecls of
// `struct Option`, `enum Result`, `spec Option`, etc.
func isReservedBuiltinTypeName(name string) bool {
	return reservedBuiltinTypeNames[name]
}

// builtinOptionDecl returns a fresh *EnumDecl describing the synthetic
// `enum Option[T] { Some(T), None }`. A new decl is constructed per call so
// the AST node is not aliased across modules — every module's typeck pass
// gets its own copy to walk.
func builtinOptionDecl() *EnumDecl {
	tRef := &TypeRef{Kind: TypeRefNamed, Name: "T"}
	return &EnumDecl{
		Pos:        Position{},
		Name:       "Option",
		TypeParams: []TypeParam{{Name: "T"}},
		Variants: []VariantDecl{
			{Name: "Some", Payload: []*TypeRef{tRef}},
			{Name: "None"},
		},
	}
}

// builtinResultDecl returns a fresh *EnumDecl describing the synthetic
// `enum Result[T, E] { Ok(T), Err(E) }`.
func builtinResultDecl() *EnumDecl {
	tRef := &TypeRef{Kind: TypeRefNamed, Name: "T"}
	eRef := &TypeRef{Kind: TypeRefNamed, Name: "E"}
	return &EnumDecl{
		Pos:        Position{},
		Name:       "Result",
		TypeParams: []TypeParam{{Name: "T"}, {Name: "E"}},
		Variants: []VariantDecl{
			{Name: "Ok", Payload: []*TypeRef{tRef}},
			{Name: "Err", Payload: []*TypeRef{eRef}},
		},
	}
}

// injectBuiltinEnums wires the v0.6 built-in Option / Result decls into c's
// tables. Called from newChecker so every module's collect pass already sees
// the names. The synthetic decls live in builtinEnumDecls and enumAST so the
// generic-decl path can look them up; they are NOT placed in c.enums (which
// holds non-generic concrete enum types) — every use site monomorphizes
// through instantiateGenericEnum which writes into c.monoEnums.
func injectBuiltinEnums(c *checker) {
	if c.builtinEnumDecls == nil {
		c.builtinEnumDecls = map[string]*EnumDecl{}
	}
	if c.monoEnums == nil {
		c.monoEnums = map[string]*Type{}
	}
	for _, decl := range []*EnumDecl{builtinOptionDecl(), builtinResultDecl()} {
		c.builtinEnumDecls[decl.Name] = decl
		c.enumAST[decl.Name] = decl
	}
}

// monoKey is the cache key for a generic enum instantiation: the decl's name
// plus the canonical printable form of the type-arg vector. The decl-name
// alone disambiguates Option vs Result; the args string disambiguates
// Option[int] from Option[str], etc. Nested instances (Option[Result[int,
// str]]) round-trip through the same printable form their owning *Type
// uses, so structurally-equal arg vectors produce one cache hit.
type monoKey struct {
	declName string
	argsSig  string
}

// monoEnumArgsSig builds the cache key suffix from a vector of resolved
// type-arg *Type pointers. The signature uses Type.String for primitives /
// composites; for two struct or enum instances with the same Name (cross-
// module collisions are out of scope at v0.6 Unit 2) this collapses on
// pointer-equal canonicals.
func monoEnumArgsSig(args []*Type) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteString(",")
		}
		if a == nil {
			b.WriteString("<nil>")
			continue
		}
		b.WriteString(a.String())
	}
	return b.String()
}

// instantiateGenericEnum returns the canonical *Type for a generic enum
// decl applied to the resolved type-arg vector. Caches by (decl.Name,
// argsSig) so two type-resolutions of `Option[int]` (one direct, one via
// `int?` desugaring) return the same pointer.
//
// Argument-shape validation (correct number of type-args) happens here; the
// caller (resolveTypeRef) has already resolved each arg to a *Type.
func (c *checker) instantiateGenericEnum(decl *EnumDecl, args []*Type, refPos Position) (*Type, error) {
	if len(args) != len(decl.TypeParams) {
		return nil, typeErr(refPos,
			"generic type %q expects %d type argument(s), got %d",
			decl.Name, len(decl.TypeParams), len(args))
	}
	for i, a := range args {
		if a == nil {
			return nil, typeErr(refPos,
				"generic type %q argument %d failed to resolve", decl.Name, i+1)
		}
		if a == tVoid {
			return nil, typeErr(refPos,
				"generic type %q argument %d cannot be void", decl.Name, i+1)
		}
	}
	if c.monoEnums == nil {
		c.monoEnums = map[string]*Type{}
	}
	key := decl.Name + "[" + monoEnumArgsSig(args) + "]"
	if t, ok := c.monoEnums[key]; ok {
		return t, nil
	}
	// Build the substitution map T → arg.
	subst := map[string]*Type{}
	for i, tp := range decl.TypeParams {
		subst[tp.Name] = args[i]
	}
	variants := make([]string, len(decl.Variants))
	payloads := make([][]*Type, len(decl.Variants))
	for i, v := range decl.Variants {
		variants[i] = v.Name
		if len(v.Payload) == 0 {
			payloads[i] = nil
			continue
		}
		row := make([]*Type, len(v.Payload))
		for j, p := range v.Payload {
			t, err := c.resolveTypeRefWithSubst(p, subst)
			if err != nil {
				return nil, err
			}
			row[j] = t
		}
		payloads[i] = row
	}
	mono := &Type{
		Kind:            TypeEnum,
		Name:            key,
		Variants:        variants,
		VariantPayloads: payloads,
	}
	c.monoEnums[key] = mono
	// v0.6 Unit 3.5: built-in Option / Result decls have no user-declared
	// generic impls, but user-defined generic enums may. Skip expansion for
	// the built-ins (their decls live under <builtin> and never carry an
	// impl record); for user-defined enums, fan out per-instantiation.
	if _, isBuiltin := c.builtinEnumDecls[decl.Name]; !isBuiltin {
		if err := c.expandGenericImplsForType(nil, decl, mono, args, refPos); err != nil {
			return nil, err
		}
	}
	return mono, nil
}

// resolveTypeRefWithSubst is a substitution-aware variant of resolveTypeRef
// used by instantiateGenericEnum to walk a generic decl's payload TypeRefs
// with type-parameter names mapped to concrete *Type values. A bare named
// reference whose name matches a key in subst short-circuits to the
// substituted type; nested generics (`Option[T]` inside another generic
// payload) recurse through the regular instantiation path.
//
// Unit 2 only uses this path from instantiateGenericEnum on the synthetic
// Option / Result decls, whose payload references are bare type-param names
// — but the implementation handles the general shape so future user-defined
// generic enum decls (Unit 3) can share the routine.
func (c *checker) resolveTypeRefWithSubst(ref *TypeRef, subst map[string]*Type) (*Type, error) {
	if ref == nil {
		return nil, nil
	}
	switch ref.Kind {
	case TypeRefNamed:
		if ref.Module == "" && len(ref.TypeArgs) == 0 {
			if t, ok := subst[ref.Name]; ok {
				if ref.Nullable {
					return c.wrapOption(t, ref.Pos)
				}
				return t, nil
			}
		}
		// Fall through to a regular resolution. Nullable / TypeArgs paths
		// re-enter the substitution-free resolveTypeRef which (for non-
		// substituted names) is the canonical path.
		if len(ref.TypeArgs) > 0 {
			args := make([]*Type, len(ref.TypeArgs))
			for i, a := range ref.TypeArgs {
				t, err := c.resolveTypeRefWithSubst(a, subst)
				if err != nil {
					return nil, err
				}
				args[i] = t
			}
			decl, ok := c.builtinEnumDecls[ref.Name]
			if !ok {
				decl = c.enumAST[ref.Name]
			}
			if decl == nil || len(decl.TypeParams) == 0 {
				return nil, typeErr(ref.Pos,
					"type %q is not generic but was given type arguments", ref.Name)
			}
			t, err := c.instantiateGenericEnum(decl, args, ref.Pos)
			if err != nil {
				return nil, err
			}
			if ref.Nullable {
				return c.wrapOption(t, ref.Pos)
			}
			return t, nil
		}
		t, err := c.resolveTypeRef(ref)
		if err != nil {
			return nil, err
		}
		return t, nil
	case TypeRefList:
		elem, err := c.resolveTypeRefWithSubst(ref.Element, subst)
		if err != nil {
			return nil, err
		}
		t := NewListType(elem)
		if ref.Nullable {
			return c.wrapOption(t, ref.Pos)
		}
		return t, nil
	case TypeRefTuple:
		elems := make([]*Type, len(ref.Elements))
		for i, sub := range ref.Elements {
			t, err := c.resolveTypeRefWithSubst(sub, subst)
			if err != nil {
				return nil, err
			}
			elems[i] = t
		}
		t := NewTupleType(elems)
		if ref.Nullable {
			return c.wrapOption(t, ref.Pos)
		}
		return t, nil
	}
	return nil, typeErr(ref.Pos, "internal: unknown TypeRef kind %d", int(ref.Kind))
}

// wrapOption returns the canonical *Type for `Option[t]`. Used by
// resolveTypeRef when a TypeRef carries the postfix `?` bit.
func (c *checker) wrapOption(t *Type, refPos Position) (*Type, error) {
	decl := c.builtinEnumDecls["Option"]
	if decl == nil {
		return nil, typeErr(refPos, "internal: built-in Option not registered")
	}
	return c.instantiateGenericEnum(decl, []*Type{t}, refPos)
}
