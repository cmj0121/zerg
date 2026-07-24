package emit

// This file carries the Phase 1d iteration-2 additions to the C backend: value
// COPY semantics (U2) and the refcounted heap box Ref[T] (U3). The whole surface
// is additive and gated on non-POD types: a value-only program (every numbered
// example — int/float/bool/str) transitively holds no Ref, so nothing here fires
// and its emitted C stays byte-identical to Phase 0.
//
// The model:
//   - A type is POD ("plain old data") when it transitively holds no Ref. A POD
//     value copies with a bare C '=' at every bind/param/return — unchanged.
//   - A Ref[T] value is a 'void*' to a zrt_ref_alloc'd header+payload. Copying an
//     existing Ref retains (zrt_ref_copy); dropping one releases (zrt_release);
//     the last holder frees it exactly once. Constructing one moves (no retain).
//   - A non-POD struct gets a generated zg_copy_<T> (retain inner Refs) and
//     zg_drop_<T> (release inner Refs, reverse field order).
//
// Copy-vs-move: copying an lvalue (an Ident/Field naming existing storage) retains;
// a fresh producer (a constructor/call rvalue) is moved, so a newly built Ref is
// not double-counted.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// --- POD-ness (structural) ----------------------------------------------------

// containsRef reports whether a type transitively holds a Ref[T] (or a channel,
// also a ref type). Such a type is NOT plain-old-data: copying it must retain and
// dropping it must release. Every primitive, str, and ptr — and any struct/tuple/
// array built only from those — is POD, so its copy is a bare C '='.
func containsRef(t sema.Type) bool {
	switch x := t.(type) {
	case *types.Ref:
		return true
	case *types.List:
		// a list is NEVER plain-old-data: even a list[int] owns a heap buffer, so a bare
		// C `=` would alias it and double-free at the two holders' scope exits. It must
		// always flow through zrt_list_copy / zrt_list_drop (docs/collections.md).
		return true
	case *types.Map:
		// a map is NEVER plain-old-data (like a list): even a map[int,int] owns heap
		// storage, so it must always flow through zrt_map_copy / zrt_map_drop.
		return true
	case *types.Chan:
		// a channel is a ref type per memory.md; its runtime is 1e, so it never
		// actually reaches a copy site this phase, but POD-ness must still exclude it.
		return true
	case *types.Tuple:
		for _, e := range x.Elems {
			if containsRef(e) {
				return true
			}
		}
	case *types.Array:
		return containsRef(x.Elem)
	case *types.Opt:
		return containsRef(x.Elem)
	case *types.Struct:
		for _, ft := range structFieldTypes(x) {
			if containsRef(ft) {
				return true
			}
		}
	case *types.Enum:
		for _, pt := range enumPayloadTypes(x) {
			if containsRef(pt) {
				return true
			}
		}
	}
	return false
}

// structFieldTypes returns a use-site struct's field types with the struct's
// generic parameters bound to its type arguments, so 'Box[Ref[int]]' reads its
// field as 'Ref[int]'.
func structFieldTypes(s *types.Struct) []sema.Type {
	if s.Def == nil || s.Def.Struct == nil {
		return nil
	}
	sub := paramSub(s.Def.Params, s.Args)
	out := make([]sema.Type, 0, len(s.Def.Struct.Fields))
	for _, f := range s.Def.Struct.Fields {
		out = append(out, sema.Substitute(f.Type, sub, nil))
	}
	return out
}

// enumPayloadTypes returns a use-site enum's payload types with its generic
// parameters bound to its type arguments.
func enumPayloadTypes(e *types.Enum) []sema.Type {
	if e.Def == nil || e.Def.Enum == nil {
		return nil
	}
	sub := paramSub(e.Def.Params, e.Args)
	var out []sema.Type
	for _, v := range e.Def.Enum.Variants {
		for _, pt := range v.Payload {
			out = append(out, sema.Substitute(pt, sub, nil))
		}
	}
	return out
}

