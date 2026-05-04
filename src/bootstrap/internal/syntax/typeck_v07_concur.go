package syntax

// v0.7 Unit 3 — typeck for anonymous functions (with closure-capture
// analysis), the `spawn` statement, the `defer` statement, and the
// `wait_group` / WaitGroup built-in.
//
// Anonymous functions resolve to a fresh TypeFn whose param vector and
// return type are the resolved annotations. Capture analysis walks the body
// after the parameter scope is in place: every IDENT reference whose
// binding lives in a scope OUTSIDE the anon-fn frame is recorded on the
// AnonFnExpr.Captures slice. Captures must be immutable — capturing a
// `mut` outer binding rejects with the "cannot capture mut binding"
// diagnostic. Captures of fn names (regular FnDecl callees, built-ins) are
// admitted because fn names aren't bindings in the value-scope at all —
// they live in c.fns.
//
// `spawn <call>` validates the call is a fn-call shape (CallExpr or
// MethodCallExpr) and type-checks it as if it were a regular call statement.
// The result is discarded. `?` propagation cannot leave a spawn — the
// parser's `spawn <expr>` shape can syntactically contain a propagate; we
// reject at typeck so the v0.6 early-return semantics don't escape the
// task boundary.
//
// `defer <stmt>` walks its body through the regular stmt typecheck path;
// the only side-effect at typeck time is recording HasDefers on the
// enclosing FnDecl / AnonFnExpr so downstream halves can dispatch.
//
// `wait_group()` is a built-in fn returning a synthetic `WaitGroup` struct
// type with three methods (`add(n: int)`, `done()`, `wait()`). Reservation
// fires at every binding site — `let wait_group := 1`, `struct WaitGroup
// {}`, etc. all reject with the uniform reservation diagnostic.

// reservedV07ConcurNames is the set of additional v0.7 names users may not
// redeclare at any binding site. The list extends — `chan` / `close` are
// owned by Unit 2's reservation set; `WaitGroup` belongs to the type-name
// set in typeck_v06_builtin.go (added there directly so the
// collectTopLevel struct/enum/spec checks pick it up).
func isReservedV07ConcurName(name string) bool {
	switch name {
	case "wait_group", "WaitGroup", "spawn", "defer", "select":
		return true
	}
	return false
}

// anonFnFrame is one rung of the AnonFnExpr typecheck stack. parentScope is
// the scope chain head at the moment the anon-fn started its body walk —
// any IdentExpr whose binding resolves into this chain (and not into the
// inner param/body scopes) is a capture. captures collects unique-by-name
// records in encounter order.
type anonFnFrame struct {
	parentScope *scope
	captures    []Capture
	captureSet  map[string]bool
	anonNode    *AnonFnExpr
	hasDefers   bool
}

// pushAnonFnFrame records a new frame on the checker stack. Returns the
// frame so the caller can mutate the captures list as the body walk runs.
func (c *checker) pushAnonFnFrame(anon *AnonFnExpr, parent *scope) *anonFnFrame {
	f := &anonFnFrame{
		parentScope: parent,
		captureSet:  map[string]bool{},
		anonNode:    anon,
	}
	c.anonFrames = append(c.anonFrames, f)
	return f
}

// popAnonFnFrame removes the most-recent frame; the caller must ensure
// pushAnonFnFrame and popAnonFnFrame are paired.
func (c *checker) popAnonFnFrame() *anonFnFrame {
	n := len(c.anonFrames)
	f := c.anonFrames[n-1]
	c.anonFrames = c.anonFrames[:n-1]
	return f
}

