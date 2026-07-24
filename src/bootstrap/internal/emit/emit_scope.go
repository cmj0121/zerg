package emit

// This file carries the Phase 1d iteration-3 additions to the C backend: drop
// ORDER on every exit path plus `defer`/`del`/`with` (U4/U5). The whole surface is
// additive and driven by the runtime cleanup stack: a scope that owns any teardown
// records a mark at entry (zrt_scope_mark), registers each Ref release and `defer`
// thunk on the stack, and unwinds it (zrt_unwind_to) at every normal exit —
// fallthrough, `return` (value copied out first), `break`, and `continue`. An abort
// unwinds the same stack from zrt_abort, so a `defer` and a Ref drop each run
// exactly once per path, including the abort path.
//
// A value-only program (every numbered example) owns no teardown, so it records no
// mark, registers nothing, and emits no unwind — its C is byte-identical to Phase 0.

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// scope is one lexical scope's teardown frame. markVar is the C variable holding
// its zrt_scope_mark height (empty when the scope owns no teardown). isLoop marks a
// loop body, the target a `break`/`continue` unwinds to. items records the scope's
// droppable bindings so a `del name` can find a binding's type.
type scope struct {
	markVar string
	isLoop  bool
	items   []dropItem
}

// dropItem is one droppable binding: its C name and its type. Whether a `del`
// revoked it is not tracked here: the runtime slot guard makes a Ref's release
// idempotent (a NULL slot is skipped), and sema rejects any use-after-del, so the
// backend never needs a compile-time liveness flag.
type dropItem struct {
	cname string
	typ   sema.Type
}

// openScope pushes a teardown frame. When markNeeded the scope records a
// zrt_scope_mark to unwind back to; a teardown-free scope records nothing, so a
// value-only program emits no mark. isLoop marks a `break`/`continue` target.
func (e *emitter) openScope(markNeeded, isLoop bool) {
	mv := ""
	if markNeeded {
		mv = e.freshName("mk")
		e.line(fmt.Sprintf("size_t %s = zrt_scope_mark();", mv))
	}
	e.drops = append(e.drops, &scope{markVar: mv, isLoop: isLoop})
}

// closeScope pops the innermost teardown frame, unwinding its mark (running the
// scope's registered releases and defers LIFO) when it owns one.
func (e *emitter) closeScope() {
	top := e.popScopeRaw()
	if top.markVar != "" {
		e.line(fmt.Sprintf("zrt_unwind_to(%s);", top.markVar))
	}
}

// popScopeRaw pops the innermost teardown frame without emitting its unwind — used
// by the function root, whose unwind is emitted explicitly around the return value.
func (e *emitter) popScopeRaw() *scope {
	top := e.drops[len(e.drops)-1]
	e.drops = e.drops[:len(e.drops)-1]
	return top
}

// curScope returns the innermost teardown frame.
func (e *emitter) curScope() *scope { return e.drops[len(e.drops)-1] }

// loopMark returns the mark var of the innermost enclosing loop body (empty when
// none owns teardown), the target a `break`/`continue` unwinds to.
func (e *emitter) loopMark() string {
	for i := len(e.drops) - 1; i >= 0; i-- {
		if e.drops[i].isLoop {
			return e.drops[i].markVar
		}
	}
	return ""
}

