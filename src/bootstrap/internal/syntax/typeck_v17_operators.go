package syntax

// v0.17 — operator-spec wiring.
//
// Three built-in specs (Arithmetic / Comparable / From) are injected
// into c.specs at newChecker() time. User syntax is
//
//   impl BigInt for Arithmetic { fn add(other: BigInt) -> BigInt; ... }
//   impl BigInt for Comparable { fn eq(other: BigInt) -> bool;    ... }
//
// — no `[T]` argument; the receiver type IS the implicit Self for the
// bundled specs. From[T] is declared as a contract (Rust-style
// `From<T>`) but its impl/dispatch surface awaits v0.18 (needs
// `impl X for Spec[T]` parser support and static-method call syntax).
//
// After spec injection, per-primitive synthetic *Impl records are wired
// into c.impls and c.implsByType. They carry IsPrimitiveBuiltin = true
// and ast == nil method entries; cgen/run check the flag at dispatch
// time and route to the existing primitive-arithmetic emit/eval paths
// instead of attempting a fn-body call.
//
// The synthetic *Impl records make `fn sum[T: Arithmetic](xs: list[T])
// -> T` compose uniformly across int and user types: assignableTo's
// concrete→spec widening path finds either the user-declared impl or a
// synthetic primitive impl in c.impls.

// operatorMethodSpec is one method on a bundled operator spec. arity is
// 0 (Neg) or 1 (everything else). returnSelf signals the method returns
// the receiver type; otherwise the spec dictates a fixed return type
// (bool for Comparable.eq / lt, Self for From.from).
type operatorMethodSpec struct {
	name       string
	arity      int  // 0 or 1
	returnSelf bool // true → return type matches receiver; false → fixed
}

// bundledOperatorSpec describes one of the built-in operator specs.
// methods enumerates the required impl methods (declaration order is
// preserved for vtable layout); coverage lists primitive types that
// auto-satisfy ALL methods of the bundle (so generic bounds compose).
//
// fixedReturn is the substituted return type for methods whose
// returnSelf is false — bool for Comparable.
type bundledOperatorSpec struct {
	name        string
	methods     []operatorMethodSpec
	coverage    []*Type
	fixedReturn *Type // applies to methods with returnSelf=false
}

// operatorSpecCatalog enumerates the bundled built-in operator specs:
//
//	Arithmetic   add sub mul div mod (binary) + neg (unary)  → int, float
//	Comparable   eq lt                                       → int, float, byte, rune, str
//	From         from(value: T) -> Self                      → no primitive coverage
//
// Comparable.lt is the irreducible ordering primitive; `> >= <=`
// desugar to (b.lt(a)) / (!a.lt(b)) / (!b.lt(a)) at checkBinary time.
// Comparable.eq is the equality primitive; `!=` desugars to !eq.
//
// Bool is *not* in Comparable.coverage — the primitive `==/!=` arm in
// checkBinary handles bool directly, but `<` on bool is undefined and
// no `Comparable for bool` impl exists.
var operatorSpecCatalog = []bundledOperatorSpec{
	{
		name: "Arithmetic",
		methods: []operatorMethodSpec{
			{"add", 1, true},
			{"sub", 1, true},
			{"mul", 1, true},
			{"div", 1, true},
			{"mod", 1, true},
			{"neg", 0, true},
		},
		coverage: []*Type{tInt, tFloat},
	},
	{
		name: "Comparable",
		methods: []operatorMethodSpec{
			{"eq", 1, false},
			{"lt", 1, false},
		},
		coverage:    []*Type{tInt, tFloat, tByte, tRune, tStr},
		fixedReturn: tBool,
	},
	{
		name: "From",
		// from(value: T) -> Self  — declared as a contract only.
		// arity=1 and returnSelf=true; parameter type is the From's
		// type-arg (deferred until v0.18 parser supports `Spec[T]`
		// in impl-decl head). No primitive coverage; no impls
		// admitted yet.
		methods: []operatorMethodSpec{
			{"from", 1, true},
		},
	},
}

