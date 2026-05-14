// v0.7 Unit 7 — codegen for the concurrency surface: channels, spawn,
// anonymous fns, defer, select, and the wait_group built-in.
//
// Every chan element type T grows one set of helpers (struct + make / send /
// recv / close / ready). Two uses of `chan[int]` share a single set, mirroring
// v0.6 generic monomorphisation. Helpers live alongside the per-shape list /
// tuple / struct / enum helpers in the emitted .c file.
//
// Anon fns lower to a top-level C fn plus a heap-allocated environment
// struct. SpawnStmt allocates the env, deep-copies captured composites via
// the per-shape <T>_copy helpers, and hands the (fn, env) pair to
// zerg_spawn. DeferStmt allocates the same kind of env and calls
// zerg_defer_push at the defer site; the per-fn epilogue (emitted on fns
// flagged HasDefers) calls zerg_defer_drain at every exit, including the
// `?` early-return path Unit 5.5 specifies.

package build

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// chanShape is one entry in the per-cgen chan-helper registry. The key is
// the mangled element type ("int64_t", "zerg_str", "zerg_list_int64_t" ...);
// elem is the canonical *Type so the helper emit picks the right element
// C type and the recv result reaches the right T? enum mangle.
type chanShape struct {
	elem      *syntax.Type
	elemC     string
	elemMang  string
	chanMang  string
	optionT   *syntax.Type
	optionMng string
}

// addChanShape registers a TypeChan with the cgen's chan registry. Idempotent:
// two registrations for chan[int] dedupe on the element-type mangle.
func (g *cgen) addChanShape(t *syntax.Type) {
	if t == nil || t.Kind != syntax.TypeChan {
		return
	}
	if g.chanShapes == nil {
		g.chanShapes = map[string]*chanShape{}
	}
	elemMang := g.mangleType(t.Element)
	if _, ok := g.chanShapes[elemMang]; ok {
		return
	}
	cs := &chanShape{
		elem:     t.Element,
		elemC:    g.cTypeName(t.Element),
		elemMang: elemMang,
		chanMang: "zerg_chan_" + elemMang,
	}
	if g.chanNullableLookup != nil {
		cs.optionT = g.chanNullableLookup(t.Element)
	}
	if cs.optionT == nil {
		cs.optionT = g.synthNullableType(t.Element)
	}
	if cs.optionT != nil {
		cs.optionMng = g.mangleType(cs.optionT)
		g.shapes.addType(g, cs.optionT)
		// Register in the harvested map so subsequent for-chan / select sites
		// share the same canonical pointer — the Some payload field shape
		// must match exactly.
		if g.chanNullableByElemKey != nil {
			g.chanNullableByElemKey[t.Element.String()] = cs.optionT
		}
	}
	g.chanShapes[elemMang] = cs
	g.chanOrder = append(g.chanOrder, elemMang)
}

// synthNullableType builds a synthetic T? *Type matching the canonical
// shape typeck constructs for `T?` / wrapNullable. Used when no RecvExpr has
// seeded the harvest map for this element type — the v0.7 for-chan path
// doesn't introduce a syntactic RecvExpr, so a chan-only program that
// loops via `for v in ch` lacks a typeck-stamped T?. The synthetic
// instance matches the built-in monomorphisation Name format ("T?")
// so mangleType routes through the zerg_builtin owner-mangle.
func (g *cgen) synthNullableType(elem *syntax.Type) *syntax.Type {
	if elem == nil {
		return nil
	}
	t := &syntax.Type{
		Kind:            syntax.TypeEnum,
		Name:            "Option[" + elem.String() + "]",
		Variants:        []string{"Some", "None"},
		VariantPayloads: [][]*syntax.Type{{elem}, nil},
	}
	return t
}

// emitChanForwardDecls writes one struct typedef per registered chan element
// type, ahead of the body emission so helpers can refer to the struct by name.
func (g *cgen) emitChanForwardDecls() {
	if len(g.chanOrder) == 0 {
		return
	}
	keys := append([]string(nil), g.chanOrder...)
	sort.Strings(keys)
	g.b.WriteString("/* v0.7 channel struct forward declarations. */\n")
	for _, k := range keys {
		fmt.Fprintf(&g.b, "typedef struct %s %s;\n", g.chanShapes[k].chanMang, g.chanShapes[k].chanMang)
	}
	g.b.WriteString("\n")
}

// emitChanTypedefs writes the struct definition for every chan element type.
// The capacity slot is 0 for unbuffered (the runtime treats 0 as a
// rendezvous: every send must hand off directly to a parked receiver).
// v0.12 layout: condvar pair replaced by send/recv wait queues; the
// per-element-type helpers below park the calling coroutine on the queue
// when the op would block.
func (g *cgen) emitChanTypedefs() {
	if len(g.chanOrder) == 0 {
		return
	}
	keys := append([]string(nil), g.chanOrder...)
	sort.Strings(keys)
	const tmpl = `struct %[1]s {
    pthread_mutex_t mu;
    %[2]s *buf;
    int64_t cap;
    int64_t count;
    int64_t head;
    int64_t tail;
    int     closed;
    zerg_chan_wait_node_t *send_head;
    zerg_chan_wait_node_t *send_tail;
    zerg_chan_wait_node_t *recv_head;
    zerg_chan_wait_node_t *recv_tail;
};
`
	g.b.WriteString("/* v0.12 channel struct definitions. */\n")
	for _, k := range keys {
		cs := g.chanShapes[k]
		fmt.Fprintf(&g.b, tmpl, cs.chanMang, cs.elemC)
	}
	g.b.WriteString("\n")
}

// emitChanHelpers writes make / send / recv / close / ready helpers for each
// chan element type. Helpers are static so two TUs cannot collide.
func (g *cgen) emitChanHelpers() {
	if len(g.chanOrder) == 0 {
		return
	}
	keys := append([]string(nil), g.chanOrder...)
	sort.Strings(keys)
	for _, k := range keys {
		cs := g.chanShapes[k]
		g.emitChanOneHelpers(cs)
	}
}