// registerDrop schedules a droppable binding's teardown on the runtime cleanup
// stack at its point of declaration and records it for `del`. A Ref registers its
// slot with the zg_ref_drop guard (so a later `del`/reassignment retargets the
// release); a non-POD struct registers its address with the type's drop-env thunk.
// A POD binding owns no teardown and is not registered.
func (e *emitter) registerDrop(cname string, typ sema.Type, at ast.Node) {
	if !e.containsRef(typ) || len(e.drops) == 0 {
		return
	}
	top := e.curScope()
	top.items = append(top.items, dropItem{cname: cname, typ: typ})
	// A managed str local registers its slot with the zg_str_drop guard (S2), so a later
	// `del`/reassignment retargets the release and the cell is freed exactly once.
	if typ.Kind() == types.KStr {
		e.line(fmt.Sprintf("zrt_defer(zg_str_drop, &%s);", cname))
		return
	}
	// A boxed nullable-Opt local (`Node?`, S1) is a bare `void*` cell: it registers the
	// same zg_ref_drop slot guard as a Ref local, so a later `del`/reassignment retargets
	// the release (NULL-tolerant).
	if e.isBoxedOpt(typ) {
		e.line(fmt.Sprintf("zrt_defer(zg_ref_drop, &%s);", cname))
		return
	}
	switch t := typ.(type) {
	case *types.Fn:
		// a function-value local releases its refcounted environment box at scope exit
		// through the generic env-slot guard (a NULL env is a no-op) — managed only.
		e.line(fmt.Sprintf("zrt_defer(zg_fnval_drop, &%s);", cname))
	case *types.Ref:
		e.line(fmt.Sprintf("zrt_defer(zg_ref_drop, &%s);", cname))
	case *types.Enum:
		// a recursive/non-POD enum local (S1) frees its boxed payload cells at scope exit
		// through its generated drop-env thunk, like a struct.
		e.line(fmt.Sprintf("zrt_defer(zg_dropenv_%s, &%s);", e.ctype(typ), cname))
	case *types.List:
		// a list frees its buffer (and drops each element) at scope exit through its
		// instance's drop-env thunk, scheduled on the runtime cleanup stack.
		e.line(fmt.Sprintf("zrt_defer(%s, &%s);", e.listDropEnv(typ), cname))
	case *types.Map:
		// a map frees its storage (and drops each key/value) at scope exit through its
		// instance's drop-env thunk, scheduled on the runtime cleanup stack.
		e.line(fmt.Sprintf("zrt_defer(%s, &%s);", e.mapDropEnv(typ), cname))
	case *types.Chan:
		// a channel handle releases at scope exit through a slot guard (so a later `del`
		// nulls the slot); a send-capable handle releases as a sender (may auto-close).
		e.line(fmt.Sprintf("zrt_defer(%s, &%s);", e.chanDropThunk(t), cname))
	case *types.Struct:
		e.line(fmt.Sprintf("zrt_defer(zg_dropenv_%s, &%s);", e.ctype(typ), cname))
	case *types.Opt:
		// a non-boxed non-POD Opt local (`s: str?`, a `Ref[T]?`/`list[T]?`/…) frees its
		// payload at scope exit through its carrier's generated drop-env thunk, which
		// guards on presence (an absent carrier owns nothing). A boxed Opt took the
		// isBoxedOpt slot-guard path above; a POD Opt owns nothing and never reaches here.
		e.line(fmt.Sprintf("zrt_defer(%s, &%s);", e.optDropEnvName(typ), cname))
	default:
		// an unsupported non-POD shape (e.g. a tuple holding a Ref): report it against the
		// binding's own span rather than 0:0 (completeness iteration 3, F5).
		e.unsupportedRef(at, typ)
	}
}

// findDrop returns the drop item for a C name, searching innermost-out.
func (e *emitter) findDrop(cname string) (dropItem, bool) {
	for i := len(e.drops) - 1; i >= 0; i-- {
		for _, it := range e.drops[i].items {
			if it.cname == cname {
				return it, true
			}
		}
	}
	return dropItem{}, false
}

// emitInlineDrop releases a droppable binding in place (used by a reassignment to
// drop the old value before the new one is bound). Unlike a scope-exit release it
// runs immediately, on the normal path only.
func (e *emitter) emitInlineDrop(it dropItem) {
	if it.typ.Kind() == types.KStr {
		e.line(fmt.Sprintf("zrt_str_release(%s);", it.cname))
		return
	}
	if e.isBoxedOpt(it.typ) {
		e.line(fmt.Sprintf("zrt_release(%s);", it.cname))
		return
	}
	switch t := it.typ.(type) {
	case *types.Fn:
		// release a function-value local's refcounted environment box in place (a
		// reassignment drops the old closure before binding the new one) — managed only.
		e.line(fmt.Sprintf("zrt_release((%s).env);", it.cname))
	case *types.Ref:
		e.line(fmt.Sprintf("zrt_release(%s);", it.cname))
	case *types.Enum:
		e.line(fmt.Sprintf("%s(&%s);", e.dropHelperName(it.typ), it.cname))
	case *types.List:
		e.line(fmt.Sprintf("zrt_list_drop(&%s);", it.cname))
	case *types.Map:
		e.line(fmt.Sprintf("zrt_map_drop(&%s);", it.cname))
	case *types.Chan:
		e.line(fmt.Sprintf("%s(%s);", e.chanReleaseFn(t), it.cname))
	case *types.Struct:
		e.line(fmt.Sprintf("%s(&%s);", e.dropHelperName(it.typ), it.cname))
	case *types.Opt:
		// release a non-boxed non-POD Opt local's payload in place (a reassignment drops the
		// old optional before binding the new one), guarded on presence.
		e.line(e.optFieldDrop(t, it.cname))
	default:
		e.unsupportedRef(nil, it.typ)
	}
}