// paramSub builds the substitution binding a nominal type's declared parameters to
// its use-site type arguments.
func paramSub(params []*types.Param, args []sema.Type) map[string]sema.Type {
	if len(params) == 0 || len(args) == 0 {
		return nil
	}
	sub := map[string]sema.Type{}
	for i, p := range params {
		if i < len(args) {
			sub[p.Name] = args[i]
		}
	}
	return sub
}

// --- runtime pre-pass ---------------------------------------------------------

// prepareRuntime scans the program for Ref use before the header is printed. It
// sets needsRuntime (so "zergrt.h" is included and the runtime linked) and collects
// the distinct element types of every Ref construction, each of which needs a
// zg_refnew_<n> allocation helper. It leaves needsRuntime untouched for a value-only
// program, keeping that program byte-identical.
func (e *emitter) prepareRuntime() {
	e.refnewIdx = map[string]int{}
	if e.programUsesRef() || e.programUsesRuntimeStmt() {
		e.needsRuntime = true
	}
	// Concurrency is the 1e gate: a program that uses `spawn` (or a channel) links the
	// scheduler and runs main under it. It implies the runtime. A program with none of
	// these leaves it false and links nothing new, so its C stays byte-identical.
	if e.programUsesConcurrency() {
		e.concurrency = true
		e.needsRuntime = true
	}
	// Number the channel receive element types and detect channel-handle drops, so the
	// Result[T] carriers and drop thunks are ready before any body is emitted.
	e.prepareChannels()
	// Number the general Result/Either/optional carriers (Phase 1f U0), after the
	// channel prepass so a channel recv's Result[T] keeps its own carrier and is not
	// double-registered. Sets needsResult/needsRuntime only when a carrier is found.
	e.prepareResults()
	// Number the tuple value shapes (completeness iteration 2, U2), after the Result
	// prepass so a tuple's element ctype (which a Result carrier may influence) is
	// settled. Leaves the tuple map empty for a program with no tuple value.
	e.prepareTuples()
	// Number the list instances (docs/collections.md), keyed by element type. Leaves
	// the list map empty for a program with no list value, which stays byte-identical.
	e.prepareLists()
	// Number the map instances (docs/collections.md), keyed by the K,V pair. Leaves the
	// map map empty for a program with no map value, which stays byte-identical.
	e.prepareMaps()
	// A program that imports io (lowers a write intrinsic) or that carries a
	// Result[nil] in any signature (e.g. a bundled io function) needs the runtime:
	// the io primitives ride in the always-linked sys.c, and Result[nil] spells
	// zrt_result_nil from zergrt.h. A program with neither leaves needsRuntime as it
	// was, so it stays byte-identical.
	if e.programUsesIO() {
		e.needsIO = true
		e.needsRuntime = true
	}
	if e.programUsesFormat() {
		e.needsFormat = true
		e.needsRuntime = true
	}
	if e.programUsesResultNil() {
		e.needsRuntime = true
	}
	// Concatenating two strings calls zrt_str_concat, which rides in the always-linked
	// fmt.c, so the program needs the runtime. A str comparison lowers to strcmp and
	// needs nothing beyond <string.h>.
	if e.programUsesStrConcat() {
		e.needsRuntime = true
	}
	// Deterministically number the Ref construction element types (sorted by their
	// source spelling), so the emitted helper names are stable run to run.
	seen := map[string]sema.Type{}
	for node, t := range e.info.ExprTypes {
		call, ok := node.(*ast.Call)
		if !ok || !e.isRefConstruction(call) {
			continue
		}
		if ref, ok := t.(*types.Ref); ok && ref.Elem != nil {
			seen[ref.Elem.String()] = ref.Elem
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		e.refnewIdx[k] = i
		e.refnewElems = append(e.refnewElems, seen[k])
	}
}

// programUsesRuntimeStmt reports whether the program contains a `defer`, `with`, or
// `raise` — statements that drive the runtime cleanup/abort machinery even when no
// Ref is present, so the emitted C must include and link the runtime.
func (e *emitter) programUsesRuntimeStmt() bool {
	for _, inst := range e.prog.Funcs {
		found := false
		walkStmts(inst.Origin.Body, func(s ast.Stmt) {
			switch s.(type) {
			case *ast.DeferStmt, *ast.WithStmt, *ast.RaiseStmt:
				found = true
			}
		})
		if found {
			return true
		}
	}
	return false
}

// programUsesIO reports whether the program lowers a stdlib `io` write intrinsic
// (`__zrt_write` / `__zrt_write_int`) — the leaf of a bundled io function. A shadowed
// spelling is left to the ordinary call path (info.Refs records the user binding), so
// it does not count as an io use.
func (e *emitter) programUsesIO() bool {
	for node := range e.info.ExprTypes {
		call, ok := node.(*ast.Call)
		if !ok {
			continue
		}
		id, ok := call.Callee.(*ast.Ident)
		if !ok {
			continue
		}
		if _, shadowed := e.info.Refs[id]; shadowed {
			continue
		}
		if id.Name == "__zrt_write" || id.Name == "__zrt_write_int" {
			return true
		}
	}
	return false
}

// programUsesResultNil reports whether any Result[nil] value appears in the program
// — as a function signature (return/parameter), or as the type of any expression or
// binding (A12: a `guard { void }` / if-expr / block-expr yielding Result[nil]). Such
// a value is spelled zrt_result_nil, which needs the runtime header even when the
// program registers no general carrier. isResultNil matches only Either[nil, Err], so
// a value-only program has none and stays byte-identical.
func (e *emitter) programUsesResultNil() bool {
	for _, sig := range e.info.Funcs {
		if isResultNil(sig.Ret) {
			return true
		}
		for _, p := range sig.Params {
			if isResultNil(p) {
				return true
			}
		}
	}
	for _, t := range e.info.ExprTypes {
		if isResultNil(t) {
			return true
		}
	}
	for _, t := range e.info.BindTypes {
		if isResultNil(t) {
			return true
		}
	}
	return false
}

// programUsesRef reports whether any binding, expression, or function signature in
// the program has a type that transitively holds a Ref — the single trigger that
// pulls in the runtime for value-copy/refcount code.
func (e *emitter) programUsesRef() bool {
	for _, t := range e.info.BindTypes {
		if containsRef(t) {
			return true
		}
	}
	for _, t := range e.info.ExprTypes {
		if containsRef(t) {
			return true
		}
	}
	for _, sig := range e.info.Funcs {
		if containsRef(sig.Ret) {
			return true
		}
		for _, p := range sig.Params {
			if containsRef(p) {
				return true
			}
		}
	}
	return false
}

// isRefConstruction reports whether a call is a 'Ref(v)' / 'Ref[T](v)' box
// construction (as opposed to 'deref' or any other call): its callee is the
// unshadowed builtin name 'Ref'. A shadowing binding records the callee in
// info.Refs, so its presence there means the name is a user symbol, not the builtin.
func (e *emitter) isRefConstruction(n *ast.Call) bool {
	switch c := n.Callee.(type) {
	case *ast.Ident:
		_, shadowed := e.info.Refs[c]
		return c.Name == "Ref" && !shadowed
	case *ast.Bracket:
		id, ok := c.Base.(*ast.Ident)
		if !ok {
			return false
		}
		_, shadowed := e.info.Refs[id]
		return id.Name == "Ref" && !shadowed
	}
	return false
}

// isDerefCall reports whether a call is the unshadowed 'deref(r)' reader.
func (e *emitter) isDerefCall(n *ast.Call) bool {
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	_, shadowed := e.info.Refs[id]
	return id.Name == "deref" && !shadowed
}

// --- builtin call emit --------------------------------------------------------

// builtinCallEmit lowers the Phase 1d Ref builtins. A construction 'Ref(v)' becomes
// a call to the per-element zg_refnew_<n> helper (which allocates, stores v, and
// returns the box); 'deref(r)' reads the payload back through zrt_ref_payload.
func (e *emitter) builtinCallEmit(n *ast.Call) (string, bool) {
	switch {
	case e.isRefConstruction(n):
		ref, _ := e.cur.ExprType(e.info, n).(*types.Ref)
		arg := ""
		if len(n.Args) == 1 {
			arg = e.copyValue(ref.Elem, n.Args[0].Value)
		}
		return fmt.Sprintf("%s(%s)", e.refnewName(ref.Elem), arg), true
	case e.isDerefCall(n):
		if len(n.Args) != 1 {
			return "0", true
		}
		elem := e.cur.ExprType(e.info, n)
		arg := e.expr(n.Args[0].Value)
		// A fresh-container index (`deref({…}[k])`, `deref([…][i])`) hands deref an OWNED
		// Ref copy with no other holder — the materialized-base fieldCopy — so reading its
		// payload and dropping it on the floor leaks the box (balance 1). Read the payload
		// into a value, THEN release the transient box, so it is freed exactly once. A
		// non-POD payload (Ref-of-Ref) is left on the plain read (the value would need its
		// own retain before the release); it is exotic and keeps the prior behaviour.
		if !containsRef(elem) && e.producesOwnedTransientRef(n.Args[0].Value) {
			box := e.freshName("drefbox")
			val := e.freshName("drefval")
			ct := e.ctype(elem)
			return fmt.Sprintf("({ void *%s = %s; %s %s = (*(%s*)zrt_ref_payload(%s)); zrt_release(%s); %s; })",
				box, arg, ct, val, ct, box, box, val), true
		}
		return fmt.Sprintf("(*(%s*)zrt_ref_payload(%s))", e.ctype(elem), arg), true
	}
	if s, ok := e.atomicIntrinsicEmit(n); ok {
		return s, true
	}
	return e.writeIntrinsicEmit(n)
}

// atomicIntrinsicEmit lowers the Phase 1f Atomic[int] intrinsics — `__zrt_atomic_load`
// / `_store` / `_swap` / `_add` / `_cas` — that the stdlib `atomic` module's leaves
// call. The first argument is the shared cell, a `Ref[int]` (a `void*` box); its int64
// payload pointer, `(int64_t*)zrt_ref_payload(box)`, is passed to the matching sys.c
// SC primitive, with any remaining int operands forwarded. A shadowed spelling defers
// to the ordinary call path, so the intrinsic never masks a user symbol.
func (e *emitter) atomicIntrinsicEmit(n *ast.Call) (string, bool) {
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return "", false
	}
	if _, shadowed := e.info.Refs[id]; shadowed {
		return "", false
	}
	fn, ok := map[string]string{
		"__zrt_atomic_load":  "zrt_atomic_load",
		"__zrt_atomic_store": "zrt_atomic_store",
		"__zrt_atomic_swap":  "zrt_atomic_swap",
		"__zrt_atomic_add":   "zrt_atomic_add",
		"__zrt_atomic_cas":   "zrt_atomic_cas",
	}[id.Name]
	if !ok || len(n.Args) < 1 {
		return "", false
	}
	cell := fmt.Sprintf("(int64_t*)zrt_ref_payload(%s)", e.expr(n.Args[0].Value))
	args := []string{cell}
	for _, a := range n.Args[1:] {
		args = append(args, e.expr(a.Value))
	}
	return fmt.Sprintf("%s(%s)", fn, strings.Join(args, ", ")), true
}