// emitChanOneHelpers writes make / send / recv / close / ready helpers for
// one chan element type using positional fmt verbs:
//
//	%[1]s = chanMang  (e.g. "zerg_chan_int64_t")
//	%[2]s = elemC     (e.g. "int64_t")
//	%[3]s = optionMng (e.g. "zerg_opt_int64_t") — recv block only
//
// The send / recv park paths rely on zerg_coro_park's deferred-unlock: the
// chan mu is released only AFTER the parker's ucontext is fully saved, so
// an unparker that subsequently acquires mu never queues the coro before
// its ctx is ready. The ready probe mirrors selectChanReadyC's reference:
// recv ready when buffer non-empty OR a parked sender OR closed; send
// ready when buffer has room AND not closed, OR a parked receiver.
func (g *cgen) emitChanOneHelpers(cs *chanShape) {
	const makeSendCloseReadyTmpl = `static %[1]s *%[1]s_make(int64_t cap) {
    %[1]s *ch = (%[1]s *)calloc(1, sizeof(%[1]s));
    pthread_mutex_init(&ch->mu, 0);
    int64_t slots = cap > 0 ? cap : 0;
    if (slots > 0) ch->buf = (%[2]s *)malloc((size_t)slots * sizeof(%[2]s));
    ch->cap = cap;
    return ch;
}

static void %[1]s_send(%[1]s *ch, %[2]s v) {
    pthread_mutex_lock(&ch->mu);
    if (ch->closed) {
        pthread_mutex_unlock(&ch->mu);
        fprintf(stderr, "zerg: runtime: send on closed channel\n");
        exit(1);
    }
    /* Direct hand-off to a parked receiver. */
    zerg_chan_wait_node_t *r = zerg_chan_wait_pop(&ch->recv_head, &ch->recv_tail);
    if (r) {
        *(%[2]s *)r->value_ptr = v;
        zerg_coro_t *target = r->coro;
        pthread_mutex_unlock(&ch->mu);
        zerg_coro_unpark(target);
        return;
    }
    if (ch->count < ch->cap) {
        ch->buf[ch->tail] = v;
        ch->tail = (ch->tail + 1) %% ch->cap;
        ch->count++;
        pthread_mutex_unlock(&ch->mu);
        return;
    }
    /* Park on the send wait queue. */
    zerg_chan_wait_node_t node;
    node.coro = zerg_current_coro;
    node.value_ptr = &v;
    node.closed_flag = 0;
    zerg_chan_wait_push(&ch->send_head, &ch->send_tail, &node);
    zerg_coro_park(&ch->mu);
    if (node.closed_flag) {
        fprintf(stderr, "zerg: runtime: send on closed channel\n");
        exit(1);
    }
}

static void %[1]s_close(%[1]s *ch) {
    pthread_mutex_lock(&ch->mu);
    if (ch->closed) {
        pthread_mutex_unlock(&ch->mu);
        fprintf(stderr, "zerg: runtime: close on already-closed channel\n");
        exit(1);
    }
    ch->closed = 1;
    zerg_chan_wait_node_t *s;
    while ((s = zerg_chan_wait_pop(&ch->send_head, &ch->send_tail)) != 0) {
        s->closed_flag = 1;
        zerg_coro_unpark(s->coro);
    }
    zerg_chan_wait_node_t *r;
    while ((r = zerg_chan_wait_pop(&ch->recv_head, &ch->recv_tail)) != 0) {
        r->closed_flag = 1;
        zerg_coro_unpark(r->coro);
    }
    pthread_mutex_unlock(&ch->mu);
}

static int %[1]s_ready(void *p, int kind) {
    %[1]s *ch = (%[1]s *)p;
    pthread_mutex_lock(&ch->mu);
    int r = 0;
    int64_t slots = ch->cap > 0 ? ch->cap : 1;
    if (kind == 0) {
        r = (ch->count > 0) || (ch->send_head != 0) || ch->closed;
    } else if (kind == 1) {
        r = ((ch->count < slots) && !ch->closed) || (ch->recv_head != 0);
    }
    pthread_mutex_unlock(&ch->mu);
    return r;
}

`
	const recvTmpl = `static %[3]s %[1]s_recv(%[1]s *ch) {
    pthread_mutex_lock(&ch->mu);
    if (ch->count > 0) {
        %[2]s v = ch->buf[ch->head];
        ch->head = (ch->head + 1) %% ch->cap;
        ch->count--;
        zerg_chan_wait_node_t *s = zerg_chan_wait_pop(&ch->send_head, &ch->send_tail);
        if (s) {
            ch->buf[ch->tail] = *(%[2]s *)s->value_ptr;
            ch->tail = (ch->tail + 1) %% ch->cap;
            ch->count++;
            zerg_coro_t *target = s->coro;
            pthread_mutex_unlock(&ch->mu);
            zerg_coro_unpark(target);
        } else {
            pthread_mutex_unlock(&ch->mu);
        }
        return ((%[3]s){.tag = 0, .payload.p0 = {.a0 = v}});
    }
    zerg_chan_wait_node_t *s = zerg_chan_wait_pop(&ch->send_head, &ch->send_tail);
    if (s) {
        %[2]s v = *(%[2]s *)s->value_ptr;
        zerg_coro_t *target = s->coro;
        pthread_mutex_unlock(&ch->mu);
        zerg_coro_unpark(target);
        return ((%[3]s){.tag = 0, .payload.p0 = {.a0 = v}});
    }
    if (ch->closed) {
        pthread_mutex_unlock(&ch->mu);
        return ((%[3]s){.tag = 1});
    }
    %[2]s slot;
    memset(&slot, 0, sizeof(%[2]s));
    zerg_chan_wait_node_t node;
    node.coro = zerg_current_coro;
    node.value_ptr = &slot;
    node.closed_flag = 0;
    zerg_chan_wait_push(&ch->recv_head, &ch->recv_tail, &node);
    zerg_coro_park(&ch->mu);
    if (node.closed_flag) {
        return ((%[3]s){.tag = 1});
    }
    return ((%[3]s){.tag = 0, .payload.p0 = {.a0 = slot}});
}

`
	fmt.Fprintf(&g.b, makeSendCloseReadyTmpl, cs.chanMang, cs.elemC)
	if cs.optionT != nil {
		fmt.Fprintf(&g.b, recvTmpl, cs.chanMang, cs.elemC, cs.optionMng)
	}
}

// programUsesV07 reports whether any module in the bundle references a
// v0.7 concurrency primitive (chan / spawn / defer / wait_group / select /
// anon-fn / send / recv). The result gates emission of the v0.7 runtime
// prelude so v0.0–v0.6 programs continue to fit under the size guard.
func (g *cgen) programUsesV07() bool {
	for i := range g.modules {
		if g.programUsesV07Walk(g.modules[i].prog) {
			return true
		}
	}
	return false
}

func (g *cgen) programUsesV07Walk(prog *syntax.Program) bool {
	if prog == nil {
		return false
	}
	found := false
	var walkE func(syntax.Expr)
	var walkS func(syntax.Stmt)
	checkType := func(t *syntax.Type) {
		if t == nil {
			return
		}
		if t.Kind == syntax.TypeChan {
			found = true
			return
		}
		if t.Kind == syntax.TypeStruct && t.Name == "WaitGroup" {
			found = true
			return
		}
	}
	walkE = func(e syntax.Expr) {
		if e == nil || found {
			return
		}
		checkType(e.Type())
		switch x := e.(type) {
		case *syntax.ChanConstructorExpr, *syntax.RecvExpr, *syntax.AnonFnExpr:
			_ = x
			found = true
			return
		case *syntax.UnaryExpr:
			walkE(x.Operand)
		case *syntax.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.ParenExpr:
			walkE(x.Inner)
		case *syntax.CallExpr:
			if id, ok := x.Callee.(*syntax.IdentExpr); ok {
				if id.Name == "close" || id.Name == "wait_group" {
					found = true
					return
				}
			}
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.MethodCallExpr:
			walkE(x.Receiver)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.IndexExpr:
			walkE(x.Receiver)
			walkE(x.Index)
		case *syntax.FieldAccessExpr:
			walkE(x.Receiver)
		case *syntax.ListLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.TupleLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.StructLit:
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *syntax.EnumLit:
			for _, sub := range x.Payload {
				walkE(sub)
			}
		case *syntax.PropagateExpr:
			walkE(x.Inner)
		case *syntax.CoalesceExpr:
			walkE(x.Left)
			walkE(x.Right)
		}
	}
	walkS = func(s syntax.Stmt) {
		if s == nil || found {
			return
		}
		switch n := s.(type) {
		case *syntax.SpawnStmt, *syntax.DeferStmt, *syntax.SendStmt, *syntax.SelectStmt:
			_ = n
			found = true
			return
		case *syntax.PrintStmt:
			walkE(n.Expr)
		case *syntax.ExprStmt:
			walkE(n.Expr)
		case *syntax.LetStmt:
			if n.Type != nil && n.Type.Resolved != nil {
				checkType(n.Type.Resolved)
			}
			walkE(n.Value)
		case *syntax.MutStmt:
			if n.Type != nil && n.Type.Resolved != nil {
				checkType(n.Type.Resolved)
			}
			walkE(n.Value)
		case *syntax.ConstStmt:
			walkE(n.Value)
		case *syntax.AssignStmt:
			walkE(n.Value)
		case *syntax.MultiAssignStmt:
			walkE(n.Value)
		case *syntax.IfStmt:
			walkE(n.Cond)
			walkBlock(n.Then, walkS)
			for _, ec := range n.Elifs {
				walkE(ec.Cond)
				walkBlock(ec.Body, walkS)
			}
			if n.Else != nil {
				walkBlock(n.Else, walkS)
			}
		case *syntax.ForStmt:
			if n.Kind == syntax.ForChan {
				found = true
				return
			}
			if n.Iter != nil {
				walkE(n.Iter)
			}
			if n.Cond != nil {
				walkE(n.Cond)
			}
			walkBlock(n.Body, walkS)
		case *syntax.ReturnStmt:
			if n.Value != nil {
				walkE(n.Value)
			}
		case *syntax.MatchStmt:
			walkE(n.Subject)
			for _, arm := range n.Arms {
				walkBlock(arm.Body, walkS)
			}
		case *syntax.FnDecl:
			if n.HasDefers {
				found = true
				return
			}
			for _, p := range n.Params {
				if p.Type != nil && p.Type.Resolved != nil {
					checkType(p.Type.Resolved)
				}
			}
			if n.Return != nil && n.Return.Resolved != nil {
				checkType(n.Return.Resolved)
			}
			walkBlock(n.Body, walkS)
		}
	}
	for _, st := range prog.Statements {
		walkS(st)
		if found {
			return true
		}
	}
	for _, fn := range prog.MonoFns {
		if fn == nil {
			continue
		}
		walkBlock(fn.Body, walkS)
		if found {
			return true
		}
	}
	return found
}