// --- del ----------------------------------------------------------------------

// delStmt lowers `del name` (GRAMMAR group 11). For a Ref/chan holder it drops a
// refcount now and nulls the slot, so the scope-exit guard skips it — the name is
// freed exactly once whether or not the `del` is reached (flow-consistent, no
// separate drop flag). For a POD/borrowed name the revoke has no C teardown.
func (e *emitter) delStmt(n *ast.DelStmt) {
	cname := e.resolve(n.Name)
	it, ok := e.findDrop(cname)
	if !ok {
		return // POD, a borrow (mut &), or a captured value: nothing to free
	}
	// `del s` of a managed str releases the cell now and nulls the slot, so the scope-exit
	// guard skips it — freed exactly once whether or not the `del` is reached (S2).
	if it.typ.Kind() == types.KStr {
		e.line(fmt.Sprintf("zrt_str_release(%s);", cname))
		e.line(fmt.Sprintf("%s = NULL;", cname))
		return
	}
	// `del x` of a boxed nullable-Opt local (S1) releases the cell now and nulls the slot,
	// so the scope-exit guard skips it — freed exactly once, like a Ref del.
	if e.isBoxedOpt(it.typ) {
		e.line(fmt.Sprintf("zrt_release(%s);", cname))
		e.line(fmt.Sprintf("%s = NULL;", cname))
		return
	}
	switch t := it.typ.(type) {
	case *types.Ref:
		// Release now and null the slot. The scope-exit guard reads the slot, so it
		// skips a nulled binding — the box is freed exactly once whether or not the
		// `del` is reached on a given path (flow-consistent, and safe under a
		// conditional del because zrt_release(NULL) is a no-op).
		e.line(fmt.Sprintf("zrt_release(%s);", cname))
		e.line(fmt.Sprintf("%s = NULL;", cname))
	case *types.Chan:
		// `del ch` (GRAMMAR group 11) drops a channel refcount now — and for a
		// send-capable handle, closes the channel if it was the last sender. Nulling the
		// slot makes the scope-exit guard skip it, so the drop happens exactly once.
		e.line(fmt.Sprintf("%s(%s);", e.chanReleaseFn(t), cname))
		e.line(fmt.Sprintf("%s = NULL;", cname))
	default:
		e.diags.Add(n.Span(), "del of an owning %s value is not supported in Phase 1d", it.typ)
	}
}

// --- with ---------------------------------------------------------------------

// withStmt lowers `with e (as y)? { body }` (GRAMMAR group 6) by desugaring it, in
// the backend, to a scoped binding: `{ y := e; body }`. The bound resource's Scoped
// teardown is its drop — for a Ref, the zg_ref_drop release the scope schedules —
// so teardown runs on every exit (fallthrough, early return/break, abort). An
// omitted `as y` binds a fresh hidden name held only for the scope's duration.
func (e *emitter) withStmt(n *ast.WithStmt) {
	rt := e.cur.ExprType(e.info, n.Resource)
	if !e.containsRef(rt) {
		// GATE (A6): `with` teardown rides the runtime cleanup stack, which only knows
		// how to release a Ref-bearing resource. A resource with no automatic cleanup (a
		// POD struct/value) would bind and run the body with NO teardown scheduled — a
		// silent no-op that never runs the resource's teardown. The user `Scoped`
		// teardown protocol is unbuilt, so reject it loudly rather than drop the cleanup.
		e.diags.Add(n.Resource.Span(), "with requires a resource with automatic cleanup (a Ref); a user teardown protocol is not yet supported")
		return
	}
	e.line("{")
	e.indent++
	e.pushScope()
	need := e.containsRef(rt) || e.directTeardown(n.Body.Stmts)
	e.openScope(need, false)

	name := n.Var
	if name == "" {
		name = "_with"
	}
	cy := e.declareName(name)
	e.line(fmt.Sprintf("%s = %s;", e.localDecl(rt, cy), e.copyValue(rt, n.Resource)))
	e.registerDrop(cy, rt, n.Resource)

	for _, s := range n.Body.Stmts {
		e.stmt(s)
	}

	e.closeScope()
	e.popScope()
	e.indent--
	e.line("}")
}

// --- raise --------------------------------------------------------------------

