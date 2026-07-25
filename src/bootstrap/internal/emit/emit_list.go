package emit

// This file carries the built-in list[T] container's C backend (docs/collections.md).
// A list is a by-value header (zrt_list, list.c) whose only heap is its element
// buffer; the header copies/drops through value semantics exactly like a Ref or a
// tuple carrier. Per distinct list ELEMENT type the backend emits:
//   - zg_lcopy_<n> / zg_ldrop_<n>  element deep-copy / drop thunks (non-POD elem only)
//   - zg_listvt_<n>                the static zrt_elem_vt the runtime indirects through
//   - zg_listcopy_<n>              a value-returning header deep-copy (copyValue site)
//   - zg_listdropenv_<n>           the cleanup-stack drop-env thunk (registerDrop site)
// Every list is the same C type (zrt_list), so only these element helpers differ.
//
// The surface is additive and gated on list use: a program that names no list value
// registers no instance, so nothing here fires and its emitted C is byte-identical.

import (
	"fmt"
	"sort"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// listCarrier is one monomorphized list element instance: its numeric id (shared by
// every zg_list*_<id> helper) and the concrete element type it wraps.
type listCarrier struct {
	id   int
	elem sema.Type
}

// prepareLists numbers every distinct list instance the program uses, keyed by the
// element type's String(). It is STRONGER than prepareTuples' analysis-map-only scan:
// it iterates each monomorphized instance and reads its SUBSTITUTED expression and
// binding types, so a generic-derived list — e.g. `fn box[T](x: T) -> list[T]` at
// `box[int]` yielding `list[int]` — is registered even though the shared analysis type
// is the still-parametric `list[T]`. A nested list (list[list[int]]) registers its
// inner element before the outer, so the emitted helpers are in dependency order.
func (e *emitter) prepareLists() {
	e.lists = map[string]*listCarrier{}
	elems := map[string]sema.Type{}
	var consider func(t sema.Type)
	consider = func(t sema.Type) {
		if t == nil {
			return
		}
		switch x := t.(type) {
		case *types.List:
			if mentionsParam(x) {
				return
			}
			consider(x.Elem) // register the inner element first (dependency order)
			if _, dup := elems[x.String()]; !dup {
				elems[x.String()] = x.Elem
			}
		case *types.Map:
			// a list nested in a map's key/value (map[int, list[int]]) must register too.
			consider(x.Key)
			consider(x.Val)
		case *types.Tuple:
			for _, el := range x.Elems {
				consider(el)
			}
		case *types.Array:
			consider(x.Elem)
		case *types.Opt:
			consider(x.Elem)
		case *types.Ref:
			consider(x.Elem)
		}
	}
	// Concrete, non-generic positions (the analysis maps), like prepareTuples.
	for _, sig := range e.info.Funcs {
		consider(sig.Ret)
		for _, p := range sig.Params {
			consider(p)
		}
	}
	for _, t := range e.info.BindTypes {
		consider(t)
	}
	for _, t := range e.info.ExprTypes {
		consider(t)
	}
	// Substituted positions per instance: catches every generic-derived list.
	for _, inst := range e.prog.Funcs {
		consider(inst.Ret)
		consider(inst.Recv)
		for _, p := range inst.Params {
			consider(p)
		}
		for node := range e.info.ExprTypes {
			consider(inst.ExprType(e.info, node))
		}
		for node := range e.info.BindTypes {
			consider(inst.BindType(e.info, node))
		}
	}
	// A list nested in a specialized nominal type's field/payload.
	for _, ti := range e.prog.Types {
		for _, f := range ti.Fields {
			consider(f.Type)
		}
		for _, v := range ti.Variants {
			for _, pt := range v.Payload {
				consider(pt)
			}
		}
	}

	keys := make([]string, 0, len(elems))
	for k := range elems {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		e.lists[k] = &listCarrier{id: i, elem: elems[k]}
	}
	if len(e.lists) > 0 {
		e.needsRuntime = true
	}
}

// listFor returns the carrier registered for a list type, if any.
func (e *emitter) listFor(t sema.Type) (*listCarrier, bool) {
	lst, ok := t.(*types.List)
	if !ok || len(e.lists) == 0 {
		return nil, false
	}
	c, ok := e.lists[lst.String()]
	return c, ok
}

// listCopyFn / listDropEnv / listVT / listElemCopy / listElemDrop name a list
// instance's generated helpers; the element type keys them so every site agrees.
func (e *emitter) listCopyFn(t sema.Type) string {
	if c, ok := e.listFor(t); ok {
		return fmt.Sprintf("zg_listcopy_%d", c.id)
	}
	return "zg_listcopy_0" // unreachable for a checked program
}

func (e *emitter) listDropEnv(t sema.Type) string {
	if c, ok := e.listFor(t); ok {
		return fmt.Sprintf("zg_listdropenv_%d", c.id)
	}
	return "zg_listdropenv_0"
}

func (e *emitter) listVT(t sema.Type) string {
	if c, ok := e.listFor(t); ok {
		return fmt.Sprintf("zg_listvt_%d", c.id)
	}
	return "zg_listvt_0"
}

// orderedLists returns the list carriers sorted by id, so the emitted helpers are
// deterministic and an inner (nested) element precedes the list holding it.
func (e *emitter) orderedLists() []*listCarrier {
	out := make([]*listCarrier, 0, len(e.lists))
	for _, c := range e.lists {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// emitListForwardDecls forward-declares every list helper that a struct copy/drop or
// another list helper may reference before its definition, so cross-references (a
// struct with a list field, a list of structs, a nested list) resolve regardless of
// emit order. Emitted before the struct helpers; nothing for a list-free program.
func (e *emitter) emitListForwardDecls() {
	if len(e.lists) == 0 {
		return
	}
	for _, c := range e.orderedLists() {
		e.line(fmt.Sprintf("static zrt_list zg_listcopy_%d(zrt_list s);", c.id))
		e.line(fmt.Sprintf("static void zg_listdropenv_%d(void *p);", c.id))
		if e.containsRef(c.elem) {
			e.line(fmt.Sprintf("static void zg_lcopy_%d(void *dst, const void *src);", c.id))
			e.line(fmt.Sprintf("static void zg_ldrop_%d(void *elem);", c.id))
		}
	}
	e.blank()
}

// emitListHelpers writes each list instance's element vtable and the value-returning
// copy / drop-env helpers, plus the element deep-copy / drop thunks for a non-POD
// element. A POD element (no contained Ref) carries a NULL vtable and takes the
// runtime's memcpy fast path, so it needs no thunk. Emits nothing for a list-free
// program.
func (e *emitter) emitListHelpers() {
	for _, c := range e.orderedLists() {
		ct := e.ctype(c.elem)
		if e.containsRef(c.elem) {
			src := fmt.Sprintf("(*(%s*)src)", ct)
			e.line(fmt.Sprintf("static void zg_lcopy_%d(void *dst, const void *src) { *(%s*)dst = %s; }",
				c.id, ct, e.fieldCopy(c.elem, src)))
			elem := fmt.Sprintf("(*(%s*)elem)", ct)
			e.line(fmt.Sprintf("static void zg_ldrop_%d(void *elem) { %s }", c.id, e.fieldDrop(c.elem, elem)))
			e.line(fmt.Sprintf("static const zrt_elem_vt zg_listvt_%d = { zg_lcopy_%d, zg_ldrop_%d };", c.id, c.id, c.id))
		} else {
			e.line(fmt.Sprintf("static const zrt_elem_vt zg_listvt_%d = { NULL, NULL };", c.id))
		}
		e.line(fmt.Sprintf("static zrt_list zg_listcopy_%d(zrt_list s) { zrt_list d; zrt_list_copy(&d, &s); return d; }", c.id))
		e.line(fmt.Sprintf("static void zg_listdropenv_%d(void *p) { zrt_list_drop((zrt_list*)p); }", c.id))
		e.blank()
	}
}

// --- literals -----------------------------------------------------------------

// listLit lowers a list literal `[a, b, …]` (in list, not array, position) to a GNU
// statement-expression that inits a fresh header and pushes each element by value:
// each element goes through wrapValue+copyValue, so a Ref element retains (move-in)
// and a nested list deep-copies. The list's own drop at scope exit releases them.
func (e *emitter) listLit(n *ast.ListLit, lt *types.List) string {
	ct := e.ctype(lt.Elem)
	t := e.freshName("lst")
	var b string
	b += fmt.Sprintf("zrt_list %s; zrt_list_init(&%s, sizeof(%s), &%s); ", t, t, ct, e.listVT(lt))
	for _, el := range n.Elems {
		ev := e.freshName("le")
		val := e.wrapValue(lt.Elem, e.cur.ExprType(e.info, el), e.copyValue(lt.Elem, el))
		b += fmt.Sprintf("%s %s = %s; zrt_list_push(&%s, &%s); ", ct, ev, val, t, ev)
	}
	return fmt.Sprintf("({ %s%s; })", b, t)
}

// listFill lowers the fill form `[v; N]` in list position to N pushes of a copy of v
// (N retains for a Ref element). An ARRAY-typed target keeps the existing C-array
// initializer path in expr(); only a list target reaches here.
func (e *emitter) listFill(n *ast.ListFill, lt *types.List) string {
	ct := e.ctype(lt.Elem)
	t := e.freshName("lst")
	iv := e.freshName("i")
	le := e.freshName("le")
	init := fmt.Sprintf("zrt_list %s; zrt_list_init(&%s, sizeof(%s), &%s); ", t, t, ct, e.listVT(lt))
	val := e.wrapValue(lt.Elem, e.cur.ExprType(e.info, n.Value), e.copyValue(lt.Elem, n.Value))
	loop := fmt.Sprintf("for (size_t %s = 0; %s < (size_t)(%s); %s++) { %s %s = %s; zrt_list_push(&%s, &%s); } ",
		iv, iv, e.expr(n.Count), iv, ct, le, val, t, le)
	return fmt.Sprintf("({ %s%s%s; })", init, loop, t)
}

// --- index / methods ----------------------------------------------------------

// listReceiver renders access to a list receiver expression as a `zrt_list*`. An
// addressable place (an Ident or Field) is taken by `&(place)` with no teardown. Any
// other expression is a FRESH owned list (a literal, a call result, or an outer
// index `xss[i]`): it is materialized into a temporary in `pre` and must be dropped
// by `post`, both spliced into the surrounding statement-expression by the caller.
func (e *emitter) listReceiver(x ast.Expr) (ptr, pre, post string) {
	if isLValueExpr(x) {
		return "&(" + e.expr(x) + ")", "", ""
	}
	// A non-place receiver (a literal, a call, or an outer index `xss[i]` that borrows a
	// slot) is materialized into an OWNED temporary: copyValue deep-copies a borrow and
	// moves a fresh producer, so dropping the temporary never frees a buffer the source
	// still owns.
	lt := e.cur.ExprType(e.info, x)
	tmp := e.freshName("lst")
	return "&" + tmp, fmt.Sprintf("zrt_list %s = %s; ", tmp, e.copyValue(lt, x)), fmt.Sprintf(" zrt_list_drop(&%s);", tmp)
}

// listIndex lowers `xs[i]` on a list base: it reads element i's slot through the
// runtime (aborting on OOB = IndexError). On an addressable base it yields a BORROW —
// the slot value itself — and the retain/deep-copy is inserted at the copy site
// (copyValue treats a list index as naming storage), so a transient read (`deref(xs[i])`)
// never over-retains and a bound element (`v := xs[i]`) owns its own copy. When the
// base is a FRESH list that had to be materialized into a temporary, the temporary is
// dropped here, so this must return an OWNED copy that outlives it.
func (e *emitter) listIndex(n *ast.Bracket, lt *types.List) string {
	ct := e.ctype(lt.Elem)
	ptr, pre, post := e.listReceiver(n.Base)
	slot := fmt.Sprintf("(*(%s*)zrt_list_at(%s, %s))", ct, ptr, e.expr(n.Elems[0]))
	if pre == "" {
		return slot // a borrow into an addressable list's buffer
	}
	// materialized base: copy the element out before the temporary list is dropped.
	val := slot
	if e.containsRef(lt.Elem) {
		val = e.fieldCopy(lt.Elem, slot)
	}
	r := e.freshName("le")
	return fmt.Sprintf("({ %s%s %s = %s;%s %s; })", pre, ct, r, val, post, r)
}

// listMethodEmit lowers a built-in list method call `xs.len()` / `.get(i)` /
// `.append(v)` (typed in sema.listMethodCall), returning ("", false) for any other
// call so the generic call path is unaffected.
func (e *emitter) listMethodEmit(n *ast.Call) (string, bool) {
	fld, ok := n.Callee.(*ast.Field)
	if !ok {
		return "", false
	}
	lt, ok := e.cur.ExprType(e.info, fld.X).(*types.List)
	if !ok {
		return "", false
	}
	ptr, pre, post := e.listReceiver(fld.X)
	ct := e.ctype(lt.Elem)
	switch fld.Name {
	case "len":
		if pre == "" {
			return fmt.Sprintf("((int64_t)zrt_list_len(%s))", ptr), true
		}
		r := e.freshName("ln")
		return fmt.Sprintf("({ %sint64_t %s = (int64_t)zrt_list_len(%s);%s %s; })", pre, r, ptr, post, r), true
	case "get":
		// -> T?: a present slot builds Some(copy), a NULL (OOB) slot the None case, both
		// through the optional carrier wrapValue already knows how to construct.
		optT := e.cur.ExprType(e.info, n)
		p := e.freshName("gp")
		slot := fmt.Sprintf("(*(%s*)%s)", ct, p)
		some := e.wrapValue(optT, lt.Elem, slot)
		if e.containsRef(lt.Elem) {
			some = e.wrapValue(optT, lt.Elem, e.fieldCopy(lt.Elem, slot))
		}
		none := e.wrapValue(optT, sema.Nil, "0")
		head := fmt.Sprintf("%s *%s = zrt_list_get(%s, %s)", ct, p, ptr, e.expr(n.Args[0].Value))
		pick := fmt.Sprintf("%s ? %s : %s", p, some, none)
		if pre == "" {
			return fmt.Sprintf("({ %s; %s; })", head, pick), true
		}
		// materialized: capture the picked optional BEFORE dropping the temporary list.
		r := e.freshName("go")
		return fmt.Sprintf("({ %s%s; %s %s = %s;%s %s; })", pre, head, e.ctype(optT), r, pick, post, r), true
	case "append":
		v := e.freshName("le")
		val := e.wrapValue(lt.Elem, e.cur.ExprType(e.info, n.Args[0].Value), e.copyValue(lt.Elem, n.Args[0].Value))
		return fmt.Sprintf("({ %s%s %s = %s; zrt_list_push(%s, &%s);%s })", pre, ct, v, val, ptr, v, post), true
	}
	return "", false
}

// listIndexAssign lowers `xs[i] = v` on a mut list target to zrt_list_set (which drops
// the old element then stores the new), returning ("", false) for any other target so
// an ordinary assignment is unaffected. The value is copied in, so a Ref element
// retains and the replaced element releases inside zrt_list_set.
func (e *emitter) listIndexAssign(n *ast.Reassign) (string, bool) {
	lv, ok := n.Target.(*ast.LValueTarget)
	if !ok {
		return "", false
	}
	br, ok := lv.X.(*ast.Bracket)
	if !ok || len(br.Elems) != 1 {
		return "", false
	}
	lt, ok := e.cur.ExprType(e.info, br.Base).(*types.List)
	if !ok {
		return "", false
	}
	ct := e.ctype(lt.Elem)
	v := e.freshName("le")
	val := e.wrapValue(lt.Elem, e.cur.ExprType(e.info, n.Value), e.copyValue(lt.Elem, n.Value))
	return fmt.Sprintf("({ %s %s = %s; zrt_list_set(&(%s), %s, &%s); })",
		ct, v, val, e.expr(br.Base), e.expr(br.Elems[0]), v), true
}