// harvestChanNullableTypes walks the typed AST and records, for each chan
// element type encountered at a RecvExpr or `for v in ch` site, the
// canonical T? *Type that typeck stamped on that expression's
// Type(). chanNullableLookup consults the resulting index so chan recv
// helpers and select recv arms can name the right T? enum.
func (g *cgen) harvestChanNullableTypes(prog *syntax.Program) {
	if prog == nil {
		return
	}
	var walkE func(syntax.Expr)
	var walkS func(syntax.Stmt)
	walkE = func(e syntax.Expr) {
		if e == nil {
			return
		}
		switch x := e.(type) {
		case *syntax.RecvExpr:
			if chT := x.Chan.Type(); chT != nil && chT.Kind == syntax.TypeChan {
				if optT := x.Type(); optT != nil {
					g.chanNullableByElemKey[chT.Element.String()] = optT
				}
			}
			walkE(x.Chan)
		case *syntax.UnaryExpr:
			walkE(x.Operand)
		case *syntax.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.ParenExpr:
			walkE(x.Inner)
		case *syntax.CallExpr:
			walkE(x.Callee)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.MethodCallExpr:
			walkE(x.Receiver)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.IndexExpr:
			walkE(x.Receiver)
			walkE(x.Index)
		case *syntax.FieldAccessExpr:
			walkE(x.Receiver)
		case *syntax.ListLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.TupleLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.StructLit:
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *syntax.EnumLit:
			for _, sub := range x.Payload {
				walkE(sub)
			}
		case *syntax.AnonFnExpr:
			walkBlock(x.Body, walkS)
		case *syntax.PropagateExpr:
			walkE(x.Inner)
		case *syntax.CoalesceExpr:
			walkE(x.Left)
			walkE(x.Right)
		}
	}
	walkS = func(s syntax.Stmt) {
		if s == nil {
			return
		}
		switch n := s.(type) {
		case *syntax.PrintStmt:
			walkE(n.Expr)
		case *syntax.ExprStmt:
			walkE(n.Expr)
		case *syntax.LetStmt:
			walkE(n.Value)
		case *syntax.MutStmt:
			walkE(n.Value)
		case *syntax.ConstStmt:
			walkE(n.Value)
		case *syntax.AssignStmt:
			walkE(n.Target)
			walkE(n.Value)
		case *syntax.MultiAssignStmt:
			for _, t := range n.Targets {
				walkE(t)
			}
			walkE(n.Value)
		case *syntax.IfStmt:
			walkE(n.Cond)
			walkBlock(n.Then, walkS)
			for _, ec := range n.Elifs {
				walkE(ec.Cond)
				walkBlock(ec.Body, walkS)
			}
			if n.Else != nil {
				walkBlock(n.Else, walkS)
			}
		case *syntax.ForStmt:
			if n.Cond != nil {
				walkE(n.Cond)
			}
			if n.Iter != nil {
				walkE(n.Iter)
				if n.Kind == syntax.ForChan {
					if chT := n.Iter.Type(); chT != nil && chT.Kind == syntax.TypeChan {
						// Synthesise the T? from the recv shape — same
						// optionality as a stand-alone RecvExpr would carry.
						if optT, ok := g.findNullableForElem(chT.Element); ok {
							g.chanNullableByElemKey[chT.Element.String()] = optT
						}
					}
				}
			}
			walkBlock(n.Body, walkS)
		case *syntax.ReturnStmt:
			if n.Value != nil {
				walkE(n.Value)
			}
		case *syntax.SpawnStmt:
			walkE(n.Call)
		case *syntax.SendStmt:
			walkE(n.Chan)
			walkE(n.Value)
		case *syntax.DeferStmt:
			walkBlock(n.Body, walkS)
		case *syntax.SelectStmt:
			for _, arm := range n.Arms {
				if arm.Chan != nil {
					walkE(arm.Chan)
					if arm.Op == syntax.SelectRecvBind || arm.Op == syntax.SelectRecvDiscard {
						if chT := arm.Chan.Type(); chT != nil && chT.Kind == syntax.TypeChan {
							if optT, ok := g.findNullableForElem(chT.Element); ok {
								g.chanNullableByElemKey[chT.Element.String()] = optT
							}
						}
					}
				}
				if arm.Value != nil {
					walkE(arm.Value)
				}
				walkBlock(arm.Body, walkS)
			}
		case *syntax.MatchStmt:
			walkE(n.Subject)
			for _, arm := range n.Arms {
				walkBlock(arm.Body, walkS)
			}
		case *syntax.FnDecl:
			walkBlock(n.Body, walkS)
		}
	}
	for _, st := range prog.Statements {
		walkS(st)
	}
	for _, fn := range prog.MonoFns {
		if fn == nil {
			continue
		}
		walkBlock(fn.Body, walkS)
	}
	for _, im := range prog.MonoImpls {
		if im == nil {
			continue
		}
		for _, m := range im.Methods {
			if m != nil {
				walkBlock(m.Body, walkS)
			}
		}
	}
}

// findNullableForElem locates the canonical elem? *Type by scanning
// the harvested map. Used at for-chan / select-recv sites that don't carry
// the nullable type directly on their AST node — we rely on a paired
// RecvExpr having seeded the map first. Returns the nullable *Type and true
// when found. v0.7 corpora always have at least one RecvExpr per chan
// element type (the for-chan body's recv), so the lookup hits.
func (g *cgen) findNullableForElem(elem *syntax.Type) (*syntax.Type, bool) {
	if elem == nil {
		return nil, false
	}
	t, ok := g.chanNullableByElemKey[elem.String()]
	return t, ok
}

// collectChanShapes walks every typed expression / statement in prog and
// registers each TypeChan it encounters. Mirrors the shape-registry walks
// the v0.0–v0.6 codegen runs over list / tuple / struct / enum types.
func (g *cgen) collectChanShapes(prog *syntax.Program) {
	if prog == nil {
		return
	}
	var walkE func(syntax.Expr)
	var walkS func(syntax.Stmt)
	checkType := func(t *syntax.Type) {
		if t == nil {
			return
		}
		if t.Kind == syntax.TypeChan {
			g.addChanShape(t)
		}
		switch t.Kind {
		case syntax.TypeList:
			if t.Element != nil && t.Element.Kind == syntax.TypeChan {
				g.addChanShape(t.Element)
			}
		}
	}
	walkE = func(e syntax.Expr) {
		if e == nil {
			return
		}
		checkType(e.Type())
		switch x := e.(type) {
		case *syntax.UnaryExpr:
			walkE(x.Operand)
		case *syntax.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.ParenExpr:
			walkE(x.Inner)
		case *syntax.CallExpr:
			walkE(x.Callee)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.MethodCallExpr:
			walkE(x.Receiver)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.IndexExpr:
			walkE(x.Receiver)
			walkE(x.Index)
		case *syntax.FieldAccessExpr:
			walkE(x.Receiver)
		case *syntax.ListLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.TupleLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.StructLit:
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *syntax.EnumLit:
			for _, sub := range x.Payload {
				walkE(sub)
			}
		case *syntax.ChanConstructorExpr:
			if t := x.Type(); t != nil {
				g.addChanShape(t)
			}
		case *syntax.RecvExpr:
			walkE(x.Chan)
		case *syntax.AnonFnExpr:
			walkBlock(x.Body, walkS)
		case *syntax.PropagateExpr:
			walkE(x.Inner)
		case *syntax.CoalesceExpr:
			walkE(x.Left)
			walkE(x.Right)
		}
	}
	walkS = func(s syntax.Stmt) {
		if s == nil {
			return
		}
		switch n := s.(type) {
		case *syntax.PrintStmt:
			walkE(n.Expr)
		case *syntax.ExprStmt:
			walkE(n.Expr)
		case *syntax.LetStmt:
			if n.Type != nil && n.Type.Resolved != nil {
				checkType(n.Type.Resolved)
			}
			walkE(n.Value)
		case *syntax.MutStmt:
			if n.Type != nil && n.Type.Resolved != nil {
				checkType(n.Type.Resolved)
			}
			walkE(n.Value)
		case *syntax.ConstStmt:
			walkE(n.Value)
		case *syntax.AssignStmt:
			walkE(n.Target)
			walkE(n.Value)
		case *syntax.MultiAssignStmt:
			for _, t := range n.Targets {
				walkE(t)
			}
			walkE(n.Value)
		case *syntax.IfStmt:
			walkE(n.Cond)
			walkBlock(n.Then, walkS)
			for _, ec := range n.Elifs {
				walkE(ec.Cond)
				walkBlock(ec.Body, walkS)
			}
			if n.Else != nil {
				walkBlock(n.Else, walkS)
			}
		case *syntax.ForStmt:
			if n.Cond != nil {
				walkE(n.Cond)
			}
			if n.Iter != nil {
				walkE(n.Iter)
			}
			walkBlock(n.Body, walkS)
		case *syntax.ReturnStmt:
			if n.Value != nil {
				walkE(n.Value)
			}
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.BreakStmt:
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.ContinueStmt:
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.SpawnStmt:
			walkE(n.Call)
		case *syntax.SendStmt:
			walkE(n.Chan)
			walkE(n.Value)
		case *syntax.DeferStmt:
			walkBlock(n.Body, walkS)
		case *syntax.SelectStmt:
			for _, arm := range n.Arms {
				if arm.Chan != nil {
					walkE(arm.Chan)
				}
				if arm.Value != nil {
					walkE(arm.Value)
				}
				walkBlock(arm.Body, walkS)
			}
		case *syntax.MatchStmt:
			walkE(n.Subject)
			for _, arm := range n.Arms {
				walkBlock(arm.Body, walkS)
			}
		case *syntax.FnDecl:
			for _, p := range n.Params {
				if p.Type != nil && p.Type.Resolved != nil {
					checkType(p.Type.Resolved)
				}
			}
			if n.Return != nil && n.Return.Resolved != nil {
				checkType(n.Return.Resolved)
			}
			walkBlock(n.Body, walkS)
		}
	}
	for _, st := range prog.Statements {
		walkS(st)
	}
	for _, fn := range prog.MonoFns {
		if fn == nil {
			continue
		}
		walkBlock(fn.Body, walkS)
	}
	for _, im := range prog.MonoImpls {
		if im == nil {
			continue
		}
		for _, m := range im.Methods {
			if m != nil {
				walkBlock(m.Body, walkS)
			}
		}
	}
}