// raiseStmt lowers `raise e (from c)` (GRAMMAR group 8) to the Err-carrying abort
// exit (Decision D): it unwinds the cleanup stack to the nearest handler (running
// every pending defer/drop) and longjmps there, carrying a zrt_err VALUE so a
// surrounding `guard`/`?` recovers the actual error, not just a message. A string
// operand becomes the Err message; `from c` records c's message as the cause. A
// non-string operand carries a placeholder message for the MVP (a per-type
// display() is a later iteration).
func (e *emitter) raiseStmt(n *ast.RaiseStmt) {
	// An Err-VALUED operand (a built-in error kind `raise ValueError("bad")`, or any
	// value already of type Err) carries its own kind and message, so it is raised
	// directly — this is what lets a `guard`/`?` recover the actual kind (docs/errors.md).
	// A `from cause` wraps it in a nested Err. A non-Err operand (a bare string, or a
	// value with no display() yet) falls back to a message-only Err.
	if isErrType(e.cur.ExprType(e.info, n.Value)) {
		err := e.expr(n.Value)
		if n.From != nil {
			err = fmt.Sprintf("zrt_err_with_cause((%s).msg, %s)", e.expr(n.Value), e.raiseCause(n.From))
		}
		e.line(fmt.Sprintf("zrt_raise_err(%s);", err))
		return
	}
	err := fmt.Sprintf("zrt_err_new(%s)", e.errMessage(n.Value))
	if n.From != nil {
		err = fmt.Sprintf("zrt_err_with_cause(%s, %s)", e.errMessage(n.Value), e.raiseCause(n.From))
	}
	e.line(fmt.Sprintf("zrt_raise_err(%s);", err))
}

// raiseCause renders the `from cause` Err of a `raise e from cause`: an Err-valued cause
// is carried whole (kind and message), any other value becomes a message-only Err.
func (e *emitter) raiseCause(x ast.Expr) string {
	if isErrType(e.cur.ExprType(e.info, x)) {
		return e.expr(x)
	}
	return fmt.Sprintf("zrt_err_new(%s)", e.errMessage(x))
}

// errMessage renders the C string message for a raised value: a string literal
// verbatim, any other expression a placeholder (its display() is a later iteration).
func (e *emitter) errMessage(x ast.Expr) string {
	if s, ok := x.(*ast.StrLit); ok {
		return cString(s.Value)
	}
	return "\"raise\""
}

// --- defer --------------------------------------------------------------------

// deferStmt lowers `defer f(args)` (GRAMMAR group 11): it captures the call's receiver
// (a method callee) and argument values into the site's env struct and schedules the
// pre-generated thunk on the cleanup stack, so the call runs LIFO at the enclosing
// scope's exit on every path. A no-capture call schedules the thunk with a NULL env.
func (e *emitter) deferStmt(n *ast.DeferStmt) {
	idx, ok := e.deferIdx[n]
	if !ok {
		e.diags.Add(n.Span(), "defer supports only a direct, namespace, or method function call in Phase 1d")
		return
	}
	call, _ := n.Call.(*ast.Call)
	_, recv, _, _, _ := e.capturedCall(call)
	if recv == nil && len(call.Args) == 0 {
		e.line(fmt.Sprintf("zrt_defer(zg_deferfn_%d, NULL);", idx))
		return
	}
	env := e.freshName("denv")
	b := e.captureValues(recv, call.Args) // defer OWNS its receiver (survives a `del`/temporary of the original)
	e.line(fmt.Sprintf("zg_deferenv_%d %s = { %s };", idx, env, joinComma(b)))
	e.line(fmt.Sprintf("zrt_defer(zg_deferfn_%d, &%s);", idx, env))
}

// emitDeferHelpers generates, before any function body, the env struct and thunk for
// every `defer f(args)` in the program (numbered per distinct site). The thunk
// unpacks the captured receiver/args and makes the call; a no-capture call needs only
// the thunk. It emits nothing for a program with no defers.
func (e *emitter) emitDeferHelpers() {
	e.deferIdx = map[*ast.DeferStmt]int{}
	for _, inst := range e.prog.Funcs {
		e.cur = inst
		walkStmts(inst.Origin.Body, func(s ast.Stmt) {
			d, ok := s.(*ast.DeferStmt)
			if !ok {
				return
			}
			if _, seen := e.deferIdx[d]; seen {
				return
			}
			call, ok := d.Call.(*ast.Call)
			if !ok {
				return
			}
			target, recv, recvT, erased, ok := e.capturedCall(call)
			if !ok {
				return
			}
			idx := len(e.deferIdx)
			e.deferIdx[d] = idx
			e.emitDeferThunk(idx, target, recv, recvT, erased, call.Args)
		})
	}
	e.cur = nil
}

