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