// walkBlock invokes f on every statement in b. Helper for collect / register
// passes that walk the whole AST without emitting C.
func walkBlock(b *syntax.Block, f func(syntax.Stmt)) {
	if b == nil {
		return
	}
	for _, st := range b.Statements {
		f(st)
	}
}

// preregisterAnonFns walks prog and pre-allocates an anonFnEmit record for
// every spawn / defer / spawn-of-named-call site, in encounter order. The
// record's fnName is fixed at this point so user-fn bodies (emitted later)
// can call zerg_spawn(<name>, env) without forcing a forward-decl rewrite.
func (g *cgen) preregisterAnonFns(prog *syntax.Program) {
	if prog == nil {
		return
	}
	var walkS func(syntax.Stmt)
	var walkE func(syntax.Expr)
	walkE = func(e syntax.Expr) {
		if e == nil {
			return
		}
		switch x := e.(type) {
		case *syntax.AnonFnExpr:
			// preregisterSpawn already registered an anonFnSpawn record for
			// the spawn-IIFE shape; skip re-registration in that case so the
			// id allocated for the spawn record stays stable.
			if _, already := g.anonByNode[x]; !already {
				rec := g.preallocAnon(anonFnValue)
				rec.anon = x
				g.anonByNode[x] = rec
			}
			walkBlock(x.Body, walkS)
		case *syntax.UnaryExpr:
			walkE(x.Operand)
		case *syntax.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.ParenExpr:
			walkE(x.Inner)
		case *syntax.CallExpr:
			walkE(x.Callee)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.MethodCallExpr:
			walkE(x.Receiver)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.ListLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.TupleLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		}
	}
	walkS = func(s syntax.Stmt) {
		if s == nil {
			return
		}
		switch n := s.(type) {
		case *syntax.SpawnStmt:
			g.preregisterSpawn(n)
			// Walk the spawned call so nested defers inside an IIFE-anon-fn
			// body get pre-registered. preregisterSpawn ALSO records the
			// spawn record itself, so the order of registration matches
			// emit order.
			walkE(n.Call)
		case *syntax.DeferStmt:
			rec := g.preallocAnon(anonFnDefer)
			rec.deferBody = n.Body
			rec.deferEnv = g.collectDeferEnv(n.Body)
			g.anonByNode[n] = rec
			walkBlock(n.Body, walkS)
		case *syntax.IfStmt:
			walkBlock(n.Then, walkS)
			for _, ec := range n.Elifs {
				walkBlock(ec.Body, walkS)
			}
			if n.Else != nil {
				walkBlock(n.Else, walkS)
			}
		case *syntax.ForStmt:
			walkBlock(n.Body, walkS)
		case *syntax.MatchStmt:
			for _, arm := range n.Arms {
				walkBlock(arm.Body, walkS)
			}
		case *syntax.SelectStmt:
			for _, arm := range n.Arms {
				walkBlock(arm.Body, walkS)
			}
		case *syntax.FnDecl:
			walkBlock(n.Body, walkS)
		case *syntax.PrintStmt:
			walkE(n.Expr)
		case *syntax.ExprStmt:
			walkE(n.Expr)
		case *syntax.LetStmt:
			walkE(n.Value)
		case *syntax.MutStmt:
			walkE(n.Value)
		case *syntax.ConstStmt:
			walkE(n.Value)
		case *syntax.AssignStmt:
			walkE(n.Value)
		case *syntax.MultiAssignStmt:
			walkE(n.Value)
		case *syntax.SendStmt:
			walkE(n.Value)
		case *syntax.ReturnStmt:
			if n.Value != nil {
				walkE(n.Value)
			}
		}
	}
	for _, st := range prog.Statements {
		walkS(st)
	}
	for _, fn := range prog.MonoFns {
		if fn == nil {
			continue
		}
		walkBlock(fn.Body, walkS)
	}
	for _, im := range prog.MonoImpls {
		if im == nil {
			continue
		}
		for _, m := range im.Methods {
			if m != nil {
				walkBlock(m.Body, walkS)
			}
		}
	}
}

// preallocAnon creates a fresh anonFnEmit with the given mode and a unique
// id; emits no C, just reserves the slot.
func (g *cgen) preallocAnon(mode anonFnMode) *anonFnEmit {
	g.anonFnCounter++
	id := g.anonFnCounter
	rec := &anonFnEmit{
		id:      id,
		envName: fmt.Sprintf("zerg_env_%d", id),
		mode:    mode,
	}
	switch mode {
	case anonFnSpawn:
		rec.fnName = fmt.Sprintf("zerg_anonfn_%d", id)
	case anonFnDefer:
		rec.fnName = fmt.Sprintf("zerg_defer_%d", id)
	case anonFnSpawnCall:
		rec.fnName = fmt.Sprintf("zerg_spawn_call_%d", id)
	case anonFnValue:
		rec.fnName = fmt.Sprintf("zerg_anonfn_v_%d", id)
	}
	g.anonFns = append(g.anonFns, rec)
	return rec
}

// preregisterSpawn handles the two shapes of `spawn <call>`: an IIFE-anon-fn
// (callee is *AnonFnExpr) and a bare named-fn call. The record's
// AnonFnExpr / spawnCall is stamped here so emitSpawn can route by mode.
func (g *cgen) preregisterSpawn(s *syntax.SpawnStmt) {
	switch call := s.Call.(type) {
	case *syntax.CallExpr:
		if anon, ok := call.Callee.(*syntax.AnonFnExpr); ok {
			rec := g.preallocAnon(anonFnSpawn)
			rec.anon = anon
			g.anonByNode[s] = rec
			// Mark the AnonFnExpr itself so the IIFE-walker doesn't try to
			// re-register it as an anonFnValue when it walks into n.Call.
			g.anonByNode[anon] = rec
			return
		}
		rec := g.preallocAnon(anonFnSpawnCall)
		// Build a synthetic body / env for the spawn-of-named-call so the
		// emitter can render the call body and field-list uniformly.
		exprStmt := &syntax.ExprStmt{Pos: call.Pos, Expr: call}
		rec.deferBody = &syntax.Block{Pos: call.Pos, Statements: []syntax.Stmt{exprStmt}}
		env := &anonFnEnv{}
		for _, a := range call.Args {
			env.names = append(env.names, fmt.Sprintf("__a%d", len(env.names)))
			env.types = append(env.types, a.Type())
		}
		rec.deferEnv = env
		rec.spawnCall = call
		g.anonByNode[s] = rec
	case *syntax.MethodCallExpr:
		// v0.7 codegen does not lower spawn-of-method; emitSpawn returns an
		// error. No record needed.
	}
}

