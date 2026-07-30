package emit

// The channel half of the concurrency backend (GRAMMAR group 9): the `chan[T](cap?)`
// constructor, `ch <- v`, `<-ch`, `select`, and the channel as a for-in iterator. The
// spawn half is emit_spawn.go; the two share the concurrency gate below, which is what
// decides whether a program links the scheduler at all.
//
// A channel value is an opaque `zrt_chan *` (chan.c owns the layout). Copying a handle
// retains it — and, for a send-capable handle, bumps the sender count — so the runtime
// can auto-close when the last sender leaves; dropping a handle releases it. There is no
// explicit close in the language, which is why `del ch` matters: it is how a holder gives
// up its send end early.
//
// A `<-ch` receive yields a real `Result[T]` on emit_result.go's general carrier, so
// every group-8 operator that reads a Result back — `?`, `!`, `??`, `if v := <-ch`,
// `match <-ch { Left(v) … Right(e) … }` — works on a receive with no channel-specific
// code behind it. The carrier is built by a per-element-type helper because the runtime
// reports closure through zrt_chan_recv's RETURN value while writing the element through
// a pointer, and a C expression cannot do both at once.
//
// This is the seed, so it lowers the happy path and refuses the rest by name: a
// directional channel type is checked by sema but has no lowering here, and saying so is
// what keeps the seed from emitting C that cc would reject instead.

