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
type Impl struct {
	Pos       Position
	TypeName  string
	SpecName  string         // "" for inherent
	Receiver  *Type          // resolved receiver type (struct or enum)
	Methods   []*implMethod  // declaration order
	methodIdx map[string]*implMethod
	ast       *ImplDecl
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
}

// Check is the public entry point. It walks prog, annotates every Expr with
// a type, resolves every TypeRef, and returns the FIRST type or scope error
// encountered.
func Check(prog *Program) error {
	c := &checker{
		scope:         newScope(nil),
		fns:           map[string]fnSig{},
		structs:       map[string]*Type{},
		enums:         map[string]*Type{},
		structAST:     map[string]*StructDecl{},
		enumAST:       map[string]*EnumDecl{},
		specs:         map[string]*Spec{},
		impls:         map[implKey]*Impl{},
		implsByType:   map[string][]*Impl{},
		methodVisible: map[string]map[string][]*methodSource{},
	}
	// Pre-populate the function table with `len`. It's resolved as a built-in
	// at call time — the params slot here is a placeholder that the call-site
	// resolver special-cases (any list[T] is admissible).
	c.fns["len"] = fnSig{
		params:  []*Type{NewListType(tInt)}, // documentation-only sentinel
		ret:     tInt,
		builtin: true,
	}
	// v0.3 builtins: `push(xs, v)` mutates a mut-bound list in place; `clone(xs)`
	// returns a fresh deep copy of any composite. Both special-case at the call
	// site (see checkCall) — the params/ret slots here are documentation-only
	// sentinels and are never type-checked against directly.
	c.fns["push"] = fnSig{
		params:  []*Type{NewListType(tInt), tInt}, // documentation-only sentinel
		ret:     tVoid,
		builtin: true,
	}
	c.fns["clone"] = fnSig{
		params:  []*Type{NewListType(tInt)}, // documentation-only sentinel
		ret:     NewListType(tInt),
		builtin: true,
	}

	if err := c.collectTopLevel(prog); err != nil {
		return err
	}
	if err := c.resolveStructFields(prog); err != nil {
		return err
	}
	if err := c.resolveEnumPayloads(prog); err != nil {
		return err
	}
	if err := c.detectTypeCycles(); err != nil {
		return err
	}
	if err := c.resolveFnSignatures(prog); err != nil {
		return err
	}
	if err := c.resolveSpecs(prog); err != nil {
		return err
	}
	if err := c.resolveImpls(prog); err != nil {
		return err
	}
	if err := c.buildMethodVisibility(); err != nil {
		return err
	}
	if err := c.checkSpecBodies(prog); err != nil {
		return err
	}
	if err := c.checkImplBodies(prog); err != nil {
		return err
	}
	for _, stmt := range prog.Statements {
		if err := c.checkStmt(stmt); err != nil {
			return err
		}
	}
	// v0.3 Unit 3: borrow check runs after typeck so every Expr already has a
	// resolved Type. The two passes share a process but produce distinct error
	// types (TypeError vs BorrowError) — keeping them separate makes it
	// trivial for tests and tooling to attribute a diagnostic to its source.
	if err := borrowCheck(prog, c.fns, c.structs, c.enums, c.specs); err != nil {
		return err
	}
	return nil
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
			c.structs[s.Name] = NewStructType(s.Name, nil) // fields filled by second pass
			c.structAST[s.Name] = s
		case *EnumDecl:
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
			c.enums[s.Name] = &Type{Kind: TypeEnum, Name: s.Name, Variants: variants, VariantPayloads: payloads}
			c.enumAST[s.Name] = s
		case *SpecDecl:
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
			// v0.3 adds `push` and `clone` to the same reserved set.
			switch s.Name {
			case "len", "push", "clone":
				return typeErr(s.Pos, "cannot redefine built-in '%s'", s.Name)
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
func (c *checker) resolveStructFields(_ *Program) error {
	for name, decl := range c.structAST {
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
			for i, f := range st.Fields {
				if err := visitType(f.Type, decl.Fields[i].Pos, fmt.Sprintf("via field %q", f.Name), visit); err != nil {
					return err
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
			if err := visit(name, c.structAST[name].Pos, ""); err != nil {
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
func (c *checker) resolveFnSignatures(prog *Program) error {
	for _, stmt := range prog.Statements {
		fn, ok := stmt.(*FnDecl)
		if !ok {
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

// resolveImpls walks every ImplDecl, validates the receiver type rule, builds
// per-impl method tables, and registers each impl in the impl tables. The
// per-(Type, Spec) duplicate check fires here.
func (c *checker) resolveImpls(prog *Program) error {
	for _, stmt := range prog.Statements {
		id, ok := stmt.(*ImplDecl)
		if !ok {
			continue
		}
		// Receiver type must be a struct or enum (PLAN-pinned). Primitives,
		// lists, tuples, and specs all reject. Unknown names also reject.
		var receiver *Type
		if st, ok := c.structs[id.Type]; ok {
			receiver = st
		} else if en, ok := c.enums[id.Type]; ok {
			receiver = en
		} else if _, ok := c.specs[id.Type]; ok {
			return typeErr(id.Pos, "%q cannot impl spec at v0.4 — only struct and enum types can implement specs", id.Type)
		} else {
			// Primitive name?
			switch id.Type {
			case "int", "float", "bool", "str", "byte", "rune":
				return typeErr(id.Pos, "%q cannot impl spec at v0.4 — only struct and enum types can implement specs", id.Type)
			}
			return typeErr(id.Pos, "unknown type %q", id.Type)
		}
		// Spec name (if any) must be known.
		var spec *Spec
		if id.Spec != "" {
			s, ok := c.specs[id.Spec]
			if !ok {
				return typeErr(id.Pos, "unknown spec %q", id.Spec)
			}
			spec = s
		}
		key := implKey{typeName: id.Type, specName: id.Spec}
		if prev, exists := c.impls[key]; exists {
			if id.Spec == "" {
				// Multiple inherent impls are admitted — they aggregate. Fall
				// through to merge below.
				_ = prev
			} else {
				return typeErr(id.Pos,
					"duplicate impl: %s already implements %s at %s",
					id.Type, id.Spec, prev.Pos)
			}
		}
		// Build implMethod entries with resolved param/ret types.
		methods := make([]*implMethod, 0, len(id.Methods))
		methodIdx := map[string]*implMethod{}
		for _, fn := range id.Methods {
			if fn.Name == "this" {
				return typeErr(fn.Pos, "method must not be named 'this'")
			}
			// Reject explicit `this` parameter.
			for _, p := range fn.Params {
				if p.Name == "this" {
					return typeErr(p.Pos, "method %q in impl %s must not declare an explicit 'this' parameter ('this' is implicit)", fn.Name, id.Type)
				}
			}
			if _, dup := methodIdx[fn.Name]; dup {
				return typeErr(fn.Pos, "duplicate method %q in impl block for %s", fn.Name, id.Type)
			}
			params := make([]*Type, len(fn.Params))
			for i, p := range fn.Params {
				t, err := c.resolveTypeRef(p.Type)
				if err != nil {
					return err
				}
				if t == tVoid {
					return typeErr(p.Pos, "parameter %q cannot have void type", p.Name)
				}
				params[i] = t
			}
			ret := tVoid
			if fn.Return != nil {
				rt, err := c.resolveTypeRef(fn.Return)
				if err != nil {
					return err
				}
				if rt == tVoid {
					return typeErr(fn.Return.Pos, "use no return annotation instead of declaring a void return")
				}
				ret = rt
			}
			im := &implMethod{
				pos:    fn.Pos,
				name:   fn.Name,
				ast:    fn,
				params: params,
				ret:    ret,
			}
			methods = append(methods, im)
			methodIdx[fn.Name] = im
		}
		// For spec impls, validate that every method name corresponds to a
		// declared spec method, and that the override signature matches the
		// spec's declared signature.
		if spec != nil {
			for _, im := range methods {
				sm, ok := spec.methodIdx[im.name]
				if !ok {
					return typeErr(im.pos, "method %q is not declared in spec %q", im.name, spec.Name)
				}
				if len(im.params) != len(sm.params) {
					return typeErr(im.pos, "method %q expects %d parameter(s) per spec %q, got %d", im.name, len(sm.params), spec.Name, len(im.params))
				}
				for i := range im.params {
					if !typeEq(im.params[i], sm.params[i]) {
						return typeErr(im.pos, "method %q parameter %d type %s does not match spec %q signature %s", im.name, i+1, im.params[i], spec.Name, sm.params[i])
					}
				}
				if !typeEq(im.ret, sm.ret) {
					return typeErr(im.pos, "method %q return type %s does not match spec %q return type %s", im.name, im.ret, spec.Name, sm.ret)
				}
			}
		}
		impl := &Impl{
			Pos:       id.Pos,
			TypeName:  id.Type,
			SpecName:  id.Spec,
			Receiver:  receiver,
			Methods:   methods,
			methodIdx: methodIdx,
			ast:       id,
		}
		// Aggregate inherent impls onto a single record; reject duplicates for
		// spec impls (already handled above).
		if id.Spec == "" {
			c.rawInherentDecls = append(c.rawInherentDecls, id)
			if prev, exists := c.impls[key]; exists {
				// Merge inherent methods, watching for duplicate names across
				// blocks. A duplicate-name across inherent blocks is reported
				// later by the visibility pass with both positions, so we just
				// concatenate here.
				prev.Methods = append(prev.Methods, methods...)
				for _, im := range methods {
					if existing, dup := prev.methodIdx[im.name]; dup {
						_ = existing
						// Keep the first; visibility pass will diagnose with
						// both positions.
					} else {
						prev.methodIdx[im.name] = im
					}
				}
			} else {
				c.impls[key] = impl
				c.implsByType[id.Type] = append(c.implsByType[id.Type], impl)
			}
		} else {
			c.impls[key] = impl
			c.implsByType[id.Type] = append(c.implsByType[id.Type], impl)
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
			spec := c.specs[impl.SpecName]
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
			c.currentFn = &sig
			c.currentSpec = spec
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
			c.currentFn = &sig
			c.currentReceiver = impl.Receiver
			c.scope = newScope(c.scope)
			for i, p := range fn.Params {
				if !c.scope.declare(p.Name, binding{kind: bindLet, typ: im.params[i]}) {
					c.scope = c.scope.parent
					c.currentFn = savedFn
					c.currentReceiver = savedRecv
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
func (c *checker) resolveTypeRef(ref *TypeRef) (*Type, error) {
	if ref == nil {
		return nil, nil
	}
	switch ref.Kind {
	case TypeRefNamed:
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
		return c.checkDecl(s.Pos, s.Name, s.Type, s.Value, bindLet)
	case *MutStmt:
		if s.Tuple != nil {
			return c.checkTupleDestructure(s.Pos, s.Tuple, s.Value, bindMut)
		}
		return c.checkDecl(s.Pos, s.Name, s.Type, s.Value, bindMut)
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
		return c.checkDecl(s.Pos, s.Name, s.Type, s.Value, bindConst)
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
	}
	return typeErr(stmt.StmtPos(), "internal: unhandled statement %T", stmt)
}

// checkPrint validates `print expr`. v0.2 accepts every printable shape:
// primitives, lists, tuples, structs, enums. Void is rejected.
func (c *checker) checkPrint(s *PrintStmt) error {
	t, err := c.checkExpr(s.Expr)
	if err != nil {
		return err
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
	annotated, err := c.resolveTypeRef(ref)
	if err != nil {
		return err
	}
	// Pass the annotated type as a hint so empty-list literals can latch onto
	// it. Other expression forms ignore the hint.
	observed, err := c.checkExprHint(value, annotated)
	if err != nil {
		return err
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
		_, ok := c.impls[implKey{typeName: from.Name, specName: to.Name}]
		return ok
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

// checkTupleDestructure handles `let (a, b) := expr` (and the mut form). The
// RHS must be a tuple type with arity matching the LHS name list; each name
// is then bound in the surrounding scope with its element type. The parser
// has already rejected repeated names; the only diagnostics generated here
// are for arity / shape mismatch and shadowing-in-the-same-scope.
//
// Annotated destructure (`let (a, b): tuple[int, int] = ...`) is rejected by
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
		return typeErr(s.Pos, "cannot assign to %q[i] (declared with let — use mut to allow element mutation)", id.Name)
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
		return typeErr(s.Pos, "cannot assign to %q (declared with let)", target.Name)
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
		// `for x in xs { ... }` — iterate over a list-typed expression. The
		// loop variable's type is the element type. Empty lists are allowed
		// (the body never runs); typeck rejects unannotated empty literals
		// upstream so the iterable's element type is always concrete here.
		iterT, err := c.checkExpr(s.Iter)
		if err != nil {
			return err
		}
		if iterT == nil || iterT.Kind != TypeList {
			return typeErr(s.Iter.ExprPos(), "'for ... in' iterable must be a list, got %s", iterT)
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
	sig := c.fns[fn.Name]
	c.currentFn = &sig
	defer func() { c.currentFn = nil }()

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
	t, err := c.checkExprHint(s.Value, c.currentFn.ret)
	if err != nil {
		return err
	}
	if c.currentFn.ret == tVoid {
		return typeErr(s.Pos, "function returns no value but 'return' has expression of type %s", t)
	}
	if !typeEq(t, c.currentFn.ret) {
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
		if expected.Name != p.TypeName {
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
		b, ok := c.scope.lookup(e.Name)
		if !ok {
			return nil, typeErr(e.Pos, "undefined name %q", e.Name)
		}
		e.setType(b.typ)
		return b.typ, nil
	case *BinaryExpr:
		return c.checkBinary(e)
	case *UnaryExpr:
		return c.checkUnary(e)
	case *CallExpr:
		return c.checkCall(e)
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
		return c.checkStructLit(e)
	case *IndexExpr:
		return c.checkIndex(e)
	case *SliceExpr:
		return c.checkSlice(e)
	case *FieldAccessExpr:
		return c.checkFieldAccess(e)
	case *MethodCallExpr:
		return c.checkMethodCall(e)
	case *ThisExpr:
		if c.currentReceiver == nil {
			return nil, typeErr(e.Pos, "'this' is only valid inside an impl method body")
		}
		e.setType(c.currentReceiver)
		return c.currentReceiver, nil
	case *EnumLit:
		return c.checkEnumLit(e)
	}
	return nil, typeErr(expr.ExprPos(), "internal: unhandled expression %T", expr)
}

// checkListLit handles `[e1, e2, ...]` and the empty-list special case. With
// elements present, every element type must equal the first; the result is
// list[T]. With zero elements, the literal latches onto a list-shaped hint
// (annotated let / call argument / function return); otherwise it errors out.
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
	// `let xs: list[list[int]] = [[]]` can fill in the inner empty list.
	var elemHint *Type
	if hint != nil && hint.Kind == TypeList {
		elemHint = hint.Element
	}
	// When the hint asks for a list of a spec type, the result list is
	// list[Spec] regardless of which concrete types appear in the literal —
	// each element only needs to impl the spec. Otherwise use the first
	// element's observed type and require subsequent elements to match.
	first, err := c.checkExprHint(e.Elements[0], elemHint)
	if err != nil {
		return nil, err
	}
	if elemHint != nil && elemHint.Kind == TypeSpec {
		if !c.assignableTo(first, elemHint) {
			return nil, typeErr(e.Elements[0].ExprPos(), "list element 1 has type %s, expected %s", first, elemHint)
		}
		for i := 1; i < len(e.Elements); i++ {
			t, err := c.checkExprHint(e.Elements[i], elemHint)
			if err != nil {
				return nil, err
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
		t, err := c.checkExprHint(e.Elements[i], elemHint)
		if err != nil {
			return nil, err
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
func (c *checker) checkStructLit(e *StructLit) (*Type, error) {
	st, ok := c.structs[e.TypeName]
	if !ok {
		return nil, typeErr(e.Pos, "unknown struct type %q", e.TypeName)
	}
	declared := map[string]*Type{}
	for _, f := range st.Fields {
		declared[f.Name] = f.Type
	}
	provided := map[string]bool{}
	for _, init := range e.Fields {
		ft, ok := declared[init.Name]
		if !ok {
			return nil, typeErr(init.Pos, "struct %q has no field %q", e.TypeName, init.Name)
		}
		if provided[init.Name] {
			return nil, typeErr(init.Pos, "field %q already initialised in struct literal", init.Name)
		}
		provided[init.Name] = true
		vt, err := c.checkExprHint(init.Value, ft)
		if err != nil {
			return nil, err
		}
		if !c.assignableTo(vt, ft) {
			return nil, typeErr(init.Pos, "field %q expects %s, got %s", init.Name, ft, vt)
		}
	}
	for _, f := range st.Fields {
		if !provided[f.Name] {
			return nil, typeErr(e.Pos, "struct %q literal is missing field %q", e.TypeName, f.Name)
		}
	}
	e.setType(st)
	return st, nil
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
func (c *checker) checkFieldAccess(e *FieldAccessExpr) (*Type, error) {
	if id, ok := e.Receiver.(*IdentExpr); ok {
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
	if rt.Kind == TypeList {
		switch e.Method {
		case "push", "clone", "len":
			return c.lowerListBuiltinFromMethodCall(e)
		}
	}
	if rt.Kind != TypeStruct && rt.Kind != TypeEnum {
		return nil, typeErr(e.MethodPos, "method %q does not exist on %s", e.Method, rt)
	}
	return c.dispatchConcreteMethod(e, rt)
}

// lowerListBuiltinFromMethodCall rewrites `xs.push(v)` / `xs.clone()` /
// `xs.len()` to a synthetic CallExpr and runs it through checkCall, then
// stashes the call on e.LoweredCall so downstream consumers see the builtin
// shape. Diagnostics fall out of checkCall with the synthetic CallExpr's
// position pinned to the method-call site.
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
func (c *checker) dispatchConcreteMethod(e *MethodCallExpr, recv *Type) (*Type, error) {
	visible := c.methodVisible[recv.Name]
	srcs := visible[e.Method]
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
			// signature is still the one we type-check against.
			sm := c.specs[src.specName].methodIdx[src.name]
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
	ident, ok := e.Callee.(*IdentExpr)
	if !ok {
		return nil, typeErr(e.Pos, "callee must be a function name")
	}
	sig, ok := c.fns[ident.Name]
	if !ok {
		if _, isVar := c.scope.lookup(ident.Name); isVar {
			return nil, typeErr(ident.Pos, "%q is not a function", ident.Name)
		}
		return nil, typeErr(ident.Pos, "undefined function %q", ident.Name)
	}
	if sig.builtin && ident.Name == "len" {
		// `len(xs)` accepts exactly one list argument and returns int. We do
		// not promote `len` to a generic in the type system; this is the only
		// generic-feeling intrinsic at v0.2 and the special-case keeps the
		// rest of the type system monomorphic.
		if len(e.Args) != 1 {
			return nil, typeErr(e.Pos, "function %q expects 1 argument, got %d", ident.Name, len(e.Args))
		}
		at, err := c.checkExpr(e.Args[0])
		if err != nil {
			return nil, err
		}
		if at == nil || at.Kind != TypeList {
			return nil, typeErr(e.Args[0].ExprPos(), "argument to len must be a list, got %s", at)
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
	for i, a := range e.Args {
		at, err := c.checkExprHint(a, sig.params[i])
		if err != nil {
			return nil, err
		}
		if !typeEq(at, sig.params[i]) {
			return nil, typeErr(a.ExprPos(), "argument %d to %q has type %s, expected %s", i+1, ident.Name, at, sig.params[i])
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
// because fn params are bindLet (and inner-block let push := ... is fine
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