// writeIntrinsicEmit lowers the Phase 1f io write intrinsics — `__zrt_write(fd, s)`
// and `__zrt_write_int(fd, n)` — that the stdlib `io` module's leaves call. Each
// maps to its always-linked sys.c primitive, cast to the intrinsic's int result. A
// name shadowed by a user binding (recorded in info.Refs) is left to the ordinary
// call path, so the intrinsic never masks a user symbol.
func (e *emitter) writeIntrinsicEmit(n *ast.Call) (string, bool) {
	id, ok := n.Callee.(*ast.Ident)
	if !ok || len(n.Args) != 2 {
		return "", false
	}
	if _, shadowed := e.info.Refs[id]; shadowed {
		return "", false
	}
	fd, val := e.expr(n.Args[0].Value), e.expr(n.Args[1].Value)
	switch id.Name {
	case "__zrt_write":
		return fmt.Sprintf("((int64_t)zrt_write_str(%s, %s))", fd, val), true
	case "__zrt_write_int":
		return fmt.Sprintf("((int64_t)zrt_write_int(%s, %s))", fd, val), true
	}
	return "", false
}

// refnewName is the allocation helper for a Ref whose payload is elem.
func (e *emitter) refnewName(elem sema.Type) string {
	return fmt.Sprintf("zg_refnew_%d", e.refnewIdx[elem.String()])
}