// chanConstructorStr lowers `chan[T]()` / `chan[T](N)` to the per-element
// chan-make helper. Capacity defaults to 0 (unbuffered rendezvous).
func (g *cgen) chanConstructorStr(e *syntax.ChanConstructorExpr) (string, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeChan {
		return "", fmt.Errorf("codegen: chan constructor at %s has non-chan type", e.Pos)
	}
	g.addChanShape(t)
	cm := "zerg_chan_" + g.mangleType(t.Element)
	if e.Capacity == nil {
		return fmt.Sprintf("%s_make(0)", cm), nil
	}
	cap, err := g.exprStr(e.Capacity)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_make(%s)", cm, cap), nil
}

// emitSend lowers `ch <- v` to a send-helper call.
func (g *cgen) emitSend(s *syntax.SendStmt) error {
	chT := s.Chan.Type()
	if chT == nil || chT.Kind != syntax.TypeChan {
		return fmt.Errorf("codegen: send on non-chan at %s", s.Pos)
	}
	g.addChanShape(chT)
	chS, err := g.exprStr(s.Chan)
	if err != nil {
		return err
	}
	vS, err := g.exprStr(s.Value)
	if err != nil {
		return err
	}
	g.writeIndent()
	cm := "zerg_chan_" + g.mangleType(chT.Element)
	fmt.Fprintf(&g.b, "%s_send(%s, %s);\n", cm, chS, vS)
	return nil
}

// recvStr lowers `<- ch` to the recv-helper call. The result is T?.
func (g *cgen) recvStr(e *syntax.RecvExpr) (string, error) {
	chT := e.Chan.Type()
	if chT == nil || chT.Kind != syntax.TypeChan {
		return "", fmt.Errorf("codegen: recv on non-chan at %s", e.Pos)
	}
	g.addChanShape(chT)
	chS, err := g.exprStr(e.Chan)
	if err != nil {
		return "", err
	}
	cm := "zerg_chan_" + g.mangleType(chT.Element)
	return fmt.Sprintf("%s_recv(%s)", cm, chS), nil
}