import (
	"fmt"
	"sort"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// --- concurrency build gate ---------------------------------------------------

// programUsesConcurrency reports whether the program uses concurrency: any function body
// contains a `spawn`/send/`select`, any binding, expression, or signature type transitively
// holds a channel, or the program lowers a scheduler-floor intrinsic. It is the single
// trigger for Manifest.Concurrency (mirroring programUsesRef), so a value-only program links
// none of the scheduler and stays byte-identical.
func (e *emitter) programUsesConcurrency() bool {
	// A scheduler-floor intrinsic is a use of the scheduler even in a program that names no
	// channel and no `spawn`: its primitive lives in sched.c, which only Concurrency links.
	if e.programUsesSchedFloor() {
		return true
	}
	for _, inst := range e.prog.Funcs {
		found := false
		walkStmts(inst.Origin.Body, func(s ast.Stmt) {
			switch s.(type) {
			case *ast.SpawnStmt, *ast.SendStmt, *ast.SelectStmt:
				found = true
			}
		})
		if found {
			return true
		}
	}
	for _, t := range e.info.BindTypes {
		if containsChan(t) {
			return true
		}
	}
	for _, t := range e.info.ExprTypes {
		if containsChan(t) {
			return true
		}
	}
	for _, sig := range e.info.Funcs {
		if containsChan(sig.Ret) {
			return true
		}
		for _, p := range sig.Params {
			if containsChan(p) {
				return true
			}
		}
	}
	return false
}

// containsChan reports whether a type transitively holds a channel, the value form that
// pulls in the scheduler. It mirrors containsRef's structural walk, with a visited set so
// a recursive (S1) type terminates instead of looping forever — a nominal already on the
// walk contributes no new channel, so revisiting it yields false. The set is allocated only
// once a nominal is actually reached, since this is asked of every recorded type in the
// program and the overwhelming majority are scalars.
func containsChan(t sema.Type) bool {
	return containsChanSeen(t, nil)
}

func containsChanSeen(t sema.Type, seen map[string]bool) bool {
	nominal := func(name string, parts []sema.Type) bool {
		if seen == nil {
			seen = map[string]bool{}
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		for _, p := range parts {
			if containsChanSeen(p, seen) {
				return true
			}
		}
		return false
	}
	switch x := t.(type) {
	case *types.Chan:
		return true
	case *types.Tuple:
		for _, el := range x.Elems {
			if containsChanSeen(el, seen) {
				return true
			}
		}
	case *types.Array:
		return containsChanSeen(x.Elem, seen)
	case *types.Opt:
		return containsChanSeen(x.Elem, seen)
	case *types.List:
		return containsChanSeen(x.Elem, seen)
	case *types.Struct:
		return nominal(x.String(), structFieldTypes(x))
	case *types.Enum:
		return nominal(x.String(), enumPayloadTypes(x))
	}
	return false
}

// --- pre-pass ----------------------------------------------------------------

// prepareChannels numbers the distinct element types a `<-ch` receives (so each gets a
// stable carrier helper), records whether the program drops any receive-only /
// send-capable channel handle (gating the two drop thunks), and refuses the channel
// shapes past the seed's line. It runs before prepareResults, which registers the
// `Result[T]` carrier for each element type gathered here.
func (e *emitter) prepareChannels() {
	e.recvIdx = map[string]int{}
	if !e.concurrency {
		return
	}
	e.rejectDirectionalChans()

	seen := map[string]sema.Type{}
	consider := func(elem sema.Type) {
		if elem == nil || e.ctype(elem) == "void" {
			return // an un-modelled element type; do not register a broken carrier
		}
		seen[elem.String()] = elem
	}
	for node, t := range e.info.ExprTypes {
		if _, ok := node.(*ast.Recv); !ok {
			continue
		}
		if ei, ok := t.(*types.Either); ok {
			consider(ei.Left)
		}
	}
	// One walk of the bodies, for one fact: a `select` recv arm binds its `id` to Result[T]
	// like a bare `<-ch`, so its element type needs a carrier too — and its channel is
	// reached through the arm rather than an ast.Recv node.
	for _, inst := range e.prog.Funcs {
		e.cur = inst
		walkStmts(inst.Origin.Body, func(s ast.Stmt) {
			switch n := s.(type) {
			case *ast.SelectStmt:
				for i := range n.Arms {
					if arm := &n.Arms[i]; arm.Kind == ast.SelectRecv {
						if ch, ok := e.cur.ExprType(e.info, arm.Chan).(*types.Chan); ok {
							consider(ch.Elem)
						}
					}
				}
			}
		})
	}
	e.cur = nil

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		e.recvIdx[k] = i
		e.recvElems = append(e.recvElems, seen[k])
	}

	// A held handle needs the SENDER drop thunk, and in practice only that one. The
	// plain-hold thunk existed for the hold the old `del ch` kept behind; `close(ch)` moves
	// no count at all now, so nothing in a seed program is dropped as a plain hold.
	holdsChan := false
	note := func(t sema.Type) {
		if _, ok := t.(*types.Chan); ok {
			holdsChan = true
		}
	}
	for _, t := range e.info.BindTypes {
		note(t)
	}
	// A by-value channel PARAMETER is a holder too — it is the callee's own handle, and its
	// release is what a producer coroutine gives its send end back with. A parameter is no
	// BindStmt, so its type is only in the signature.
	for _, sig := range e.info.Funcs {
		note(sig.Ret)
		for _, p := range sig.Params {
			note(p)
		}
	}
	e.needChanSenderDrop = holdsChan
	// The plain-hold thunk has no user left. chanDropThunk names it only for a ChanRecv
	// handle, and rejectDirectionalChans refuses every directional type the PROGRAM could
	// declare one from. A bundled module is not refused (see there) and could in principle
	// drop one, so this is no longer an impossibility — it is a loud failure: the C would
	// name a function nothing defined and cc would say so, which is the seed's whole
	// contract. No stdlib module drops a receive-only handle today.
	e.needChanDrop = false
}

// rejectDirectionalChans refuses a receive-only / send-only channel type in a signature
// or a binding. Sema accepts and narrows one (sema/chan_test.go pins the diagnostics), but
// the seed's release path is chosen from the handle's direction at the DECLARATION, and
// getting that wrong silently leaks a channel or closes it early — so the seed says it
// cannot lower one rather than guessing. The self-hosted compiler is where directions land.
//
// It is scoped to the PROGRAM's own code — the entry module — and not to an imported
// module bundled in beside it. The whole-program flatten hands the seed every member of
// every module it imports, reachable or not, so `import "time"` alone drags in `after` and
// `ticker`, whose ends are receive-only; refusing over those would stop a program that only
// reads `time.now()` over a signature it never asked for. Nothing is lost by letting them
// through: a directional handle cannot reach the program's own code without appearing there
// as a signature, a binding, or an expression — all three of which are refused below — so
// every release path the seed would actually have to choose is still named.
//
// Instances are walked in the monomorphizer's order, which does not reach the output:
// diag.List sorts every diagnostic by source offset on the way out.
func (e *emitter) rejectDirectionalChans() {
	// One refusal per source line: a narrowed end is reached both as a binding and as the
	// call that produced it, and naming the same line twice says nothing the first did not.
	// Keying on the line is sound because only the entry module is walked, so every span
	// counted here is a position in the one file the program was compiled from.
	seen := map[int]bool{}
	refuse := func(at token.Span, t sema.Type) {
		d, ok := directionalChan(t)
		if !ok || seen[at.Start.Line] {
			return
		}
		seen[at.Start.Line] = true
		e.diags.Add(at, "the bootstrap seed does not lower a directional channel type %s; use a bidirectional chan[T]", d)
	}
	for _, inst := range e.prog.Funcs {
		if inst.Origin == nil || inst.Origin.Module != "" {
			continue
		}
		// A signature's refusal points at the TYPE that was written, not at the `fn` that
		// opens the declaration: reporting a parameter's channel direction at 1:1 sends a
		// reader to the top of the file to look for something spelled further down. The
		// declaration's own span is the fallback for an instance whose parameters do not
		// line up with the source's — a method, whose receiver is not written in the list.
		refuse(declSpan(inst.Origin, inst.Origin.Ret), inst.Ret)
		aligned := inst.Origin.Params != nil && len(inst.Params) == len(inst.Origin.Params)
		for i, p := range inst.Params {
			at := inst.Origin.Span()
			if aligned {
				at = declSpan(inst.Origin, inst.Origin.Params[i].Type)
			}
			refuse(at, p)
		}
		walkStmts(inst.Origin.Body, func(s ast.Stmt) {
			if b, ok := s.(*ast.BindStmt); ok {
				refuse(b.Span(), e.info.BindTypes[b])
			}
		})
		// Calls only. Every other directional expression in the program's own code READS
		// something already declared directional — a parameter, a binding — and both are
		// refused above, so walking them all would repeat one refusal per mention. A call is
		// the one shape neither covers: a bundled module's function can HAND BACK a narrowed
		// end (`time.after(d)`), and the value is refused whether or not it is ever bound.
		walkBlockExprs(inst.Origin.Body, func(x ast.Expr) {
			if _, ok := x.(*ast.Call); ok {
				refuse(x.Span(), e.info.ExprTypes[x])
			}
		})
	}
}

// declSpan is the span of a written type, or the declaration's own when the type was not
// written at all (an omitted result) or carries no position.
func declSpan(d *ast.FuncDecl, t ast.Type) token.Span {
	if t == nil {
		return d.Span()
	}
	at := t.Span()
	if at.Start.Line == 0 {
		return d.Span()
	}
	return at
}

// directionalChan reports whether a type is a narrowed channel end, spelled the way the
// source writes it — which is what types.Chan.String() answers.
func directionalChan(t sema.Type) (string, bool) {
	ch, ok := t.(*types.Chan)
	if !ok || ch.Dir == types.ChanBidi {
		return "", false
	}
	return ch.String(), true
}

// chanReleaseFn / chanDropThunk name the release entry and the scope-exit drop thunk for
// a channel handle, selected by its direction: a receive-only handle releases as a plain
// holder, a bidirectional or send-only handle releases as a sender (which may auto-close
// the channel).
func (e *emitter) chanReleaseFn(t *types.Chan) string {
	if t.Dir == types.ChanRecv {
		return "zrt_chan_release"
	}
	return "zrt_chan_sender_release"
}

func (e *emitter) chanDropThunk(t *types.Chan) string {
	if t.Dir == types.ChanRecv {
		return "zg_chan_drop"
	}
	return "zg_chan_sender_drop"
}

// --- close ---------------------------------------------------------------------

// closeCallStmt lowers a `close(ch)` statement, reporting whether the expression was one.
//
// `close(ch)` marks the CHANNEL no-longer-sendable. It is a property of the channel and
// not of a holder, so it moves no count: `ch` keeps its reference and stays readable, a
// receive drains what is buffered and then answers the Right, and closing twice changes
// nothing. That is the whole lowering — one runtime call.
//
// It does NOT replace auto-close, which stays the everyday shape: a producer closes by
// FINISHING, its unwind dropping the last send end. Two things depend on that and an
// explicit flag cannot do either — a crashing producer never reaches a `close` call, and
// its unwind is what carries the crash Err to the receiver; and four producers fanning in
// need no coordination because the last to return closes. `close` is the conditional,
// early form: end this stream before my scope does.
//
// It stays a STATEMENT rather than a value. Nothing in the lowering forbids an expression
// today, but `close` yields nothing a program can use, and keeping one spelling is what
// lets both compilers say the same thing about it.
func (e *emitter) closeCallStmt(x ast.Expr) bool {
	call, ok := x.(*ast.Call)
	if !ok || !isCloseCall(call) {
		return false
	}
	if len(call.Args) != 1 {
		return true // sema reported the arity; emit nothing rather than guess
	}
	if _, ok := e.cur.ExprType(e.info, call.Args[0].Value).(*types.Chan); !ok {
		return true // sema reported the non-channel
	}
	e.line(fmt.Sprintf("zrt_chan_close(%s);", e.expr(call.Args[0].Value)))
	return true
}

// closeOutOfStatement refuses a `close(...)` written where a statement cannot go — inside
// an expression, or behind a `defer` or a `spawn` — reporting whether it was one. It is the
// one message for all three, because they are one mistake.
func (e *emitter) closeOutOfStatement(call *ast.Call) bool {
	if !isCloseCall(call) {
		return false
	}
	e.diags.Add(call.Span(), "'close' is a statement, not a value: write `close(ch)` on a line of its own")
	return true
}

// isCloseCall reports whether a call names the `close` built-in.
func isCloseCall(call *ast.Call) bool {
	id, ok := call.Callee.(*ast.Ident)
	return ok && id.Name == "close"
}

func (e *emitter) chanNew(n *ast.ChanNew) string {
	ch, ok := e.cur.ExprType(e.info, n).(*types.Chan)
	if !ok {
		return e.systemError(n, "a chan[T] constructor with no channel type")
	}
	capExpr := "0"
	if n.Cap != nil {
		capExpr = e.expr(n.Cap)
	}
	return fmt.Sprintf("zrt_chan_new(sizeof(%s), %s)", e.ctype(ch.Elem), capExpr)
}

// sendStmt lowers `ch <- v`. The value is copied into a temporary (retained/deep-copied
// for a Ref-holding element) whose address is handed to zrt_chan_send, which buffers,
// rendezvous-hands-off, or parks the coroutine.
func (e *emitter) sendStmt(n *ast.SendStmt) {
	ch, ok := e.cur.ExprType(e.info, n.Chan).(*types.Chan)
	if !ok {
		return
	}
	tmp := e.freshName("sv")
	e.line(fmt.Sprintf("%s = %s;", e.localDecl(ch.Elem, tmp), e.copyValue(ch.Elem, n.Value)))
	e.line(fmt.Sprintf("zrt_chan_send(%s, &%s);", e.expr(n.Chan), tmp))
}

// recvExpr lowers `<-ch` to a call of the element type's carrier helper, yielding the
// program's ordinary `Result[T]` value.
func (e *emitter) recvExpr(n *ast.Recv) string {
	ei, ok := e.cur.ExprType(e.info, n).(*types.Either)
	if !ok {
		return e.systemError(n, "a receive whose result is not a carrier")
	}
	idx, ok := e.recvIdx[ei.Left.String()]
	if !ok {
		return e.systemError(n, "the receive helper for %s was not emitted", ei.Left)
	}
	return fmt.Sprintf("zg_chanrecv_%d(%s)", idx, e.expr(n.X))
}

// forInChan lowers `for v in ch`. The channel IS the iterator: it yields values until its
// last sender leaves, and that close is the StopIteration which ends the loop. This is
// where a `yield` keyword and a generator type would have gone — a producer coroutine
// sending on a channel is the generator, and the send is the yield.
//
// A closed channel still hands over what it is holding, so the loop drains the buffer
// before it ends; that is zrt_chan_recv's own rule, not something repeated here.
//
// The one thing a close does not say by itself is WHY, so the reason is read once the loop
// is over: the StopIteration KIND is the ordinary end, and any other kind means a sender
// died and this loop was handed a truncated stream. Ending quietly there would turn a
// crash into a short answer, so the sender's own Err is re-raised whole — message, cause
// and kind — rather than a message copied out of it.
func (e *emitter) forInChan(n *ast.ForStmt, ct *types.Chan) {
	it := e.freshName("cit")
	e.line("{")
	e.indent++
	e.line(fmt.Sprintf("zrt_chan *%s = %s;", it, e.expr(n.Iter)))
	e.pushScope()
	cname := e.declareName(n.Var)
	e.line(e.localDecl(ct.Elem, cname) + ";")
	e.line(fmt.Sprintf("while (zrt_chan_recv(%s, &%s) == 0) {", it, cname))
	e.body(n.Body, true)
	e.line("}")
	e.popScope()
	ce := e.freshName("cerr")
	e.line(fmt.Sprintf("zrt_err %s = zrt_chan_close_err(%s);", ce, it))
	e.line(fmt.Sprintf("if (%s.kind != ZRT_ERR_STOP_ITERATION) { zrt_raise_err(%s); }", ce, ce))
	e.indent--
	e.line("}")
}

// selectStmt lowers `select { arm+ }`, the one multi-way wait. It evaluates every
// recv/send arm's channel (and a send arm's value) into a case-descriptor array, calls
// zrt_select once to pick and perform a ready arm fairly (or park on all of them until one
// is), and dispatches on the returned index. The contextual `done` and `_` arms are not
// descriptors: they are passed as the has_done / has_default flags and dispatched from the
// ZRT_SEL_DONE / ZRT_SEL_DEFAULT sentinels. A recv arm's `id` binds the same `Result[T]` a
// bare `<-ch` yields, built from the received value and the runtime's closed flag.
func (e *emitter) selectStmt(n *ast.SelectStmt) {
	var ops []selOp
	var doneBody, defaultBody ast.Expr
	hasDone, hasDefault := false, false
	for i := range n.Arms {
		arm := &n.Arms[i]
		switch arm.Kind {
		case ast.SelectRecv, ast.SelectSend:
			var elem sema.Type
			if ch, ok := e.cur.ExprType(e.info, arm.Chan).(*types.Chan); ok {
				elem = ch.Elem
			}
			ops = append(ops, selOp{arm, elem, arm.Kind == ast.SelectRecv})
		case ast.SelectDone:
			hasDone, doneBody = true, arm.Body
		case ast.SelectDefault:
			hasDefault, defaultBody = true, arm.Body
		}
	}

	e.line("{")
	e.indent++
	// Per-case value temps: a recv target to receive into, or a copied-in send value. The
	// channel head expressions are evaluated into the descriptor array below, so the arm
	// heads are evaluated before the wait.
	vals := make([]string, len(ops))
	for k, op := range ops {
		vals[k] = e.freshName("selv")
		if op.recv {
			e.line(e.localDecl(op.elem, vals[k]) + ";")
		} else {
			e.line(fmt.Sprintf("%s = %s;", e.localDecl(op.elem, vals[k]), e.copyValue(op.elem, op.arm.Value)))
		}
	}
	pick := e.freshName("selpick")
	cs := ""
	if len(ops) == 0 {
		e.line(fmt.Sprintf("int %s = zrt_select(NULL, 0, %s, %s);", pick, boolLit(hasDefault), boolLit(hasDone)))
	} else {
		cs = e.freshName("selcs")
		e.line(fmt.Sprintf("zrt_sel_case %s[%d];", cs, len(ops)))
		for k, op := range ops {
			opk := "ZRT_SEL_RECV"
			if !op.recv {
				opk = "ZRT_SEL_SEND"
			}
			e.line(fmt.Sprintf("%s[%d] = (zrt_sel_case){ %s, %s, &%s, 0 };", cs, k, opk, e.expr(op.arm.Chan), vals[k]))
		}
		e.line(fmt.Sprintf("int %s = zrt_select(%s, %d, %s, %s);", pick, cs, len(ops), boolLit(hasDefault), boolLit(hasDone)))
	}
	e.selectDispatch(pick, ops, vals, cs, hasDone, hasDefault, doneBody, defaultBody)
	e.indent--
	e.line("}")
}

// selOp is one recv/send arm of a select flattened into a case descriptor: its arm, its
// channel element type, and whether it receives (else sends).
type selOp struct {
	arm  *ast.SelectArm
	elem sema.Type
	recv bool
}

// selectDispatch writes the dispatch on zrt_select's returned pick as an if / else-if
// chain (NOT a C switch): a Zerg `break`/`continue` in an arm body lowers to a bare
// `break;`/`continue;`, which a C switch would capture — inside `for { select { … } }`
// that would break the switch, not the loop, hanging the program. An if-chain keeps a
// Zerg `break` bound to the enclosing loop.
func (e *emitter) selectDispatch(
	pick string, ops []selOp, vals []string, cs string,
	hasDone, hasDefault bool, doneBody, defaultBody ast.Expr,
) {
	// Each branch opens with `if`/`} else if` and is left OPEN (no closing brace); the next
	// branch's `} else if` closes it and the final `e.line("}")` closes the last, exactly as
	// ifStmt spells an if/else-if chain.
	kw := "if"
	any := false
	for k, op := range ops {
		e.line(fmt.Sprintf("%s (%s == %d) {", kw, pick, k))
		e.indent++
		e.pushScope()
		if op.recv && op.arm.HasBind && op.arm.Bind != "" && op.arm.Bind != "_" {
			e.selectRecvBind(op, vals[k], fmt.Sprintf("%s[%d]", cs, k))
		}
		e.selectArmBody(op.arm.Body)
		e.popScope()
		e.indent--
		kw = "} else if"
		any = true
	}
	if hasDone {
		e.line(fmt.Sprintf("%s (%s == ZRT_SEL_DONE) {", kw, pick))
		e.indent++
		e.selectArmBody(doneBody)
		e.indent--
		kw = "} else if"
		any = true
	}
	if hasDefault {
		e.line(fmt.Sprintf("%s (%s == ZRT_SEL_DEFAULT) {", kw, pick))
		e.indent++
		e.selectArmBody(defaultBody)
		e.indent--
		any = true
	}
	if any {
		e.line("}")
	}
}

// selectRecvBind declares a recv arm's `id`, holding the same `Result[T]` a bare `<-ch`
// yields. The runtime performed the receive into val and reported closure through the
// descriptor's `closed` output, so the carrier is assembled from those two rather than by
// calling the recv helper again — which would take a SECOND value off the channel. The
// Right side reads the arm's own channel out of the descriptor (`slot.ch`) so the close
// reason arrives whole, the same Err a bare `<-ch` would have carried.
func (e *emitter) selectRecvBind(op selOp, val, slot string) {
	c, ok := e.carrierFor(sema.ResultOf(op.elem))
	if !ok {
		return
	}
	cname := e.declareName(op.arm.Bind)
	e.line(fmt.Sprintf("%s %s = {0};", c.name, cname))
	e.line(fmt.Sprintf("%s.tag = (int32_t)%s.closed;", cname, slot))
	e.line(fmt.Sprintf("if (%s.tag == 0) { %s.%s = %s; } else { %s.err = zrt_chan_close_err(%s.ch); }",
		cname, cname, c.okField(), val, cname, slot))
}

// selectArmBody emits a select arm's body, run for effect. A block body's statements are
// emitted in the arm's case scope (so a recv arm's binding is in view); any other
// expression body is emitted as an expression-statement.
func (e *emitter) selectArmBody(body ast.Expr) {
	if body == nil {
		return
	}
	if blk, ok := body.(*ast.Block); ok {
		for _, s := range blk.Stmts {
			e.stmt(s)
		}
		return
	}
	e.line(e.expr(body) + ";")
}

// boolLit renders a Go bool as the C99 stdbool literal.
func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// --- generated helpers -------------------------------------------------------

// emitChanHelpers writes the channel scope-exit drop thunks (each only when the program
// drops a handle of that direction) and, for every distinct received element type, the
// receive helper that builds the `Result[T]`. It emits nothing for a program with no
// channels.
func (e *emitter) emitChanHelpers() {
	if e.needChanDrop {
		e.line("static void zg_chan_drop(void *slot) {")
		e.indent++
		e.line("zrt_chan **s = (zrt_chan **)slot;")
		e.line("if (*s != NULL) { zrt_chan_release(*s); }")
		e.indent--
		e.line("}")
		e.blank()
	}
	if e.needChanSenderDrop {
		e.line("static void zg_chan_sender_drop(void *slot) {")
		e.indent++
		e.line("zrt_chan **s = (zrt_chan **)slot;")
		e.line("if (*s != NULL) { zrt_chan_sender_release(*s); }")
		e.indent--
		e.line("}")
		e.blank()
	}
	for idx, elem := range e.recvElems {
		e.emitChanRecvHelper(idx, elem)
	}
}

// emitChanRecvHelper writes one element type's receive helper: zrt_chan_recv reports
// closure through its RETURN value while writing the element through a pointer, so a
// receive cannot be a single C expression and needs this to become one.
//
// The Right side carries WHY the channel ended, which is the whole reason a receive is a
// Result rather than an optional: zrt_chan_close_err hands back the channel's WHOLE Err —
// the StopIteration sentinel (by kind) for an ordinary close, and the own Err of a sender
// that aborted while holding the last send end, message, cause and kind intact. Building
// an Err from a message here instead would leave `err is StopIteration` false and force
// the receiver to compare strings, which is exactly what carrying a kind exists to avoid.
func (e *emitter) emitChanRecvHelper(idx int, elem sema.Type) {
	c, ok := e.carrierFor(sema.ResultOf(elem))
	if !ok {
		return
	}
	e.line(fmt.Sprintf("static %s zg_chanrecv_%d(zrt_chan *ch) {", c.name, idx))
	e.indent++
	e.line(fmt.Sprintf("%s r = {0};", c.name))
	e.line(fmt.Sprintf("r.tag = (int32_t)zrt_chan_recv(ch, &r.%s);", c.okField()))
	e.line("if (r.tag != 0) { r.err = zrt_chan_close_err(ch); }")
	e.line("return r;")
	e.indent--
	e.line("}")
	e.blank()
}