// --- copy insertion -----------------------------------------------------------

// copyValue renders the C for an expression used as a copied value at a bind,
// argument, or return site. A POD value copies with a bare '=' (byte-identical). A
// non-POD value that names existing storage (an lvalue Ident/Field) is retained or
// deep-copied so both holders own it; a fresh producer (a constructor/call rvalue)
// is moved unchanged, so a newly built Ref is not over-counted.
func (e *emitter) copyValue(typ sema.Type, x ast.Expr) string {
	base := e.expr(x)
	if !containsRef(typ) || !e.namesStorage(x) {
		return base
	}
	switch t := typ.(type) {
	case *types.Ref:
		return fmt.Sprintf("zrt_ref_copy(%s)", base)
	case *types.List:
		// copying a list value that names existing storage deep-copies its buffer (each
		// element via the instance vtable), so the two holders never alias.
		return fmt.Sprintf("%s(%s)", e.listCopyFn(t), base)
	case *types.Map:
		// copying a map value that names existing storage deep-copies its storage (each
		// value via the instance vtable), so the two holders never alias.
		return fmt.Sprintf("%s(%s)", e.mapCopyFn(t), base)
	case *types.Chan:
		// copying a channel handle retains it; a send-capable handle (bidi/send-only)
		// also bumps the sender count, a receive-only handle does not.
		if t.Dir == types.ChanRecv {
			return fmt.Sprintf("zrt_chan_copy(%s)", base)
		}
		return fmt.Sprintf("zrt_chan_sender_copy(%s)", base)
	case *types.Struct:
		return fmt.Sprintf("%s(%s)", e.copyHelperName(typ), base)
	}
	e.unsupportedRef(x, typ)
	return base
}