// noteIdentResolved is called by checkExpr right after a successful IDENT
// lookup. It walks the active anon-fn frame stack from innermost outward;
// for each frame whose parentScope chain contains the binding, it records
// a capture (unless one is already recorded). Capture-of-mut rejects.
//
// The frame stack matters because nested anon-fns inherit captures from
// their enclosing anon-fn's locals: `fn() { let x := 1; let f := fn() {
// print x } }` — x is captured by the inner fn but not the outer (it's a
// local of the outer). The walk records the capture only on frames where
// the binding lives outside that frame's own scope chain (i.e. in the
// frame's parentScope chain or further out).
func (c *checker) noteIdentResolved(name string, pos Position, b binding, definingScope *scope) error {
	for i := len(c.anonFrames) - 1; i >= 0; i-- {
		f := c.anonFrames[i]
		if !scopeContains(f.parentScope, definingScope) {
			// Binding lives in this frame's own (param/body) scope or in an
			// inner anon-fn — not a capture for this frame.
			return nil
		}
		if b.kind == bindMut {
			return typeErr(pos,
				"cannot capture mut binding %q in closure (only immutable bindings can be captured)",
				name)
		}
		if !f.captureSet[name] {
			f.captureSet[name] = true
			f.captures = append(f.captures, Capture{
				Name: name,
				Pos:  pos,
				Type: b.typ,
			})
		}
	}
	return nil
}

// scopeContains reports whether target is reachable from the head scope by
// walking parent links — i.e. target is the head, an ancestor of head, or
// head itself. Used by noteIdentResolved to decide whether a binding's
// defining scope is outside the anon-fn's own scope range.
func scopeContains(head, target *scope) bool {
	for s := head; s != nil; s = s.parent {
		if s == target {
			return true
		}
	}
	return false
}

// lookupWithScope walks the scope chain returning both the binding and the
// scope in which it was found. The standard scope.lookup discards the
// owning-scope; capture analysis needs it to decide which anon-fn frame (if
// any) sees the binding as an outer reference.
func (s *scope) lookupWithScope(name string) (binding, *scope, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if b, ok := cur.names[name]; ok {
			return b, cur, true
		}
	}
	return binding{}, nil, false
}

// checkAnonFnExpr type-checks an `fn(params) [-> R] { body }` expression.
// Result type is a TypeFn with the resolved param/return vector. Capture
// analysis runs while the body is being walked: every outer-binding
// reference is added to anon.Captures (unique-by-name, immutable-only).
func (c *checker) checkAnonFnExpr(anon *AnonFnExpr) (*Type, error) {
	params := make([]*Type, len(anon.Params))
	for i := range anon.Params {
		t, err := c.resolveTypeRef(anon.Params[i].Type)
		if err != nil {
			return nil, err
		}
		if t == tVoid {
			return nil, typeErr(anon.Params[i].Pos,
				"parameter %q cannot have void type", anon.Params[i].Name)
		}
		if isReservedV07BuiltinName(anon.Params[i].Name) ||
			isReservedV07ConcurName(anon.Params[i].Name) ||
			anon.Params[i].Name == "close" {
			return nil, typeErr(anon.Params[i].Pos,
				"name %q is reserved (built-in)", anon.Params[i].Name)
		}
		params[i] = t
	}
	ret := tVoid
	if anon.Return != nil {
		rt, err := c.resolveTypeRef(anon.Return)
		if err != nil {
			return nil, err
		}
		if rt == tVoid {
			return nil, typeErr(anon.Return.Pos,
				"use no return annotation instead of declaring a void return")
		}
		ret = rt
	}

	// Snapshot the parent scope so capture analysis can tell apart inner
	// (param/body) bindings from outer bindings reached through the chain.
	parentScope := c.scope

	// Push the frame BEFORE we open the param scope so noteIdentResolved
	// sees the anon-fn's own scopes as "inside".
	frame := c.pushAnonFnFrame(anon, parentScope)
	defer c.popAnonFnFrame()

	// Synthetic fnSig so `return expr` inside the body type-checks against
	// the declared return type. The currentFn pointer also gates `return`
	// usage at all (return outside a fn rejects).
	sig := fnSig{params: params, ret: ret, pos: anon.Pos}
	savedFn := c.currentFn
	c.currentFn = &sig
	defer func() { c.currentFn = savedFn }()

	c.scope = newScope(parentScope)
	defer func() { c.scope = parentScope }()

	for i, p := range anon.Params {
		if !c.scope.declare(p.Name, binding{kind: bindLet, typ: params[i]}) {
			return nil, typeErr(p.Pos, "parameter %q already declared", p.Name)
		}
	}
	for _, st := range anon.Body.Statements {
		if err := c.checkStmt(st); err != nil {
			return nil, err
		}
	}

	anon.Captures = frame.captures
	anon.HasDefers = frame.hasDefers
	t := &Type{Kind: TypeFn, FnParams: params, FnReturn: ret}
	anon.setType(t)
	return t, nil
}

