package syntax

// v0.17 — operator-spec wiring.
//
// Eight built-in specs (Add / Sub / Mul / Div / Mod / Neg / Eq / Ord) are
// injected into c.specs at newChecker() time. User syntax is
//
//   impl BigInt for Add { fn add(other: BigInt) -> BigInt { ... } }
//
// — no `[T]` argument; the receiver type IS the implicit T.
//
// After spec injection, per-primitive synthetic *Impl records are wired
// into c.impls and c.implsByType. They carry IsPrimitiveBuiltin = true
// and ast == nil method entries; cgen/run check the flag at dispatch
// time and route to the existing primitive-arithmetic emit/eval paths
// instead of attempting a fn-body call.
//
// The synthetic *Impl records make `fn sum[T: Add[T]](xs: list[T]) -> T`
// compose uniformly across int and user types: assignableTo's
// concrete→spec widening path finds either the user-declared impl or a
// synthetic primitive impl in c.impls.

// operatorSpec is one entry in the operator-spec catalog. Coverage
// captures the primitive *Type values that automatically satisfy the
// spec via a synthetic primitive impl.
type operatorSpec struct {
	spec     string
	method   string
	coverage []*Type
}

// operatorSpecCatalog enumerates the eight built-in operator specs and
// the primitive types that auto-satisfy each. Declaration order is
// stable across runs so impl-table iteration is deterministic.
//
// Coverage:
//   Add..Mod  int, float
//   Neg       int, float
//   Ord       int, float, byte, rune, str
//   Eq        int, float, byte, rune, str, bool
var operatorSpecCatalog = []operatorSpec{
	{"Add", "add", []*Type{tInt, tFloat}},
	{"Sub", "sub", []*Type{tInt, tFloat}},
	{"Mul", "mul", []*Type{tInt, tFloat}},
	{"Div", "div", []*Type{tInt, tFloat}},
	{"Mod", "mod", []*Type{tInt, tFloat}},
	{"Neg", "neg", []*Type{tInt, tFloat}},
	{"Ord", "cmp", []*Type{tInt, tFloat, tByte, tRune, tStr}},
	{"Eq", "eq", []*Type{tInt, tFloat, tByte, tRune, tStr, tBool}},
}

// operatorSpecByName indexes operatorSpecCatalog by spec name for
// constant-time lookup at hot dispatch sites.
var operatorSpecByName = func() map[string]*operatorSpec {
	m := make(map[string]*operatorSpec, len(operatorSpecCatalog))
	for i := range operatorSpecCatalog {
		op := &operatorSpecCatalog[i]
		m[op.spec] = op
	}
	return m
}()

// injectOperatorSpecs registers the eight synthetic operator specs in
// c.specs. The method-shape (receiver-typed for arity-2 specs, no-arg
// for Neg, bool-returning for Eq, int-returning for Ord) is checked at
// impl-resolution time via validateOperatorSpecImpl — the synthetic
// methods here carry params/ret = nil because the substituted shape
// depends on each impl's receiver type T.
func injectOperatorSpecs(c *checker) {
	for _, op := range operatorSpecCatalog {
		sm := &specMethod{name: op.method, Synthetic: true}
		c.specs[op.spec] = &Spec{
			Name:      op.spec,
			Methods:   []*specMethod{sm},
			methodIdx: map[string]*specMethod{op.method: sm},
			Synthetic: true,
			// Carry a canonical TypeSpec so generic-fn bound
			// validation (`fn sum[T: Add[T]]`) accepts the bound
			// as a spec reference. The bound's type-arg is
			// advisory — the actual T is the bound's owner.
			typ: NewSpecType(op.spec),
		}
	}
}

// injectPrimitiveOperatorImpls wires synthetic primitive impls into
// c.impls and c.implsByType for every (primitive, applicable spec)
// pair. Called from newChecker after injectOperatorSpecs. Each impl
// has IsPrimitiveBuiltin = true and one *implMethod with params/ret
// set to the substituted shape (so typeck arity checking still
// applies) and ast == nil (cgen/run see the flag and bypass fn-body
// lookup, routing to the primitive emit/eval path).
func injectPrimitiveOperatorImpls(c *checker) {
	for _, op := range operatorSpecCatalog {
		for _, t := range op.coverage {
			c.installPrimitiveOperatorImpl(t, &op)
		}
	}
}

// installPrimitiveOperatorImpl materialises a single synthetic *Impl
// for (recv, op). Idempotent: a duplicate call for the same
// (recv, op.spec) is a no-op.
func (c *checker) installPrimitiveOperatorImpl(recv *Type, op *operatorSpec) {
	typeName := primitiveTypeName(recv)
	if typeName == "" {
		return
	}
	key := implKey{typeName: typeName, specName: op.spec}
	if _, exists := c.impls[key]; exists {
		return
	}
	im := &implMethod{
		name:   op.method,
		params: operatorMethodParams(op.spec, recv),
		ret:    operatorMethodReturn(op.spec, recv),
	}
	impl := &Impl{
		TypeName:           typeName,
		SpecName:           op.spec,
		Receiver:           recv,
		Methods:            []*implMethod{im},
		methodIdx:          map[string]*implMethod{op.method: im},
		IsPrimitiveBuiltin: true,
	}
	c.impls[key] = impl
	c.implsByType[typeName] = append(c.implsByType[typeName], impl)
}

// operatorMethodParams returns the substituted parameter-type slice
// for an operator method on a receiver of type recv. Neg is the only
// no-arg spec; everything else takes one same-typed argument.
func operatorMethodParams(specName string, recv *Type) []*Type {
	if specName == "Neg" {
		return nil
	}
	return []*Type{recv}
}