// isLValueExpr reports whether an expression names existing storage (so copying it
// must retain), as opposed to producing a fresh value (which is moved). Only a bare
// identifier or a field access qualifies in this iteration.
func isLValueExpr(x ast.Expr) bool {
	switch x.(type) {
	case *ast.Ident, *ast.Field:
		return true
	}
	return false
}

// namesStorage extends isLValueExpr with a list index `xs[i]`, which borrows the
// element's slot storage: copying it must retain/deep-copy the element (so a `v :=
// xs[i]` owns an independent copy), while a transient read leaves the borrow alone.
// A non-list bracket (an array index, a type-arg bracket) is not treated as owned
// storage here, keeping every existing copy site byte-identical.
func (e *emitter) namesStorage(x ast.Expr) bool {
	if isLValueExpr(x) {
		return true
	}
	if br, ok := x.(*ast.Bracket); ok {
		switch e.cur.ExprType(e.info, br.Base).(type) {
		case *types.List, *types.Map:
			// Only an ADDRESSABLE container's index borrows its slot storage (listIndex/
			// mapIndex take the borrow branch), so copying it must retain/deep-copy. A FRESH
			// (rvalue) container's index took the materialized branch and ALREADY returned an
			// OWNED copy; retaining again would double-count (leak on the bound/argument path).
			// The base is storage only when it is itself an lvalue — a nested `xss[i][j]`'s
			// base `xss[i]` is a Bracket (already materialized to an owned copy), so it must
			// move, not retain.
			return isLValueExpr(br.Base)
		}
		return false
	}
	return false
}

