package syntax

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Type representation.
//
// v0.1's design relied on every Type being a package-level singleton so equality
// was pointer-equality. v0.2 keeps that invariant for primitives — TInt, TStr,
// etc. remain singletons returned by package-level helpers — but adds composite
// kinds (list, tuple, struct, enum) where the same shape can legitimately be
// constructed at multiple call sites. Those use *structural* equality via
// Type.Equals so two `list[int]` types compare equal regardless of which
// constructor produced them.
//
// Struct and enum types are stored in a per-program type table keyed by name,
// and every reference to `Point` in a v0.2 program shares the single canonical
// *Type pointer. Equality on those is therefore still effectively pointer
// equality, but Equals handles the general case so external callers don't have
// to know which path produced a given Type.
// ---------------------------------------------------------------------------

// TypeKind enumerates v0.2 primitive types, the v0.1 void marker, and the
// composite kinds. Adding a new kind appends.
type TypeKind int

// Type kinds. Stable ordering — appending preserves wire compatibility for any
// future serialized typed-AST.
const (
	TypeUnknown TypeKind = iota // empty list literal awaiting context
	TypeInt
	TypeFloat
	TypeBool
	TypeStr
	TypeByte
	TypeRune
	TypeVoid
	TypeList   // Element non-nil
	TypeTuple  // Tuple non-nil (≥ 2 elements)
	TypeStruct // Name + Fields populated; canonical instance stored in struct table
	TypeEnum   // Name + Variants populated; canonical instance stored in enum table
	TypeSpec   // v0.4 spec-as-type: Name populated; canonical instance stored in spec table
	TypeChan   // v0.7 channel: Element non-nil; canonical instance cached per element-type
	TypeFn     // v0.7 fn value: FnParams + FnReturn populated (FnReturn may be nil/void)
)

// NamedField is one field of a struct type. Order matches declaration order
// because PLAN.md pins struct print order to declaration order.
type NamedField struct {
	Name string
	Type *Type
}

// Type is a v0.2 type descriptor. Use the package-level helpers (TInt, TByte,
// NewListType, NewStructType, ...) — never construct a Type literal by hand
// because primitives rely on pointer equality and composites rely on the
// type table for struct/enum canonicalisation.
//
// v0.4 adds two pieces of bookkeeping to TypeEnum and the new TypeSpec:
//
//   - VariantPayloads is the per-variant payload type slice for TypeEnum.
//     Index-aligned with Variants; nil/empty entries indicate bare variants.
//     Filled in by the typeck collect/resolve pass once enum payload types
//     are known.
//   - TypeSpec uses Name as the spec name. Method resolution off a spec-typed
//     value goes through the per-program spec table, not the Type itself.
type Type struct {
	Kind            TypeKind
	Element         *Type        // TypeList
	Tuple           []*Type      // TypeTuple
	Name            string       // TypeStruct / TypeEnum / TypeSpec
	Fields          []NamedField // TypeStruct, declaration order
	Variants        []string     // TypeEnum, declaration order
	VariantPayloads [][]*Type    // TypeEnum, declaration order; nil entries for bare variants
	// v0.7 Unit 3: TypeFn carries the parameter type vector and the return
	// type. FnReturn is nil when the fn returns void (no annotation). The
	// pair is populated by checker.checkAnonFnExpr; structural equality on
	// TypeFn compares both.
	FnParams []*Type
	FnReturn *Type
}

// String returns the type spelled the way the source spells it. Used in error
// messages.
func (t *Type) String() string {
	if t == nil {
		return "<nil>"
	}
	switch t.Kind {
	case TypeUnknown:
		return "<unknown>"
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeBool:
		return "bool"
	case TypeStr:
		return "str"
	case TypeByte:
		return "byte"
	case TypeRune:
		return "rune"
	case TypeVoid:
		return "()"
	case TypeList:
		return "list[" + t.Element.String() + "]"
	case TypeTuple:
		var b strings.Builder
		b.WriteString("tuple[")
		for i, e := range t.Tuple {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(e.String())
		}
		b.WriteString("]")
		return b.String()
	case TypeStruct, TypeEnum, TypeSpec:
		return t.Name
	case TypeChan:
		return "chan[" + t.Element.String() + "]"
	case TypeFn:
		var b strings.Builder
		b.WriteString("fn(")
		for i, p := range t.FnParams {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(p.String())
		}
		b.WriteString(")")
		if t.FnReturn != nil && t.FnReturn != tVoid {
			b.WriteString(" -> ")
			b.WriteString(t.FnReturn.String())
		}
		return b.String()
	}
	return fmt.Sprintf("Type(%d)", int(t.Kind))
}

// Equals reports whether t and u describe the same shape. Primitives compare
// by pointer (singletons); composites compare structurally.
func (t *Type) Equals(u *Type) bool {
	if t == u {
		return true
	}
	if t == nil || u == nil {
		return false
	}
	if t.Kind != u.Kind {
		return false
	}
	switch t.Kind {
	case TypeList:
		return t.Element.Equals(u.Element)
	case TypeTuple:
		if len(t.Tuple) != len(u.Tuple) {
			return false
		}
		for i := range t.Tuple {
			if !t.Tuple[i].Equals(u.Tuple[i]) {
				return false
			}
		}
		return true
	case TypeStruct, TypeEnum, TypeSpec:
		// Canonical name suffices because the type table guarantees one
		// instance per name. Equal names ⇒ equal types.
		return t.Name == u.Name
	case TypeChan:
		return t.Element.Equals(u.Element)
	case TypeFn:
		if len(t.FnParams) != len(u.FnParams) {
			return false
		}
		for i := range t.FnParams {
			if !t.FnParams[i].Equals(u.FnParams[i]) {
				return false
			}
		}
		// Treat a nil FnReturn as void for equality purposes so two fn types
		// produced by different sites (one with explicit -> R; the other
		// inferred) compare consistently.
		tr := t.FnReturn
		ur := u.FnReturn
		if tr == nil {
			tr = tVoid
		}
		if ur == nil {
			ur = tVoid
		}
		return tr.Equals(ur)
	}
	// Primitives only reach here when pointer-equality already held above; for
	// safety we still treat same-Kind primitives as equal so a stray non-canonical
	// instance constructed by a future caller doesn't silently mis-compare.
	return true
}

// Package-level singletons. Pointer equality on these works exactly like v0.1.
var (
	tInt   = &Type{Kind: TypeInt}
	tFloat = &Type{Kind: TypeFloat}
	tBool  = &Type{Kind: TypeBool}
	tStr   = &Type{Kind: TypeStr}
	tByte  = &Type{Kind: TypeByte}
	tRune  = &Type{Kind: TypeRune}
	tVoid  = &Type{Kind: TypeVoid}
)

// TInt returns the canonical int singleton.
func TInt() *Type { return tInt }

// TFloat returns the canonical float singleton.
func TFloat() *Type { return tFloat }

// TBool returns the canonical bool singleton.
func TBool() *Type { return tBool }

// TStr returns the canonical str singleton.
func TStr() *Type { return tStr }

// TByte returns the canonical byte singleton (v0.2).
func TByte() *Type { return tByte }

// TRune returns the canonical rune singleton (v0.2).
func TRune() *Type { return tRune }

// TVoid returns the canonical void singleton.
func TVoid() *Type { return tVoid }

// NewListType constructs a list[T] type. The receiver is fresh, but Equals
// makes structural equality safe — callers do not need to share Type pointers
// across construction sites.
func NewListType(elem *Type) *Type {
	return &Type{Kind: TypeList, Element: elem}
}

// NewTupleType constructs a tuple[T1, T2, ...] type. The slice is owned by the
// returned Type.
func NewTupleType(elems []*Type) *Type {
	return &Type{Kind: TypeTuple, Tuple: elems}
}

// NewStructType constructs a struct type. Only the typeck collector should call
// this — one canonical struct lives in the type table per name.
func NewStructType(name string, fields []NamedField) *Type {
	return &Type{Kind: TypeStruct, Name: name, Fields: fields}
}

// NewEnumType constructs an enum type. Only the typeck collector should call
// this — one canonical enum lives in the type table per name.
func NewEnumType(name string, variants []string) *Type {
	return &Type{Kind: TypeEnum, Name: name, Variants: variants}
}

// NewSpecType constructs a spec-as-type marker. Only the typeck collector
// should call this — one canonical spec type lives in the spec table per name.
func NewSpecType(name string) *Type {
	return &Type{Kind: TypeSpec, Name: name}
}

// ---------------------------------------------------------------------------
// TypeError.
// ---------------------------------------------------------------------------

// TypeError is the error returned by Check.
type TypeError struct {
	Pos     Position
	Message string
}

// Error implements the error interface.
func (e *TypeError) Error() string {
	return fmt.Sprintf("type error at %s: %s", e.Pos, e.Message)
}

func typeErr(pos Position, format string, args ...any) error {
	return &TypeError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}

// ---------------------------------------------------------------------------
// Scope and binding.
// ---------------------------------------------------------------------------

// bindKind tags whether a name was introduced by let, mut, or const so the
// assignment checker can report a precise reason when assignment is illegal.
type bindKind int

const (
	bindLet bindKind = iota
	bindMut
	bindConst
)

// binding is a single name → type mapping inside a scope.
type binding struct {
	kind bindKind
	typ  *Type
}

// scope is one rung of the lexical-scope stack.
type scope struct {
	names  map[string]binding
	parent *scope
}

func newScope(parent *scope) *scope {
	return &scope{names: map[string]binding{}, parent: parent}
}

// declare binds name in this scope. Returns false if name is already bound at
// this rung; same-name in an inner scope (shadowing) is allowed.
func (s *scope) declare(name string, b binding) bool {
	if _, exists := s.names[name]; exists {
		return false
	}
	s.names[name] = b
	return true
}

// lookup walks the scope chain.
func (s *scope) lookup(name string) (binding, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if b, ok := cur.names[name]; ok {
			return b, true
		}
	}
	return binding{}, false
}

// fnSig is the signature of a top-level function. builtin marks `len` so we
// can detect (and reject) user attempts to redefine it.
type fnSig struct {
	params  []*Type
	ret     *Type
	pos     Position
	builtin bool
}

// ---------------------------------------------------------------------------
// v0.4 spec / impl tables.
//
// These tables live alongside structs/enums in the per-program checker state.
// They are populated in two passes: a first "register" pass that records the
// declarations, and a second "resolve" pass that turns parameter / return /
// payload TypeRefs into *Type values. Spec/impl bodies are walked last, after
// every name is known.
// ---------------------------------------------------------------------------

// specMethod is one method declared in a spec body. Body == nil signals
// signature-only; non-nil is a default body the impl may inherit.
type specMethod struct {
	pos    Position
	name   string
	params []*Type   // resolved param types (excluding implicit `this`)
	ret    *Type     // void if no declared return
	ast    *SpecMethod
}

// Spec is the per-program record for a spec declaration. Methods preserves
// declaration order for vtable layout determinism; methodIdx is the lookup-by-
// name index that drives method dispatch on a spec-typed receiver.
type Spec struct {
	Pos       Position
	Name      string
	Methods   []*specMethod        // declaration order
	methodIdx map[string]*specMethod
	typ       *Type                // canonical TypeSpec singleton
}

// implMethod is one method that lives inside an impl block. We track the
// resolved receiver type and the underlying *FnDecl. resolved param/ret types
// are filled in once the impl table is complete.
type implMethod struct {
	pos    Position
	name   string
	ast    *FnDecl
	params []*Type
	ret    *Type
}

// Impl is the per-(Type, Spec) impl record. Spec is empty for inherent impls.
//
// recvOwner / specOwner are populated by the v0.5 cross-module resolver:
// they point at the ModuleView that DEFINED the receiver type / spec. The
// fields are nil for single-program (non-Bundle) Check, and identical to
// the importing module when the type/spec was defined locally. The cross-
// module collision pass uses (recvOwner, TypeName, specOwner, SpecName) as
// the canonical key so basename collisions across modules don't
// accidentally fold into one.
type Impl struct {
	Pos        Position
	TypeName   string
	SpecName   string // "" for inherent
	Receiver   *Type  // resolved receiver type (struct or enum)
	Methods    []*implMethod // declaration order
	methodIdx  map[string]*implMethod
	ast        *ImplDecl
	recvOwner  ModuleView // v0.5: defining module of TypeName, nil for single-program
	specOwner  ModuleView // v0.5: defining module of SpecName, nil for inherent or single-program
}

// methodSource is one place a method name is visible on a type — either an
// inherent impl method, or a method exposed via a spec impl (override or
// inherited default). The collision check distinguishes the two via kind.
type methodSourceKind int

const (
	mskInherent methodSourceKind = iota
	mskSpec
)

type methodSource struct {
	kind     methodSourceKind
	pos      Position    // position of override; for inherited defaults, the spec method pos
	name     string      // method name (redundant but convenient at collision sites)
	specName string      // empty for inherent
	impl     *Impl       // backing impl (for both kinds)
	implFn   *implMethod // non-nil iff the impl supplies the method body
	defaultM *specMethod // non-nil iff the source is a spec default (no override in impl)
	inherent *implMethod // mskInherent only: the inherent impl method
}

// implKey deduplicates impls by (type, spec).
type implKey struct {
	typeName string
	specName string
}

// ---------------------------------------------------------------------------
// Checker state and entry point.
// ---------------------------------------------------------------------------

// checker holds all transient state for a single Check pass. structs and enums
// share a single namespace at the top level because they live in the same
// type-name space (a struct and an enum can't share a name).
type checker struct {
	scope     *scope
	fns       map[string]fnSig
	structs   map[string]*Type     // Name → struct *Type (Kind TypeStruct)
	enums     map[string]*Type     // Name → enum *Type (Kind TypeEnum)
	structAST map[string]*StructDecl // Name → AST node, for the second-pass field resolution
	enumAST   map[string]*EnumDecl   // Name → AST node, for second-pass payload resolution
	specs     map[string]*Spec       // Name → Spec
	impls     map[implKey]*Impl      // (type, spec) → Impl
	implsByType map[string][]*Impl   // type → [Impl ...] (decl order, both inherent and spec)
	// rawInherentDecls is the per-block inherent ImplDecl AST list (preserved
	// in declaration order) so the collision pass can attribute method names
	// to their owning block even after method-list merging.
	rawInherentDecls []*ImplDecl
	// methodVisible[type][methodName] is the list of method sources visible on
	// that type — populated after collision resolution. Empty when the type
	// has no methods.
	methodVisible map[string]map[string][]*methodSource
	currentFn *fnSig                 // nil at top level
	currentReceiver *Type            // non-nil only inside an impl method body
	currentSpec *Spec                // non-nil only inside a spec default body
	loopDepth int
	// crossMod is non-nil when the checker is part of a multi-module
	// CheckBundle pass. It carries the importing module's import-binding
	// table and a handle to the per-Module checker map, which together
	// drive cross-module name resolution and pub gating. Nil for
	// single-program Check (preserves v0.0–v0.4 behaviour).
	crossMod *crossModCtx
	// builtinEnumDecls is the v0.6 Unit 2 registry of synthetic generic
	// enum decls (Option, Result). Populated by injectBuiltinEnums at
	// newChecker time so every module's collect / resolve passes see the
	// names without an explicit import.
	builtinEnumDecls map[string]*EnumDecl
	// monoEnums caches per-instantiation canonical *Type values for
	// generic enum decls. Key is `Decl[arg1,arg2,...]`. Two type-resolutions
	// of the same instance (e.g. `Option[int]` and `int?`) return the same
	// canonical pointer, so downstream pointer-equality dispatch works.
	//
	// v0.6 Unit 3: when a CheckBundle is in play, every module's checker
	// shares the same map via crossMod.bundleMono — the map pointer is
	// installed there and assigned to each c.monoEnums so cross-module
	// instantiations canonicalise to one *Type.
	monoEnums map[string]*Type
	// monoStructs is the v0.6 Unit 3 cache for generic struct
	// instantiations. Same shape as monoEnums; bundle-shared via crossMod.
	monoStructs map[string]*Type
	// monoFns caches generic-fn specialisations keyed by
	// `Decl[arg1,arg2,...]`. The cached value is the cloned + fully
	// type-checked FnDecl. Bundle-shared via crossMod.
	monoFns map[string]*FnDecl
	// genericFnAST records every generic-fn AST decl by name so
	// checkGenericFnCall can find the original decl from a call site.
	genericFnAST map[string]*FnDecl
	// ownProg is the Program owned by this checker — set by CheckBundle when
	// it constructs the per-module checker. Used by specialiseGenericFn to
	// route monomorphised FnDecl clones back to the defining module's
	// Program.MonoFns slice so codegen can iterate them.
	ownProg *Program
	// genericImpls records this module's generic ImplDecls (those with a
	// non-empty TypeParams list). Each is held until per-receiver-type
	// monomorphisation expands it into a concrete impl entry. Bundle-shared
	// via crossMod.bundleMono so a foreign module's impl on a local type
	// expands when the local type is monomorphised.
	genericImpls []*genericImpl
	// activeSubst is the impl-level type-parameter substitution active during
	// a generic-impl body walk. Set by expandGenericImpls before walking each
	// cloned method's body so resolveTypeRef sees `T` bound to the chosen
	// concrete arg. Nil at the top level and inside non-generic impls.
	activeSubst map[string]*Type
	// monoChans caches per-element-type canonical chan *Type values (v0.7).
	// Same shape as monoEnums / monoStructs; chans don't escape modules at
	// v0.7 so the cache is per-checker rather than bundle-shared.
	monoChans map[string]*Type
	// anonFrames is the v0.7 Unit 3 stack of AnonFnExpr typecheck contexts.
	// Each frame remembers the parent scope at the moment the anon-fn body
	// began so capture analysis can decide whether an IDENT lookup
	// references an outer or inner binding. Pushed by checkAnonFnExpr;
	// popped on body-walk completion. Empty outside any anon-fn.
	anonFrames []*anonFnFrame
	// currentFnDecl points at the *FnDecl whose body is currently being
	// walked, or nil at top level / inside a spec/impl body. Set by
	// checkFnDecl alongside currentFn so checkDeferStmt can record
	// HasDefers on the right node.
	currentFnDecl *FnDecl
	// currentSpecMethod points at the *SpecMethod whose default-impl body
	// is currently being walked. Set alongside currentSpec when entering
	// a spec default body; mirrored by checkDeferStmt so HasDefers can be
	// recorded on the right node (analogous to currentFnDecl).
	currentSpecMethod *SpecMethod
	// waitGroupType is the canonical synthetic WaitGroup struct *Type.
	// Populated by injectWaitGroupBuiltin at newChecker time.
	waitGroupType *Type
}

