package emit

// This file carries the Phase 1e slice-C2 additions to the C backend: channels —
// the `chan[T](cap?)` constructor, `ch <- v` send, and `<-ch` receive (yielding
// Result[T]). It is additive on top of slice C1 (spawn) and gated on channel use, so
// a program with no channel emits byte-identical C to the pre-channel path.
//
// A channel value is an opaque `zrt_chan *` (chan.c owns the layout). Copying a handle
// retains it — and, for a send-capable handle, bumps the sender count — so the runtime
// can auto-close when the last sender leaves; dropping a handle releases it. A `<-ch`
// receive is lowered to a per-element Result[T] carrier struct `zg_recv_<n>` whose tag
// distinguishes Left(value) from Right(closed/crash); the general Result operator
// bridging (`?`/`!`/`??`) is a later phase, but `!` on a recv result is supported here
// so a received value is usable end to end.

import (
	"fmt"
	"sort"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// --- pre-pass ----------------------------------------------------------------

// prepareChannels numbers the distinct element types a `<-ch` receives (so each gets a
// stable Result[T] carrier struct and its helpers) and records whether the program
// drops any receive-only / send-capable channel handle (gating the two drop thunks). It
// runs after the concurrency gate; a program with no channel leaves everything empty.
func (e *emitter) prepareChannels() {
	e.recvIdx = map[string]int{}
	if !e.concurrency {
		return
	}
	seen := map[string]sema.Type{}
	for node, t := range e.info.ExprTypes {
		if _, ok := node.(*ast.Recv); !ok {
			continue
		}
		ei, ok := t.(*types.Either)
		if !ok || ei.Left == nil || e.ctype(ei.Left) == "void" {
			continue // an un-modelled element type; do not emit a broken carrier
		}
		seen[ei.Left.String()] = ei.Left
	}
	// A `select` recv arm binds its `id` to Result[T] like a bare `<-ch`, so its element
	// type also needs a carrier struct. Walk each function's select arms and register the
	// receivable element type of every recv arm's channel.
	for _, inst := range e.prog.Funcs {
		e.cur = inst
		walkStmts(inst.Origin.Body, func(s ast.Stmt) {
			sel, ok := s.(*ast.SelectStmt)
			if !ok {
				return
			}
			for i := range sel.Arms {
				arm := &sel.Arms[i]
				if arm.Kind != ast.SelectRecv {
					continue
				}
				ch, ok := e.cur.ExprType(e.info, arm.Chan).(*types.Chan)
				if !ok || ch.Elem == nil || e.ctype(ch.Elem) == "void" {
					continue
				}
				seen[ch.Elem.String()] = ch.Elem
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

	consider := func(t sema.Type) {
		if ch, ok := t.(*types.Chan); ok {
			if ch.Dir == types.ChanRecv {
				e.needChanDrop = true
			} else {
				e.needChanSenderDrop = true
			}
		}
	}
	for _, t := range e.info.BindTypes {
		consider(t)
	}
	for _, sig := range e.info.Funcs {
		consider(sig.Ret)
		for _, p := range sig.Params {
			consider(p)
		}
	}
}

// chanReleaseFn / chanDropThunk name the release entry and the scope-exit drop thunk
// for a channel handle, selected by its direction: a receive-only handle releases as a
// plain holder, a bidirectional or send-only handle releases as a sender (which may
// auto-close the channel).
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

// --- expressions / statements ------------------------------------------------

// chanNew lowers `chan[T](cap?)` to zrt_chan_new(sizeof(T), cap) — an omitted capacity
// is the unbuffered rendezvous (0). The fresh handle is bidirectional (rc = senders = 1).
func (e *emitter) chanNew(n *ast.ChanNew) string {
	ch, ok := e.cur.ExprType(e.info, n).(*types.Chan)
	if !ok {
		return "0"
	}
	capExpr := "0"
	if n.Cap != nil {
		capExpr = e.expr(n.Cap)
	}
	return fmt.Sprintf("zrt_chan_new(sizeof(%s), %s)", e.ctype(ch.Elem), capExpr)
}

// sendStmt lowers `ch <- v` (GRAMMAR group 9). The value is copied into a temporary
// (retained/deep-copied for a Ref-holding element) whose address is handed to
// zrt_chan_send, which buffers, rendezvous-hands-off, or parks the coroutine.
func (e *emitter) sendStmt(n *ast.SendStmt) {
	ch, ok := e.cur.ExprType(e.info, n.Chan).(*types.Chan)
	if !ok {
		return
	}
	tmp := e.freshName("sv")
	e.line(fmt.Sprintf("%s = %s;", e.localDecl(ch.Elem, tmp), e.copyValue(ch.Elem, n.Value)))
	e.line(fmt.Sprintf("zrt_chan_send(%s, &%s);", e.expr(n.Chan), tmp))
}

// recvExpr lowers `<-ch` to a call to the element type's carrier helper, yielding a
// zg_recv_<n> holding the Result[T] (tag 0 = Left(value), 1 = Right(closed/crash)).
func (e *emitter) recvExpr(n *ast.Recv) string {
	ei, ok := e.cur.ExprType(e.info, n).(*types.Either)
	if !ok {
		return "0"
	}
	idx, ok := e.recvIdx[ei.Left.String()]
	if !ok {
		return "0"
	}
	return fmt.Sprintf("zg_chanrecv_%d(%s)", idx, e.expr(n.X))
}

// forceExpr lowers `e!` when e is a channel receive's Result[T]: it unwraps the Left
// value, aborting on a Right (a closed/crashed channel). A `!` on any other value is
// outside slice C2 and lowers to the operand unchanged (no example uses it).
func (e *emitter) forceExpr(n *ast.Force) string {
	if ei, ok := e.cur.ExprType(e.info, n.X).(*types.Either); ok {
		if idx, ok := e.recvIdx[ei.Left.String()]; ok {
			return fmt.Sprintf("zg_force_%d(%s)", idx, e.expr(n.X))
		}
	}
	return e.expr(n.X)
}

// selectStmt lowers `select { arm+ }` (GRAMMAR group 9). It evaluates every recv/send
// arm's channel (and a send arm's value) into a case-descriptor array, calls zrt_select
// once to pick and perform a ready arm fairly (or park on all of them until one is), and
// switches on the returned index to run the chosen arm's body. The contextual `done` and
// `_` arms are not descriptors: they are passed as the has_done / has_default flags and
// dispatched from the ZRT_SEL_DONE / ZRT_SEL_DEFAULT sentinel labels. A recv arm's `id`
// is bound to the element type's Result[T] carrier built from the received value and the
// runtime's closed flag (tag 0 = Left(value), 1 = Right(closed)).
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
	// Per-case value temps: a recv target to receive into, or a copied-in send value.
	// The channel head expressions are evaluated into the descriptor array below (GRAMMAR:
	// the arm heads are evaluated before the wait).
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
	if len(ops) == 0 {
		e.line(fmt.Sprintf("int %s = zrt_select(NULL, 0, %s, %s);", pick, boolLit(hasDefault), boolLit(hasDone)))
	} else {
		cs := e.freshName("selcs")
		e.line(fmt.Sprintf("zrt_sel_case %s[%d];", cs, len(ops)))
		for k, op := range ops {
			opk := "ZRT_SEL_RECV"
			if !op.recv {
				opk = "ZRT_SEL_SEND"
			}
			e.line(fmt.Sprintf("%s[%d] = (zrt_sel_case){ %s, %s, &%s, 0 };", cs, k, opk, e.expr(op.arm.Chan), vals[k]))
		}
		e.line(fmt.Sprintf("int %s = zrt_select(%s, %d, %s, %s);", pick, cs, len(ops), boolLit(hasDefault), boolLit(hasDone)))
		e.selectDispatch(pick, ops, vals, cs, hasDone, hasDefault, doneBody, defaultBody)
		e.indent--
		e.line("}")
		return
	}
	e.selectDispatch(pick, ops, vals, "", hasDone, hasDefault, doneBody, defaultBody)
	e.indent--
	e.line("}")
}

// selectDispatch writes the `switch` over zrt_select's returned index: one case per
// recv/send arm (0..k-1) binding a recv arm's `id` before its body, plus the sentinel
// ZRT_SEL_DONE / ZRT_SEL_DEFAULT cases when those arms are present.
// selOp is one recv/send arm of a select flattened into a case descriptor: its arm, its
// channel element type, and whether it receives (else sends).
type selOp struct {
	arm  *ast.SelectArm
	elem sema.Type
	recv bool
}

func (e *emitter) selectDispatch(
	pick string, ops []selOp, vals []string, cs string,
	hasDone, hasDefault bool, doneBody, defaultBody ast.Expr,
) {
	e.line(fmt.Sprintf("switch (%s) {", pick))
	for k, op := range ops {
		e.line(fmt.Sprintf("case %d: {", k))
		e.indent++
		e.pushScope()
		if op.recv && op.arm.HasBind && op.arm.Bind != "" && op.arm.Bind != "_" {
			idx := e.recvIdx[op.elem.String()]
			cname := e.declareName(op.arm.Bind)
			e.line(fmt.Sprintf("zg_recv_%d %s = { (int32_t)%s[%d].closed, %s };", idx, cname, cs, k, vals[k]))
		}
		e.selectArmBody(op.arm.Body)
		e.popScope()
		e.line("break;")
		e.indent--
		e.line("}")
	}
	if hasDone {
		e.line("case ZRT_SEL_DONE: {")
		e.indent++
		e.selectArmBody(doneBody)
		e.line("break;")
		e.indent--
		e.line("}")
	}
	if hasDefault {
		e.line("case ZRT_SEL_DEFAULT: {")
		e.indent++
		e.selectArmBody(defaultBody)
		e.line("break;")
		e.indent--
		e.line("}")
	}
	e.line("}")
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
// Result[T] carrier struct plus its recv and force helpers. It emits nothing for a
// program with no channels.
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
	for _, elem := range e.recvElems {
		e.emitRecvHelper(elem)
	}
}

// emitRecvHelper writes one element type's Result[T] carrier and helpers: the carrier
// struct (an int32 tag plus the value), zg_chanrecv_<n> (calls zrt_chan_recv and packs
// the tag), and zg_force_<n> (unwraps Left, aborting on Right).
func (e *emitter) emitRecvHelper(elem sema.Type) {
	idx := e.recvIdx[elem.String()]
	ct := e.ctype(elem)
	e.line(fmt.Sprintf("typedef struct { int32_t tag; %s val; } zg_recv_%d;", ct, idx))
	e.line(fmt.Sprintf("static zg_recv_%d zg_chanrecv_%d(zrt_chan *ch) {", idx, idx))
	e.indent++
	e.line(fmt.Sprintf("zg_recv_%d r;", idx))
	e.line("r.tag = (int32_t)zrt_chan_recv(ch, &r.val);")
	e.line("return r;")
	e.indent--
	e.line("}")
	e.line(fmt.Sprintf("static %s zg_force_%d(zg_recv_%d r) {", ct, idx, idx))
	e.indent++
	e.line("if (r.tag != 0) { zrt_abort(\"force-unwrap of a closed channel receive\"); }")
	e.line("return r.val;")
	e.indent--
	e.line("}")
	e.blank()
}