// producesOwnedTransientRef reports whether an expression is a FRESH-base container
// index whose element is Ref-bearing — the exact shape for which listIndex/mapIndex
// return an OWNED copy (the materialized-base fieldCopy) rather than a borrow. A
// transient reader of such a value (deref) must release that owned copy after reading
// it, or the box leaks (balance 1). A named-base index is a borrow and must NOT be
// released here (the container still owns it).
func (e *emitter) producesOwnedTransientRef(x ast.Expr) bool {
	br, ok := x.(*ast.Bracket)
	if !ok || len(br.Elems) != 1 || isLValueExpr(br.Base) {
		return false
	}
	switch b := e.cur.ExprType(e.info, br.Base).(type) {
	case *types.List:
		return containsRef(b.Elem)
	case *types.Map:
		return containsRef(b.Val)
	}
	return false
}

// copyHelperName / dropHelperName name the generated per-type copy and drop
// helpers; the C type name (a specialized struct's mangled name) keys them, so they
// agree with the definitions emitted in emitRefHelpers.
func (e *emitter) copyHelperName(t sema.Type) string { return "zg_copy_" + e.ctype(t) }
func (e *emitter) dropHelperName(t sema.Type) string { return "zg_drop_" + e.ctype(t) }

// unsupportedRef records that a non-POD shape outside this iteration's supported
// set (bare Ref and struct-of-Ref) reached a copy/drop site, so the program fails
// to compile with a clear message rather than emitting a refcount-leaking '='.
func (e *emitter) unsupportedRef(at ast.Node, t sema.Type) {
	span := token.Span{}
	if at != nil {
		span = at.Span()
	}
	e.diags.Add(span, "copying a %s is not supported in Phase 1d iteration 2 (only Ref[T] and structs holding Refs)", t)
}

// --- generated helpers --------------------------------------------------------

// emitRefHelpers writes the per-type copy/drop helpers for every non-POD struct the
// program specializes, then the zg_refnew_<n> allocation helper for every Ref
// construction element type. They are emitted after the struct typedefs and before
// the function bodies, so a body can call them. A value-only program specializes no
// non-POD type and constructs no Ref, so this emits nothing.
func (e *emitter) emitRefHelpers() {
	// zg_ref_drop is the cleanup-stack thunk for a bare Ref local: it reads the
	// current pointer through the binding's slot and releases it unless a 'del'
	// nulled the slot. Registering the slot (not the value) lets a later 'del' or
	// reassignment change what the scope-exit release targets, so a Ref is freed
	// exactly once on every path (U4/U5).
	if e.programHasRefLocal() {
		e.line("static void zg_ref_drop(void *slot) {")
		e.indent++
		e.line("void **s = (void **)slot;")
		e.line("if (*s != NULL) { zrt_release(*s); }")
		e.indent--
		e.line("}")
		e.blank()
	}
	// Forward-declare the list helpers before the struct helpers, so a struct with a
	// list field (whose zg_copy_ references zg_listcopy_<n>) resolves; the definitions
	// follow after the struct helpers, where a list-of-struct's element thunk can see
	// the struct's own zg_copy_.
	e.emitListForwardDecls()
	e.emitMapForwardDecls()
	for _, ti := range e.prog.Types {
		if !e.tiContainsRef(ti) {
			continue
		}
		if ti.IsEnum {
			e.diags.Add(token.Span{}, "Ref inside an enum is not supported in Phase 1d iteration 2")
			continue
		}
		e.structCopyDrop(ti)
	}
	for _, elem := range e.refnewElems {
		e.refnewHelper(elem)
	}
	// List element vtables + copy/drop definitions (after the struct helpers so a
	// list-of-struct's element thunk can call the struct's zg_copy_/zg_drop_).
	e.emitListHelpers()
	// Map key/val vtables + copy/drop definitions (after the list helpers so a map value
	// that is a list can call the list's zg_listcopy_/zg_listdropenv_).
	e.emitMapHelpers()
	e.emitDeferHelpers()
	e.emitSpawnHelpers()
	e.emitChanHelpers()
	// Result/Either/optional force helpers (Phase 1f U0); emits nothing when the
	// program registered no carrier.
	e.emitResultHelpers()
}

