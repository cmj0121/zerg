package syntax

// v0.7 Unit 2 — typeck for channel constructors, send / receive operators,
// the close() built-in, and `for v in ch` desugaring.
//
// The `chan` name is a built-in generic type — same plumbing as v0.6's
// Option / Result, except chan lives entirely outside the user-facing enum
// machinery. resolveTypeRef intercepts `chan[T]` ahead of the regular
// generic-decl path; chanInstance produces (and caches) the canonical
// *Type{Kind: TypeChan, Element: T} so two uses of `chan[int]` share one
// pointer for downstream pointer-equality dispatch.
//
// `close` is recognised in checkCallHint as a built-in fn name — same shape
// as `len` / `push` / `clone`, but with no fnSig entry because the built-in
// is type-driven (the arg must be chan-typed) rather than name-driven.

// isReservedV07BuiltinName reports whether name collides with a v0.7 built-in
// that may not be redeclared at any binding site (let / mut / const /
// fn / struct / enum / spec / impl-method param). v0.7 reserves `chan` as a
// type-position keyword; future builtins append.
func isReservedV07BuiltinName(name string) bool {
	return name == "chan"
}

// isChanType reports whether t is a chan[T] instance.
func isChanType(t *Type) bool {
	return t != nil && t.Kind == TypeChan
}

// chanInstance returns the canonical *Type for `chan[elem]`. Caches by the
// elem's Type.String() form so two resolutions of `chan[int]` produce one
// pointer. The cache lives on the checker so a Bundle's per-module checkers
// each have their own — chan instances aren't shared cross-module today
// because no module exposes a chan-typed binding (channels are values, not
// types in the import surface). If that ever changes, the cache promotes to
// crossMod.bundleMono like monoEnums / monoStructs.
func (c *checker) chanInstance(elem *Type) *Type {
	if c.monoChans == nil {
		c.monoChans = map[string]*Type{}
	}
	key := "chan[" + elem.String() + "]"
	if t, ok := c.monoChans[key]; ok {
		return t
	}
	t := &Type{Kind: TypeChan, Element: elem}
	c.monoChans[key] = t
	return t
}

// resolveChanTypeRef handles a `TypeRef{Name: "chan", TypeArgs: [...]}` shape
// in type position (e.g. `let ch: chan[int] = ...`, `fn run(ch: chan[int])`).
// Validates arity (exactly one type-arg) and rejects void elements with the
// same diagnostic shape as list / tuple. Caller (resolveTypeRef) routes here
// when ref.Name == "chan" and len(ref.TypeArgs) > 0; a bare `chan` with no
// args reaches the named-type path and is reported via the unknown-type
// diagnostic with chan's specific reservation message.
func (c *checker) resolveChanTypeRef(ref *TypeRef) (*Type, error) {
	if len(ref.TypeArgs) != 1 {
		return nil, typeErr(ref.Pos,
			"chan takes exactly one type argument, got %d", len(ref.TypeArgs))
	}
	elem, err := c.resolveTypeRef(ref.TypeArgs[0])
	if err != nil {
		return nil, err
	}
	if elem == nil {
		return nil, typeErr(ref.Pos, "chan element type failed to resolve")
	}
	if elem == tVoid {
		return nil, typeErr(ref.Pos, "chan element type cannot be void")
	}
	t := c.chanInstance(elem)
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

// checkChanConstructor handles `chan[T]()` (unbuffered) and `chan[T](N)`
// (buffered). The result type is the canonical chan[T]. Capacity, when
// present, must be int — typeck checks the type only; runtime panics on a
// negative value (matches Go).
func (c *checker) checkChanConstructor(e *ChanConstructorExpr) (*Type, error) {
	elem, err := c.resolveTypeRef(e.Element)
	if err != nil {
		return nil, err
	}
	if elem == nil {
		return nil, typeErr(e.Pos, "chan element type failed to resolve")
	}
	if elem == tVoid {
		return nil, typeErr(e.Pos, "chan element type cannot be void")
	}
	t := c.chanInstance(elem)
	if e.Capacity != nil {
		ct, err := c.checkExprHint(e.Capacity, tInt)
		if err != nil {
			return nil, err
		}
		if ct != tInt {
			return nil, typeErr(e.Capacity.ExprPos(),
				"chan capacity must be int, got %s", ct)
		}
	}
	e.setType(t)
	return t, nil
}

// checkSend type-checks a `ch <- v` send statement: ch must be chan[T] and v
// must be assignable to T (with the v0.6 T → T? lift fired when T is an
// Option instance).
func (c *checker) checkSend(s *SendStmt) error {
	chT, err := c.checkExpr(s.Chan)
	if err != nil {
		return err
	}
	if !isChanType(chT) {
		return typeErr(s.Chan.ExprPos(),
			"send requires a channel on the left, got %s", chT)
	}
	elem := chT.Element
	newVal, vt, err := c.checkExprLift(s.Value, elem)
	if err != nil {
		return err
	}
	if newVal != s.Value {
		s.Value = newVal
	}
	if !c.assignableTo(vt, elem) {
		return typeErr(s.Value.ExprPos(),
			"cannot send value of type %s on %s", vt, chT)
	}
	return nil
}

// checkRecv type-checks a `<- ch` prefix-receive expression. The operand
// must be chan[T]; the expression's type is Option[T] so the closed-channel
// case lands as None and `for v in ch` desugars on the v0.6 Option machinery.
func (c *checker) checkRecv(e *RecvExpr) (*Type, error) {
	chT, err := c.checkExpr(e.Chan)
	if err != nil {
		return nil, err
	}
	if !isChanType(chT) {
		return nil, typeErr(e.Pos,
			"receive requires a channel operand, got %s", chT)
	}
	wrapped, err := c.wrapOption(chT.Element, e.Pos)
	if err != nil {
		return nil, err
	}
	e.setType(wrapped)
	return wrapped, nil
}

// recognizeCloseCall returns true when e is a v0.7 `close(ch)` call shape —
// the callee is a bare IdentExpr named "close" and the user has NOT shadowed
// `close` with a local binding or a same-named user fn. The call dispatch in
// checkCallHint uses this to route to checkCloseCall before the regular fn
// lookup so unknown-fn errors don't fire.
//
// The reservation diagnostic at name-binding sites prevents users from ever
// shadowing `close` with a local fn / let / mut / const / param, so this
// helper's "shadow" check is defensive — once the reservation fires, the
// only path that can reach checkCallHint with callee name "close" is the
// genuine built-in.
func recognizeCloseCall(e *CallExpr) bool {
	id, ok := e.Callee.(*IdentExpr)
	if !ok {
		return false
	}
	return id.Name == "close"
}

// checkCloseCall type-checks the v0.7 `close(ch)` built-in. Arity is fixed
// at 1; the argument must be chan-typed. Result is void (statement-expr).
func (c *checker) checkCloseCall(e *CallExpr, ident *IdentExpr) (*Type, error) {
	if len(e.Args) != 1 {
		return nil, typeErr(e.Pos,
			"function %q expects 1 argument, got %d", ident.Name, len(e.Args))
	}
	at, err := c.checkExpr(e.Args[0])
	if err != nil {
		return nil, err
	}
	if !isChanType(at) {
		return nil, typeErr(e.Args[0].ExprPos(),
			"close: argument must be a channel, got %s", at)
	}
	ident.setType(tVoid)
	e.setType(tVoid)
	return tVoid, nil
}