// checkSpawnStmt type-checks a `spawn <call>` statement. The parser has
// already narrowed Call to *CallExpr or *MethodCallExpr; the only typeck
// constraint beyond the regular call-typeck path is that the call must not
// propagate `?` (Unit 5.5 owns the defer × ? interaction; spawn is a
// separate task and `?` cannot escape it).
func (c *checker) checkSpawnStmt(s *SpawnStmt) error {
	// Reject `spawn foo()?` — a CallExpr / MethodCallExpr wrapped in a
	// PropagateExpr would already be unreachable (the parser narrows to a
	// call shape), but the body of a call could contain `?` in arg
	// positions; that's fine. The constraint is only about `?` ON the
	// spawned call itself, which the narrowing already prevents. We keep
	// the diagnostic for forward-compat (a future PropagateExpr-of-call
	// shape would surface here).
	if _, ok := s.Call.(*PropagateExpr); ok {
		return typeErr(s.Pos, "'?' cannot leave a spawn")
	}
	switch s.Call.(type) {
	case *CallExpr, *MethodCallExpr:
		// fall-through
	default:
		return typeErr(s.Pos, "spawn requires a function call expression, got %T", s.Call)
	}
	if _, err := c.checkExpr(s.Call); err != nil {
		return err
	}
	return nil
}

// checkDeferStmt type-checks a `defer <body>` statement. Defer is admitted
// only inside a fn body (parser enforces fn-body-scope-only); typeck
// records HasDefers on the enclosing fn / anon-fn so downstream halves can
// dispatch.
//
// The body is a *Block by parser-construction; we walk it through the
// standard block-walk path so any inner statements / expressions
// type-check normally. Diagnostics inside the body anchor on the inner
// statements rather than the defer keyword.
func (c *checker) checkDeferStmt(s *DeferStmt) error {
	if c.currentFn == nil {
		return typeErr(s.Pos, "'defer' outside of a function")
	}
	if err := c.checkBlock(s.Body); err != nil {
		return err
	}
	// Mark the enclosing fn as having a defer. Anon-fns surface through the
	// frame stack; named FnDecls surface through c.currentFnDecl which is
	// set by checkFnDecl / checkImplBodies / checkSpecBodies.
	if len(c.anonFrames) > 0 {
		c.anonFrames[len(c.anonFrames)-1].hasDefers = true
		return nil
	}
	if c.currentFnDecl != nil {
		c.currentFnDecl.HasDefers = true
	}
	return nil
}

// ---------------------------------------------------------------------------
// wait_group / WaitGroup built-in.
// ---------------------------------------------------------------------------

// builtinWaitGroupTypeName is the name of the synthetic WaitGroup struct
// type. The reservation diagnostic at every binding site uses this name;
// the type itself is constructed once per checker by injectWaitGroupBuiltin
// and cached on c.waitGroupType so pointer-equality dispatch works.
const builtinWaitGroupTypeName = "WaitGroup"