// operatorSpecByName indexes operatorSpecCatalog by spec name for
// constant-time lookup at hot dispatch sites.
var operatorSpecByName = func() map[string]*bundledOperatorSpec {
	m := make(map[string]*bundledOperatorSpec, len(operatorSpecCatalog))
	for i := range operatorSpecCatalog {
		op := &operatorSpecCatalog[i]
		m[op.name] = op
	}
	return m
}()

// methodSpecByName looks up the per-method shape inside a bundled spec.
func (b *bundledOperatorSpec) methodSpecByName(method string) *operatorMethodSpec {
	for i := range b.methods {
		if b.methods[i].name == method {
			return &b.methods[i]
		}
	}
	return nil
}

// injectOperatorSpecs registers the three bundled operator specs in
// c.specs. The substituted method shapes (parameters typed to the
// receiver, return type per the spec) are recomputed per-impl at
// validation time via validateOperatorSpecImpl.
func injectOperatorSpecs(c *checker) {
	for i := range operatorSpecCatalog {
		op := &operatorSpecCatalog[i]
		methods := make([]*specMethod, len(op.methods))
		idx := make(map[string]*specMethod, len(op.methods))
		for j := range op.methods {
			sm := &specMethod{name: op.methods[j].name, Synthetic: true}
			methods[j] = sm
			idx[op.methods[j].name] = sm
		}
		c.specs[op.name] = &Spec{
			Name:      op.name,
			Methods:   methods,
			methodIdx: idx,
			Synthetic: true,
			typ:       NewSpecType(op.name),
		}
	}
}

// injectPrimitiveOperatorImpls wires synthetic primitive impls into
// c.impls and c.implsByType for every (primitive, bundle) pair where
// the primitive auto-satisfies all methods of the bundle. Called from
// newChecker after injectOperatorSpecs. Each impl has
// IsPrimitiveBuiltin = true; cgen/run see the flag and bypass fn-body
// lookup, routing to the primitive emit/eval path keyed off method
// name.
func injectPrimitiveOperatorImpls(c *checker) {
	for i := range operatorSpecCatalog {
		op := &operatorSpecCatalog[i]
		for _, t := range op.coverage {
			c.installPrimitiveOperatorImpl(t, op)
		}
	}
}

// installPrimitiveOperatorImpl materialises a synthetic *Impl for
// (recv, op). Idempotent: a duplicate call for the same
// (recv, op.name) is a no-op.
func (c *checker) installPrimitiveOperatorImpl(recv *Type, op *bundledOperatorSpec) {
	typeName := primitiveTypeName(recv)
	if typeName == "" {
		return
	}
	key := implKey{typeName: typeName, specName: op.name}
	if _, exists := c.impls[key]; exists {
		return
	}
	methods := make([]*implMethod, len(op.methods))
	idx := make(map[string]*implMethod, len(op.methods))
	for j := range op.methods {
		m := &op.methods[j]
		im := &implMethod{
			name:   m.name,
			params: operatorMethodParams(m, recv),
			ret:    operatorMethodReturn(m, op, recv),
		}
		methods[j] = im
		idx[m.name] = im
	}
	impl := &Impl{
		TypeName:           typeName,
		SpecName:           op.name,
		Receiver:           recv,
		Methods:            methods,
		methodIdx:          idx,
		IsPrimitiveBuiltin: true,
	}
	c.impls[key] = impl
	c.implsByType[typeName] = append(c.implsByType[typeName], impl)
}

// operatorMethodParams returns the substituted parameter-type slice
// for a bundled-spec method on a receiver of type recv. arity-0
// methods (e.g. Arithmetic.neg) return nil; arity-1 take one same-
// typed argument.
func operatorMethodParams(m *operatorMethodSpec, recv *Type) []*Type {
	if m.arity == 0 {
		return nil
	}
	return []*Type{recv}
}