// programHasRefLocal reports whether the program binds a bare Ref[T] to a name (a
// let binding, a by-value parameter, or a 'with' resource) — the only shape that
// registers the zg_ref_drop cleanup thunk. A struct-of-Ref uses its own drop-env
// thunk instead, so this stays false for a program that only nests Refs in structs.
func (e *emitter) programHasRefLocal() bool {
	isRef := func(t sema.Type) bool { _, ok := t.(*types.Ref); return ok }
	for _, t := range e.info.BindTypes {
		if isRef(t) {
			return true
		}
	}
	for _, sig := range e.info.Funcs {
		for _, p := range sig.Params {
			if isRef(p) {
				return true
			}
		}
	}
	for _, inst := range e.prog.Funcs {
		found := false
		walkStmts(inst.Origin.Body, func(s ast.Stmt) {
			if w, ok := s.(*ast.WithStmt); ok && isRef(inst.ExprType(e.info, w.Resource)) {
				found = true
			}
			// A `for x in xs` over a list whose element bears a Ref copies each element
			// into a Ref-bearing loop var that registers the zg_ref_drop guard per
			// iteration (forInListBody), so the thunk must be emitted even when the program
			// binds no other bare Ref local.
			if f, ok := s.(*ast.ForStmt); ok && f.Iter != nil {
				if lt, ok := inst.ExprType(e.info, f.Iter).(*types.List); ok && containsRef(lt.Elem) {
					found = true
				}
			}
		})
		if found {
			return true
		}
	}
	return false
}

// tiContainsRef reports whether a specialized nominal type instance transitively
// holds a Ref (its fields/payloads are already concrete).
func (e *emitter) tiContainsRef(ti *mono.TypeInstance) bool {
	for _, f := range ti.Fields {
		if containsRef(f.Type) {
			return true
		}
	}
	for _, v := range ti.Variants {
		for _, pt := range v.Payload {
			if containsRef(pt) {
				return true
			}
		}
	}
	return false
}

// structCopyDrop emits a non-POD struct's copy and drop helpers. copy shallow-copies
// the struct (POD fields and Ref pointers) then retains/deep-copies each non-POD
// field; drop releases/deep-drops each non-POD field in reverse declaration order.
func (e *emitter) structCopyDrop(ti *mono.TypeInstance) {
	name := ti.Mangled

	e.line(fmt.Sprintf("static %s zg_copy_%s(%s x) {", name, name, name))
	e.indent++
	e.line(fmt.Sprintf("%s r = x;", name))
	for _, f := range ti.Fields {
		if !containsRef(f.Type) {
			continue
		}
		e.line(fmt.Sprintf("r.zg_%s = %s;", f.Name, e.fieldCopy(f.Type, "x.zg_"+f.Name)))
	}
	e.line("return r;")
	e.indent--
	e.line("}")

	e.line(fmt.Sprintf("static void zg_drop_%s(%s *x) {", name, name))
	e.indent++
	for i := len(ti.Fields) - 1; i >= 0; i-- {
		f := ti.Fields[i]
		if !containsRef(f.Type) {
			continue
		}
		e.line(e.fieldDrop(f.Type, "x->zg_"+f.Name))
	}
	e.indent--
	e.line("}")

	// zg_dropenv_<T> adapts the typed deep-drop to the runtime cleanup stack's
	// void* thunk signature, so a struct local's teardown can be scheduled on scope
	// entry and run on every exit path (fallthrough, early return/break, abort).
	e.line(fmt.Sprintf("static void zg_dropenv_%s(void *p) { zg_drop_%s((%s *)p); }", name, name, name))
	e.blank()
}