// emitDeferThunk writes one deferred call's env struct (when it captures a receiver or
// args) and its cleanup-stack thunk.
func (e *emitter) emitDeferThunk(idx int, target string, recv ast.Expr, recvT sema.Type, erased bool, args []ast.Arg) {
	if recv == nil && len(args) == 0 {
		e.line(fmt.Sprintf("static void zg_deferfn_%d(void *p) { (void)p; %s(); }", idx, target))
		e.blank()
		return
	}
	e.line(fmt.Sprintf("typedef struct { %s } zg_deferenv_%d;", e.captureFields(recvT, args), idx))
	e.line(fmt.Sprintf("static void zg_deferfn_%d(void *p) {", idx))
	e.indent++
	e.line(fmt.Sprintf("zg_deferenv_%d *zg_c = (zg_deferenv_%d *)p;", idx, idx))
	e.line(fmt.Sprintf("%s(%s);", target, captureCallArgs("zg_c", recv != nil, erased, len(args))))
	// The defer OWNS its captured receiver (deferStmt retained/deep-copied it), so a non-POD
	// receiver's owned copy is released here, after the call — balancing the capture retain
	// and freeing it exactly once whether the original was dropped, `del`'d, or a temporary.
	if recv != nil {
		e.releaseOwnedReceiver("zg_c", recvT)
	}
	e.indent--
	e.line("}")
	e.blank()
}

// --- captured (defer/spawn) call plumbing -------------------------------------

// capturedCall resolves a defer/spawn call's callee to its mangled C target and, for a
// method callee, the borrowed receiver to capture alongside the arguments. It accepts a
// direct function (Ident), a namespace member `mod.func(args)` (no receiver — it lowers
// to the merged top-level function, like namespaceCallEmit), and a method
// `recv.method(args)` (the receiver is captured as the leading value, dispatched like
// methodCall). It returns ok=false for any other callee (e.g. a call through a function
// value), which leaves deferIdx/spawnIdx unset so the site reports the loud stub.
func (e *emitter) capturedCall(call *ast.Call) (target string, recv ast.Expr, recvT sema.Type, erased, ok bool) {
	switch callee := call.Callee.(type) {
	case *ast.Ident:
		return e.callTarget(call, callee), nil, nil, false, true
	case *ast.Field:
		if id, isID := callee.X.(*ast.Ident); isID {
			if sym, found := e.info.Refs[id]; found && sym.Kind == sema.SymNamespace {
				key := sema.NamespaceMemberName(sym, id.Name, callee.Name)
				if k, has := e.info.NsMembers[callee]; has {
					key = k
				}
				t := e.prog.CallTarget(key)
				if m, has := e.cur.Calls[call]; has {
					t = m
				}
				return t, nil, nil, false, true
			}
		}
		if md, found := e.cur.MethodCalls[call]; found {
			return md.Mangled, callee.X, e.cur.ExprType(e.info, callee.X), md.Erased, true
		}
	}
	return "", nil, nil, false, false
}

// captureValues renders the initializer values for a captured defer/spawn env. Every
// value — the method receiver and each argument alike — is captured by value
// (retained/deep-copied so the deferred/spawned call OWNS an independent copy), so the
// captured call is self-contained and safe regardless of what happens to the originals
// after the capture site:
//
//   - a spawned coroutine is fire-and-forget (sched.c: never joined) and its env is
//     heap-copied, so it may run AFTER the spawner's scope dropped the originals;
//   - a defer runs at scope exit, but the receiver's original may have been `del`'d (or
//     is a temporary with no owner at all), so a bare borrow could dangle there too.
//
// The captured copy is released when the call is done — the argument copies by the
// callee's own param drop (as any by-value call), and the OWNED receiver by the thunk
// itself (emitDeferThunk / emitSpawnThunk), since a method borrows its receiver and never
// releases it. A POD receiver/argument owns no cells, so copyValue is a bare copy and the
// release is elided — byte-identical to a plain capture.
func (e *emitter) captureValues(recv ast.Expr, args []ast.Arg) []string {
	var b []string
	if recv != nil {
		b = append(b, e.copyValue(e.cur.ExprType(e.info, recv), recv))
	}
	for _, a := range args {
		b = append(b, e.copyValue(e.cur.ExprType(e.info, a.Value), a.Value))
	}
	return b
}

