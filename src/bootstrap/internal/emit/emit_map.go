package emit

// This file carries the built-in map[K, V] container's C backend (docs/collections.md).
// A map is a by-value header (zrt_map, map.c) whose only heap is its two storage buffers;
// the header copies/drops through value semantics exactly like a list. Per distinct map
// INSTANCE (a K,V pair) the backend emits:
//   - zg_mvcopy_<n> / zg_mvdrop_<n>  value deep-copy / drop thunks (non-POD value only)
//   - zg_mapvt_<n>                   the static zrt_map_vt the runtime indirects through
//                                    (its hash/eq chosen by the key type: int or str)
//   - zg_mapcopy_<n>                 a value-returning header deep-copy (copyValue site)
//   - zg_mapdropenv_<n>              the cleanup-stack drop-env thunk (registerDrop site)
// Every map is the same C type (zrt_map); only these key/value helpers differ. Keys are
// int or str this phase — both POD — so key_copy/key_drop are always NULL.
//
// The surface is additive and gated on map use: a program that names no map value
// registers no instance, so nothing here fires and its emitted C is byte-identical.

import (
	"fmt"
	"sort"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// mapCarrier is one monomorphized map instance: its numeric id (shared by every
// zg_map*_<id> / zg_mv*_<id> helper) and the concrete key/value types it wraps.
type mapCarrier struct {
	id  int
	key sema.Type
	val sema.Type
}

// prepareMaps numbers every distinct map instance the program uses, keyed by the map
// type's String(). Like prepareLists it reads each monomorphized instance's SUBSTITUTED
// expression and binding types, so a generic-derived map is registered even though the
// shared analysis type is still parametric. A map value nested in another map (or list)
// registers its inner instance first (dependency order).
func (e *emitter) prepareMaps() {
	e.maps = map[string]*mapCarrier{}
	insts := map[string]*types.Map{}
	var consider func(t sema.Type)
	consider = func(t sema.Type) {
		if t == nil {
			return
		}
		switch x := t.(type) {
		case *types.Map:
			if mentionsParam(x) {
				return
			}
			consider(x.Key) // register any inner instance first (dependency order)
			consider(x.Val)
			if _, dup := insts[x.String()]; !dup {
				insts[x.String()] = x
			}
		case *types.List:
			consider(x.Elem)
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

	keys := make([]string, 0, len(insts))
	for k := range insts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		m := insts[k]
		e.maps[k] = &mapCarrier{id: i, key: m.Key, val: m.Val}
	}
	if len(e.maps) > 0 {
		e.needsRuntime = true
	}
}

// mapFor returns the carrier registered for a map type, if any.
func (e *emitter) mapFor(t sema.Type) (*mapCarrier, bool) {
	mp, ok := t.(*types.Map)
	if !ok || len(e.maps) == 0 {
		return nil, false
	}
	c, ok := e.maps[mp.String()]
	return c, ok
}

// mapCopyFn / mapDropEnv / mapVT name a map instance's generated helpers; the map type
// keys them so every site agrees.
func (e *emitter) mapCopyFn(t sema.Type) string {
	if c, ok := e.mapFor(t); ok {
		return fmt.Sprintf("zg_mapcopy_%d", c.id)
	}
	return "zg_mapcopy_0" // unreachable for a checked program
}

func (e *emitter) mapDropEnv(t sema.Type) string {
	if c, ok := e.mapFor(t); ok {
		return fmt.Sprintf("zg_mapdropenv_%d", c.id)
	}
	return "zg_mapdropenv_0"
}

func (e *emitter) mapVT(t sema.Type) string {
	if c, ok := e.mapFor(t); ok {
		return fmt.Sprintf("zg_mapvt_%d", c.id)
	}
	return "zg_mapvt_0"
}

// orderedMaps returns the map carriers sorted by id, so the emitted helpers are
// deterministic and an inner (nested) instance precedes the map holding it.
func (e *emitter) orderedMaps() []*mapCarrier {
	out := make([]*mapCarrier, 0, len(e.maps))
	for _, c := range e.maps {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// mapHashEq returns the built-in hash/eq function names for a map key type (int or
// str, the only keys this phase — enforced by sema's checkMapKey).
func mapHashEq(key sema.Type) (hash, eq string) {
	if key == sema.Str {
		return "zrt_hash_str", "zrt_eq_str"
	}
	return "zrt_hash_int", "zrt_eq_int"
}

// emitMapForwardDecls forward-declares every map helper that a struct/list/map copy or
// drop may reference before its definition, so cross-references resolve regardless of
// emit order. Emitted alongside the list forward decls; nothing for a map-free program.
func (e *emitter) emitMapForwardDecls() {
	if len(e.maps) == 0 {
		return
	}
	for _, c := range e.orderedMaps() {
		e.line(fmt.Sprintf("static zrt_map zg_mapcopy_%d(zrt_map s);", c.id))
		e.line(fmt.Sprintf("static void zg_mapdropenv_%d(void *p);", c.id))
		if containsRef(c.val) {
			e.line(fmt.Sprintf("static void zg_mvcopy_%d(void *dst, const void *src);", c.id))
			e.line(fmt.Sprintf("static void zg_mvdrop_%d(void *val);", c.id))
		}
	}
	e.blank()
}

// emitMapHelpers writes each map instance's key/value vtable and the value-returning
// copy / drop-env helpers, plus the value deep-copy / drop thunks for a non-POD value.
// A POD value carries a NULL copy/drop in the vtable (the runtime's memcpy fast path);
// keys are int/str (POD), so key_copy/key_drop are always NULL. Emits nothing for a
// map-free program.
func (e *emitter) emitMapHelpers() {
	for _, c := range e.orderedMaps() {
		hash, eq := mapHashEq(c.key)
		vct := e.ctype(c.val)
		valCopy, valDrop := "NULL", "NULL"
		if containsRef(c.val) {
			src := fmt.Sprintf("(*(%s*)src)", vct)
			e.line(fmt.Sprintf("static void zg_mvcopy_%d(void *dst, const void *src) { *(%s*)dst = %s; }",
				c.id, vct, e.fieldCopy(c.val, src)))
			val := fmt.Sprintf("(*(%s*)val)", vct)
			e.line(fmt.Sprintf("static void zg_mvdrop_%d(void *val) { %s }", c.id, e.fieldDrop(c.val, val)))
			valCopy = fmt.Sprintf("zg_mvcopy_%d", c.id)
			valDrop = fmt.Sprintf("zg_mvdrop_%d", c.id)
		}
		e.line(fmt.Sprintf("static const zrt_map_vt zg_mapvt_%d = { %s, %s, NULL, NULL, %s, %s };",
			c.id, hash, eq, valCopy, valDrop))
		e.line(fmt.Sprintf("static zrt_map zg_mapcopy_%d(zrt_map s) { zrt_map d; zrt_map_copy(&d, &s); return d; }", c.id))
		e.line(fmt.Sprintf("static void zg_mapdropenv_%d(void *p) { zrt_map_drop((zrt_map*)p); }", c.id))
		e.blank()
	}
}

// --- literals -----------------------------------------------------------------

// mapLit lowers a map literal `{k: v, …}` (or the empty `{:}` in an annotated position)
// to a GNU statement-expression that inits a fresh header and inserts each entry by
// value: the key is copied in (int/str keys are POD, so a plain copy) and the value goes
// through wrapValue+copyValue (a Ref value retains, a nested list/map deep-copies). The
// map's own drop at scope exit releases them.
func (e *emitter) mapLit(n *ast.MapLit, mt *types.Map) string {
	kct, vct := e.ctype(mt.Key), e.ctype(mt.Val)
	t := e.freshName("mp")
	b := fmt.Sprintf("zrt_map %s; zrt_map_init(&%s, sizeof(%s), sizeof(%s), &%s); ",
		t, t, kct, vct, e.mapVT(mt))
	for _, en := range n.Entries {
		kv := e.freshName("mk")
		vv := e.freshName("mv")
		key := e.copyValue(mt.Key, en.Key)
		val := e.wrapValue(mt.Val, e.cur.ExprType(e.info, en.Value), e.copyValue(mt.Val, en.Value))
		b += fmt.Sprintf("%s %s = %s; %s %s = %s; zrt_map_set(&%s, &%s, &%s); ",
			kct, kv, key, vct, vv, val, t, kv, vv)
	}
	return fmt.Sprintf("({ %s%s; })", b, t)
}

// mapMembership lowers `k in m` (GRAMMAR group 4) to zrt_map_has, returning
// ("", false) when the right operand is not a map so the ordinary binary path is
// unaffected. The key is materialized into a temporary and passed by pointer.
func (e *emitter) mapMembership(n *ast.Binary) (string, bool) {
	mt, ok := e.cur.ExprType(e.info, n.R).(*types.Map)
	if !ok {
		return "", false
	}
	ptr, pre, post := e.mapReceiver(n.R)
	kdecl, kref := e.mapKeyPtr(mt, n.L)
	if pre == "" {
		return fmt.Sprintf("({ %szrt_map_has(%s, %s); })", kdecl, ptr, kref), true
	}
	r := e.freshName("mh")
	return fmt.Sprintf("({ %s%sbool %s = zrt_map_has(%s, %s);%s %s; })", pre, kdecl, r, ptr, kref, post, r), true
}

// --- for-in -------------------------------------------------------------------

// forInMap lowers `for k in m` over a map. Each iteration copies the i-th entry's KEY
// (in insertion order) into a `K k` local the body reads; the value is reached in the
// body via `m[k]`. Keys are int/str (POD), so the loop var needs no per-iteration drop.
// The iterated map is frozen against structural change (sema), so the length read each
// turn is stable. A fresh map (a literal, a call result) is materialized into an owned
// temp, dropped on every exit path through the cleanup stack, then walked.
func (e *emitter) forInMap(n *ast.ForStmt, mt *types.Map) {
	if n.Mut {
		// `for mut k in m` would bind the key mutably, but a map key is an immutable frozen
		// snapshot with no write-back — gate it rather than silently drop the edit.
		e.diags.Add(n.Span(), "`for mut k in m` is not supported; a map key is immutable")
		return
	}
	if !isLValueExpr(n.Iter) {
		e.line("{")
		e.indent++
		e.pushScope()
		e.openScope(true, false)
		tmp := e.freshName("foriter")
		e.line(fmt.Sprintf("zrt_map %s = %s;", tmp, e.copyValue(mt, n.Iter)))
		e.registerDrop(tmp, mt, n.Iter)
		e.forInMapBody(n, mt, tmp)
		e.closeScope()
		e.popScope()
		e.indent--
		e.line("}")
		return
	}
	e.forInMapBody(n, mt, e.expr(n.Iter))
}

// forInMapBody emits the counted map iteration over the map named by `base` (an
// addressable place or a materialized temp), copying each entry's key into the loop var
// in insertion order.
func (e *emitter) forInMapBody(n *ast.ForStmt, mt *types.Map, base string) {
	kct := e.ctype(mt.Key)
	iv := e.freshName("i")
	e.line(fmt.Sprintf("for (size_t %s = 0; %s < zrt_map_len(&(%s)); %s++) {", iv, iv, base, iv))
	e.indent++
	e.pushScope()
	cv := e.declareName(n.Var)
	e.openScope(e.subtreeTeardown(n.Body.Stmts), true)
	e.line(fmt.Sprintf("%s = *(%s*)zrt_map_key_at(&(%s), %s);", e.localDecl(mt.Key, cv), kct, base, iv))
	for _, s := range n.Body.Stmts {
		e.stmt(s)
	}
	e.closeScope()
	e.popScope()
	e.indent--
	e.line("}")
}

// --- index / methods ----------------------------------------------------------

// mapReceiver renders access to a map receiver expression as a `zrt_map*`. An
// addressable place (an Ident or Field) is taken by `&(place)` with no teardown; any
// other expression is a FRESH owned map materialized into a temporary in `pre` and
// dropped by `post` (both spliced into the surrounding statement-expression). Mirrors
// listReceiver.
func (e *emitter) mapReceiver(x ast.Expr) (ptr, pre, post string) {
	if isLValueExpr(x) {
		return "&(" + e.expr(x) + ")", "", ""
	}
	mt := e.cur.ExprType(e.info, x)
	tmp := e.freshName("mp")
	return "&" + tmp, fmt.Sprintf("zrt_map %s = %s; ", tmp, e.copyValue(mt, x)), fmt.Sprintf(" zrt_map_drop(&%s);", tmp)
}

// mapKeyPtr renders a `&K tmp` holding the index/argument key so it can be passed to the
// runtime by pointer. Keys are int/str (POD), so a plain copy suffices; the temporary
// lives inside the caller's statement-expression.
func (e *emitter) mapKeyPtr(mt *types.Map, key ast.Expr) (decl, ref string) {
	kct := e.ctype(mt.Key)
	k := e.freshName("mk")
	return fmt.Sprintf("%s %s = %s; ", kct, k, e.expr(key)), "&" + k
}

// mapIndex lowers `m[k]` on a map base: it reads the value slot through the runtime
// (aborting on a missing key = KeyError). On an addressable base it yields a BORROW —
// the slot value itself — and the retain/deep-copy is inserted at the copy site
// (namesStorage treats a map index as storage), so a transient read (`deref(m[k])`)
// never over-retains and a bound value (`v := m[k]`) owns its own copy. When the base is
// a FRESH map materialized into a temporary, the value is copied out before the
// temporary is dropped, so this returns an OWNED copy that outlives it.
func (e *emitter) mapIndex(n *ast.Bracket, mt *types.Map) string {
	vct := e.ctype(mt.Val)
	ptr, pre, post := e.mapReceiver(n.Base)
	kdecl, kref := e.mapKeyPtr(mt, n.Elems[0])
	slot := fmt.Sprintf("(*(%s*)zrt_map_index(%s, %s))", vct, ptr, kref)
	if pre == "" {
		return fmt.Sprintf("({ %s%s; })", kdecl, slot) // a borrow into an addressable map's storage
	}
	// materialized base: copy the value out before the temporary map is dropped.
	val := slot
	if containsRef(mt.Val) {
		val = e.fieldCopy(mt.Val, slot)
	}
	r := e.freshName("mr")
	return fmt.Sprintf("({ %s%s%s %s = %s;%s %s; })", pre, kdecl, vct, r, val, post, r)
}

// mapMethodEmit lowers a built-in map method call `m.len()` / `.get(k)` (typed in
// sema.mapMethodCall), returning ("", false) for any other call.
func (e *emitter) mapMethodEmit(n *ast.Call) (string, bool) {
	fld, ok := n.Callee.(*ast.Field)
	if !ok {
		return "", false
	}
	mt, ok := e.cur.ExprType(e.info, fld.X).(*types.Map)
	if !ok {
		return "", false
	}
	ptr, pre, post := e.mapReceiver(fld.X)
	vct := e.ctype(mt.Val)
	switch fld.Name {
	case "len":
		if pre == "" {
			return fmt.Sprintf("((int64_t)zrt_map_len(%s))", ptr), true
		}
		r := e.freshName("ln")
		return fmt.Sprintf("({ %sint64_t %s = (int64_t)zrt_map_len(%s);%s %s; })", pre, r, ptr, post, r), true
	case "get":
		// -> V?: a present value builds Some(copy), a NULL (missing) the None case, both
		// through the optional carrier wrapValue already knows how to construct.
		optT := e.cur.ExprType(e.info, n)
		kdecl, kref := e.mapKeyPtr(mt, n.Args[0].Value)
		p := e.freshName("gp")
		slot := fmt.Sprintf("(*(%s*)%s)", vct, p)
		some := e.wrapValue(optT, mt.Val, slot)
		if containsRef(mt.Val) {
			some = e.wrapValue(optT, mt.Val, e.fieldCopy(mt.Val, slot))
		}
		none := e.wrapValue(optT, sema.Nil, "0")
		head := fmt.Sprintf("%s%s *%s = zrt_map_get(%s, %s)", kdecl, vct, p, ptr, kref)
		pick := fmt.Sprintf("%s ? %s : %s", p, some, none)
		if pre == "" {
			return fmt.Sprintf("({ %s; %s; })", head, pick), true
		}
		r := e.freshName("go")
		return fmt.Sprintf("({ %s%s; %s %s = %s;%s %s; })", pre, head, e.ctype(optT), r, pick, post, r), true
	}
	return "", false
}

// mapIndexAssign lowers `m[k] = v` on a mut map target to zrt_map_set (which drops the
// old value on a hit then stores the new), returning ("", false) for any other target.
// The key and value are copied in, so a Ref value retains and a replaced value releases
// inside zrt_map_set.
func (e *emitter) mapIndexAssign(n *ast.Reassign) (string, bool) {
	lv, ok := n.Target.(*ast.LValueTarget)
	if !ok {
		return "", false
	}
	br, ok := lv.X.(*ast.Bracket)
	if !ok || len(br.Elems) != 1 {
		return "", false
	}
	mt, ok := e.cur.ExprType(e.info, br.Base).(*types.Map)
	if !ok {
		return "", false
	}
	kct, vct := e.ctype(mt.Key), e.ctype(mt.Val)
	k := e.freshName("mk")
	v := e.freshName("mv")
	key := e.copyValue(mt.Key, br.Elems[0])
	val := e.wrapValue(mt.Val, e.cur.ExprType(e.info, n.Value), e.copyValue(mt.Val, n.Value))
	return fmt.Sprintf("({ %s %s = %s; %s %s = %s; zrt_map_set(&(%s), &%s, &%s); })",
		kct, k, key, vct, v, val, e.expr(br.Base), k, v), true
}