// operatorMethodReturn returns the substituted return type for an
// operator method. Eq returns bool; Ord returns int; everything else
// returns the receiver type.
func operatorMethodReturn(specName string, recv *Type) *Type {
	switch specName {
	case "Eq":
		return tBool
	case "Ord":
		return tInt
	}
	return recv
}

// validateOperatorSpecImpl is the impl-conformance check for a synthetic
// operator spec. It substitutes the impl's receiver type for the
// implicit T and verifies the method's signature matches the canonical
// shape. Called from validateImplAgainstSpec when spec.Synthetic is set.
func (c *checker) validateOperatorSpecImpl(id *ImplDecl, methods []*implMethod, spec *Spec) error {
	op, ok := operatorSpecByName[spec.Name]
	if !ok {
		return typeErr(id.Pos, "internal: unknown operator spec %q", spec.Name)
	}
	if len(methods) != 1 {
		return typeErr(id.Pos,
			"impl of %q must declare exactly one method (%q), got %d",
			spec.Name, op.method, len(methods))
	}
	im := methods[0]
	if im.name != op.method {
		return typeErr(im.pos,
			"impl of %q expects method %q, got %q",
			spec.Name, op.method, im.name)
	}
	recv := id.Receiver
	if recv == nil {
		return typeErr(id.Pos, "internal: impl receiver unresolved for operator spec %q", spec.Name)
	}
	wantParams := operatorMethodParams(spec.Name, recv)
	if len(im.params) != len(wantParams) {
		return typeErr(im.pos,
			"method %q in impl of %q expects %d parameter(s), got %d",
			op.method, spec.Name, len(wantParams), len(im.params))
	}
	for i, want := range wantParams {
		if !typeEq(im.params[i], want) {
			return typeErr(im.pos,
				"method %q in impl of %q parameter %d type %s does not match %s",
				op.method, spec.Name, i+1, im.params[i], want)
		}
	}
	wantRet := operatorMethodReturn(spec.Name, recv)
	if !typeEq(im.ret, wantRet) {
		return typeErr(im.pos,
			"method %q in impl of %q return type %s does not match %s",
			op.method, spec.Name, im.ret, wantRet)
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

// IsOperatorSpecName reports whether specName is one of the eight
// synthetic operator specs injected at newChecker time. cgen / run
// use this check to skip vtable emission and spec-value bindings —
// operator specs never appear as a value type, only as an impl
// target or a generic-fn bound.
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
// surface op. binDesugarPlain — the method-call result IS the binary
// result. binDesugarEqNot — surface op was `!=`, so negate the bool.
// binDesugarCmp — surface op was `< <= > >=`, so compare cmp()
// against 0 with the surface op.
type binDesugarKind int

const (
	binDesugarNone binDesugarKind = iota
	binDesugarPlain
	binDesugarEqNot
	binDesugarCmp
)

// operatorDispatchFor maps a BinaryOp to (specName, methodName,
// composition-kind). Returns ("", "", binDesugarNone) for ops that
// don't take part in operator-spec desugar (bit ops, logical ops,
// floor-div).
func operatorDispatchFor(op BinaryOp) (string, string, binDesugarKind) {
	switch op {
	case BinAdd:
		return "Add", "add", binDesugarPlain
	case BinSub:
		return "Sub", "sub", binDesugarPlain
	case BinMul:
		return "Mul", "mul", binDesugarPlain
	case BinDiv:
		return "Div", "div", binDesugarPlain
	case BinMod:
		return "Mod", "mod", binDesugarPlain
	case BinEq:
		return "Eq", "eq", binDesugarPlain
	case BinNE:
		return "Eq", "eq", binDesugarEqNot
	case BinLT, BinLE, BinGT, BinGE:
		return "Ord", "cmp", binDesugarCmp
	}
	return "", "", binDesugarNone
}

// tryOperatorSpecDesugar attempts to lower a binary operator on user-
// struct / user-enum operands into the corresponding spec-method call.
// Returns (true, resultType, nil) on success — caller treats the
// BinaryExpr as fully checked. Returns (false, nil, nil) when no
// matching impl exists; caller falls through to the primitive arms,
// which will reject with the standard same-type diagnostic.
func (c *checker) tryOperatorSpecDesugar(e *BinaryExpr, t *Type) (bool, *Type, error) {
	specName, methodName, kind := operatorDispatchFor(e.Op)
	if specName == "" {
		return false, nil, nil
	}
	impl := c.lookupImplCrossMod(t.Name, specName)
	if impl == nil {
		return false, nil, nil
	}
	mc := &MethodCallExpr{
		Pos:       e.Pos,
		Receiver:  e.Left,
		Method:    methodName,
		MethodPos: e.Pos,
		Args:      []Expr{e.Right},
	}
	src, err := c.synthesisedMethodSourceFor(impl, methodName)
	if err != nil {
		return false, nil, err
	}
	if _, err := c.bindResolvedMethodCall(mc, src); err != nil {
		return false, nil, err
	}
	e.Lowered = mc
	switch kind {
	case binDesugarPlain:
		e.setType(mc.Type())
		return true, mc.Type(), nil
	case binDesugarEqNot:
		e.LoweredNot = true
		e.setType(tBool)
		return true, tBool, nil
	case binDesugarCmp:
		e.LoweredCmpOp = e.Op
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
	impl := c.lookupImplCrossMod(t.Name, "Neg")
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