// fieldCopy renders the retained/deep copy of one non-POD field access.
func (e *emitter) fieldCopy(t sema.Type, access string) string {
	switch t.(type) {
	case *types.Ref:
		return fmt.Sprintf("zrt_ref_copy(%s)", access)
	case *types.List:
		// a list field / element deep-copies through its instance's value copy helper.
		return fmt.Sprintf("%s(%s)", e.listCopyFn(t), access)
	case *types.Map:
		// a map field / element / value deep-copies through its instance's value copy helper.
		return fmt.Sprintf("%s(%s)", e.mapCopyFn(t), access)
	case *types.Struct:
		return fmt.Sprintf("%s(%s)", e.copyHelperName(t), access)
	}
	e.unsupportedRef(nil, t)
	return access
}

// fieldDrop renders the release/deep drop of one non-POD field access.
func (e *emitter) fieldDrop(t sema.Type, access string) string {
	switch t.(type) {
	case *types.Ref:
		return fmt.Sprintf("zrt_release(%s);", access)
	case *types.List:
		return fmt.Sprintf("zrt_list_drop(&(%s));", access)
	case *types.Map:
		return fmt.Sprintf("zrt_map_drop(&(%s));", access)
	case *types.Struct:
		return fmt.Sprintf("%s(&%s);", e.dropHelperName(t), access)
	}
	e.unsupportedRef(nil, t)
	return ";"
}

// refnewHelper emits a Ref allocation helper for a payload type: it allocates the
// box (with the payload's drop thunk, or NULL for a POD payload), stores the value,
// and returns the box pointer. A single expression, so a construction site is one
// call — and it exercises zrt_ref_alloc / zrt_ref_payload exactly as the design
// specifies the payload is reached.
func (e *emitter) refnewHelper(elem sema.Type) {
	ct := e.ctype(elem)
	idx := e.refnewIdx[elem.String()]
	// A non-POD struct payload needs a thunk adapting its typed drop to void*.
	if _, ok := elem.(*types.Struct); ok && containsRef(elem) {
		e.line(fmt.Sprintf("static void zg_refdrop_%d(void *p) { %s((%s*)p); }", idx, e.dropHelperName(elem), ct))
	}
	e.line(fmt.Sprintf("static void *zg_refnew_%d(%s v) {", idx, ct))
	e.indent++
	e.line(fmt.Sprintf("void *r = zrt_ref_alloc(sizeof(%s), %s);", ct, e.refDropFn(elem)))
	e.line(fmt.Sprintf("*(%s*)zrt_ref_payload(r) = v;", ct))
	e.line("return r;")
	e.indent--
	e.line("}")
	e.blank()
}

// refDropFn is the zrt_drop_fn a Ref carries for a payload type: NULL for a POD
// payload (nothing to tear down), else a thunk that adapts the payload's typed drop
// helper to the runtime's void* signature. A payload that itself holds a Ref but is
// not a supported struct shape is reported (see unsupportedRef).
func (e *emitter) refDropFn(elem sema.Type) string {
	if !containsRef(elem) {
		return "NULL"
	}
	if _, ok := elem.(*types.Struct); ok {
		return "&" + e.refThunkName(elem)
	}
	e.unsupportedRef(nil, elem)
	return "NULL"
}

func (e *emitter) refThunkName(elem sema.Type) string {
	return fmt.Sprintf("zg_refdrop_%d", e.refnewIdx[elem.String()])
}