// injectWaitGroupBuiltin wires the v0.7 WaitGroup type and the
// `wait_group()` constructor fn into c's tables. Called from newChecker so
// every module's collect pass already sees the names.
//
// The WaitGroup is modelled as an opaque struct with no fields. Its three
// methods (`add(n: int)`, `done()`, `wait()`) are added to c.methodVisible
// so dispatchConcreteMethod resolves them — without going through an
// ImplDecl. The hand-built methods carry a synthetic *implMethod with
// Receiver pointing at the WaitGroup type.
func injectWaitGroupBuiltin(c *checker) {
	wg := &Type{Kind: TypeStruct, Name: builtinWaitGroupTypeName}
	c.waitGroupType = wg
	c.structs[builtinWaitGroupTypeName] = wg

	addM := &implMethod{
		pos:    Position{},
		name:   "add",
		params: []*Type{tInt},
		ret:    tVoid,
	}
	doneM := &implMethod{
		pos:    Position{},
		name:   "done",
		params: nil,
		ret:    tVoid,
	}
	waitM := &implMethod{
		pos:    Position{},
		name:   "wait",
		params: nil,
		ret:    tVoid,
	}
	impl := &Impl{
		Pos:       Position{},
		TypeName:  builtinWaitGroupTypeName,
		SpecName:  "",
		Receiver:  wg,
		Methods:   []*implMethod{addM, doneM, waitM},
		methodIdx: map[string]*implMethod{"add": addM, "done": doneM, "wait": waitM},
	}
	c.impls[implKey{typeName: builtinWaitGroupTypeName, specName: ""}] = impl
	c.implsByType[builtinWaitGroupTypeName] = []*Impl{impl}

	visible := map[string][]*methodSource{}
	for _, m := range impl.Methods {
		src := &methodSource{
			kind:     mskInherent,
			name:     m.name,
			impl:     impl,
			implFn:   m,
			inherent: m,
		}
		visible[m.name] = []*methodSource{src}
	}
	c.methodVisible[builtinWaitGroupTypeName] = visible

	c.fns["wait_group"] = fnSig{
		params:  nil,
		ret:     wg,
		builtin: true,
	}
}

// checkFnValueCall type-checks a CallExpr whose callee is a fn-typed value
// (either an AnonFnExpr IIFE or a let-bound fn value). The call's arity and
// per-position arg types must match the TypeFn's FnParams; the result type
// is FnReturn (or void when nil). `name` is used in diagnostics — the
// caller passes a friendly label (e.g. an ident name or "anonymous
// function").
func (c *checker) checkFnValueCall(e *CallExpr, ft *Type, name string) (*Type, error) {
	if len(e.Args) != len(ft.FnParams) {
		return nil, typeErr(e.Pos,
			"function %q expects %d argument(s), got %d", name, len(ft.FnParams), len(e.Args))
	}
	for i := range e.Args {
		newExpr, at, err := c.checkExprLift(e.Args[i], ft.FnParams[i])
		if err != nil {
			return nil, err
		}
		if newExpr != e.Args[i] {
			e.Args[i] = newExpr
		}
		if !c.assignableTo(at, ft.FnParams[i]) {
			return nil, typeErr(e.Args[i].ExprPos(),
				"argument %d to %q has type %s, expected %s",
				i+1, name, at, ft.FnParams[i])
		}
	}
	ret := ft.FnReturn
	if ret == nil {
		ret = tVoid
	}
	e.setType(ret)
	return ret, nil
}

// recognizeWaitGroupCall returns true when e is a v0.7 `wait_group()` call
// shape — the callee is a bare IdentExpr named "wait_group" and the
// reservation has prevented user shadowing.
func recognizeWaitGroupCall(e *CallExpr) bool {
	id, ok := e.Callee.(*IdentExpr)
	if !ok {
		return false
	}
	return id.Name == "wait_group"
}

// checkWaitGroupCall type-checks the v0.7 `wait_group()` built-in. Arity 0;
// result is the canonical WaitGroup struct type.
func (c *checker) checkWaitGroupCall(e *CallExpr, ident *IdentExpr) (*Type, error) {
	if len(e.Args) != 0 {
		return nil, typeErr(e.Pos,
			"function %q expects 0 arguments, got %d", ident.Name, len(e.Args))
	}
	if c.waitGroupType == nil {
		return nil, typeErr(e.Pos, "internal: WaitGroup type not registered")
	}
	ident.setType(c.waitGroupType)
	e.setType(c.waitGroupType)
	return c.waitGroupType, nil
}