// releaseOwnedReceiver releases a captured OWNED receiver (env field f0) after the
// deferred/spawned method call, before the env is freed — balancing the copyValue retain
// captureValues did (a method borrows its receiver and never releases it). ptr is the env
// pointer name ("zg_c" for defer, "zg_e" for spawn). A POD receiver owns no cells, so
// nothing is emitted and the thunk stays byte-identical to a plain capture.
func (e *emitter) releaseOwnedReceiver(ptr string, recvT sema.Type) {
	if recvT == nil || !e.containsRef(recvT) {
		return
	}
	e.emitInlineDrop(dropItem{cname: ptr + "->f0", typ: recvT})
}

// captureFields renders a captured defer/spawn env struct's field declarations: an
// optional leading receiver field (a method callee), then one field per argument, each
// typed by its concrete type. The fields are named f0.. in capture order.
func (e *emitter) captureFields(recvT sema.Type, args []ast.Arg) string {
	var b []string
	i := 0
	if recvT != nil {
		b = append(b, fmt.Sprintf("%s f%d;", e.ctype(recvT), i))
		i++
	}
	for _, a := range args {
		b = append(b, fmt.Sprintf("%s f%d;", e.ctype(e.cur.ExprType(e.info, a.Value)), i))
		i++
	}
	return join(b, " ")
}

// captureCallArgs renders a defer/spawn thunk's call arguments, unpacked from the env
// pointer named ptr (e.g. "zg_c"): the receiver first when present (address-taken and
// const-cast for an erased provided-method dispatch, W3), then each argument field.
func captureCallArgs(ptr string, hasRecv, erased bool, nargs int) string {
	var out []string
	fi := 0
	if hasRecv {
		r := fmt.Sprintf("%s->f%d", ptr, fi)
		if erased {
			r = "(const void*)&" + r
		}
		out = append(out, r)
		fi++
	}
	for j := 0; j < nargs; j++ {
		out = append(out, fmt.Sprintf("%s->f%d", ptr, fi))
		fi++
	}
	return joinComma(out)
}

// --- teardown detection -------------------------------------------------------

// directTeardown reports whether a block's own statements (not its nested blocks)
// register any teardown — a `defer` or a Ref/struct binding — so the scope must
// record a mark to unwind at its natural end.
func (e *emitter) directTeardown(stmts []ast.Stmt) bool {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.DeferStmt:
			return true
		case *ast.BindStmt:
			if e.containsRef(e.cur.BindType(e.info, n)) {
				return true
			}
		}
	}
	return false
}

// subtreeTeardown reports whether a block or anything nested in it registers
// teardown. A function root and a loop body need a mark whenever their subtree owns
// teardown, because a deep `return`/`break` must unwind through every enclosing
// scope down to that mark.
func (e *emitter) subtreeTeardown(stmts []ast.Stmt) bool {
	found := false
	for _, s := range stmts {
		walkStmt(s, func(x ast.Stmt) {
			switch n := x.(type) {
			case *ast.DeferStmt:
				found = true
			case *ast.BindStmt:
				if e.containsRef(e.cur.BindType(e.info, n)) {
					found = true
				}
			case *ast.WithStmt:
				if e.containsRef(e.cur.ExprType(e.info, n.Resource)) {
					found = true
				}
			}
		})
	}
	return found
}

// anyRefParam reports whether an instance takes a by-value Ref parameter, which the
// function root must schedule for release on return.
func (e *emitter) anyRefParam(inst *mono.Instance) bool {
	if inst.Recv != nil && e.containsRef(inst.Recv) {
		return true
	}
	for _, p := range inst.Params {
		if e.containsRef(p) {
			return true
		}
	}
	return false
}

// --- statement walking --------------------------------------------------------

// walkStmts applies fn to every statement in a block and, recursively, in its
// nested blocks (if/for/with bodies). fn sees each statement before its own nested
// blocks are descended.
func walkStmts(b *ast.Block, fn func(ast.Stmt)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmt(s, fn)
	}
}

// walkStmt applies fn to s and every statement nested within it.
func walkStmt(s ast.Stmt, fn func(ast.Stmt)) {
	fn(s)
	switch n := s.(type) {
	case *ast.IfStmt:
		for _, br := range n.Branches {
			walkStmts(br.Body, fn)
		}
		walkStmts(n.Else, fn)
	case *ast.ForStmt:
		walkStmts(n.Body, fn)
	case *ast.WithStmt:
		walkStmts(n.Body, fn)
	}
}

// --- small string helpers -----------------------------------------------------

func joinComma(xs []string) string { return join(xs, ", ") }

func join(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}