// emitForChan lowers `for v in ch` to a while loop with recv + nullable match.
//
//	while (1) {
//	    T? __opt = ch_recv(ch);
//	    if (__opt.tag != 0) break;
//	    T v = __opt.payload.p0.a0;
//	    <body>
//	}
func (g *cgen) emitForChan(s *syntax.ForStmt) error {
	chT := s.Iter.Type()
	if chT == nil || chT.Kind != syntax.TypeChan {
		return fmt.Errorf("codegen: for-chan iter has non-chan type at %s", s.Pos)
	}
	g.addChanShape(chT)
	iterS, err := g.exprStr(s.Iter)
	if err != nil {
		return err
	}
	cm := "zerg_chan_" + g.mangleType(chT.Element)
	chTmp := g.freshTmp("ch")
	optTmp := g.freshTmp("opt")

	// Need the T? mangled name; addChanShape ensured the optionT was
	// registered in the shape registry. Look it up via the per-cgen lookup.
	var optionT *syntax.Type
	if g.chanNullableLookup != nil {
		optionT = g.chanNullableLookup(chT.Element)
	}
	if optionT == nil {
		return fmt.Errorf("codegen: missing Option[T] for chan recv at %s", s.Pos)
	}
	optionMng := g.mangleType(optionT)
	elemC := g.cTypeName(chT.Element)
	v := mangle(s.Var)

	g.writeIndent()
	g.b.WriteString("{\n")
	g.indent++
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s *%s = %s;\n", cm, chTmp, iterS)
	g.writeIndent()
	g.b.WriteString("while (1) {\n")
	g.indent++
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s %s = %s_recv(%s);\n", optionMng, optTmp, cm, chTmp)
	g.writeIndent()
	fmt.Fprintf(&g.b, "if (%s.tag != 0) break;\n", optTmp)
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s %s = %s.payload.p0.a0;\n", elemC, v, optTmp)
	for _, st := range s.Body.Statements {
		if err := g.emitStmt(st); err != nil {
			return err
		}
	}
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// ---------------------------------------------------------------------------
// Anonymous fns + spawn + defer.
// ---------------------------------------------------------------------------

// anonFnEmit holds the bookkeeping for one AnonFnExpr that must be emitted
// as a top-level C fn. Each spawn / defer / non-IIFE anon-fn registers one
// entry; the EmitBundle pass walks them after user-fn emission.
type anonFnEmit struct {
	id        int
	anon      *syntax.AnonFnExpr
	envName   string // mangled C struct name for the captured env
	fnName    string // generated thread/defer fn name
	mode      anonFnMode
	deferBody *syntax.Block    // mode == anonFnDefer / anonFnSpawnCall
	deferEnv  *anonFnEnv       // names referenced by the body
	spawnCall *syntax.CallExpr // mode == anonFnSpawnCall
}

type anonFnMode int

const (
	anonFnSpawn anonFnMode = iota
	anonFnDefer
	anonFnValue
	anonFnSpawnCall
)

// anonFnEnv records the capture set for an anon-fn or defer body. Each
// capture becomes one field in the env struct; the spawning / defer site
// initialises the field with a deep-copy of the source binding.
type anonFnEnv struct {
	names []string
	types []*syntax.Type
}

// emitAnonFnHeaders writes env-struct typedefs + forward declarations for
// every pre-registered anon-fn / defer / spawn-call record. Called BEFORE
// user-fn bodies so spawn / defer sites can reference the trampoline names.
func (g *cgen) emitAnonFnHeaders() error {
	if len(g.anonFns) == 0 {
		return nil
	}
	for _, rec := range g.anonFns {
		if err := g.emitAnonEnvStruct(rec); err != nil {
			return err
		}
	}
	for _, rec := range g.anonFns {
		switch rec.mode {
		case anonFnSpawn, anonFnSpawnCall:
			fmt.Fprintf(&g.b, "static void *%s(void *env);\n", rec.fnName)
		case anonFnDefer:
			fmt.Fprintf(&g.b, "static void %s(void *env);\n", rec.fnName)
		case anonFnValue:
			g.writeAnonValueSig(rec)
			g.b.WriteString(";\n")
		}
	}
	g.b.WriteString("\n")
	return nil
}

// writeAnonValueSig renders the C signature of an anonFnValue body fn
// (no trailing punctuation): `static <RetT> <fnName>(void *__env, <params>)`.
// Captures travel through the void* env so all signatures share an ABI shape
// — the bind site casts the void* to the right env-struct pointer inside
// the body.
func (g *cgen) writeAnonValueSig(rec *anonFnEmit) {
	ret := "void"
	if rec.anon.Return != nil && rec.anon.Return.Resolved != nil && rec.anon.Return.Resolved != syntax.TVoid() {
		ret = g.cTypeName(rec.anon.Return.Resolved)
	}
	fmt.Fprintf(&g.b, "static %s %s(void *__env_raw", ret, rec.fnName)
	for _, p := range rec.anon.Params {
		if p.Type == nil || p.Type.Resolved == nil {
			continue
		}
		fmt.Fprintf(&g.b, ", %s %s", g.cTypeName(p.Type.Resolved), mangle(p.Name))
	}
	g.b.WriteByte(')')
}

// emitAnonFnBodies writes the trampoline / body fns. Called AFTER user-fn
// bodies so spawn-of-named-call trampolines can reference those mangled
// symbols.
func (g *cgen) emitAnonFnBodies() error {
	for _, rec := range g.anonFns {
		if err := g.emitAnonBody(rec); err != nil {
			return err
		}
	}
	return nil
}

// emitAnonEnvStruct writes the env-struct typedef for one anon-fn record.
// Each captured binding becomes one field with the binding's resolved type.
func (g *cgen) emitAnonEnvStruct(rec *anonFnEmit) error {
	fmt.Fprintf(&g.b, "typedef struct {\n")
	switch rec.mode {
	case anonFnSpawn, anonFnValue:
		for _, cap := range rec.anon.Captures {
			fmt.Fprintf(&g.b, "    %s %s;\n", g.cTypeName(cap.Type), mangle(cap.Name))
		}
		if len(rec.anon.Captures) == 0 {
			g.b.WriteString("    char _empty;\n")
		}
	case anonFnDefer:
		if rec.deferEnv != nil {
			for i, n := range rec.deferEnv.names {
				fmt.Fprintf(&g.b, "    %s %s;\n", g.cTypeName(rec.deferEnv.types[i]), mangle(n))
			}
		}
		if rec.deferEnv == nil || len(rec.deferEnv.names) == 0 {
			g.b.WriteString("    char _empty;\n")
		}
	case anonFnSpawnCall:
		// The names recorded for spawnCall are synthetic positional slots
		// (`__a0`, `__a1`, ...) generated by the spawn-of-named-call walker.
		// They are compiler-internal and never collide with a user name, so
		// they emit verbatim — no mangle. emitAnonBody / emitSpawnCall both
		// reference the bare `__aN` form, so the struct must match.
		if rec.deferEnv != nil {
			for i, n := range rec.deferEnv.names {
				fmt.Fprintf(&g.b, "    %s %s;\n", g.cTypeName(rec.deferEnv.types[i]), n)
			}
		}
		if rec.deferEnv == nil || len(rec.deferEnv.names) == 0 {
			g.b.WriteString("    char _empty;\n")
		}
	}
	fmt.Fprintf(&g.b, "} %s;\n", rec.envName)
	return nil
}

// emitAnonBody writes the body fn for one anon-fn / defer record. For spawn
// the fn loads each capture into a local matching the captured name, runs
// the body, and returns NULL. For defer the same scheme, then frees env.
func (g *cgen) emitAnonBody(rec *anonFnEmit) error {
	switch rec.mode {
	case anonFnSpawn:
		fmt.Fprintf(&g.b, "static void *%s(void *__env_raw) {\n", rec.fnName)
		fmt.Fprintf(&g.b, "    %s *__env = (%s *)__env_raw;\n", rec.envName, rec.envName)
		// Load each capture into a local of the same name so the body's
		// IdentExpr emits resolve to the loaded local.
		for _, cap := range rec.anon.Captures {
			fmt.Fprintf(&g.b, "    %s %s = __env->%s;\n",
				g.cTypeName(cap.Type), mangle(cap.Name), mangle(cap.Name))
		}
		// Param fns of a spawned anon-fn are zero; spawned fn-call has no args
		// at the call site (parser narrows spawn to `<call>` whose callee may
		// be an AnonFnExpr with zero params per the v0.7 surface). For named
		// fn-call the spawn path takes a different code path (emitSpawnCall).
		prevDeferBaseStack := g.inDeferDrain
		prevHasDef := g.currentHasDefers
		prevEndLabel := g.currentFnEndLabel
		g.inDeferDrain = false
		g.currentHasDefers = rec.anon.HasDefers
		if rec.anon.HasDefers {
			label := fmt.Sprintf("__zerg_anon_end_%d", rec.id)
			g.currentFnEndLabel = label
			g.b.WriteString("    zerg_defer_rec *__zerg_defer_marker = zerg_defer_top;\n")
		}
		prevIndent := g.indent
		g.indent = 1
		for _, st := range rec.anon.Body.Statements {
			if err := g.emitStmt(st); err != nil {
				return err
			}
		}
		if rec.anon.HasDefers {
			fmt.Fprintf(&g.b, "    %s: ;\n", g.currentFnEndLabel)
			g.b.WriteString("    zerg_defer_drain(__zerg_defer_marker);\n")
		}
		g.indent = prevIndent
		g.inDeferDrain = prevDeferBaseStack
		g.currentHasDefers = prevHasDef
		g.currentFnEndLabel = prevEndLabel
		g.b.WriteString("    free(__env);\n")
		g.b.WriteString("    zerg_main_wg_done();\n")
		g.b.WriteString("    return 0;\n")
		g.b.WriteString("}\n\n")
	case anonFnDefer:
		fmt.Fprintf(&g.b, "static void %s(void *__env_raw) {\n", rec.fnName)
		fmt.Fprintf(&g.b, "    %s *__env = (%s *)__env_raw;\n", rec.envName, rec.envName)
		if rec.deferEnv != nil {
			for i, n := range rec.deferEnv.names {
				fmt.Fprintf(&g.b, "    %s %s = __env->%s;\n",
					g.cTypeName(rec.deferEnv.types[i]), mangle(n), mangle(n))
			}
		}
		prevIndent := g.indent
		g.indent = 1
		for _, st := range rec.deferBody.Statements {
			if err := g.emitStmt(st); err != nil {
				return err
			}
		}
		g.indent = prevIndent
		g.b.WriteString("    free(__env);\n")
		g.b.WriteString("}\n\n")
	case anonFnValue:
		g.writeAnonValueSig(rec)
		g.b.WriteString(" {\n")
		fmt.Fprintf(&g.b, "    %s *__env = (%s *)__env_raw;\n", rec.envName, rec.envName)
		_ = rec // silence unused if Captures is empty
		for _, cap := range rec.anon.Captures {
			fmt.Fprintf(&g.b, "    %s %s = __env->%s;\n",
				g.cTypeName(cap.Type), mangle(cap.Name), mangle(cap.Name))
		}
		prevDeferBaseStack := g.inDeferDrain
		prevHasDef := g.currentHasDefers
		prevEndLabel := g.currentFnEndLabel
		g.inDeferDrain = false
		g.currentHasDefers = rec.anon.HasDefers
		if rec.anon.HasDefers {
			label := fmt.Sprintf("__zerg_anon_end_%d", rec.id)
			g.currentFnEndLabel = label
			g.b.WriteString("    zerg_defer_rec *__zerg_defer_marker = zerg_defer_top;\n")
		}
		prevIndent := g.indent
		g.indent = 1
		for _, st := range rec.anon.Body.Statements {
			if err := g.emitStmt(st); err != nil {
				return err
			}
		}
		if rec.anon.HasDefers {
			fmt.Fprintf(&g.b, "    %s: ;\n", g.currentFnEndLabel)
			g.b.WriteString("    zerg_defer_drain(__zerg_defer_marker);\n")
		}
		g.indent = prevIndent
		g.inDeferDrain = prevDeferBaseStack
		g.currentHasDefers = prevHasDef
		g.currentFnEndLabel = prevEndLabel
		g.b.WriteString("}\n\n")
	case anonFnSpawnCall:
		fmt.Fprintf(&g.b, "static void *%s(void *__env_raw) {\n", rec.fnName)
		fmt.Fprintf(&g.b, "    %s *__env = (%s *)__env_raw;\n", rec.envName, rec.envName)
		// Build the call expression by substituting __env->__aN for each arg.
		// We render the original CallExpr's callee (which is an IdentExpr or
		// MethodCallExpr) verbatim, then synthesise the arg list from env
		// fields. This avoids an AST rewrite that would mutate the user's
		// program.
		ident, ok := rec.spawnCall.Callee.(*syntax.IdentExpr)
		if !ok {
			return fmt.Errorf("codegen: spawned named-call requires bare ident callee at %s", rec.spawnCall.Pos)
		}
		// Resolve fn name through the same path callStr uses.
		fn := g.lookupCurrentFn(ident.Name)
		var fnSym string
		if fn != nil {
			fnSym = g.fnCName(fn)
		} else {
			fnSym = mangle(ident.Name)
		}
		g.b.WriteString("    ")
		fmt.Fprintf(&g.b, "%s(", fnSym)
		for i := range rec.spawnCall.Args {
			if i > 0 {
				g.b.WriteString(", ")
			}
			fmt.Fprintf(&g.b, "__env->__a%d", i)
		}
		g.b.WriteString(");\n")
		g.b.WriteString("    free(__env);\n")
		g.b.WriteString("    zerg_main_wg_done();\n")
		g.b.WriteString("    return 0;\n")
		g.b.WriteString("}\n\n")
	}
	return nil
}

// fnValueSig renders the C function-pointer type matching a TypeFn:
// `<RetT> (*)(void *, <param_c_types>...)`. Used at fn-value call sites to
// cast the void* fn-pointer in a zerg_fn_value back to the right shape.
func (g *cgen) fnValueSig(t *syntax.Type) string {
	ret := "void"
	if t.FnReturn != nil && t.FnReturn != syntax.TVoid() {
		ret = g.cTypeName(t.FnReturn)
	}
	var sb strings.Builder
	sb.WriteString(ret)
	sb.WriteString(" (*)(void *")
	for _, p := range t.FnParams {
		sb.WriteString(", ")
		sb.WriteString(g.cTypeName(p))
	}
	sb.WriteByte(')')
	return sb.String()
}

// anonFnValueStr emits an AnonFnExpr in value position (not as IIFE callee
// or spawn body). Builds a heap-allocated env populated with cloned captures
// and produces a `zerg_fn_value` literal that pairs the static body fn
// pointer with the env. Bind sites store this struct directly.
func (g *cgen) anonFnValueStr(e *syntax.AnonFnExpr) (string, error) {
	rec := g.anonByNode[e]
	if rec == nil || rec.mode != anonFnValue {
		return "", fmt.Errorf("codegen: anon-fn at %s missing pre-registered value record", e.Pos)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "({ %s *__env = (%s *)malloc(sizeof(%s)); ",
		rec.envName, rec.envName, rec.envName)
	if len(e.Captures) == 0 {
		sb.WriteString("(void)__env; ")
	}
	for _, cap := range e.Captures {
		fmt.Fprintf(&sb, "__env->%s = %s; ",
			mangle(cap.Name), g.copyExpr(cap.Type, mangle(cap.Name)))
	}
	fmt.Fprintf(&sb, "(zerg_fn_value){.fn = (void *)%s, .env = __env}; })", rec.fnName)
	return sb.String(), nil
}

// iifeCallStr lowers `fn(params) -> R { body }(args)` to a stmt-expression
// that allocates the env on the stack, populates captures, and calls the
// pre-registered top-level body fn directly. No fn-value indirection is
// needed because the callee is statically known at the call site.
//
// Stack-allocating the env keeps the IIFE allocation-free in the common
// no-capture case. The body fn casts `__env_raw` to the env-struct pointer
// regardless of allocation site.
func (g *cgen) iifeCallStr(anon *syntax.AnonFnExpr, args []syntax.Expr) (string, error) {
	rec := g.anonByNode[anon]
	if rec == nil || rec.mode != anonFnValue {
		return "", fmt.Errorf("codegen: IIFE anon-fn at %s missing pre-registered record", anon.Pos)
	}
	var paramTypes []*syntax.Type
	for _, p := range anon.Params {
		if p.Type != nil {
			paramTypes = append(paramTypes, p.Type.Resolved)
		} else {
			paramTypes = append(paramTypes, nil)
		}
	}
	argStrs, err := g.coerceArgs(args, paramTypes)
	if err != nil {
		return "", err
	}
	retT := (*syntax.Type)(nil)
	if anon.Return != nil {
		retT = anon.Return.Resolved
	}
	var sb strings.Builder
	sb.WriteString("({ ")
	fmt.Fprintf(&sb, "%s __env; ", rec.envName)
	if len(anon.Captures) == 0 {
		sb.WriteString("(void)&__env; ")
	}
	for _, cap := range anon.Captures {
		fmt.Fprintf(&sb, "__env.%s = %s; ",
			mangle(cap.Name), g.copyExpr(cap.Type, mangle(cap.Name)))
	}
	if retT != nil && retT != syntax.TVoid() {
		fmt.Fprintf(&sb, "%s(&__env", rec.fnName)
		for _, a := range argStrs {
			sb.WriteString(", ")
			sb.WriteString(a)
		}
		sb.WriteString("); })")
	} else {
		fmt.Fprintf(&sb, "%s(&__env", rec.fnName)
		for _, a := range argStrs {
			sb.WriteString(", ")
			sb.WriteString(a)
		}
		sb.WriteString("); 0; })")
	}
	return sb.String(), nil
}

// fnValueCallStr lowers a call through a fn-typed binding (`f(args)` where
// f's resolved type is TypeFn). Casts the void* fn-pointer in the
// zerg_fn_value to the per-signature C fn-pointer type and invokes through
// the paired .env pointer.
func (g *cgen) fnValueCallStr(ident *syntax.IdentExpr, t *syntax.Type, args []syntax.Expr) (string, error) {
	argStrs, err := g.coerceArgs(args, t.FnParams)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "((%s)(%s).fn)((%s).env",
		g.fnValueSig(t), mangle(ident.Name), mangle(ident.Name))
	for _, a := range argStrs {
		sb.WriteString(", ")
		sb.WriteString(a)
	}
	sb.WriteByte(')')
	return sb.String(), nil
}