// Check is the public single-program entry point. It walks prog, annotates
// every Expr with a type, resolves every TypeRef, and returns the FIRST type
// or scope error encountered. v0.5 routes through CheckBundle with a
// one-module bundle so single-program callers and Bundle callers share a
// code path.
func Check(prog *Program) error {
	return CheckSingle(prog)
}

// collectTopLevel registers every top-level struct, enum, and fn name across
// a single shared name space. PLAN: a struct and an enum cannot share a name,
// nor can a struct and a fn (the table is checked across all three groups).
// Field types and fn signatures are NOT resolved here — that needs the full
// table populated first so forward references work.
func (c *checker) collectTopLevel(prog *Program) error {
	declared := map[string]Position{} // name → position of first declaration, used for diagnostics

	register := func(name string, pos Position, kind string) error {
		if prev, exists := declared[name]; exists {
			_ = prev
			return typeErr(pos, "%s %q already declared", kind, name)
		}
		declared[name] = pos
		return nil
	}

	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *StructDecl:
			if isReservedBuiltinTypeName(s.Name) {
				return typeErr(s.Pos, "name %q is reserved (built-in)", s.Name)
			}
			if err := register(s.Name, s.Pos, "struct"); err != nil {
				return err
			}
			if len(s.Fields) == 0 {
				// PLAN: empty struct/enum decls are rejected. The grammar
				// admits them syntactically; typeck is the authoritative gate.
				return typeErr(s.Pos, "struct %q must declare at least one field", s.Name)
			}
			// Reject duplicate field names within the same struct so the
			// struct table never contains an ambiguous field set.
			seen := map[string]bool{}
			for _, f := range s.Fields {
				if seen[f.Name] {
					return typeErr(f.Pos, "duplicate field %q in struct %q", f.Name, s.Name)
				}
				seen[f.Name] = true
			}
			// v0.6 Unit 3: generic structs are NOT registered in c.structs
			// — every use site monomorphizes through instantiateGenericStruct
			// which writes into c.monoStructs instead. structAST still
			// records the decl so the generic-decl path can find it.
			if len(s.TypeParams) == 0 {
				c.structs[s.Name] = NewStructType(s.Name, nil) // fields filled by second pass
			}
			c.structAST[s.Name] = s
		case *EnumDecl:
			if isReservedBuiltinTypeName(s.Name) {
				return typeErr(s.Pos, "name %q is reserved (built-in)", s.Name)
			}
			if err := register(s.Name, s.Pos, "enum"); err != nil {
				return err
			}
			if len(s.Variants) == 0 {
				return typeErr(s.Pos, "enum %q must declare at least one variant", s.Name)
			}
			seen := map[string]bool{}
			variants := make([]string, len(s.Variants))
			payloads := make([][]*Type, len(s.Variants))
			for i, v := range s.Variants {
				if seen[v.Name] {
					return typeErr(v.Pos, "duplicate variant %q in enum %q", v.Name, s.Name)
				}
				seen[v.Name] = true
				variants[i] = v.Name
				// Payload types are resolved in resolveEnumPayloads once every
				// top-level type name is known. Leave the payload slot nil; the
				// resolution pass fills it in based on the AST.
				_ = payloads
			}
			// v0.6 Unit 3: generic enums are NOT registered in c.enums —
			// instances live in c.monoEnums keyed by `Decl[arg1,...]`. The
			// enumAST entry is still recorded so the generic-decl path
			// can find it (and so the reservation diagnostic in the
			// builtin path can fire when needed).
			if len(s.TypeParams) == 0 {
				c.enums[s.Name] = &Type{Kind: TypeEnum, Name: s.Name, Variants: variants, VariantPayloads: payloads}
			}
			c.enumAST[s.Name] = s
		case *SpecDecl:
			if isReservedBuiltinTypeName(s.Name) {
				return typeErr(s.Pos, "name %q is reserved (built-in)", s.Name)
			}
			if err := register(s.Name, s.Pos, "spec"); err != nil {
				return err
			}
			// Reject duplicate method names within a single spec body so the
			// spec table never exposes an ambiguous method set.
			seen := map[string]bool{}
			for _, m := range s.Methods {
				if seen[m.Name] {
					return typeErr(m.Pos, "duplicate method %q in spec %q", m.Name, s.Name)
				}
				seen[m.Name] = true
				// Reject explicit `this` parameter — `this` is implicit on
				// every method and the user must not declare it.
				for _, p := range m.Params {
					if p.Name == "this" {
						return typeErr(p.Pos, "method %q in spec %q must not declare an explicit 'this' parameter ('this' is implicit)", m.Name, s.Name)
					}
				}
			}
			c.specs[s.Name] = &Spec{
				Pos:       s.Pos,
				Name:      s.Name,
				methodIdx: map[string]*specMethod{},
				typ:       NewSpecType(s.Name),
			}
		case *FnDecl:
			// PLAN-pinned: cannot redefine built-in fns. v0.2 reserves `len`;
			// v0.3 adds `push` and `clone`; v0.14 adds `bytes` and `to_str`
			// (the str ↔ list[byte] bridge primitives) to the same reserved
			// set.
			switch s.Name {
			case "len", "push", "clone", "bytes", "to_str", "panic":
				return typeErr(s.Pos, "cannot redefine built-in '%s'", s.Name)
			}
			// v0.7: `chan` is reserved as a type-position keyword and `close`
			// is reserved as a type-driven built-in fn. Both reject with the
			// uniform reservation diagnostic.
			if s.Name == "chan" || s.Name == "close" {
				return typeErr(s.Pos, "name %q is reserved (built-in)", s.Name)
			}
			// v0.7 Unit 3: additional concurrency reservations.
			if isReservedV07ConcurName(s.Name) {
				return typeErr(s.Pos, "name %q is reserved (built-in)", s.Name)
			}
			if err := register(s.Name, s.Pos, "function"); err != nil {
				return err
			}
			c.fns[s.Name] = fnSig{pos: s.Pos} // signature filled by resolveFnSignatures
		}
	}
	return nil
}

// resolveStructFields resolves field types now that every top-level name is
// known. Forward references between structs and back-references to enums
// resolve here.
//
// v0.6 Unit 3: generic struct decls (TypeParams non-empty) are skipped here
// — their fields name the declared type-parameters by raw identifier, and
// the canonical per-instance *Type is built on demand by
// instantiateGenericStruct.
func (c *checker) resolveStructFields(_ *Program) error {
	for name, decl := range c.structAST {
		if len(decl.TypeParams) > 0 {
			continue
		}
		fields := make([]NamedField, len(decl.Fields))
		for i, f := range decl.Fields {
			t, err := c.resolveTypeRef(f.Type)
			if err != nil {
				return err
			}
			if t == tVoid {
				return typeErr(f.Pos, "field %q cannot have void type", f.Name)
			}
			fields[i] = NamedField{Name: f.Name, Type: t}
		}
		c.structs[name].Fields = fields
	}
	return nil
}

// resolveEnumPayloads walks each enum's variant declarations and fills in
// VariantPayloads with resolved *Type entries. Forward references between
// enums and structs work the same way as struct fields — every top-level
// type name is already in the table by the time we run.
func (c *checker) resolveEnumPayloads(_ *Program) error {
	for name, decl := range c.enumAST {
		// Generic enum decls (incl. the v0.6 built-ins Option / Result)
		// are not resolved here — their payload type-refs name the
		// declared type-parameters by raw identifier, and the canonical
		// per-instance *Type is built on demand by instantiateGenericEnum.
		if len(decl.TypeParams) > 0 {
			continue
		}
		en := c.enums[name]
		for i, v := range decl.Variants {
			if len(v.Payload) == 0 {
				en.VariantPayloads[i] = nil
				continue
			}
			payload := make([]*Type, len(v.Payload))
			for j, ref := range v.Payload {
				t, err := c.resolveTypeRef(ref)
				if err != nil {
					return err
				}
				if t == tVoid {
					return typeErr(ref.Pos, "enum variant payload type cannot be void")
				}
				payload[j] = t
			}
			en.VariantPayloads[i] = payload
		}
	}
	return nil
}