// operatorMethodReturn returns the substituted return type for a
// bundled-spec method. returnSelf=true substitutes the receiver type;
// otherwise the bundle's fixedReturn applies (bool for Comparable).
func operatorMethodReturn(m *operatorMethodSpec, op *bundledOperatorSpec, recv *Type) *Type {
	if m.returnSelf {
		return recv
	}
	return op.fixedReturn
}

// validateOperatorSpecImpl is the impl-conformance check for a
// bundled operator spec. It walks every method declared on the
// bundle and checks that the impl carries a matching signature
// after Self → receiver substitution. From[T] impls are rejected
// outright at v0.17: the parser does not accept `for Spec[T]` so
// no concrete source-level impl can target it.
func (c *checker) validateOperatorSpecImpl(id *ImplDecl, methods []*implMethod, spec *Spec) error {
	op, ok := operatorSpecByName[spec.Name]
	if !ok {
		return typeErr(id.Pos, "internal: unknown operator spec %q", spec.Name)
	}
	if spec.Name == "From" {
		return typeErr(id.Pos,
			"impl of %q is not yet supported (requires v0.18 spec type-args + static-method dispatch)",
			spec.Name)
	}
	recv := id.Receiver
	if recv == nil {
		return typeErr(id.Pos, "internal: impl receiver unresolved for operator spec %q", spec.Name)
	}
	if len(methods) != len(op.methods) {
		want := make([]string, len(op.methods))
		for i := range op.methods {
			want[i] = op.methods[i].name
		}
		return typeErr(id.Pos,
			"impl of %q must declare exactly %d method(s): %v — got %d",
			spec.Name, len(op.methods), want, len(methods))
	}
	implByName := make(map[string]*implMethod, len(methods))
	for _, im := range methods {
		implByName[im.name] = im
	}
	for i := range op.methods {
		m := &op.methods[i]
		im, present := implByName[m.name]
		if !present {
			return typeErr(id.Pos,
				"impl of %q is missing required method %q",
				spec.Name, m.name)
		}
		wantParams := operatorMethodParams(m, recv)
		if len(im.params) != len(wantParams) {
			return typeErr(im.pos,
				"method %q in impl of %q expects %d parameter(s), got %d",
				m.name, spec.Name, len(wantParams), len(im.params))
		}
		for k, want := range wantParams {
			if !typeEq(im.params[k], want) {
				return typeErr(im.pos,
					"method %q in impl of %q parameter %d type %s does not match %s",
					m.name, spec.Name, k+1, im.params[k], want)
			}
		}
		wantRet := operatorMethodReturn(m, op, recv)
		if !typeEq(im.ret, wantRet) {
			return typeErr(im.pos,
				"method %q in impl of %q return type %s does not match %s",
				m.name, spec.Name, im.ret, wantRet)
		}
	}
	return nil
}

// primitiveTypeName returns the canonical name of a primitive *Type,
// matching the spelling used as the impl key (and the name a user
// would write in `impl <name> for <Spec>`). Returns "" for non-
// primitive types.
func primitiveTypeName(t *Type) string {
	switch t {
	case tInt:
		return "int"
	case tFloat:
		return "float"
	case tBool:
		return "bool"
	case tStr:
		return "str"
	case tByte:
		return "byte"
	case tRune:
		return "rune"
	}
	return ""
}

// IsOperatorSpecName reports whether specName is one of the bundled
// operator specs injected at newChecker time. cgen / run use this
// check to skip vtable emission and spec-value bindings — operator
// specs never appear as a value type, only as an impl target or a
// generic-fn bound.
func IsOperatorSpecName(specName string) bool {
	_, ok := operatorSpecByName[specName]
	return ok
}