// emitSpawn lowers `spawn <call>` to env-alloc + zerg_spawn(rec.fnName, env).
// The pre-registered record (preregisterAnonFns) supplies the trampoline name.
func (g *cgen) emitSpawn(s *syntax.SpawnStmt) error {
	if _, ok := s.Call.(*syntax.MethodCallExpr); ok {
		return fmt.Errorf("codegen: spawn of method call not supported at v0.7 (at %s)", s.Pos)
	}
	rec := g.anonByNode[s]
	if rec == nil {
		return fmt.Errorf("codegen: spawn at %s missing pre-registered record", s.Pos)
	}
	switch rec.mode {
	case anonFnSpawn:
		return g.emitSpawnAnon(rec)
	case anonFnSpawnCall:
		return g.emitSpawnCall(rec)
	}
	return fmt.Errorf("codegen: unexpected spawn record mode at %s", s.Pos)
}

// emitSpawnAnon emits the env-alloc + zerg_spawn call for an IIFE-anon-fn.
// Each capture is cloned via the per-shape <T>_copy helper so the spawned
// closure owns an independent value (PLAN.md §Closure capture semantics).
func (g *cgen) emitSpawnAnon(rec *anonFnEmit) error {
	anon := rec.anon
	envTmp := g.freshTmp("env")
	g.writeIndent()
	g.b.WriteString("{\n")
	g.indent++
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s *%s = (%s *)malloc(sizeof(%s));\n",
		rec.envName, envTmp, rec.envName, rec.envName)
	for _, cap := range anon.Captures {
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s->%s = %s;\n",
			envTmp, mangle(cap.Name), g.copyExpr(cap.Type, mangle(cap.Name)))
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "zerg_spawn(%s, %s);\n", rec.fnName, envTmp)
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// emitSpawnCall handles `spawn foo(args)` where the callee is a named fn.
// Emits a per-call env (one field per arg) and a zerg_spawn call to the
// pre-registered trampoline.
func (g *cgen) emitSpawnCall(rec *anonFnEmit) error {
	envTmp := g.freshTmp("env")
	g.writeIndent()
	g.b.WriteString("{\n")
	g.indent++
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s *%s = (%s *)malloc(sizeof(%s));\n",
		rec.envName, envTmp, rec.envName, rec.envName)
	for i, a := range rec.spawnCall.Args {
		argS, err := g.exprStr(a)
		if err != nil {
			return err
		}
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s->__a%d = %s;\n", envTmp, i, g.copyExpr(a.Type(), argS))
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "zerg_spawn(%s, %s);\n", rec.fnName, envTmp)
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// emitDefer emits a zerg_defer_push call. The pre-registered record supplies
// the body fn name; the env is populated from the body's free-variable set.
func (g *cgen) emitDefer(s *syntax.DeferStmt) error {
	rec := g.anonByNode[s]
	if rec == nil {
		return fmt.Errorf("codegen: defer at %s missing pre-registered record", s.Pos)
	}
	if rec.deferEnv == nil {
		rec.deferEnv = g.collectDeferEnv(s.Body)
	}
	env := rec.deferEnv
	envTmp := g.freshTmp("denv")
	g.writeIndent()
	g.b.WriteString("{\n")
	g.indent++
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s *%s = (%s *)malloc(sizeof(%s));\n",
		rec.envName, envTmp, rec.envName, rec.envName)
	for i, n := range env.names {
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s->%s = %s;\n", envTmp, mangle(n), g.copyExpr(env.types[i], mangle(n)))
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "zerg_defer_push(%s, %s);\n", rec.fnName, envTmp)
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// collectDeferEnv walks a defer body and harvests every IdentExpr name whose
// binding is presumed to live outside the body itself. v0.7 admits defer
// only at fn-body top-level scope (parser-enforced), so every IdentExpr
// inside the body either refers to a fn parameter / local declared above
// the defer or to a fn name (which has no env footprint). We err on the
// side of inclusion: any IdentExpr whose Type()'s resolved is non-nil and
// non-fn becomes an env field. Fn-call callees and built-in idents fall
// through because typeck stamps them with TypeFn or with no Type() at all
// in some paths; the heuristic is good enough for v0.7's corpus.
func (g *cgen) collectDeferEnv(body *syntax.Block) *anonFnEnv {
	seen := map[string]bool{}
	env := &anonFnEnv{}
	var walkE func(syntax.Expr)
	var walkS func(syntax.Stmt)
	addIdent := func(id *syntax.IdentExpr) {
		if id == nil {
			return
		}
		if seen[id.Name] {
			return
		}
		t := id.Type()
		if t == nil || t.Kind == syntax.TypeFn {
			return
		}
		seen[id.Name] = true
		env.names = append(env.names, id.Name)
		env.types = append(env.types, t)
	}
	walkE = func(e syntax.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *syntax.IdentExpr:
			addIdent(x)
		case *syntax.UnaryExpr:
			walkE(x.Operand)
		case *syntax.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.ParenExpr:
			walkE(x.Inner)
		case *syntax.CallExpr:
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.MethodCallExpr:
			walkE(x.Receiver)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.IndexExpr:
			walkE(x.Receiver)
			walkE(x.Index)
		case *syntax.FieldAccessExpr:
			walkE(x.Receiver)
		case *syntax.ListLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.TupleLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.StructLit:
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *syntax.EnumLit:
			for _, sub := range x.Payload {
				walkE(sub)
			}
		case *syntax.RecvExpr:
			walkE(x.Chan)
		case *syntax.PropagateExpr:
			walkE(x.Inner)
		case *syntax.CoalesceExpr:
			walkE(x.Left)
			walkE(x.Right)
		}
	}
	walkS = func(s syntax.Stmt) {
		switch n := s.(type) {
		case *syntax.PrintStmt:
			walkE(n.Expr)
		case *syntax.ExprStmt:
			walkE(n.Expr)
		case *syntax.LetStmt:
			walkE(n.Value)
		case *syntax.MutStmt:
			walkE(n.Value)
		case *syntax.ConstStmt:
			walkE(n.Value)
		case *syntax.AssignStmt:
			walkE(n.Value)
		case *syntax.MultiAssignStmt:
			walkE(n.Value)
		case *syntax.IfStmt:
			walkE(n.Cond)
			if n.Then != nil {
				for _, st := range n.Then.Statements {
					walkS(st)
				}
			}
			if n.Else != nil {
				for _, st := range n.Else.Statements {
					walkS(st)
				}
			}
		case *syntax.SendStmt:
			walkE(n.Chan)
			walkE(n.Value)
		}
	}
	for _, st := range body.Statements {
		walkS(st)
	}
	return env
}