// detectTypeCycles walks every struct AND enum with DFS and rejects direct
// or transitive recursion. Lists-through-self count as cycles too: lists are
// value-copied so a `struct A { xs: list[A] }` would still imply infinite size
// at value-semantics. Tuples are inline shapes, so a tuple containing a struct
// also contributes to a cycle. v0.4 extends the v0.2 rule to enum variant
// payloads, which were not present at v0.2.
func (c *checker) detectTypeCycles() error {
	const (
		gray  = 1
		black = 2
	)
	state := map[string]int{}

	var visit func(name string, viaPos Position, viaDesc string) error
	visit = func(name string, viaPos Position, viaDesc string) error {
		switch state[name] {
		case black:
			return nil
		case gray:
			kind := "struct"
			if _, ok := c.enums[name]; ok {
				kind = "enum"
			}
			return typeErr(viaPos, "recursive %s %q is not allowed (%s)", kind, name, viaDesc)
		}
		state[name] = gray
		if st, ok := c.structs[name]; ok {
			decl := c.structAST[name]
			if decl != nil {
				for i, f := range st.Fields {
					if err := visitType(f.Type, decl.Fields[i].Pos, fmt.Sprintf("via field %q", f.Name), visit); err != nil {
						return err
					}
				}
			}
		}
		if en, ok := c.enums[name]; ok {
			decl := c.enumAST[name]
			for i, payload := range en.VariantPayloads {
				for j, t := range payload {
					desc := fmt.Sprintf("via variant %q payload position %d", en.Variants[i], j+1)
					if err := visitType(t, decl.Variants[i].Pos, desc, visit); err != nil {
						return err
					}
				}
			}
		}
		state[name] = black
		return nil
	}

	for name := range c.structs {
		if state[name] == 0 {
			// v0.7 Unit 3: synthetic built-in structs (e.g. WaitGroup) have
			// no AST decl to anchor a position on. Skip the cycle walk for
			// those — they have no fields and so cannot participate in any
			// recursion.
			decl := c.structAST[name]
			if decl == nil {
				continue
			}
			if err := visit(name, decl.Pos, ""); err != nil {
				return err
			}
		}
	}
	for name := range c.enums {
		if state[name] == 0 {
			if err := visit(name, c.enumAST[name].Pos, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// visitType walks a Type's struct/enum references for cycle detection. Lists,
// tuples, and nested struct/enum names all pull the cycle through. v0.4
// extends visiting to TypeEnum so enum-via-list / enum-via-struct / direct
// enum recursion is rejected with the same diagnostic shape v0.2 used.
func visitType(t *Type, viaPos Position, viaDesc string, visit func(string, Position, string) error) error {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case TypeStruct:
		return visit(t.Name, viaPos, viaDesc)
	case TypeEnum:
		return visit(t.Name, viaPos, viaDesc)
	case TypeList:
		return visitType(t.Element, viaPos, viaDesc+" (through list)", visit)
	case TypeTuple:
		for _, e := range t.Tuple {
			if err := visitType(e, viaPos, viaDesc+" (through tuple)", visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveFnSignatures fills in every top-level fn's signature now that struct
// and enum types are known. The built-in `len` was inserted before the AST
// walk and is left untouched here.
//
// v0.6 Unit 3: generic FnDecls (TypeParams non-empty) are skipped here —
// their params and return types name declared type-params by raw
// identifier, and per-instance signatures are resolved on demand by
// specialiseGenericFn at each call site.
func (c *checker) resolveFnSignatures(prog *Program) error {
	for _, stmt := range prog.Statements {
		fn, ok := stmt.(*FnDecl)
		if !ok {
			continue
		}
		if len(fn.TypeParams) > 0 {
			continue
		}
		params := make([]*Type, len(fn.Params))
		for i := range fn.Params {
			t, err := c.resolveTypeRef(fn.Params[i].Type)
			if err != nil {
				return err
			}
			if t == tVoid {
				return typeErr(fn.Params[i].Pos, "parameter %q cannot have void type", fn.Params[i].Name)
			}
			params[i] = t
		}
		ret := tVoid
		if fn.Return != nil {
			t, err := c.resolveTypeRef(fn.Return)
			if err != nil {
				return err
			}
			if t == tVoid {
				return typeErr(fn.Return.Pos, "use no return annotation instead of declaring a void return")
			}
			ret = t
		}
		// Preserve the existing entry (with its position) and overwrite the
		// signature fields. Builtin entries never reach this loop because the
		// AST has no FnDecl for `len`.
		sig := c.fns[fn.Name]
		sig.params = params
		sig.ret = ret
		sig.pos = fn.Pos
		c.fns[fn.Name] = sig
	}
	return nil
}

// resolveSpecs walks every SpecDecl, resolves each method's parameter and
// return types, and populates Spec.Methods + methodIdx. Unknown types in
// signatures surface here with the full type-name table available.
func (c *checker) resolveSpecs(prog *Program) error {
	for _, stmt := range prog.Statements {
		sd, ok := stmt.(*SpecDecl)
		if !ok {
			continue
		}
		spec := c.specs[sd.Name]
		for _, m := range sd.Methods {
			params := make([]*Type, len(m.Params))
			for i, p := range m.Params {
				t, err := c.resolveTypeRef(p.Type)
				if err != nil {
					return err
				}
				if t == tVoid {
					return typeErr(p.Pos, "spec method parameter %q cannot have void type", p.Name)
				}
				params[i] = t
			}
			ret := tVoid
			if m.Return != nil {
				rt, err := c.resolveTypeRef(m.Return)
				if err != nil {
					return err
				}
				if rt == tVoid {
					return typeErr(m.Return.Pos, "use no return annotation instead of declaring a void return")
				}
				ret = rt
			}
			sm := &specMethod{
				pos:    m.Pos,
				name:   m.Name,
				params: params,
				ret:    ret,
				ast:    m,
			}
			spec.Methods = append(spec.Methods, sm)
			spec.methodIdx[m.Name] = sm
		}
	}
	return nil
}

// buildMethodVisibility walks every type's collected impls and produces the
// per-(type, name) source list used by method dispatch. Inherent-vs-spec and
// inherent-vs-inherent collisions are rejected here with both positions in
// the diagnostic. Cross-spec collisions are admitted — they are disambiguated
// by binding the receiver to a spec type.
func (c *checker) buildMethodVisibility() error {
	// First detect intra-inherent collisions across separate impl blocks.
	// We walk the rawInherentDecls slice (populated in resolveImpls) so we
	// see every block's methods before merging masks them.
	type inherentSrc struct {
		pos Position
	}
	inherentSeen := map[string]map[string][]inherentSrc{} // type → name → [pos, ...]
	for _, raw := range c.rawInherentDecls {
		seenNamesInBlock := map[string]bool{}
		for _, fn := range raw.Methods {
			if seenNamesInBlock[fn.Name] {
				continue // intra-block dup already reported by resolveImpls
			}
			seenNamesInBlock[fn.Name] = true
			if inherentSeen[raw.Type] == nil {
				inherentSeen[raw.Type] = map[string][]inherentSrc{}
			}
			inherentSeen[raw.Type][fn.Name] = append(
				inherentSeen[raw.Type][fn.Name],
				inherentSrc{pos: fn.Pos},
			)
		}
	}
	for typeName, byName := range inherentSeen {
		for name, srcs := range byName {
			if len(srcs) > 1 {
				return typeErr(srcs[1].pos,
					"method %q on %s is defined multiple times in inherent impl blocks at %s, %s",
					name, typeName, srcs[0].pos, srcs[1].pos)
			}
		}
	}

	// Now build the visibility map.
	for typeName, ids := range c.implsByType {
		visible := map[string][]*methodSource{}
		for _, impl := range ids {
			if impl.SpecName == "" {
				for _, im := range impl.Methods {
					src := &methodSource{
						kind:     mskInherent,
						pos:      im.pos,
						name:     im.name,
						impl:     impl,
						implFn:   im,
						inherent: im,
					}
					visible[im.name] = append(visible[im.name], src)
				}
				continue
			}
			// v0.5: the spec may live in a foreign module if this is a
			// cross-module impl (`impl T for foreign.Spec`). Look it up
			// in the impl's recorded specOwner first; fall back to the
			// local table for in-module specs.
			spec := c.lookupSpecForImpl(impl)
			if spec == nil {
				return typeErr(impl.Pos, "internal: spec %q not found for impl on %s", impl.SpecName, impl.TypeName)
			}
			// For spec impls, every spec method is visible. Override if the
			// impl supplies one; otherwise inherit the default if present;
			// otherwise the method is "not implemented at runtime" — visible
			// at typeck but a NotImplemented panic at codegen time.
			for _, sm := range spec.Methods {
				src := &methodSource{
					kind:     mskSpec,
					name:     sm.name,
					specName: spec.Name,
					impl:     impl,
				}
				if im, ok := impl.methodIdx[sm.name]; ok {
					src.pos = im.pos
					src.implFn = im
				} else {
					src.pos = sm.pos
					src.defaultM = sm
				}
				visible[sm.name] = append(visible[sm.name], src)
			}
		}
		// Detect inherent-vs-spec collision.
		for name, srcs := range visible {
			hasInherent := false
			hasSpec := false
			var inhPos, specPos Position
			var specName string
			for _, s := range srcs {
				if s.kind == mskInherent {
					if !hasInherent {
						inhPos = s.pos
					}
					hasInherent = true
				} else {
					if !hasSpec {
						specPos = s.pos
						specName = s.specName
					}
					hasSpec = true
				}
			}
			if hasInherent && hasSpec {
				return typeErr(inhPos,
					"method %q on %s is defined twice — once inherent at %s, once via spec %s at %s. Rename one or remove one.",
					name, typeName, inhPos, specName, specPos)
			}
		}
		c.methodVisible[typeName] = visible
	}
	return nil
}

// checkSpecBodies walks any spec method that carries a default body and
// type-checks it once with `this` bound to a placeholder receiver type. Per
// PLAN, the placeholder is fine for typeck — Unit 6/7 owns the per-type code
// emission. For typeck purposes we need to validate the body against the
// spec's signature and reject obviously-wrong shapes.
//
// At v0.4 default-body type-checking is intentionally relaxed: bodies that
// reference `this.field` or `this.method()` rely on the implementing type, so
// the only soundness check we make at the spec level is "does the body's
// statement list type-check at all?". When the body is empty / just `return
// expr` of a literal type matching the declared return, that succeeds; bodies
// that probe `this.field` are accepted only where the access cannot be
// statically validated against the placeholder. Since v0.4's corpus uses
// defaults that read no fields (e.g. `fn hash() -> int { return 0 }`), the
// relaxed check is sufficient. Stricter validation can be added later.
func (c *checker) checkSpecBodies(prog *Program) error {
	for _, stmt := range prog.Statements {
		sd, ok := stmt.(*SpecDecl)
		if !ok {
			continue
		}
		spec := c.specs[sd.Name]
		for _, m := range sd.Methods {
			if m.Body == nil {
				continue
			}
			sm := spec.methodIdx[m.Name]
			// Build a synthetic fnSig so return-checking inside the body
			// compares against the declared return type.
			sig := fnSig{params: sm.params, ret: sm.ret, pos: m.Pos}
			savedFn := c.currentFn
			savedSpec := c.currentSpec
			savedRecv := c.currentReceiver
			savedSpecMethod := c.currentSpecMethod
			c.currentFn = &sig
			c.currentSpec = spec
			c.currentSpecMethod = m
			// Placeholder receiver: the spec type itself. Typeck of `this`
			// inside a default body should treat field access permissively
			// (deferred to per-type at codegen). Today we represent the
			// placeholder as the spec type, so `this.method()` resolves
			// against the spec's other methods (which is sound because every
			// implementing type has them by definition).
			c.currentReceiver = spec.typ
			c.scope = newScope(c.scope)
			// Bind the declared params.
			for i, p := range m.Params {
				if !c.scope.declare(p.Name, binding{kind: bindLet, typ: sm.params[i]}) {
					c.scope = c.scope.parent
					c.currentFn = savedFn
					c.currentSpec = savedSpec
					c.currentReceiver = savedRecv
					c.currentSpecMethod = savedSpecMethod
					return typeErr(p.Pos, "parameter %q already declared", p.Name)
				}
			}
			err := func() error {
				for _, st := range m.Body.Statements {
					if err := c.checkStmt(st); err != nil {
						return err
					}
				}
				return nil
			}()
			c.scope = c.scope.parent
			c.currentFn = savedFn
			c.currentSpec = savedSpec
			c.currentReceiver = savedRecv
			c.currentSpecMethod = savedSpecMethod
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// checkImplBodies walks every impl method body with `this` bound to the
// receiver type. Param types are already resolved.
func (c *checker) checkImplBodies(prog *Program) error {
	for _, stmt := range prog.Statements {
		id, ok := stmt.(*ImplDecl)
		if !ok {
			continue
		}
		key := implKey{typeName: id.Type, specName: id.Spec}
		impl, ok := c.impls[key]
		if !ok {
			// Inherent impls may have been merged into a single record; if so,
			// look it up by type name and locate the matching ImplDecl by Pos.
			if id.Spec == "" {
				if list := c.implsByType[id.Type]; len(list) > 0 {
					impl = list[0]
				}
			}
			if impl == nil {
				continue
			}
		}
		// For each AST method, find its resolved implMethod (by position).
		for _, fn := range id.Methods {
			var im *implMethod
			for _, candidate := range impl.Methods {
				if candidate.ast == fn {
					im = candidate
					break
				}
			}
			if im == nil {
				continue
			}
			sig := fnSig{params: im.params, ret: im.ret, pos: fn.Pos}
			savedFn := c.currentFn
			savedRecv := c.currentReceiver
			savedDecl := c.currentFnDecl
			c.currentFn = &sig
			c.currentFnDecl = fn
			c.currentReceiver = impl.Receiver
			c.scope = newScope(c.scope)
			for i, p := range fn.Params {
				if !c.scope.declare(p.Name, binding{kind: bindLet, typ: im.params[i]}) {
					c.scope = c.scope.parent
					c.currentFn = savedFn
					c.currentReceiver = savedRecv
					c.currentFnDecl = savedDecl
					return typeErr(p.Pos, "parameter %q already declared", p.Name)
				}
			}
			err := func() error {
				for _, st := range fn.Body.Statements {
					if err := c.checkStmt(st); err != nil {
						return err
					}
				}
				return nil
			}()
			c.scope = c.scope.parent
			c.currentFn = savedFn
			c.currentReceiver = savedRecv
			c.currentFnDecl = savedDecl
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveTypeRef maps a TypeRef to a *Type, populates TypeRef.Resolved, and
// reports unknown names with a clear position. List and tuple type-refs
// resolve recursively.
//
// v0.5: when ref.Module is non-empty, the type ref is module-qualified
// (`mod.Color`). The resolver looks up the module binding in the
// importing module's import table, then resolves the inner name in the
// foreign module's decl tables, gating on `pub`.
func (c *checker) resolveTypeRef(ref *TypeRef) (*Type, error) {
	if ref == nil {
		return nil, nil
	}
	switch ref.Kind {
	case TypeRefNamed:
		// v0.6 Unit 3.5: when an impl-level type substitution is in scope
		// (set by expandGenericImpls during a generic-impl body walk), a
		// bare reference to a declared type-parameter resolves to its
		// concrete *Type. Substitution short-circuits before the cross-
		// module / generic-decl paths so it captures bare T uses inside
		// method bodies (`x: T = ...`).
		if c.activeSubst != nil && ref.Module == "" && len(ref.TypeArgs) == 0 {
			if t, ok := c.activeSubst[ref.Name]; ok {
				if ref.Nullable {
					wrapped, werr := c.wrapOption(t, ref.Pos)
					if werr != nil {
						return nil, werr
					}
					ref.Resolved = wrapped
					return wrapped, nil
				}
				ref.Resolved = t
				return t, nil
			}
		}
		// Cross-module qualifier: route through the importing module's
		// import binding table. The inner name MUST be a pub struct,
		// enum, or spec in the foreign module. v0.6 Unit 2 keeps built-ins
		// unqualified-only — `mod.Option` does not resolve.
		if ref.Module != "" {
			t, err := c.resolveCrossModuleType(ref)
			if err != nil {
				return nil, err
			}
			if len(ref.TypeArgs) > 0 {
				return nil, typeErr(ref.Pos,
					"generic type arguments on cross-module references are not supported at v0.6 Unit 2")
			}
			if ref.Nullable {
				wrapped, werr := c.wrapOption(t, ref.Pos)
				if werr != nil {
					return nil, werr
				}
				ref.Resolved = wrapped
				return wrapped, nil
			}
			ref.Resolved = t
			return t, nil
		}
		// v0.7: `chan[T]` is a built-in generic type. Intercept here ahead
		// of the user-decl generic path so `chan` never collides with a
		// user-defined generic name (the binding-site reservation prevents
		// it, but the resolver stays defensive).
		if ref.Name == "chan" {
			return c.resolveChanTypeRef(ref)
		}
		// Generic-name path (v0.6): a name with type-args resolves through
		// the per-decl monomorphization cache. Unit 2 handled the built-in
		// Option / Result decls; Unit 3 extends to user-defined generic
		// enums and structs (and rejects type-args on non-generic concrete
		// names).
		if len(ref.TypeArgs) > 0 {
			args := make([]*Type, len(ref.TypeArgs))
			for i, a := range ref.TypeArgs {
				ta, err := c.resolveTypeRef(a)
				if err != nil {
					return nil, err
				}
				args[i] = ta
			}
			if enumDecl := c.findGenericEnumDecl(ref.Name); enumDecl != nil {
				t, err := c.instantiateGenericEnum(enumDecl, args, ref.Pos)
				if err != nil {
					return nil, err
				}
				if ref.Nullable {
					wrapped, werr := c.wrapOption(t, ref.Pos)
					if werr != nil {
						return nil, werr
					}
					ref.Resolved = wrapped
					return wrapped, nil
				}
				ref.Resolved = t
				return t, nil
			}
			if structDecl := c.findGenericStructDecl(ref.Name); structDecl != nil {
				t, err := c.instantiateGenericStruct(structDecl, args, ref.Pos)
				if err != nil {
					return nil, err
				}
				if ref.Nullable {
					wrapped, werr := c.wrapOption(t, ref.Pos)
					if werr != nil {
						return nil, werr
					}
					ref.Resolved = wrapped
					return wrapped, nil
				}
				ref.Resolved = t
				return t, nil
			}
			// Type-args on a primitive or non-generic concrete name.
			switch ref.Name {
			case "int", "float", "bool", "str", "byte", "rune":
				return nil, typeErr(ref.Pos,
					"type %q has no type parameters", ref.Name)
			}
			if _, ok := c.structs[ref.Name]; ok {
				return nil, typeErr(ref.Pos,
					"type %q has no type parameters", ref.Name)
			}
			if _, ok := c.enums[ref.Name]; ok {
				if _, isBuiltin := c.builtinEnumDecls[ref.Name]; !isBuiltin {
					// Non-generic user enum.
					return nil, typeErr(ref.Pos,
						"type %q has no type parameters", ref.Name)
				}
			}
			if _, ok := c.specs[ref.Name]; ok {
				return nil, typeErr(ref.Pos,
					"type %q has no type parameters", ref.Name)
			}
			return nil, typeErr(ref.Pos,
				"type %q is not generic but was given type arguments", ref.Name)
		}
		// Bare-name reference to a generic decl (no args): reject.
		if _, isBuiltin := c.builtinEnumDecls[ref.Name]; isBuiltin {
			return nil, typeErr(ref.Pos,
				"generic type %q requires type arguments", ref.Name)
		}
		if d, ok := c.enumAST[ref.Name]; ok && len(d.TypeParams) > 0 {
			return nil, typeErr(ref.Pos,
				"cannot use generic type %q without type arguments", ref.Name)
		}
		if d, ok := c.structAST[ref.Name]; ok && len(d.TypeParams) > 0 {
			return nil, typeErr(ref.Pos,
				"cannot use generic type %q without type arguments", ref.Name)
		}
		if c.crossMod != nil {
			for _, fc := range c.crossMod.checkers {
				if fc == c {
					continue
				}
				if d, ok := fc.enumAST[ref.Name]; ok && len(d.TypeParams) > 0 {
					return nil, typeErr(ref.Pos,
						"cannot use generic type %q without type arguments", ref.Name)
				}
				if d, ok := fc.structAST[ref.Name]; ok && len(d.TypeParams) > 0 {
					return nil, typeErr(ref.Pos,
						"cannot use generic type %q without type arguments", ref.Name)
				}
			}
		}
		var t *Type
		switch ref.Name {
		case "int":
			t = tInt
		case "float":
			t = tFloat
		case "bool":
			t = tBool
		case "str":
			t = tStr
		case "byte":
			t = tByte
		case "rune":
			t = tRune
		default:
			if _, isBuiltin := c.builtinEnumDecls[ref.Name]; isBuiltin {
				return nil, typeErr(ref.Pos,
					"generic type %q requires type arguments", ref.Name)
			}
			if st, ok := c.structs[ref.Name]; ok {
				t = st
			} else if en, ok := c.enums[ref.Name]; ok {
				t = en
			} else if sp, ok := c.specs[ref.Name]; ok {
				t = sp.typ
			} else {
				return nil, typeErr(ref.Pos, "unknown type %q", ref.Name)
			}
		}
		if ref.Nullable {
			wrapped, werr := c.wrapOption(t, ref.Pos)
			if werr != nil {
				return nil, werr
			}
			ref.Resolved = wrapped
			return wrapped, nil
		}
		ref.Resolved = t
		return t, nil
	case TypeRefList:
		elem, err := c.resolveTypeRef(ref.Element)
		if err != nil {
			return nil, err
		}
		if elem == tVoid {
			return nil, typeErr(ref.Pos, "list element type cannot be void")
		}
		t := NewListType(elem)
		if ref.Nullable {
			wrapped, werr := c.wrapOption(t, ref.Pos)
			if werr != nil {
				return nil, werr
			}
			ref.Resolved = wrapped
			return wrapped, nil
		}
		ref.Resolved = t
		return t, nil
	case TypeRefTuple:
		elems := make([]*Type, len(ref.Elements))
		for i, sub := range ref.Elements {
			t, err := c.resolveTypeRef(sub)
			if err != nil {
				return nil, err
			}
			if t == tVoid {
				return nil, typeErr(sub.Pos, "tuple element type cannot be void")
			}
			elems[i] = t
		}
		t := NewTupleType(elems)
		if ref.Nullable {
			wrapped, werr := c.wrapOption(t, ref.Pos)
			if werr != nil {
				return nil, werr
			}
			ref.Resolved = wrapped
			return wrapped, nil
		}
		ref.Resolved = t
		return t, nil
	}
	return nil, typeErr(ref.Pos, "internal: unknown TypeRef kind %d", int(ref.Kind))
}

// ---------------------------------------------------------------------------
// Statement walking.
// ---------------------------------------------------------------------------

func (c *checker) checkStmt(stmt Stmt) error {
	switch s := stmt.(type) {
	case *NopStmt:
		return nil
	case *PrintStmt:
		return c.checkPrint(s)
	case *LetStmt:
		if s.Tuple != nil {
			return c.checkTupleDestructure(s.Pos, s.Tuple, s.Value, bindLet)
		}
		return c.checkDeclSlot(s.Pos, s.Name, s.Type, &s.Value, bindLet)
	case *MutStmt:
		if s.Tuple != nil {
			return c.checkTupleDestructure(s.Pos, s.Tuple, s.Value, bindMut)
		}
		return c.checkDeclSlot(s.Pos, s.Name, s.Type, &s.Value, bindMut)
	case *ConstStmt:
		if s.Tuple != nil {
			// PLAN: composites are not const-evaluable at v0.2 (isConstExpr
			// rejects every tuple/list/struct shape), so a destructured const
			// is unreachable in practice. Emit the precise diagnostic anyway
			// so the parser-accepted form fails with a clear reason rather
			// than an opaque "not a constant expression".
			return typeErr(s.Pos, "tuple destructure is not allowed on const at v0.2 (no const-evaluable composite expressions)")
		}
		if !isConstExpr(s.Value) {
			return typeErr(s.Pos, "const initialiser must be a constant expression")
		}
		return c.checkDeclSlot(s.Pos, s.Name, s.Type, &s.Value, bindConst)
	case *AssignStmt:
		return c.checkAssign(s)
	case *ExprStmt:
		_, err := c.checkExpr(s.Expr)
		return err
	case *IfStmt:
		return c.checkIf(s)
	case *ForStmt:
		return c.checkFor(s)
	case *FnDecl:
		return c.checkFnDecl(s)
	case *ReturnStmt:
		return c.checkReturn(s)
	case *BreakStmt:
		if c.loopDepth == 0 {
			return typeErr(s.Pos, "'break' outside of a loop")
		}
		return c.checkGuard(s.Guard)
	case *ContinueStmt:
		if c.loopDepth == 0 {
			return typeErr(s.Pos, "'continue' outside of a loop")
		}
		return c.checkGuard(s.Guard)
	case *StructDecl:
		// Already collected and resolved; nothing to do during the body walk.
		return nil
	case *EnumDecl:
		return nil
	case *MatchStmt:
		return c.checkMatch(s)
	case *SpecDecl:
		// Spec/impl bodies are walked in dedicated passes
		// (checkSpecBodies / checkImplBodies). The top-level walk only needs
		// to visit other statement kinds; specs and impls are no-ops here.
		return nil
	case *ImplDecl:
		return nil
	case *ImportDecl:
		// v0.5 Unit 1b: parser-only landing. The module loader (Unit 2)
		// consumes ImportDecls before typeck runs on the merged program; if
		// one slips through here (e.g. single-file mode without a loader),
		// it is a no-op so existing v0.0–v0.4 corpora keep behaving the same.
		return nil
	case *SendStmt:
		return c.checkSend(s)
	case *SpawnStmt:
		return c.checkSpawnStmt(s)
	case *DeferStmt:
		return c.checkDeferStmt(s)
	case *SelectStmt:
		return c.checkSelectStmt(s)
	case *AsmBlock:
		return c.checkAsmBlock(s)
	}
	return typeErr(stmt.StmtPos(), "internal: unhandled statement %T", stmt)
}

// checkPrint validates `print expr`. v0.2 accepts every printable shape:
// primitives, lists, tuples, structs, enums. Void is rejected. v0.7 rejects
// channel-typed values with a focused diagnostic — channels carry no
// printable surface and the deep-copy on send/recv would make every print
// either a snapshot of the queue (potentially racy) or a no-op.
func (c *checker) checkPrint(s *PrintStmt) error {
	t, err := c.checkExpr(s.Expr)
	if err != nil {
		return err
	}
	if isChanType(t) {
		return typeErr(s.Pos, "cannot print channel value (channels are not Printable)")
	}
	if !isPrintable(t) {
		return typeErr(s.Pos, "cannot print value of type %s", t)
	}
	return nil
}

// isPrintable reports whether print accepts a value of t. Per PLAN.md every
// concrete v0.2 shape (primitive, list, tuple, struct, enum) is admissible.
// Unknown / void are not.
func isPrintable(t *Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case TypeInt, TypeFloat, TypeBool, TypeStr, TypeByte, TypeRune:
		return true
	case TypeList:
		// Forbid printing a list whose element is non-printable (mostly defends
		// against an empty-list-with-unknown-elem leaking into print).
		return isPrintable(t.Element)
	case TypeTuple:
		for _, e := range t.Tuple {
			if !isPrintable(e) {
				return false
			}
		}
		return true
	case TypeStruct, TypeEnum:
		return true
	}
	return false
}

// checkDecl handles let/mut/const. The annotated type, when present, drives
// inference for empty-list literals.
func (c *checker) checkDecl(pos Position, name string, ref *TypeRef, value Expr, kind bindKind) error {
	// v0.5: an immutable / mut / const binding cannot shadow an imported
	// module binding. The reverse direction (module shadows local) is
	// impossible because locals are scoped below module bindings.
	if c.crossMod != nil {
		if _, ok := c.crossMod.imports[name]; ok && c.scope.parent == nil {
			// Top-level scope: the binding lives at module scope and would
			// override the import binding for every reference below.
			// Reject with a focused diagnostic.
			return typeErr(pos, "name %q shadows imported module binding", name)
		}
	}
	annotated, err := c.resolveTypeRef(ref)
	if err != nil {
		return err
	}
	// Pass the annotated type as a hint so empty-list literals can latch onto
	// it and the v0.6 T → T? lift applies. The caller-side slot is bound
	// via checkDeclSlot so a wrapping EnumLit can replace value in its
	// parent slot (caller updates the *LetStmt / *MutStmt / *ConstStmt).
	return c.checkDeclWithSlot(pos, name, annotated, value, kind, nil)
}

// checkDeclSlot is the slot-aware front door used by checkStmt for the v0.6
// lift path. It resolves the annotation and then defers to
// checkDeclWithSlot with the parent slot.
func (c *checker) checkDeclSlot(pos Position, name string, ref *TypeRef, slot *Expr, kind bindKind) error {
	if isReservedV07BuiltinName(name) || name == "close" || isReservedV07ConcurName(name) {
		return typeErr(pos, "name %q is reserved (built-in)", name)
	}
	if c.crossMod != nil {
		if _, ok := c.crossMod.imports[name]; ok && c.scope.parent == nil {
			return typeErr(pos, "name %q shadows imported module binding", name)
		}
	}
	annotated, err := c.resolveTypeRef(ref)
	if err != nil {
		return err
	}
	return c.checkDeclWithSlot(pos, name, annotated, *slot, kind, slot)
}

// checkDeclWithSlot is the slot-aware decl helper: when slot != nil and the
// hint triggers a T → T? lift, the wrapped EnumLit is installed via slot.
// Used by the immutable / mut / const checker paths.
func (c *checker) checkDeclWithSlot(pos Position, name string, annotated *Type, value Expr, kind bindKind, slot *Expr) error {
	newExpr, observed, err := c.checkExprLift(value, annotated)
	if err != nil {
		return err
	}
	if slot != nil && newExpr != value {
		*slot = newExpr
	}
	final := observed
	if annotated != nil {
		if !c.assignableTo(observed, annotated) {
			return typeErr(pos, "cannot assign %s to %s for %q", observed, annotated, name)
		}
		final = annotated
	}
	if final == nil || final.Kind == TypeUnknown {
		return typeErr(pos, "cannot infer type of %q (provide an annotation)", name)
	}
	if final == tVoid {
		return typeErr(pos, "cannot bind %q to a value of type ()", name)
	}
	if !c.scope.declare(name, binding{kind: kind, typ: final}) {
		return typeErr(pos, "name %q already declared in this scope", name)
	}
	return nil
}

// assignableTo reports whether a value of type `from` may flow into a slot
// declared as `to`. Beyond plain type equality, v0.4 admits widening from a
// concrete struct/enum that implements a spec into the spec type itself.
// list[Printable] / tuple[..., Printable] / struct field of spec type all
// follow the same rule recursively. Spec → concrete is NEVER admitted (you
// can't downcast at v0.4).
func (c *checker) assignableTo(from, to *Type) bool {
	if typeEq(from, to) {
		return true
	}
	if from == nil || to == nil {
		return false
	}
	// Spec widening: from concrete struct/enum implementing `to`.
	if to.Kind == TypeSpec {
		if from.Kind != TypeStruct && from.Kind != TypeEnum {
			return false
		}
		key := implKey{typeName: from.Name, specName: to.Name}
		if _, ok := c.impls[key]; ok {
			return true
		}
		// v0.5: an impl can live in any module across the bundle. Walk
		// every module's impl table so list[Spec] / tuple[Spec] /
		// struct-field-of-Spec coerce cross-module concrete values
		// uniformly. The impl table key is (typeName, specName); the
		// canonical *Type pointer comparison would tighten this further
		// (two modules with same-name structs collide on string-keys),
		// but the v0.5 orphan rule guarantees at most one module owns
		// each (Type, Spec) pair, and the impl's recvOwner / specOwner
		// fields disambiguate when needed at the dispatch layer.
		if c.crossMod != nil {
			for _, fc := range c.crossMod.checkers {
				if fc == c {
					continue
				}
				if _, ok := fc.impls[key]; ok {
					return true
				}
			}
		}
		return false
	}
	// Recurse into composites.
	switch to.Kind {
	case TypeList:
		if from.Kind != TypeList {
			return false
		}
		return c.assignableTo(from.Element, to.Element)
	case TypeTuple:
		if from.Kind != TypeTuple || len(from.Tuple) != len(to.Tuple) {
			return false
		}
		for i := range from.Tuple {
			if !c.assignableTo(from.Tuple[i], to.Tuple[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// checkTupleDestructure handles tuple-destructure binding `(a, b) := expr` (and the mut form). The
// RHS must be a tuple type with arity matching the LHS name list; each name
// is then bound in the surrounding scope with its element type. The parser
// has already rejected repeated names; the only diagnostics generated here
// are for arity / shape mismatch and shadowing-in-the-same-scope.
//
// Annotated destructure (`(a, b): tuple[int, int] = ...`) is rejected by
// the parser so we never see a non-nil TypeRef here.
func (c *checker) checkTupleDestructure(pos Position, tb *TupleBinding, value Expr, kind bindKind) error {
	observed, err := c.checkExpr(value)
	if err != nil {
		return err
	}
	if observed == nil || observed.Kind != TypeTuple {
		return typeErr(pos, "tuple destructure requires a tuple value, got %s", observed)
	}
	if len(observed.Tuple) != len(tb.Names) {
		return typeErr(pos, "destructure expects %d element(s), value has %d", len(tb.Names), len(observed.Tuple))
	}
	for i, name := range tb.Names {
		if isReservedV07BuiltinName(name) || name == "close" || isReservedV07ConcurName(name) {
			return typeErr(tb.NamePos[i], "name %q is reserved (built-in)", name)
		}
		elemT := observed.Tuple[i]
		if elemT == nil || elemT.Kind == TypeUnknown {
			return typeErr(tb.NamePos[i], "cannot infer type of %q from tuple element %d", name, i+1)
		}
		if elemT == tVoid {
			return typeErr(tb.NamePos[i], "cannot bind %q to a value of type ()", name)
		}
		if !c.scope.declare(name, binding{kind: kind, typ: elemT}) {
			return typeErr(tb.NamePos[i], "name %q already declared in this scope", name)
		}
	}
	return nil
}

// checkAssign validates the lhs is mut, then matches operator semantics.
func (c *checker) checkAssign(s *AssignStmt) error {
	switch lhs := s.Target.(type) {
	case *IdentExpr:
		return c.checkAssignIdent(s, lhs)
	case *IndexExpr:
		return c.checkAssignIndex(s, lhs)
	default:
		return typeErr(s.Pos, "internal: unsupported assignment target %T", s.Target)
	}
}

// checkAssignIndex is the v0.3 list-element assignment path: `xs[i] = v`.
// The receiver must be a mut-bound list variable (parse-time guarantees a
// bare ident receiver — chained indexing was rejected upstream); the index
// must be int; the value must match the list's element type. Borrow-state
// checks (Owned vs Moved vs BorrowedShared) happen in the separate borrow
// pass, not here — typeck only verifies mutability and types.
func (c *checker) checkAssignIndex(s *AssignStmt, lhs *IndexExpr) error {
	id, ok := lhs.Receiver.(*IdentExpr)
	if !ok {
		return typeErr(lhs.Pos, "list-element assignment requires a named list on the left")
	}
	b, ok := c.scope.lookup(id.Name)
	if !ok {
		return typeErr(id.Pos, "undefined name %q", id.Name)
	}
	if b.typ == nil || b.typ.Kind != TypeList {
		return typeErr(lhs.Pos, "cannot index-assign into %q (declared %s)", id.Name, b.typ)
	}
	switch b.kind {
	case bindLet:
		return typeErr(s.Pos, "cannot assign to %q[i] (immutable binding — declare with mut to allow element mutation)", id.Name)
	case bindConst:
		return typeErr(s.Pos, "cannot assign to %q[i] (declared with const)", id.Name)
	}
	id.setType(b.typ)
	// Index must be int.
	it, err := c.checkExpr(lhs.Index)
	if err != nil {
		return err
	}
	if it != tInt {
		return typeErr(lhs.Index.ExprPos(), "index must be int, got %s", it)
	}
	lhs.setType(b.typ.Element)
	// Value must match the list's element type.
	rhs, err := c.checkExprHint(s.Value, b.typ.Element)
	if err != nil {
		return err
	}
	if !typeEq(rhs, b.typ.Element) {
		return typeErr(s.Pos, "cannot assign %s to %s element of %q", rhs, b.typ.Element, id.Name)
	}
	// v0.3 list-element assignment uses the bare `=` operator only. Compound
	// forms (`xs[i] += 1`) are rejected at parse time; defensive guard here.
	if s.Op != AssignSet {
		return typeErr(s.Pos, "compound assignment to a list element is not supported at v0.3")
	}
	return nil
}

// checkAssignIdent is the v0.1 simple-assignment path: `name OP value`.
func (c *checker) checkAssignIdent(s *AssignStmt, target *IdentExpr) error {
	b, ok := c.scope.lookup(target.Name)
	if !ok {
		return typeErr(target.Pos, "undefined name %q", target.Name)
	}
	switch b.kind {
	case bindLet:
		return typeErr(s.Pos, "cannot assign to %q (immutable binding — declare with mut to allow rebinding)", target.Name)
	case bindConst:
		return typeErr(s.Pos, "cannot assign to %q (declared with const)", target.Name)
	}
	target.setType(b.typ)
	rhs, err := c.checkExprHint(s.Value, b.typ)
	if err != nil {
		return err
	}
	switch s.Op {
	case AssignSet:
		if !typeEq(rhs, b.typ) {
			return typeErr(s.Pos, "cannot assign %s to %q (declared %s)", rhs, target.Name, b.typ)
		}
	case AssignAdd, AssignSub, AssignMul, AssignDiv, AssignMod:
		if s.Op == AssignAdd && b.typ == tStr && rhs == tStr {
			break
		}
		if !isNumeric(b.typ) || !typeEq(rhs, b.typ) {
			return typeErr(s.Pos, "operator %s requires numeric operands of the same type, got %s and %s", s.Op, b.typ, rhs)
		}
	case AssignAnd, AssignOr, AssignXor, AssignShl, AssignShr:
		if b.typ != tInt || rhs != tInt {
			return typeErr(s.Pos, "operator %s requires int operands, got %s and %s", s.Op, b.typ, rhs)
		}
	default:
		return typeErr(s.Pos, "internal: unknown assignment operator %s", s.Op)
	}
	return nil
}

// checkIf type-checks the condition and walks each branch in a fresh scope.
func (c *checker) checkIf(s *IfStmt) error {
	condT, err := c.checkExpr(s.Cond)
	if err != nil {
		return err
	}
	if condT != tBool {
		return typeErr(s.Cond.ExprPos(), "'if' condition must be bool, got %s", condT)
	}
	if err := c.checkBlock(s.Then); err != nil {
		return err
	}
	for i := range s.Elifs {
		ec := &s.Elifs[i]
		ct, err := c.checkExpr(ec.Cond)
		if err != nil {
			return err
		}
		if ct != tBool {
			return typeErr(ec.Cond.ExprPos(), "'elif' condition must be bool, got %s", ct)
		}
		if err := c.checkBlock(ec.Body); err != nil {
			return err
		}
	}
	if s.Else != nil {
		if err := c.checkBlock(s.Else); err != nil {
			return err
		}
	}
	return nil
}

// checkFor handles all three for-loop shapes.
func (c *checker) checkFor(s *ForStmt) error {
	c.loopDepth++
	defer func() { c.loopDepth-- }()
	switch s.Kind {
	case ForInfinite:
		return c.checkBlock(s.Body)
	case ForCond:
		ct, err := c.checkExpr(s.Cond)
		if err != nil {
			return err
		}
		if ct != tBool {
			return typeErr(s.Cond.ExprPos(), "'for' condition must be bool, got %s", ct)
		}
		return c.checkBlock(s.Body)
	case ForRange:
		startT, err := c.checkExpr(s.Range.Start)
		if err != nil {
			return err
		}
		if startT != tInt {
			return typeErr(s.Range.Start.ExprPos(), "range start must be int, got %s", startT)
		}
		endT, err := c.checkExpr(s.Range.End)
		if err != nil {
			return err
		}
		if endT != tInt {
			return typeErr(s.Range.End.ExprPos(), "range end must be int, got %s", endT)
		}
		c.scope = newScope(c.scope)
		defer func() { c.scope = c.scope.parent }()
		if !c.scope.declare(s.Var, binding{kind: bindLet, typ: tInt}) {
			return typeErr(s.VarPos, "name %q already declared in this scope", s.Var)
		}
		for _, st := range s.Body.Statements {
			if err := c.checkStmt(st); err != nil {
				return err
			}
		}
		return nil
	case ForIter:
		// `for x in xs { ... }` — iterate over a list-typed expression, OR
		// (v0.7) over a chan-typed expression. The loop variable's type is
		// the list element / chan element. Empty lists are allowed (the body
		// never runs); typeck rejects unannotated empty literals upstream so
		// the iterable's element type is always concrete here. For channels,
		// receiving until close is desugared (at run.go / cgen.go) to a
		// match-on-Option loop; we mark the ForStmt with Kind = ForChan so
		// downstream halves can dispatch.
		iterT, err := c.checkExpr(s.Iter)
		if err != nil {
			return err
		}
		if iterT == nil {
			return typeErr(s.Iter.ExprPos(), "'for ... in' iterable has unknown type")
		}
		var elem *Type
		switch iterT.Kind {
		case TypeList:
			elem = iterT.Element
		case TypeChan:
			elem = iterT.Element
			s.Kind = ForChan
		default:
			return typeErr(s.Iter.ExprPos(), "'for ... in' iterable must be a list or channel, got %s", iterT)
		}
		c.scope = newScope(c.scope)
		defer func() { c.scope = c.scope.parent }()
		if !c.scope.declare(s.Var, binding{kind: bindLet, typ: elem}) {
			return typeErr(s.VarPos, "name %q already declared in this scope", s.Var)
		}
		for _, st := range s.Body.Statements {
			if err := c.checkStmt(st); err != nil {
				return err
			}
		}
		return nil
	case ForChan:
		// Defensive: the parser doesn't produce ForChan directly; checkFor
		// re-tags ForIter when it sees a chan-typed iterable. This case
		// keeps the dispatch table exhaustive so a future caller that
		// constructs ForChan in source-to-AST tools doesn't fall through to
		// the "internal" panic below.
		iterT, err := c.checkExpr(s.Iter)
		if err != nil {
			return err
		}
		if !isChanType(iterT) {
			return typeErr(s.Iter.ExprPos(), "'for v in ch' iterable must be a channel, got %s", iterT)
		}
		c.scope = newScope(c.scope)
		defer func() { c.scope = c.scope.parent }()
		if !c.scope.declare(s.Var, binding{kind: bindLet, typ: iterT.Element}) {
			return typeErr(s.VarPos, "name %q already declared in this scope", s.Var)
		}
		for _, st := range s.Body.Statements {
			if err := c.checkStmt(st); err != nil {
				return err
			}
		}
		return nil
	}
	return typeErr(s.Pos, "internal: unknown for kind")
}

// checkBlock pushes a new scope and walks statements.
func (c *checker) checkBlock(b *Block) error {
	c.scope = newScope(c.scope)
	defer func() { c.scope = c.scope.parent }()
	for _, st := range b.Statements {
		if err := c.checkStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// checkFnDecl walks a top-level function body.
func (c *checker) checkFnDecl(fn *FnDecl) error {
	if c.currentFn != nil {
		return typeErr(fn.Pos, "nested functions are not supported")
	}
	// v0.6 Unit 3: generic fn decls are NOT body-checked at decl time.
	// Each call site spawns a specialised clone that gets its body
	// type-checked under the substituted type-vars.
	if len(fn.TypeParams) > 0 {
		return nil
	}
	// v0.8 Unit 2: __builtin fn-decls have no body; validate the bareword
	// against the closed registry and return. The fn's signature was
	// already resolved by resolveFnSignatures so call sites see normal
	// param/ret types.
	if fn.BuiltinName != "" {
		return validateBuiltinFnDecl(fn)
	}
	sig := c.fns[fn.Name]
	c.currentFn = &sig
	c.currentFnDecl = fn
	defer func() { c.currentFn = nil; c.currentFnDecl = nil }()

	c.scope = newScope(c.scope)
	defer func() { c.scope = c.scope.parent }()
	for i, p := range fn.Params {
		if !c.scope.declare(p.Name, binding{kind: bindLet, typ: sig.params[i]}) {
			return typeErr(p.Pos, "parameter %q already declared", p.Name)
		}
	}
	for _, st := range fn.Body.Statements {
		if err := c.checkStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// checkReturn validates the return is inside a fn and the value matches.
func (c *checker) checkReturn(s *ReturnStmt) error {
	if c.currentFn == nil {
		return typeErr(s.Pos, "'return' outside of a function")
	}
	if err := c.checkGuard(s.Guard); err != nil {
		return err
	}
	if s.Value == nil {
		if c.currentFn.ret != tVoid {
			return typeErr(s.Pos, "function declared to return %s but bare 'return'", c.currentFn.ret)
		}
		return nil
	}
	newExpr, t, err := c.checkExprLift(s.Value, c.currentFn.ret)
	if err != nil {
		return err
	}
	if newExpr != s.Value {
		s.Value = newExpr
	}
	if c.currentFn.ret == tVoid {
		return typeErr(s.Pos, "function returns no value but 'return' has expression of type %s", t)
	}
	if !c.assignableTo(t, c.currentFn.ret) {
		return typeErr(s.Pos, "return type mismatch: function returns %s, got %s", c.currentFn.ret, t)
	}
	return nil
}

// checkGuard validates a break/continue/return guard expression.
func (c *checker) checkGuard(g Expr) error {
	if g == nil {
		return nil
	}
	t, err := c.checkExpr(g)
	if err != nil {
		return err
	}
	if t != tBool {
		return typeErr(g.ExprPos(), "guard must be bool, got %s", t)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Match statements.
// ---------------------------------------------------------------------------

// checkMatch walks a match statement: type-checks the subject, then for each
// arm validates the pattern against the subject type, opens a fresh scope
// populated with pattern bindings, checks the optional guard (must be bool),
// and finally walks the arm body.
func (c *checker) checkMatch(s *MatchStmt) error {
	subjT, err := c.checkExpr(s.Subject)
	if err != nil {
		return err
	}
	if subjT == nil || subjT == tVoid {
		return typeErr(s.Subject.ExprPos(), "cannot match on value of type %s", subjT)
	}
	for i := range s.Arms {
		arm := &s.Arms[i]
		// Per-arm scope so bindings vanish at arm boundary.
		armScope := newScope(c.scope)
		bindings := map[string]bool{}
		if err := c.checkPattern(arm.Pattern, subjT, armScope, bindings); err != nil {
			return err
		}
		// Walk guard and body with the arm scope active.
		saved := c.scope
		c.scope = armScope
		if arm.Guard != nil {
			gt, err := c.checkExpr(arm.Guard)
			if err != nil {
				c.scope = saved
				return err
			}
			if gt != tBool {
				c.scope = saved
				return typeErr(arm.Guard.ExprPos(), "match guard must be bool, got %s", gt)
			}
		}
		// The body itself is a Block; checkBlock pushes another scope on top
		// of the arm scope so the body is free to redeclare names without
		// clobbering pattern bindings.
		err := c.checkBlock(arm.Body)
		c.scope = saved
		if err != nil {
			return err
		}
	}
	return nil
}

// checkPattern validates pat against expected. Bindings are recorded in
// armScope; the bindings map tracks names within a single pattern so `Point
// { x, x }` is rejected.
func (c *checker) checkPattern(pat Pattern, expected *Type, armScope *scope, bindings map[string]bool) error {
	switch p := pat.(type) {
	case *WildcardPat:
		return nil
	case *BindPat:
		if bindings[p.Name] {
			return typeErr(p.Pos, "name %q already bound in this pattern", p.Name)
		}
		bindings[p.Name] = true
		if !armScope.declare(p.Name, binding{kind: bindLet, typ: expected}) {
			return typeErr(p.Pos, "name %q already declared in this scope", p.Name)
		}
		return nil
	case *LitPat:
		t, err := c.checkExpr(p.Lit)
		if err != nil {
			return err
		}
		if !typeEq(t, expected) {
			return typeErr(p.Pos, "literal pattern of type %s does not match subject type %s", t, expected)
		}
		return nil
	case *TuplePat:
		if expected == nil || expected.Kind != TypeTuple {
			return typeErr(p.Pos, "tuple pattern cannot match subject of type %s", expected)
		}
		if len(p.Elements) != len(expected.Tuple) {
			return typeErr(p.Pos, "tuple pattern has %d element(s), subject has %d", len(p.Elements), len(expected.Tuple))
		}
		for i, sub := range p.Elements {
			if err := c.checkPattern(sub, expected.Tuple[i], armScope, bindings); err != nil {
				return err
			}
		}
		return nil
	case *StructPat:
		if expected == nil || expected.Kind != TypeStruct {
			return typeErr(p.Pos, "struct pattern cannot match subject of type %s", expected)
		}
		if expected.Name != p.TypeName {
			return typeErr(p.Pos, "struct pattern type %q does not match subject type %s", p.TypeName, expected)
		}
		// Build a quick name → declared field type map for lookup, plus a set
		// of fields the pattern names so we can validate completeness.
		fieldByName := map[string]*Type{}
		for _, f := range expected.Fields {
			fieldByName[f.Name] = f.Type
		}
		named := map[string]bool{}
		for _, f := range p.Fields {
			ft, ok := fieldByName[f.Name]
			if !ok {
				return typeErr(f.Pos, "struct %q has no field %q", expected.Name, f.Name)
			}
			if named[f.Name] {
				return typeErr(f.Pos, "field %q repeated in struct pattern", f.Name)
			}
			named[f.Name] = true
			if err := c.checkPattern(f.Pattern, ft, armScope, bindings); err != nil {
				return err
			}
		}
		if !p.Rest {
			// Every declared field must appear when `..` is absent.
			for _, f := range expected.Fields {
				if !named[f.Name] {
					return typeErr(p.Pos, "struct pattern is missing field %q (use '..' to skip remaining fields)", f.Name)
				}
			}
		}
		return nil
	case *EnumPat:
		if expected == nil || expected.Kind != TypeEnum {
			return typeErr(p.Pos, "enum pattern cannot match subject of type %s", expected)
		}
		// v0.6: monomorphised generic enums carry a Name like `Pair[int,str]`.
		// Patterns spell only the bare decl name (`Pair`) — strip the
		// `[args]` suffix from the subject before comparing. Variant payload
		// types are already substituted on the mono *Type so payload-pattern
		// checks below work unchanged.
		expectedBaseName := expected.Name
		for i, r := range expectedBaseName {
			if r == '[' {
				expectedBaseName = expectedBaseName[:i]
				break
			}
		}
		if expectedBaseName != p.TypeName {
			return typeErr(p.Pos, "enum pattern type %q does not match subject type %s", p.TypeName, expected)
		}
		// Locate the variant index so we can validate payload arity and types.
		idx := -1
		for i, v := range expected.Variants {
			if v == p.VariantName {
				idx = i
				break
			}
		}
		if idx < 0 {
			return typeErr(p.Pos, "enum %q has no variant %q", expected.Name, p.VariantName)
		}
		variantPayload := expected.VariantPayloads[idx]
		// Bare pattern (no parens) — admissible only for bare variants.
		if len(p.Payload) == 0 {
			if len(variantPayload) > 0 {
				return typeErr(p.Pos, "variant %q.%q has %d payload value(s), pattern must destructure with parens", expected.Name, p.VariantName, len(variantPayload))
			}
			return nil
		}
		// Payload pattern present. Arity must match.
		if len(p.Payload) != len(variantPayload) {
			return typeErr(p.Pos, "variant %q.%q has %d payload value(s), pattern supplies %d", expected.Name, p.VariantName, len(variantPayload), len(p.Payload))
		}
		for i, sub := range p.Payload {
			if err := c.checkPattern(sub, variantPayload[i], armScope, bindings); err != nil {
				return err
			}
		}
		return nil
	}
	return typeErr(pat.PatPos(), "internal: unhandled pattern %T", pat)
}

// ---------------------------------------------------------------------------
// Expression walking.
// ---------------------------------------------------------------------------

// checkExpr walks an expression with no contextual hint. Equivalent to
// checkExprHint(expr, nil); kept as a thin wrapper because the bulk of call
// sites have no hint.
func (c *checker) checkExpr(expr Expr) (*Type, error) {
	return c.checkExprHint(expr, nil)
}

// checkExprHint is the hint-aware form. The hint is consumed only by ListLit
// (empty-list inference) and StructLit (no current use, kept for symmetry);
// every other shape ignores it.
func (c *checker) checkExprHint(expr Expr, hint *Type) (*Type, error) {
	switch e := expr.(type) {
	case *IntLit:
		v, err := strconv.ParseInt(e.Text, 0, 64)
		if err != nil {
			return nil, typeErr(e.Pos, "integer literal %s overflows int", e.Text)
		}
		e.Int = v
		e.setType(tInt)
		return tInt, nil
	case *FloatLit:
		v, err := strconv.ParseFloat(e.Text, 64)
		if err != nil || math.IsInf(v, 0) {
			return nil, typeErr(e.Pos, "float literal %s overflows float", e.Text)
		}
		e.Float = v
		e.setType(tFloat)
		return tFloat, nil
	case *StringLit:
		e.setType(tStr)
		return tStr, nil
	case *BoolLit:
		e.setType(tBool)
		return tBool, nil
	case *RuneLit:
		// PLAN: codepoint < 128 is byte; otherwise rune. Reject codepoints
		// outside valid Unicode range so the codegen and interpreter never
		// see junk values.
		if e.Value < 0 || e.Value > 0x10FFFF {
			return nil, typeErr(e.Pos, "rune codepoint %d is out of valid Unicode range (0..0x10FFFF)", e.Value)
		}
		var t *Type
		if e.Value < 128 {
			t = tByte
		} else {
			t = tRune
		}
		e.setType(t)
		return t, nil
	case *IdentExpr:
		b, defScope, ok := c.scope.lookupWithScope(e.Name)
		if !ok {
			return nil, typeErr(e.Pos, "undefined name %q", e.Name)
		}
		if len(c.anonFrames) > 0 {
			if err := c.noteIdentResolved(e.Name, e.Pos, b, defScope); err != nil {
				return nil, err
			}
		}
		e.setType(b.typ)
		return b.typ, nil
	case *BinaryExpr:
		return c.checkBinary(e)
	case *UnaryExpr:
		return c.checkUnary(e)
	case *CallExpr:
		return c.checkCallHint(e, hint)
	case *ParenExpr:
		t, err := c.checkExprHint(e.Inner, hint)
		if err != nil {
			return nil, err
		}
		e.setType(t)
		return t, nil
	case *RangeExpr:
		return nil, typeErr(e.Pos, "range expressions cannot be used as values")
	case *ListLit:
		return c.checkListLit(e, hint)
	case *TupleLit:
		return c.checkTupleLit(e, hint)
	case *StructLit:
		return c.checkStructLit(e, hint)
	case *IndexExpr:
		return c.checkIndex(e)
	case *SliceExpr:
		return c.checkSlice(e)
	case *FieldAccessExpr:
		return c.checkFieldAccessHint(e, hint)
	case *MethodCallExpr:
		return c.checkMethodCallHint(e, hint)
	case *ThisExpr:
		if c.currentReceiver == nil {
			return nil, typeErr(e.Pos, "'this' is only valid inside an impl method body")
		}
		e.setType(c.currentReceiver)
		return c.currentReceiver, nil
	case *EnumLit:
		return c.checkEnumLit(e)
	case *NilLit:
		// v0.6 Unit 2: nil resolves only when the surrounding context
		// supplies an Option[T] expected type via the hint. Unit 4
		// extends every bidirectional position (return, fn-arg, list
		// element, struct field) by routing those callers through
		// checkExprLift, which propagates the hint here.
		if hint != nil && hint.Kind == TypeEnum && isOptionInstance(hint) {
			e.setType(hint)
			return hint, nil
		}
		return nil, typeErr(e.Pos, "cannot infer type of nil — annotate the binding")
	case *PropagateExpr:
		return c.checkPropagate(e)
	case *CoalesceExpr:
		return c.checkCoalesce(e, hint)
	case *ChanConstructorExpr:
		return c.checkChanConstructor(e)
	case *RecvExpr:
		return c.checkRecv(e)
	case *AnonFnExpr:
		return c.checkAnonFnExpr(e)
	}
	return nil, typeErr(expr.ExprPos(), "internal: unhandled expression %T", expr)
}

// isOptionInstance reports whether t is a monomorphized Option[...] enum.
// Used by NilLit's bidirectional check at Unit 2 (and re-used by Unit 4 for
// the broader nil / `?` / `??` / `?.` machinery).
func isOptionInstance(t *Type) bool {
	if t == nil || t.Kind != TypeEnum {
		return false
	}
	return strings.HasPrefix(t.Name, "Option[")
}

// checkListLit handles `[e1, e2, ...]` and the empty-list special case. With
// elements present, every element type must equal the first; the result is
// list[T]. With zero elements, the literal latches onto a list-shaped hint
// (annotated binding / call argument / function return); otherwise it errors out.
func (c *checker) checkListLit(e *ListLit, hint *Type) (*Type, error) {
	if len(e.Elements) == 0 {
		// Empty list: needs context.
		if hint != nil && hint.Kind == TypeList {
			e.setType(hint)
			return hint, nil
		}
		// Mark the literal as "needs context" so the surrounding decl/return
		// path can produce a precise diagnostic. The setType is for AST
		// completeness — typeck still errors out below.
		unk := &Type{Kind: TypeUnknown}
		e.setType(unk)
		return nil, typeErr(e.Pos, "cannot infer element type of empty list literal")
	}
	// Use the hint's element type as a hint for each element so an annotated
	// `xs: list[list[int]] = [[]]` can fill in the inner empty list. The
	// v0.6 lift fires here too: `xs: list[int?] = [1, 2]` wraps each int
	// in a Some(...) at the per-element slot.
	var elemHint *Type
	if hint != nil && hint.Kind == TypeList {
		elemHint = hint.Element
	}
	// When the hint asks for a list of a spec type, the result list is
	// list[Spec] regardless of which concrete types appear in the literal —
	// each element only needs to impl the spec. Otherwise use the first
	// element's observed type and require subsequent elements to match.
	newFirst, first, err := c.checkExprLift(e.Elements[0], elemHint)
	if err != nil {
		return nil, err
	}
	if newFirst != e.Elements[0] {
		e.Elements[0] = newFirst
	}
	if elemHint != nil && elemHint.Kind == TypeSpec {
		if !c.assignableTo(first, elemHint) {
			return nil, typeErr(e.Elements[0].ExprPos(), "list element 1 has type %s, expected %s", first, elemHint)
		}
		for i := 1; i < len(e.Elements); i++ {
			newE, t, err := c.checkExprLift(e.Elements[i], elemHint)
			if err != nil {
				return nil, err
			}
			if newE != e.Elements[i] {
				e.Elements[i] = newE
			}
			if !c.assignableTo(t, elemHint) {
				return nil, typeErr(e.Elements[i].ExprPos(), "list element %d has type %s, expected %s", i+1, t, elemHint)
			}
		}
		out := NewListType(elemHint)
		e.setType(out)
		return out, nil
	}
	for i := 1; i < len(e.Elements); i++ {
		newE, t, err := c.checkExprLift(e.Elements[i], elemHint)
		if err != nil {
			return nil, err
		}
		if newE != e.Elements[i] {
			e.Elements[i] = newE
		}
		if !typeEq(t, first) {
			return nil, typeErr(e.Elements[i].ExprPos(), "list element %d has type %s, expected %s", i+1, t, first)
		}
	}
	t := NewListType(first)
	e.setType(t)
	return t, nil
}

// checkTupleLit handles `(e1, e2, ...)`. The hint, if a tuple of matching
// arity, is forwarded element-wise so nested empty lists can latch on.
func (c *checker) checkTupleLit(e *TupleLit, hint *Type) (*Type, error) {
	elems := make([]*Type, len(e.Elements))
	for i, el := range e.Elements {
		var sub *Type
		if hint != nil && hint.Kind == TypeTuple && len(hint.Tuple) == len(e.Elements) {
			sub = hint.Tuple[i]
		}
		t, err := c.checkExprHint(el, sub)
		if err != nil {
			return nil, err
		}
		if t == tVoid {
			return nil, typeErr(el.ExprPos(), "tuple element cannot have void type")
		}
		elems[i] = t
	}
	t := NewTupleType(elems)
	e.setType(t)
	return t, nil
}

// checkStructLit validates `Name { f1: v1, ... }`. The literal must match the
// declared field set exactly — same names, no missing, no extras. Field order
// in the literal can differ from declaration order.
//
// v0.5: when e.Module is non-empty, the struct type is module-qualified
// (`mod.MyStruct {...}`). The lookup goes through the importing module's
// import binding table; the foreign struct must be `pub`.
//
// v0.6 Unit 3: when the named type is generic and a hint supplies the
// instance (`b: Box[int] = Box { value: 7 }`), we use the hint's
// substituted struct *Type directly. Without a hint a bare-name reference
// to a generic struct is rejected.
func (c *checker) checkStructLit(e *StructLit, hint *Type) (*Type, error) {
	if e.Module != "" {
		st, found, err := c.resolveCrossModuleStruct(e.Module, e.TypeName)
		if !found {
			return nil, typeErr(e.Pos, "unknown struct type %q.%q", e.Module, e.TypeName)
		}
		if err != nil {
			return nil, typeErr(e.Pos,
				"cannot access '%s.%s': %q is not pub in module %q",
				e.Module, e.TypeName, e.TypeName, e.Module)
		}
		return c.checkStructLitBody(e, st)
	}
	if st, ok := c.structs[e.TypeName]; ok {
		return c.checkStructLitBody(e, st)
	}
	if d := c.findGenericStructDecl(e.TypeName); d != nil {
		if hint == nil || hint.Kind != TypeStruct {
			return nil, typeErr(e.Pos,
				"cannot infer type parameter(s) for generic struct %q — annotate the binding",
				e.TypeName)
		}
		got, ok := genericStructInstanceArgs(hint, e.TypeName, d)
		if !ok || len(got) != len(d.TypeParams) {
			return nil, typeErr(e.Pos,
				"cannot infer type parameter(s) for generic struct %q — annotate the binding",
				e.TypeName)
		}
		mono, err := c.instantiateGenericStruct(d, got, e.Pos)
		if err != nil {
			return nil, err
		}
		return c.checkStructLitBody(e, mono)
	}
	return nil, typeErr(e.Pos, "unknown struct type %q", e.TypeName)
}

// checkStructLitBody validates the field-init list of a struct literal
// against a resolved struct *Type. Split out so the cross-module path
// (`mod.MyStruct{...}`) can share the validation.
func (c *checker) checkStructLitBody(e *StructLit, st *Type) (*Type, error) {
	declared := map[string]*Type{}
	for _, f := range st.Fields {
		declared[f.Name] = f.Type
	}
	provided := map[string]bool{}
	for i := range e.Fields {
		init := &e.Fields[i]
		ft, ok := declared[init.Name]
		if !ok {
			return nil, typeErr(init.Pos, "struct %q has no field %q", st.Name, init.Name)
		}
		if provided[init.Name] {
			return nil, typeErr(init.Pos, "field %q already initialised in struct literal", init.Name)
		}
		provided[init.Name] = true
		newExpr, vt, err := c.checkExprLift(init.Value, ft)
		if err != nil {
			return nil, err
		}
		if newExpr != init.Value {
			init.Value = newExpr
		}
		if !c.assignableTo(vt, ft) {
			return nil, typeErr(init.Pos, "field %q expects %s, got %s", init.Name, ft, vt)
		}
	}
	for _, f := range st.Fields {
		if !provided[f.Name] {
			return nil, typeErr(e.Pos, "struct %q literal is missing field %q", st.Name, f.Name)
		}
	}
	e.setType(st)
	return st, nil
}

// genericStructInstanceArgs recovers the type-arg vector from a canonical
// generic struct instance against the decl's type-param ordering. Walks the
// decl's field TypeRefs to find positions where a type-param appears, then
// reads the substituted *Type from the matching position in the instance.
//
// Returns (args, ok). ok is false when t is not a struct named declName[...]
// or when the decl's fields don't reference every type-param at least once
// (unlikely for v0.6 corpus shapes; the caller falls back to a precise
// "cannot infer" diagnostic).
func genericStructInstanceArgs(t *Type, declName string, decl *StructDecl) ([]*Type, bool) {
	if t == nil || t.Kind != TypeStruct {
		return nil, false
	}
	prefix := declName + "["
	if !strings.HasPrefix(t.Name, prefix) || !strings.HasSuffix(t.Name, "]") {
		return nil, false
	}
	if len(t.Fields) != len(decl.Fields) {
		return nil, false
	}
	out := make([]*Type, len(decl.TypeParams))
	tpIdx := map[string]int{}
	for i, tp := range decl.TypeParams {
		tpIdx[tp.Name] = i
	}
	var walk func(*TypeRef, *Type)
	walk = func(ref *TypeRef, conc *Type) {
		if ref == nil || conc == nil {
			return
		}
		switch ref.Kind {
		case TypeRefNamed:
			if ref.Module == "" && len(ref.TypeArgs) == 0 && !ref.Nullable {
				if i, ok := tpIdx[ref.Name]; ok && out[i] == nil {
					out[i] = conc
				}
			}
		case TypeRefList:
			if conc.Kind == TypeList {
				walk(ref.Element, conc.Element)
			}
		case TypeRefTuple:
			if conc.Kind == TypeTuple && len(ref.Elements) == len(conc.Tuple) {
				for i, sub := range ref.Elements {
					walk(sub, conc.Tuple[i])
				}
			}
		}
	}
	for i, f := range decl.Fields {
		walk(f.Type, t.Fields[i].Type)
	}
	for _, v := range out {
		if v == nil {
			return nil, false
		}
	}
	return out, true
}

// checkIndex handles `xs[i]`. Receiver must be list[T] (result T) or str
// (result rune); index must be int.
func (c *checker) checkIndex(e *IndexExpr) (*Type, error) {
	rt, err := c.checkExpr(e.Receiver)
	if err != nil {
		return nil, err
	}
	it, err := c.checkExpr(e.Index)
	if err != nil {
		return nil, err
	}
	if it != tInt {
		return nil, typeErr(e.Index.ExprPos(), "index must be int, got %s", it)
	}
	switch {
	case rt != nil && rt.Kind == TypeList:
		// Note: negative indexing is OUT at v0.2 — runtime is expected to
		// reject `i < 0`. typeck does not constant-fold the index value.
		e.setType(rt.Element)
		return rt.Element, nil
	case rt == tStr:
		e.setType(tRune)
		return tRune, nil
	}
	return nil, typeErr(e.Pos, "cannot index value of type %s", rt)
}

// checkSlice handles `xs[a..b]` etc. Receiver must be list[T] (result list[T]).
// String slicing is deferred to v0.3 and rejected here per PLAN.
func (c *checker) checkSlice(e *SliceExpr) (*Type, error) {
	rt, err := c.checkExpr(e.Receiver)
	if err != nil {
		return nil, err
	}
	check := func(b Expr, label string) error {
		if b == nil {
			return nil
		}
		t, err := c.checkExpr(b)
		if err != nil {
			return err
		}
		if t != tInt {
			return typeErr(b.ExprPos(), "slice %s must be int, got %s", label, t)
		}
		return nil
	}
	if err := check(e.Low, "low"); err != nil {
		return nil, err
	}
	if err := check(e.High, "high"); err != nil {
		return nil, err
	}
	if rt == tStr {
		return nil, typeErr(e.Pos, "string slicing is deferred to v0.3")
	}
	if rt == nil || rt.Kind != TypeList {
		return nil, typeErr(e.Pos, "cannot slice value of type %s", rt)
	}
	e.setType(rt)
	return rt, nil
}

// checkFieldAccess handles `expr.name`. PLAN-pinned dual role: when the
// receiver is a bare ident naming a known enum, the FieldAccessExpr resolves
// as a variant access; otherwise the receiver must be a struct value and the
// name must be one of its declared fields. Receiver-as-enum is recorded by
// setting the receiver IdentExpr's type to the enum type itself, which mirrors
// how `Color.Red` evaluates at run time.
//
// v0.4: when the variant has a non-empty payload declared, a bare-name access
// without parens is rejected — the user must construct via `Variant(...)`.
// The lowered EnumLit pointer is attached so downstream consumers can route
// the construction through the same Visitor path as MethodCallExpr-style
// payloadful construction.
//
// v0.5: when the receiver is a bare ident that matches an imported module
// binding, the FieldAccessExpr resolves through the foreign module's decl
// tables. Three sub-cases:
//
//   - `mod.Color` standalone (e.g. as a value? rare — typeck rejects an enum
//     type used as a value just like the v0.4 single-program case).
//   - `mod.Color.Red` — the OUTER FieldAccessExpr's receiver is itself a
//     FieldAccessExpr `mod.Color` whose receiver is an IdentExpr `mod`.
//     We detect this shape and lower to a cross-module enum access.
//   - `mod.fn_name` — only meaningful inside a CallExpr callee; bare access
//     rejects with "not a value".
func (c *checker) checkFieldAccess(e *FieldAccessExpr) (*Type, error) {
	return c.checkFieldAccessHint(e, nil)
}

// checkFieldAccessHint is the v0.6 hint-aware variant. The hint is consumed
// when the access shape is `Option.None` / `Result.Ok` / etc. — a generic
// enum bare-variant access where the type-args must come from context.
func (c *checker) checkFieldAccessHint(e *FieldAccessExpr, hint *Type) (*Type, error) {
	// v0.6 Unit 4: `obj?.field` routes through the safe-navigation path. The
	// receiver must be Option[T]; the result is Option[fieldType]. Chains
	// compose because each ?. returns Option[...] which the next ?. consumes.
	if e.Safe {
		return c.checkSafeFieldAccess(e)
	}
	// v0.5: receiver `mod.X` (FieldAccessExpr whose own receiver is the
	// module-binding IdentExpr) where X is a foreign enum type. The
	// outer FieldName is the variant.
	if outerFA, ok := e.Receiver.(*FieldAccessExpr); ok {
		if modIdent, isModIdent := outerFA.Receiver.(*IdentExpr); isModIdent {
			if foreignMod := c.lookupImportedModule(modIdent.Name); foreignMod != nil {
				en, found, err := c.resolveCrossModuleEnum(modIdent.Name, outerFA.FieldName)
				if found {
					if err != nil {
						return nil, typeErr(outerFA.NamePos,
							"cannot access '%s.%s': %q is not pub in module %q",
							modIdent.Name, outerFA.FieldName, outerFA.FieldName, modIdent.Name)
					}
					// Found a foreign enum — treat the outer access as a
					// bare-variant enum lit.
					for i, v := range en.Variants {
						if v == e.FieldName {
							if len(en.VariantPayloads[i]) > 0 {
								return nil, typeErr(e.NamePos,
									"variant %q.%q has %d payload value(s) — use %s.%s.%s(...) to construct",
									en.Name, v, len(en.VariantPayloads[i]), modIdent.Name, en.Name, v)
							}
							modIdent.setType(en)
							outerFA.setType(en)
							e.setType(en)
							e.Lowered = &EnumLit{
								Pos:        e.Pos,
								EnumName:   en.Name,
								Module:     modIdent.Name,
								Variant:    v,
								VariantPos: e.NamePos,
							}
							e.Lowered.setType(en)
							return en, nil
						}
					}
					return nil, typeErr(e.NamePos, "enum %q has no variant %q", en.Name, e.FieldName)
				}
				// modIdent is a module but outerFA.FieldName is not a
				// foreign enum. Could be a foreign fn / struct (rare in
				// this position) — fall through: the receiver eval below
				// will produce a precise diagnostic.
				_ = foreignMod
			}
		}
	}
	if id, ok := e.Receiver.(*IdentExpr); ok {
		// v0.5: receiver is an imported module binding. Without a
		// trailing call, accessing `mod.fn_name` or `mod.Type` bare is
		// rejected — these aren't values. The grammar does admit such a
		// shape syntactically (it's a FieldAccessExpr); a precise error
		// here beats a confusing "field 'fn_name' not found" later.
		if foreignMod := c.lookupImportedModule(id.Name); foreignMod != nil {
			fc := c.crossMod.checkers[foreignMod]
			if fc != nil {
				if _, ok := fc.fns[e.FieldName]; ok {
					return nil, typeErr(e.NamePos,
						"cannot use function '%s.%s' as a value at v0.5 — call it with '()'",
						id.Name, e.FieldName)
				}
				if _, ok := fc.enums[e.FieldName]; ok {
					return nil, typeErr(e.NamePos,
						"cannot use enum type '%s.%s' as a value — construct a variant",
						id.Name, e.FieldName)
				}
				if _, ok := fc.structs[e.FieldName]; ok {
					return nil, typeErr(e.NamePos,
						"cannot use struct type '%s.%s' as a value — construct an instance",
						id.Name, e.FieldName)
				}
				if _, ok := fc.specs[e.FieldName]; ok {
					return nil, typeErr(e.NamePos,
						"cannot use spec type '%s.%s' as a value",
						id.Name, e.FieldName)
				}
			}
			return nil, typeErr(e.NamePos, "module %q has no member %q", id.Name, e.FieldName)
		}
		if en, isEnum := c.enums[id.Name]; isEnum {
			for i, v := range en.Variants {
				if v == e.FieldName {
					if len(en.VariantPayloads[i]) > 0 {
						return nil, typeErr(e.NamePos,
							"variant %q.%q has %d payload value(s) — use %s.%s(...) to construct", en.Name, v, len(en.VariantPayloads[i]), en.Name, v)
					}
					id.setType(en)
					e.setType(en)
					e.Lowered = &EnumLit{
						Pos:        e.Pos,
						EnumName:   en.Name,
						Variant:    v,
						VariantPos: e.NamePos,
					}
					e.Lowered.setType(en)
					return en, nil
				}
			}
			return nil, typeErr(e.NamePos, "enum %q has no variant %q", id.Name, e.FieldName)
		}
		// v0.6 Unit 3: receiver is a generic enum decl name (`Option`,
		// `Result`, user-defined). The variant is bare (no payload); the
		// type-args must come from the surrounding hint.
		if d := c.findGenericEnumDecl(id.Name); d != nil {
			t, ok, err := c.checkGenericEnumBareLit(e, id.Name, e.FieldName, hint)
			if ok {
				return t, err
			}
			if err != nil {
				return nil, err
			}
		}
	}
	rt, err := c.checkExpr(e.Receiver)
	if err != nil {
		return nil, err
	}
	if rt == nil || rt.Kind != TypeStruct {
		return nil, typeErr(e.Pos, "cannot access field on value of type %s", rt)
	}
	for _, f := range rt.Fields {
		if f.Name == e.FieldName {
			e.setType(f.Type)
			return f.Type, nil
		}
	}
	return nil, typeErr(e.NamePos, "struct %q has no field %q", rt.Name, e.FieldName)
}

// checkMethodCall handles `receiver.method(args)`. Three resolution paths:
//
//   1. The receiver is a bare ident naming a known enum and `method` is one
//      of its variants → lower to an EnumLit construction. The variant's
//      declared payload-type list drives arg type-checking.
//   2. The receiver has a spec type → look up the method in the spec's
//      method index and bind to the spec's signature.
//   3. The receiver has a struct or enum type → look up the method in that
//      type's method-visibility map. Inherent and single-spec sources resolve
//      cleanly; multi-spec collisions reject with the "bind to a spec" hint.
//
// Receiver expressions are walked once (paths 2 and 3 type-check the receiver
// before lookup).
func (c *checker) checkMethodCall(e *MethodCallExpr) (*Type, error) {
	return c.checkMethodCallHint(e, nil)
}

// checkMethodCallHint is the v0.6 hint-aware variant: the optional hint
// supplies missing type-args at a generic-enum lit construction site
// (`Option.Some(7)`, `Result.Err("oops")`). Non-generic dispatch ignores
// the hint.
func (c *checker) checkMethodCallHint(e *MethodCallExpr, hint *Type) (*Type, error) {
	// v0.5 path 0a: cross-module function call shape — the receiver is
	// an IdentExpr naming an imported module binding, and Method is a
	// pub function defined in that module.
	if id, ok := e.Receiver.(*IdentExpr); ok {
		if foreignMod := c.lookupImportedModule(id.Name); foreignMod != nil {
			fc := c.crossMod.checkers[foreignMod]
			if fc != nil {
				// v0.6 Unit 3: foreign module's generic fn. Pub-gated by
				// the FnDecl's Pub bit. The shape is the same as the
				// in-module generic call after we lower the
				// MethodCallExpr to a CallExpr.
				if gfn, ok := fc.genericFnAST[e.Method]; ok {
					if !gfn.Pub {
						return nil, typeErr(e.MethodPos,
							"cannot access '%s.%s': %q is not pub in module %q",
							id.Name, e.Method, e.Method, id.Name)
					}
					return c.checkCrossModuleGenericFnCall(e, gfn, hint)
				}
				if sig, found := fc.fns[e.Method]; found && !sig.builtin {
					if !fnIsPub(fc, e.Method) {
						return nil, typeErr(e.MethodPos,
							"cannot access '%s.%s': %q is not pub in module %q",
							id.Name, e.Method, e.Method, id.Name)
					}
					return c.checkCrossModuleFnCall(e, sig)
				}
				// Module has the binding but no fn by that name.
				// If it's an enum/struct/spec, check for variant
				// construction.
				if en, ok := fc.enums[e.Method]; ok {
					_ = en
					// `mod.EnumName(...)` — enum types aren't called like
					// functions. Reject with a clear message.
					return nil, typeErr(e.MethodPos,
						"cannot call enum type '%s.%s' — use a variant", id.Name, e.Method)
				}
				if _, ok := fc.structs[e.Method]; ok {
					return nil, typeErr(e.MethodPos,
						"cannot call struct type '%s.%s' — use struct literal syntax", id.Name, e.Method)
				}
				if _, ok := fc.specs[e.Method]; ok {
					return nil, typeErr(e.MethodPos,
						"cannot call spec type '%s.%s'", id.Name, e.Method)
				}
				return nil, typeErr(e.MethodPos,
					"module %q has no function %q", id.Name, e.Method)
			}
		}
	}
	// v0.5 path 0b: cross-module enum payload-variant construction —
	// receiver is `mod.EnumName` (FieldAccessExpr whose receiver is a
	// module-binding IdentExpr); Method is the variant.
	if outerFA, ok := e.Receiver.(*FieldAccessExpr); ok {
		if modIdent, isModIdent := outerFA.Receiver.(*IdentExpr); isModIdent {
			if foreignMod := c.lookupImportedModule(modIdent.Name); foreignMod != nil {
				en, found, err := c.resolveCrossModuleEnum(modIdent.Name, outerFA.FieldName)
				if found {
					if err != nil {
						return nil, typeErr(outerFA.NamePos,
							"cannot access '%s.%s': %q is not pub in module %q",
							modIdent.Name, outerFA.FieldName, outerFA.FieldName, modIdent.Name)
					}
					for i, v := range en.Variants {
						if v == e.Method {
							modIdent.setType(en)
							outerFA.setType(en)
							t, err := c.lowerCrossModuleEnumLitFromMethodCall(e, en, i, modIdent.Name)
							return t, err
						}
					}
					return nil, typeErr(e.MethodPos,
						"enum %q has no variant %q", en.Name, e.Method)
				}
				_ = foreignMod
			}
		}
	}
	// Path 1: enum-lit construction shape.
	if id, ok := e.Receiver.(*IdentExpr); ok {
		if en, isEnum := c.enums[id.Name]; isEnum {
			for i, v := range en.Variants {
				if v == e.Method {
					return c.lowerEnumLitFromMethodCall(e, en, i)
				}
			}
			// Receiver is an enum ident but `method` is not a variant — could
			// be a method on the enum type. Fall through to method-table
			// lookup with receiver type bound to the enum.
			id.setType(en)
			return c.dispatchConcreteMethod(e, en)
		}
		// v0.6 Unit 3: receiver names a generic enum decl (`Option.Some(7)`,
		// `Result.Err("oops")`, user-defined `Pair.Both(1, 2)`).
		if d := c.findGenericEnumDecl(id.Name); d != nil {
			t, ok, err := c.checkGenericEnumLit(e, id.Name, e.Method, hint)
			if ok {
				return t, err
			}
			if err != nil {
				return nil, err
			}
		}
	}
	// Paths 2 & 3 both need the receiver's type.
	rt, err := c.checkExpr(e.Receiver)
	if err != nil {
		return nil, err
	}
	if rt == nil {
		return nil, typeErr(e.Pos, "cannot call method on untyped value")
	}
	if rt.Kind == TypeSpec {
		return c.dispatchSpecMethod(e, rt)
	}
	// Path 4: list[T] receiver — `xs.push(v)`, `xs.clone()`, `xs.len()` desugar
	// to the v0.3 fn-call builtins. The rewrite hands a synthetic CallExpr to
	// checkCall (which already special-cases push / clone / len) and stashes
	// the result on e.LoweredCall so run / cgen can short-circuit to the
	// builtin path. Borrow-check rules for push / clone / len fire on the
	// synthetic CallExpr the same way they do on a hand-written one.
	//
	// v0.14 adds list[byte].to_str(): same desugaring path; the typeck for
	// the synthetic `to_str(xs)` call accepts only list[byte] receivers
	// (the registry's tByte placeholder makes assignableTo reject
	// list[int] / list[rune] / list[str]).
	if rt.Kind == TypeList {
		switch e.Method {
		case "push", "clone", "len", "to_str":
			return c.lowerListBuiltinFromMethodCall(e)
		}
	}
	// Path 5: str receiver (v0.14) — `s.len()` and `s.bytes()` desugar to
	// the same synthetic-call shape as the list methods. `s.bytes()`
	// allocates a fresh list[byte] copy of s's bytes; the cgen and run
	// implementations both copy (str is immutable in the language and
	// callers may mutate the returned list).
	if rt.Kind == TypeStr {
		switch e.Method {
		case "len", "bytes":
			return c.lowerListBuiltinFromMethodCall(e)
		}
	}
	if rt.Kind != TypeStruct && rt.Kind != TypeEnum {
		return nil, typeErr(e.MethodPos, "method %q does not exist on %s", e.Method, rt)
	}
	return c.dispatchConcreteMethod(e, rt)
}

// lowerListBuiltinFromMethodCall rewrites a method-form call (list:
// `xs.push(v)` / `xs.clone()` / `xs.len()` / `xs.to_str()`; str:
// `s.len()` / `s.bytes()`) to a synthetic CallExpr and runs it through
// checkCall, then stashes the call on e.LoweredCall so downstream
// consumers see the builtin shape. Diagnostics fall out of checkCall
// with the synthetic CallExpr's position pinned to the method-call
// site. Despite the historical "List" in the name, the helper is
// receiver-type-agnostic — it just inverts the method-call shape into
// a free-call shape for the builtin dispatch to consume.
func (c *checker) lowerListBuiltinFromMethodCall(e *MethodCallExpr) (*Type, error) {
	callee := &IdentExpr{Pos: e.MethodPos, Name: e.Method}
	args := make([]Expr, 0, len(e.Args)+1)
	args = append(args, e.Receiver)
	args = append(args, e.Args...)
	call := &CallExpr{
		Pos:    e.Pos,
		Callee: callee,
		Args:   args,
	}
	rt, err := c.checkCall(call)
	if err != nil {
		return nil, err
	}
	e.LoweredCall = call
	e.setType(rt)
	return rt, nil
}

// lowerEnumLitFromMethodCall validates payload arity / types and stamps the
// MethodCallExpr with an EnumLit lowering, then sets the call's type to the
// owning enum.
func (c *checker) lowerEnumLitFromMethodCall(e *MethodCallExpr, en *Type, variantIdx int) (*Type, error) {
	variantName := en.Variants[variantIdx]
	payload := en.VariantPayloads[variantIdx]
	if len(payload) == 0 {
		return nil, typeErr(e.MethodPos, "variant %q.%q has no payload — drop the parentheses to construct", en.Name, variantName)
	}
	if len(e.Args) != len(payload) {
		return nil, typeErr(e.Pos, "variant %q.%q expects %d payload value(s), got %d", en.Name, variantName, len(payload), len(e.Args))
	}
	args := make([]Expr, len(e.Args))
	for i, a := range e.Args {
		at, err := c.checkExprHint(a, payload[i])
		if err != nil {
			return nil, err
		}
		if !typeEq(at, payload[i]) {
			return nil, typeErr(a.ExprPos(), "variant %q.%q payload position %d expects %s, got %s", en.Name, variantName, i+1, payload[i], at)
		}
		args[i] = a
	}
	if id, ok := e.Receiver.(*IdentExpr); ok {
		id.setType(en)
	}
	lowered := &EnumLit{
		Pos:        e.Pos,
		EnumName:   en.Name,
		Variant:    variantName,
		VariantPos: e.MethodPos,
		Payload:    args,
	}
	lowered.setType(en)
	e.Lowered = lowered
	e.setType(en)
	return en, nil
}

// dispatchSpecMethod resolves a method call against a spec-typed receiver.
// Method lookup is by name in the spec's method index; the call's effective
// signature is the spec method's declared signature.
func (c *checker) dispatchSpecMethod(e *MethodCallExpr, specType *Type) (*Type, error) {
	spec := c.specs[specType.Name]
	if spec == nil {
		// v0.5: the spec may live in a foreign module. Walk the bundle's
		// checkers to locate it.
		if c.crossMod != nil {
			for _, fc := range c.crossMod.checkers {
				if s, ok := fc.specs[specType.Name]; ok {
					spec = s
					break
				}
			}
		}
	}
	if spec == nil {
		return nil, typeErr(e.Pos, "internal: spec %q not in spec table", specType.Name)
	}
	sm, ok := spec.methodIdx[e.Method]
	if !ok {
		return nil, typeErr(e.MethodPos, "method %q does not exist on spec %q", e.Method, spec.Name)
	}
	if err := c.checkMethodArgs(e, sm.params); err != nil {
		return nil, err
	}
	e.setType(sm.ret)
	return sm.ret, nil
}

// dispatchConcreteMethod resolves a method call against a struct- or enum-
// typed receiver via the method-visibility map. Inherent and single-spec
// sources resolve cleanly; multi-spec collisions reject with the "bind 'c' to
// a spec type to disambiguate" hint.
//
// v0.5: method dispatch must also search OTHER modules' impls because:
//   - The receiver's type may be defined in module A; module B can have an
//     `impl A.Type for Spec` that adds methods. Method lookup walks the
//     visibility maps of every module that has registered an impl on the
//     receiver type.
//   - Cross-module methods are pub-gated: a foreign module's inherent or
//     spec-impl method is reachable only when its FnDecl carries `pub`.
func (c *checker) dispatchConcreteMethod(e *MethodCallExpr, recv *Type) (*Type, error) {
	srcs := c.collectAllVisibleMethods(recv, e.Method)
	if len(srcs) == 0 {
		return nil, typeErr(e.MethodPos, "method %q does not exist on %s", e.Method, recv)
	}
	// At most one inherent source per type (collision rule fires earlier).
	// Filter to find the binding source: inherent is unique; spec sources
	// resolve only when there's exactly one.
	if len(srcs) == 1 {
		return c.bindResolvedMethodCall(e, srcs[0])
	}
	// Multiple sources — must all be spec; otherwise visibility pass would
	// have rejected. Multiple distinct specs implementing a method with the
	// same name → ambiguous when called via concrete receiver.
	var specNames []string
	for _, s := range srcs {
		if s.kind == mskSpec {
			specNames = append(specNames, s.specName)
		}
	}
	return nil, typeErr(e.MethodPos,
		"method %q on %s matches multiple specs (%s) — bind '%s' to a spec type to disambiguate",
		e.Method, recv, strings.Join(specNames, ", "), exprDisplay(e.Receiver))
}

// bindResolvedMethodCall completes argument-checking against the chosen
// method source's signature.
func (c *checker) bindResolvedMethodCall(e *MethodCallExpr, src *methodSource) (*Type, error) {
	var params []*Type
	var ret *Type
	switch src.kind {
	case mskInherent:
		params = src.inherent.params
		ret = src.inherent.ret
	case mskSpec:
		if src.implFn != nil {
			params = src.implFn.params
			ret = src.implFn.ret
		} else if src.defaultM != nil {
			params = src.defaultM.params
			ret = src.defaultM.ret
		} else {
			// Inherited but no body — runtime NotImplemented. The spec method
			// signature is still the one we type-check against. v0.5: if
			// the spec lives in a foreign module, fall back to the
			// owning module's spec table.
			spec := c.lookupSpecForImpl(src.impl)
			if spec == nil {
				return nil, typeErr(e.MethodPos, "internal: spec %q for method %q not found", src.specName, src.name)
			}
			sm := spec.methodIdx[src.name]
			params = sm.params
			ret = sm.ret
		}
	}
	if err := c.checkMethodArgs(e, params); err != nil {
		return nil, err
	}
	e.setType(ret)
	return ret, nil
}

// checkMethodArgs enforces arity and per-position type-equality between e.Args
// and the resolved method's declared parameter list (excluding implicit
// `this`). Args are read positions — borrow check (Unit 5) owns the move/borrow
// rules.
func (c *checker) checkMethodArgs(e *MethodCallExpr, params []*Type) error {
	if len(e.Args) != len(params) {
		return typeErr(e.Pos, "method %q expects %d argument(s), got %d", e.Method, len(params), len(e.Args))
	}
	for i, a := range e.Args {
		at, err := c.checkExprHint(a, params[i])
		if err != nil {
			return err
		}
		if !typeEq(at, params[i]) {
			return typeErr(a.ExprPos(), "argument %d to %q has type %s, expected %s", i+1, e.Method, at, params[i])
		}
	}
	return nil
}

// checkEnumLit validates an EnumLit AST node directly. The parser does not
// produce EnumLit nodes today (typeck lowers them from Method/FieldAccess),
// but the entry point exists so that future producers can hand a pre-built
// EnumLit straight to typeck.
func (c *checker) checkEnumLit(e *EnumLit) (*Type, error) {
	en, ok := c.enums[e.EnumName]
	if !ok {
		return nil, typeErr(e.Pos, "unknown enum %q", e.EnumName)
	}
	idx := -1
	for i, v := range en.Variants {
		if v == e.Variant {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, typeErr(e.VariantPos, "enum %q has no variant %q", en.Name, e.Variant)
	}
	payload := en.VariantPayloads[idx]
	if len(e.Payload) != len(payload) {
		return nil, typeErr(e.Pos, "variant %q.%q expects %d payload value(s), got %d", en.Name, e.Variant, len(payload), len(e.Payload))
	}
	for i, a := range e.Payload {
		at, err := c.checkExprHint(a, payload[i])
		if err != nil {
			return nil, err
		}
		if !typeEq(at, payload[i]) {
			return nil, typeErr(a.ExprPos(), "variant %q.%q payload position %d expects %s, got %s", en.Name, e.Variant, i+1, payload[i], at)
		}
	}
	e.setType(en)
	return en, nil
}

// exprDisplay renders an expression in source-text-ish form for diagnostics.
// Only enough fidelity for the "bind 'c' to a spec type to disambiguate"
// message — anything more elaborate stays in a future formatter.
func exprDisplay(e Expr) string {
	if id, ok := e.(*IdentExpr); ok {
		return id.Name
	}
	return "<expr>"
}

// checkBinary validates per-operator typing rules.
func (c *checker) checkBinary(e *BinaryExpr) (*Type, error) {
	lt, err := c.checkExpr(e.Left)
	if err != nil {
		return nil, err
	}
	rt, err := c.checkExpr(e.Right)
	if err != nil {
		return nil, err
	}
	var result *Type
	switch e.Op {
	case BinAdd:
		if lt == tStr && rt == tStr {
			result = tStr
		} else if isNumeric(lt) && lt == rt {
			result = lt
		} else {
			return nil, typeErr(e.Pos, "operator + requires numeric or str operands of the same type, got %s and %s", lt, rt)
		}
	case BinSub, BinMul, BinDiv, BinFloorDiv, BinMod:
		if !isNumeric(lt) || lt != rt {
			return nil, typeErr(e.Pos, "operator %s requires numeric operands of the same type, got %s and %s", e.Op, lt, rt)
		}
		result = lt
	case BinBitOr, BinBitXor, BinBitAnd, BinShl, BinShr:
		if lt != tInt || rt != tInt {
			return nil, typeErr(e.Pos, "operator %s requires int operands, got %s and %s", e.Op, lt, rt)
		}
		result = tInt
	case BinEq, BinNE:
		if reason, ok := isComparableForEq(lt); !ok {
			return nil, typeErr(e.Pos, "%s", reason)
		}
		if reason, ok := isComparableForEq(rt); !ok {
			return nil, typeErr(e.Pos, "%s", reason)
		}
		if !typeEq(lt, rt) {
			return nil, typeErr(e.Pos, "operator %s requires operands of the same type, got %s and %s", e.Op, lt, rt)
		}
		result = tBool
	case BinLT, BinGT, BinLE, BinGE:
		if lt != rt || !(isNumeric(lt) || lt == tStr || lt == tByte || lt == tRune) {
			return nil, typeErr(e.Pos, "operator %s requires same-typed numeric or str operands, got %s and %s", e.Op, lt, rt)
		}
		result = tBool
	case BinAnd, BinOr, BinXor:
		if lt != tBool || rt != tBool {
			return nil, typeErr(e.Pos, "operator %s requires bool operands, got %s and %s", e.Op, lt, rt)
		}
		result = tBool
	default:
		return nil, typeErr(e.Pos, "internal: unknown binary operator %s", e.Op)
	}
	e.setType(result)
	return result, nil
}

func (c *checker) checkUnary(e *UnaryExpr) (*Type, error) {
	t, err := c.checkExpr(e.Operand)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case UnaryNeg:
		if !isNumeric(t) {
			return nil, typeErr(e.Pos, "unary - requires a numeric operand, got %s", t)
		}
	case UnaryBitNot:
		if t != tInt {
			return nil, typeErr(e.Pos, "unary ~ requires an int operand, got %s", t)
		}
	case UnaryNot:
		if t != tBool {
			return nil, typeErr(e.Pos, "unary not requires a bool operand, got %s", t)
		}
	default:
		return nil, typeErr(e.Pos, "internal: unknown unary operator %s", e.Op)
	}
	e.setType(t)
	return t, nil
}

// checkCall enforces v0.1 callee shape: bare identifier resolving to a
// declared function. The built-in `len` is special-cased: it accepts any
// list[T] argument and returns int.
func (c *checker) checkCall(e *CallExpr) (*Type, error) {
	return c.checkCallHint(e, nil)
}

// checkCallHint is the v0.6 hint-aware variant: the optional hint feeds
// the bidirectional unifier at a generic-fn call site so e.g.
// `r: Result[int, str] = make_err()` infers the type-args from the
// surrounding annotation. Non-generic calls ignore the hint.
func (c *checker) checkCallHint(e *CallExpr, hint *Type) (*Type, error) {
	// v0.7 Unit 3: anon-fn IIFE — `fn() { ... }()` calls the anon-fn
	// directly. Type-check the AnonFnExpr to obtain a TypeFn, then dispatch
	// the call through the fn-value path.
	if anon, ok := e.Callee.(*AnonFnExpr); ok {
		ft, err := c.checkAnonFnExpr(anon)
		if err != nil {
			return nil, err
		}
		return c.checkFnValueCall(e, ft, "anonymous function")
	}
	ident, ok := e.Callee.(*IdentExpr)
	if !ok {
		return nil, typeErr(e.Pos, "callee must be a function name")
	}
	// v0.7: `close(ch)` is a type-driven built-in; it has no fnSig entry. The
	// reservation diagnostic at name-binding sites blocks user shadowing so
	// any call whose callee names "close" is the genuine built-in.
	if recognizeCloseCall(e) {
		return c.checkCloseCall(e, ident)
	}
	// v0.7 Unit 3: `wait_group()` is a built-in fn. The reservation at the
	// binding site prevents user shadowing.
	if recognizeWaitGroupCall(e) {
		return c.checkWaitGroupCall(e, ident)
	}
	// v0.7 Unit 3: a local binding may carry a TypeFn (an anon-fn captured
	// in a let). Calling such a binding routes through the fn-value path.
	// Check the scope FIRST so a local fn-typed binding shadows any
	// same-name top-level fn — same shadowing semantics as let-of-fn vs.
	// builtin push.
	if b, _, isVar := c.scope.lookupWithScope(ident.Name); isVar {
		if b.typ != nil && b.typ.Kind == TypeFn {
			ident.setType(b.typ)
			// The IDENT lookup already happened above without going
			// through checkExpr's IDENT path; record capture analysis
			// manually so an anon-fn body that calls a captured fn-value
			// captures the binding.
			if len(c.anonFrames) > 0 {
				_, defScope, _ := c.scope.lookupWithScope(ident.Name)
				if err := c.noteIdentResolved(ident.Name, ident.Pos, b, defScope); err != nil {
					return nil, err
				}
			}
			return c.checkFnValueCall(e, b.typ, ident.Name)
		}
	}
	// v0.6 Unit 3: generic-fn dispatch.
	if fn := c.findGenericFnDecl(ident.Name); fn != nil {
		return c.checkGenericFnCall(e, fn, ident, hint)
	}
	sig, ok := c.fns[ident.Name]
	if !ok {
		if _, isVar := c.scope.lookup(ident.Name); isVar {
			return nil, typeErr(ident.Pos, "%q is not a function", ident.Name)
		}
		return nil, typeErr(ident.Pos, "undefined function %q", ident.Name)
	}
	if sig.builtin && ident.Name == "len" {
		// `len(xs)` accepts exactly one list or str argument and returns
		// int. For lists, returns the element count. For strs (v0.14),
		// returns the byte count — matches list[byte].len() semantics
		// and is what stdlib byte-oriented ops want; the v0.2 rune-count
		// reading was dead code (typeck rejected str) and is retired here.
		if len(e.Args) != 1 {
			return nil, typeErr(e.Pos, "function %q expects 1 argument, got %d", ident.Name, len(e.Args))
		}
		at, err := c.checkExpr(e.Args[0])
		if err != nil {
			return nil, err
		}
		if at == nil || (at.Kind != TypeList && at.Kind != TypeStr) {
			return nil, typeErr(e.Args[0].ExprPos(), "argument to len must be a list or str, got %s", at)
		}
		ident.setType(tInt)
		e.setType(tInt)
		return tInt, nil
	}
	if sig.builtin && ident.Name == "push" {
		return c.checkPushCall(e, ident)
	}
	if sig.builtin && ident.Name == "clone" {
		return c.checkCloneCall(e, ident)
	}
	if len(e.Args) != len(sig.params) {
		return nil, typeErr(e.Pos, "function %q expects %d argument(s), got %d", ident.Name, len(sig.params), len(e.Args))
	}
	for i := range e.Args {
		newExpr, at, err := c.checkExprLift(e.Args[i], sig.params[i])
		if err != nil {
			return nil, err
		}
		if newExpr != e.Args[i] {
			e.Args[i] = newExpr
		}
		if !c.assignableTo(at, sig.params[i]) {
			return nil, typeErr(e.Args[i].ExprPos(), "argument %d to %q has type %s, expected %s", i+1, ident.Name, at, sig.params[i])
		}
	}
	ident.setType(sig.ret)
	e.setType(sig.ret)
	return sig.ret, nil
}

// checkPushCall implements typeck for the v0.3 `push(xs, v)` built-in. Arity
// is fixed at 2; the first arg must be a top-level mut-bound list variable
// (so the borrow checker at Unit 3 has a single root binding to mark dirty);
// the second arg's type must equal the list's element type. Return is void.
//
// PLAN-pinned: `push` mutates `xs` in place — list mutation through fn
// parameters or nested struct/list shapes is OUT of scope at v0.3. The
// "must be a mut-bound list variable" check enforces both: pass-by-fn rejects
// because fn params are bindLet (and inner-block push := ... is fine
// because that name shadows the builtin only at expression resolution).
func (c *checker) checkPushCall(e *CallExpr, ident *IdentExpr) (*Type, error) {
	if len(e.Args) != 2 {
		return nil, typeErr(e.Pos, "function %q expects 2 argument(s), got %d", ident.Name, len(e.Args))
	}
	// First arg: must be a bare ident naming a mut-bound list. Reject every
	// other shape with the same diagnostic so callers don't have to guess
	// which sub-rule fired.
	id, ok := e.Args[0].(*IdentExpr)
	if !ok {
		return nil, typeErr(e.Args[0].ExprPos(), "push: first argument must be a mut-bound list variable")
	}
	b, ok := c.scope.lookup(id.Name)
	if !ok {
		return nil, typeErr(id.Pos, "undefined name %q", id.Name)
	}
	if b.typ == nil || b.typ.Kind != TypeList {
		return nil, typeErr(id.Pos, "push: first argument must be a mut-bound list variable")
	}
	if b.kind != bindMut {
		return nil, typeErr(id.Pos, "push: %q must be mut to be modified — declare it with `mut %s := ...`", id.Name, id.Name)
	}
	id.setType(b.typ)
	// Second arg: must equal the list element type.
	vt, err := c.checkExprHint(e.Args[1], b.typ.Element)
	if err != nil {
		return nil, err
	}
	if !typeEq(vt, b.typ.Element) {
		return nil, typeErr(e.Args[1].ExprPos(), "push: value has type %s, list element type is %s", vt, b.typ.Element)
	}
	ident.setType(tVoid)
	e.setType(tVoid)
	return tVoid, nil
}

// checkCloneCall implements typeck for the v0.3 `clone(xs)` built-in. Arity
// is fixed at 1; the argument must be a composite type (list, tuple, struct,
// enum) — primitives are rejected because they're already value-copied at
// every bind. Return type is the same as the argument.
//
// PLAN-pinned: clone is an OBSERVATION of its argument, not a consumption —
// the borrow checker at Unit 3 will model this as a shared borrow so the
// caller retains ownership of `xs` after the call. typeck doesn't enforce
// that here; it only types the call shape.
func (c *checker) checkCloneCall(e *CallExpr, ident *IdentExpr) (*Type, error) {
	if len(e.Args) != 1 {
		return nil, typeErr(e.Pos, "function %q expects 1 argument(s), got %d", ident.Name, len(e.Args))
	}
	at, err := c.checkExpr(e.Args[0])
	if err != nil {
		return nil, err
	}
	if at == nil {
		return nil, typeErr(e.Args[0].ExprPos(), "clone: cannot infer argument type")
	}
	switch at.Kind {
	case TypeList, TypeTuple, TypeStruct, TypeEnum:
		// Composite — admissible. Return the same type; clone produces a
		// fresh deep copy at runtime, but the type is unchanged.
		ident.setType(at)
		e.setType(at)
		return at, nil
	}
	return nil, typeErr(e.Args[0].ExprPos(), "clone: argument must be a composite type — primitives don't need cloning")
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// typeEq is the type-equality wrapper used everywhere. Pointer equality first,
// structural equality for composites; primitives short-circuit to pointer.
func typeEq(a, b *Type) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equals(b)
}

func isNumeric(t *Type) bool {
	return t == tInt || t == tFloat || t == tByte || t == tRune
}

// isComparableForEq reports whether == / != is admissible on t at v0.4. The
// rule is: primitives are admissible (v0.1), and structural composites — list,
// tuple, struct, enum — are admissible recursively, with the caveat that any
// element / payload / field type must itself be admissible. Spec-typed values
// reject — Comparable is a v0.6 concern (needs generics).
//
// Recursive types are already rejected by Unit 3's cycle detection, so this
// function's recursion always terminates.
//
// Returns (reason, ok). reason is the user-visible diagnostic when ok is false.
func isComparableForEq(t *Type) (string, bool) {
	if t == nil {
		return "operands of == must have a known type", false
	}
	switch t.Kind {
	case TypeInt, TypeFloat, TypeBool, TypeStr, TypeByte, TypeRune:
		return "", true
	case TypeList:
		if reason, ok := isComparableForEq(t.Element); !ok {
			return reason, false
		}
		return "", true
	case TypeTuple:
		for _, sub := range t.Tuple {
			if reason, ok := isComparableForEq(sub); !ok {
				return reason, false
			}
		}
		return "", true
	case TypeStruct:
		for _, f := range t.Fields {
			if reason, ok := isComparableForEq(f.Type); !ok {
				return reason, false
			}
		}
		return "", true
	case TypeEnum:
		for _, payload := range t.VariantPayloads {
			for _, sub := range payload {
				if reason, ok := isComparableForEq(sub); !ok {
					return reason, false
				}
			}
		}
		return "", true
	case TypeSpec:
		return fmt.Sprintf("cannot compare values of spec type %q — defer to v0.6", t.Name), false
	}
	return fmt.Sprintf("operator == not supported for %s", t), false
}

// isConstExpr reports whether expr is admissible on the right-hand side of a
// `const` declaration. Composites are not const-evaluable at v0.2.
func isConstExpr(e Expr) bool {
	switch x := e.(type) {
	case *IntLit, *FloatLit, *StringLit, *BoolLit, *RuneLit:
		return true
	case *ParenExpr:
		return isConstExpr(x.Inner)
	case *UnaryExpr:
		return isConstExpr(x.Operand)
	case *BinaryExpr:
		return isConstExpr(x.Left) && isConstExpr(x.Right)
	}
	return false
}