// IsPrimitiveTypeForOperator reports whether t is a primitive scalar
// (int, float, bool, str, byte, rune). cgen / run use this to short-
// circuit dispatchConcrete on synthetic primitive impl records.
func IsPrimitiveTypeForOperator(t *Type) bool {
	return primitiveTypeName(t) != ""
}

// isUserStructOrEnum reports whether t is eligible for operator-spec
// lookup. Primitive types have their own fast path in checkBinary /
// checkUnary; spec-typed values never appear as the surface operand
// of `a + b` because their concrete type is hidden.
func isUserStructOrEnum(t *Type) bool {
	return t != nil && (t.Kind == TypeStruct || t.Kind == TypeEnum)
}

// lookupImplCrossMod returns the *Impl for (typeName, specName),
// walking the bundle's per-module impl tables when the current
// checker's local table misses. Mirrors the cross-module walk in
// assignableTo.
func (c *checker) lookupImplCrossMod(typeName, specName string) *Impl {
	key := implKey{typeName: typeName, specName: specName}
	if impl, ok := c.impls[key]; ok {
		return impl
	}
	if c.crossMod != nil {
		for _, fc := range c.crossMod.checkers {
			if fc == c {
				continue
			}
			if impl, ok := fc.impls[key]; ok {
				return impl
			}
		}
	}
	return nil
}

// binDesugarKind tags how the lowered method call composes with the
// surface op:
//
//	binDesugarPlain   the method-call result IS the binary result
//	binDesugarNot     surface op needs `!result` (e.g. `!=` via `eq`,
//	                   `>=` via `lt`, `<=` via swapped-lt)
type binDesugarKind int

const (
	binDesugarNone binDesugarKind = iota
	binDesugarPlain
	binDesugarNot
)

// binaryOperatorDispatch describes the lowering for one binary op:
//   - spec / method names — which bundled spec method to call
//   - swap — whether to swap operands before the call (for `>` / `<=`)
//   - kind — plain or negate-result
type binaryOperatorDispatch struct {
	spec   string
	method string
	swap   bool
	kind   binDesugarKind
}

// operatorDispatchFor maps a BinaryOp to the bundled-spec dispatch.
// Returns nil for ops that don't take part in operator-spec desugar
// (bit ops, logical ops, floor-div).
//
//	+   Arithmetic.add(other) → plain
//	-   Arithmetic.sub(other) → plain
//	*   Arithmetic.mul(other) → plain
//	/   Arithmetic.div(other) → plain
//	%   Arithmetic.mod(other) → plain
//	==  Comparable.eq(other)  → plain
//	!=  Comparable.eq(other)  → not
//	<   Comparable.lt(other)  → plain
//	>   Comparable.lt(swapped)→ plain  (a > b ↔ b < a)
//	<=  Comparable.lt(swapped)→ not    (a <= b ↔ !(b < a))
//	>=  Comparable.lt(other)  → not    (a >= b ↔ !(a < b))
func operatorDispatchFor(op BinaryOp) *binaryOperatorDispatch {
	switch op {
	case BinAdd:
		return &binaryOperatorDispatch{spec: "Arithmetic", method: "add", kind: binDesugarPlain}
	case BinSub:
		return &binaryOperatorDispatch{spec: "Arithmetic", method: "sub", kind: binDesugarPlain}
	case BinMul:
		return &binaryOperatorDispatch{spec: "Arithmetic", method: "mul", kind: binDesugarPlain}
	case BinDiv:
		return &binaryOperatorDispatch{spec: "Arithmetic", method: "div", kind: binDesugarPlain}
	case BinMod:
		return &binaryOperatorDispatch{spec: "Arithmetic", method: "mod", kind: binDesugarPlain}
	case BinEq:
		return &binaryOperatorDispatch{spec: "Comparable", method: "eq", kind: binDesugarPlain}
	case BinNE:
		return &binaryOperatorDispatch{spec: "Comparable", method: "eq", kind: binDesugarNot}
	case BinLT:
		return &binaryOperatorDispatch{spec: "Comparable", method: "lt", kind: binDesugarPlain}
	case BinGT:
		return &binaryOperatorDispatch{spec: "Comparable", method: "lt", swap: true, kind: binDesugarPlain}
	case BinLE:
		return &binaryOperatorDispatch{spec: "Comparable", method: "lt", swap: true, kind: binDesugarNot}
	case BinGE:
		return &binaryOperatorDispatch{spec: "Comparable", method: "lt", kind: binDesugarNot}
	}
	return nil
}