// ---------------------------------------------------------------------------
// Select.
// ---------------------------------------------------------------------------

// emitSelect lowers a SelectStmt to a stack-array of zerg_select_case + a
// zerg_select call + a switch dispatch on the chosen index.
func (g *cgen) emitSelect(s *syntax.SelectStmt) error {
	// Identify the default arm (index in source order) for the runtime.
	hasDefault := false
	defaultIdx := -1
	var armChans []syntax.Expr
	var armChanTypes []*syntax.Type
	for i, arm := range s.Arms {
		if arm.Op == syntax.SelectDefault {
			hasDefault = true
			defaultIdx = i
		}
		armChans = append(armChans, arm.Chan)
		armChanTypes = append(armChanTypes, nil)
	}
	for i, arm := range s.Arms {
		if arm.Chan != nil {
			t := arm.Chan.Type()
			if t != nil && t.Kind == syntax.TypeChan {
				g.addChanShape(t)
				armChanTypes[i] = t
			}
		}
	}

	g.writeIndent()
	g.b.WriteString("{\n")
	g.indent++
	// Materialise each chan operand once into a temp so side-effecting Chan
	// expressions evaluate one time per select.
	chTmps := make([]string, len(s.Arms))
	for i, arm := range s.Arms {
		if arm.Chan == nil {
			continue
		}
		chS, err := g.exprStr(arm.Chan)
		if err != nil {
			return err
		}
		t := armChanTypes[i]
		cm := "zerg_chan_" + g.mangleType(t.Element)
		tmp := g.freshTmp("schan")
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s *%s = %s;\n", cm, tmp, chS)
		chTmps[i] = tmp
	}
	// Build the case array.
	g.writeIndent()
	fmt.Fprintf(&g.b, "zerg_select_case __cases[%d];\n", len(s.Arms))
	for i, arm := range s.Arms {
		switch arm.Op {
		case syntax.SelectRecvBind, syntax.SelectRecvDiscard:
			t := armChanTypes[i]
			cm := "zerg_chan_" + g.mangleType(t.Element)
			g.writeIndent()
			fmt.Fprintf(&g.b, "__cases[%d].kind = 0; __cases[%d].chan = %s; __cases[%d].ready = %s_ready;\n",
				i, i, chTmps[i], i, cm)
		case syntax.SelectSend:
			t := armChanTypes[i]
			cm := "zerg_chan_" + g.mangleType(t.Element)
			g.writeIndent()
			fmt.Fprintf(&g.b, "__cases[%d].kind = 1; __cases[%d].chan = %s; __cases[%d].ready = %s_ready;\n",
				i, i, chTmps[i], i, cm)
		case syntax.SelectDefault:
			g.writeIndent()
			fmt.Fprintf(&g.b, "__cases[%d].kind = 2; __cases[%d].chan = 0; __cases[%d].ready = 0;\n",
				i, i, i)
		}
	}
	hd := "0"
	if hasDefault {
		hd = "1"
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "int __chosen = zerg_select(__cases, %d, %s, %d);\n",
		len(s.Arms), hd, defaultIdx)
	g.writeIndent()
	g.b.WriteString("switch (__chosen) {\n")
	for i, arm := range s.Arms {
		g.writeIndent()
		fmt.Fprintf(&g.b, "case %d: {\n", i)
		g.indent++
		switch arm.Op {
		case syntax.SelectRecvBind:
			t := armChanTypes[i]
			cm := "zerg_chan_" + g.mangleType(t.Element)
			optionT := g.chanNullableLookup(t.Element)
			optionMng := g.mangleType(optionT)
			optTmp := g.freshTmp("opt")
			g.writeIndent()
			fmt.Fprintf(&g.b, "%s %s = %s_recv(%s);\n", optionMng, optTmp, cm, chTmps[i])
			// recv-bind drops the Some/None tag for the user — the bound name
			// receives the inner T directly. The None case (closed-and-empty
			// chan) is dereferenced from zero-init union memory; surface a
			// runtime error matching the interpreter's recv-bind-on-closed
			// path so both halves agree.
			g.writeIndent()
			fmt.Fprintf(&g.b, "if (%s.tag != 0) {\n", optTmp)
			g.indent++
			g.writeIndent()
			g.b.WriteString("fprintf(stderr, \"zerg: runtime: select recv-bind on closed channel\\n\");\n")
			g.writeIndent()
			g.b.WriteString("exit(1);\n")
			g.indent--
			g.writeIndent()
			g.b.WriteString("}\n")
			g.writeIndent()
			fmt.Fprintf(&g.b, "%s %s = %s.payload.p0.a0;\n",
				g.cTypeName(t.Element), mangle(arm.BindName), optTmp)
		case syntax.SelectRecvDiscard:
			t := armChanTypes[i]
			cm := "zerg_chan_" + g.mangleType(t.Element)
			g.writeIndent()
			fmt.Fprintf(&g.b, "(void)%s_recv(%s);\n", cm, chTmps[i])
		case syntax.SelectSend:
			t := armChanTypes[i]
			cm := "zerg_chan_" + g.mangleType(t.Element)
			vS, err := g.exprStr(arm.Value)
			if err != nil {
				return err
			}
			g.writeIndent()
			fmt.Fprintf(&g.b, "%s_send(%s, %s);\n", cm, chTmps[i], vS)
		case syntax.SelectDefault:
			// nothing
		}
		for _, st := range arm.Body.Statements {
			if err := g.emitStmt(st); err != nil {
				return err
			}
		}
		g.writeIndent()
		g.b.WriteString("break;\n")
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
	}
	g.writeIndent()
	g.b.WriteString("}\n")
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// ---------------------------------------------------------------------------
// Defer-aware fn epilogue helpers.
// ---------------------------------------------------------------------------

// fnEndLabel returns the per-fn label name a `?` propagation should jump to
// when the enclosing fn has defers. Empty when no defer drain is needed.
func (g *cgen) fnEndLabel() string {
	if g.currentHasDefers {
		return g.currentFnEndLabel
	}
	return ""
}