// tryOperatorSpecDesugar attempts to lower a binary operator on user-
// struct / user-enum operands into the corresponding spec-method call.
// Returns (true, resultType, nil) on success — caller treats the
// BinaryExpr as fully checked. Returns (false, nil, nil) when no
// matching impl exists; caller falls through to the primitive arms,
// which will reject with the standard same-type diagnostic.
func (c *checker) tryOperatorSpecDesugar(e *BinaryExpr, t *Type) (bool, *Type, error) {
	disp := operatorDispatchFor(e.Op)
	if disp == nil {
		return false, nil, nil
	}
	impl := c.lookupImplCrossMod(t.Name, disp.spec)
	if impl == nil {
		return false, nil, nil
	}
	recvExpr, argExpr := e.Left, e.Right
	if disp.swap {
		recvExpr, argExpr = e.Right, e.Left
	}
	mc := &MethodCallExpr{
		Pos:       e.Pos,
		Receiver:  recvExpr,
		Method:    disp.method,
		MethodPos: e.Pos,
		Args:      []Expr{argExpr},
	}
	src, err := c.synthesisedMethodSourceFor(impl, disp.method)
	if err != nil {
		return false, nil, err
	}
	if _, err := c.bindResolvedMethodCall(mc, src); err != nil {
		return false, nil, err
	}
	e.Lowered = mc
	switch disp.kind {
	case binDesugarPlain:
		e.setType(mc.Type())
		return true, mc.Type(), nil
	case binDesugarNot:
		e.LoweredNot = true
		e.setType(tBool)
		return true, tBool, nil
	}
	return false, nil, nil
}

// tryUnaryOperatorSpecDesugar attempts to lower `-x` on user-struct /
// user-enum operands into `x.neg()`. Same return shape as
// tryOperatorSpecDesugar.
func (c *checker) tryUnaryOperatorSpecDesugar(e *UnaryExpr, t *Type) (bool, *Type, error) {
	if e.Op != UnaryNeg {
		return false, nil, nil
	}
	impl := c.lookupImplCrossMod(t.Name, "Arithmetic")
	if impl == nil {
		return false, nil, nil
	}
	mc := &MethodCallExpr{
		Pos:       e.Pos,
		Receiver:  e.Operand,
		Method:    "neg",
		MethodPos: e.Pos,
	}
	src, err := c.synthesisedMethodSourceFor(impl, "neg")
	if err != nil {
		return false, nil, err
	}
	if _, err := c.bindResolvedMethodCall(mc, src); err != nil {
		return false, nil, err
	}
	e.Lowered = mc
	e.setType(mc.Type())
	return true, mc.Type(), nil
}

// synthesisedMethodSourceFor builds a *methodSource for a synthesised
// operator-desugar call. Mirrors what buildMethodVisibility would have
// recorded had this impl been declared by the user.
func (c *checker) synthesisedMethodSourceFor(impl *Impl, methodName string) (*methodSource, error) {
	im := impl.methodIdx[methodName]
	if im == nil {
		return nil, typeErr(impl.Pos, "internal: impl on %s for %s missing method %q", impl.TypeName, impl.SpecName, methodName)
	}
	return &methodSource{
		kind:     mskSpec,
		pos:      im.pos,
		name:     methodName,
		specName: impl.SpecName,
		impl:     impl,
		implFn:   im,
	}, nil
}
