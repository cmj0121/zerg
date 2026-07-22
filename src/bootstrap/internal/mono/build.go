package mono

import (
	"strconv"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Build walks the checked file and produces the monomorphized Program (DESIGN-1c
// §4.2). It seeds the work-list with every non-generic top-level function in source
// order — a generic function is not instantiated until a concrete call reaches it —
// then specializes each queued instance to a fixpoint, enqueuing any new
// specialization it discovers.
func Build(file *ast.File, info *sema.Info) *Program {
	w := &worker{
		info: info,
		prog: &Program{
			Info:      info,
			byMangled: map[string]*Instance{},
			typeByKey: map[string]*TypeInstance{},
		},
	}
	for _, d := range file.Items {
		if fn, ok := d.(*ast.FuncDecl); ok && !isGeneric(info, fn) {
			w.enqueueFn(fn, nil, nil)
		}
	}
	for len(w.queue) > 0 {
		in := w.queue[0]
		w.queue = w.queue[1:]
		w.specialize(in)
	}
	return w.prog
}

// worker carries the fixpoint state: the analysis Info, the Program under
// construction, and the pending instance queue.
type worker struct {
	info  *sema.Info
	prog  *Program
	queue []*Instance
}

// isGeneric reports whether a function has type or value parameters, using the
// resolved signature so an empty parameter list is treated as non-generic.
func isGeneric(info *sema.Info, fn *ast.FuncDecl) bool {
	sig := info.Funcs[fn.Name]
	return sig != nil && sig.Generic != nil
}

// enqueueFn returns the instance of fn specialized by (subT, subV), creating and
// enqueuing it on first sight and deduplicating by mangled name (DESIGN-1c §4.3):
// the same function at the same type/value arguments yields one instance and one C
// function. The concrete signature is built by substituting the arguments into the
// resolved signature.
func (w *worker) enqueueFn(fn *ast.FuncDecl, subT map[string]types.Type, subV map[string]types.ConstVal) *Instance {
	sig := w.info.Funcs[fn.Name]
	mangled := mangleFn(fn.Name, sig.Generic, subT, subV)
	if in, ok := w.prog.byMangled[mangled]; ok {
		return in
	}
	in := &Instance{
		Origin:     fn,
		Mangled:    mangled,
		ParamNames: sig.ParamNames,
		Params:     make([]types.Type, len(sig.Params)),
		Ret:        sema.Substitute(sig.Ret, subT, subV),
		subT:       subT,
		subV:       subV,
		Calls:      map[*ast.Call]string{},
	}
	for i, p := range sig.Params {
		in.Params[i] = sema.Substitute(p, subT, subV)
	}
	w.prog.byMangled[mangled] = in
	w.prog.Funcs = append(w.prog.Funcs, in)
	if fn.Name == "main" && subT == nil && subV == nil {
		w.prog.Main = in
	}
	w.queue = append(w.queue, in)
	return in
}

// specialize walks an instance's signature and body, registering every specialized
// nominal type it uses and resolving every generic call site to its callee instance
// (enqueuing the callee when first seen). It reads all types through the instance's
// overlay, so a call inside a generic body resolves against concrete argument types.
func (w *worker) specialize(in *Instance) {
	w.collectType(in.Ret)
	for _, p := range in.Params {
		w.collectType(p)
	}
	if in.Origin.Body != nil {
		for _, s := range in.Origin.Body.Stmts {
			w.walkStmt(in, s)
		}
	}
}

// --- body walk ----------------------------------------------------------------

func (w *worker) walkStmt(in *Instance, s ast.Stmt) {
	switch n := s.(type) {
	case *ast.BindStmt:
		w.collectType(in.BindType(w.info, n))
		w.walkExpr(in, n.Value)
	case *ast.Reassign:
		w.walkExpr(in, n.Value)
	case *ast.PrintStmt:
		w.walkExpr(in, n.Value)
	case *ast.ReturnStmt:
		w.walkExpr(in, n.Value)
		w.walkExpr(in, n.Cond)
	case *ast.BreakStmt:
		w.walkExpr(in, n.Cond)
	case *ast.ContinueStmt:
		w.walkExpr(in, n.Cond)
	case *ast.IfStmt:
		for _, br := range n.Branches {
			w.walkExpr(in, br.Cond)
			w.walkBlock(in, br.Body)
		}
		w.walkBlock(in, n.Else)
	case *ast.ForStmt:
		w.walkExpr(in, n.Cond)
		w.walkBlock(in, n.Body)
	case *ast.ExprStmt:
		w.walkExpr(in, n.X)
	}
}

func (w *worker) walkBlock(in *Instance, b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		w.walkStmt(in, s)
	}
}

func (w *worker) walkExpr(in *Instance, e ast.Expr) {
	if e == nil {
		return
	}
	w.collectType(in.ExprType(w.info, e))
	switch n := e.(type) {
	case *ast.Unary:
		w.walkExpr(in, n.X)
	case *ast.Binary:
		w.walkExpr(in, n.L)
		w.walkExpr(in, n.R)
	case *ast.Call:
		w.walkCall(in, n)
	case *ast.Field:
		w.walkExpr(in, n.X)
	case *ast.TupleIndex:
		w.walkExpr(in, n.X)
	case *ast.Bracket:
		w.walkExpr(in, n.Base)
		for _, el := range n.Elems {
			w.walkExpr(in, el)
		}
	case *ast.ListLit:
		for _, el := range n.Elems {
			w.walkExpr(in, el)
		}
	case *ast.ListFill:
		w.walkExpr(in, n.Value)
		w.walkExpr(in, n.Count)
	case *ast.MatchExpr:
		w.walkExpr(in, n.Subject)
		for _, arm := range n.Arms {
			w.walkExpr(in, arm.Guard)
			w.walkExpr(in, arm.Body)
		}
	}
}

// walkCall resolves a call. A generic function call fixes the callee's type and
// value arguments from the concrete argument types (reusing sema's unify), enqueues
// the callee instance, and records the call's resolved mangled name in this
// instance's overlay. A non-generic call is left to the emitter's CallTarget
// fallback, and a construction ('T(...)') is not a function call.
func (w *worker) walkCall(in *Instance, n *ast.Call) {
	w.walkExpr(in, n.Callee)
	for _, a := range n.Args {
		w.walkExpr(in, a.Value)
	}
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return
	}
	sig, ok := w.info.Funcs[id.Name]
	if !ok || sig.Generic == nil {
		return
	}
	subT, subV := w.resolveArgs(in, sig, n)
	callee := w.enqueueFn(sig.Decl, subT, subV)
	in.Calls[n] = callee.Mangled
}

// resolveArgs fixes a generic callee's type and value arguments from a call's
// concrete argument types, pairing arguments positional-then-named against the
// callee's parameters and unifying each (DESIGN-1c §4.2). The argument types are
// read through the caller instance's overlay, so they are already concrete.
func (w *worker) resolveArgs(in *Instance, sig *sema.FuncSig, n *ast.Call) (map[string]types.Type, map[string]types.ConstVal) {
	exprs := make([]ast.Expr, len(sig.Params))
	idx := map[string]int{}
	for i, name := range sig.ParamNames {
		idx[name] = i
	}
	pos := 0
	for _, a := range n.Args {
		if a.Name != "" {
			if i, ok := idx[a.Name]; ok {
				exprs[i] = a.Value
			}
			continue
		}
		if pos < len(exprs) {
			exprs[pos] = a.Value
			pos++
		}
	}
	subT := map[string]types.Type{}
	subV := map[string]types.ConstVal{}
	for i := range sig.Params {
		if exprs[i] == nil {
			continue
		}
		sema.Unify(sig.Params[i], in.ExprType(w.info, exprs[i]), subT, subV)
	}
	return subT, subV
}

// --- type instantiation -------------------------------------------------------

// collectType registers every specialized nominal type reachable from t, recursing
// through composite and nominal type arguments. A user struct becomes a
// TypeInstance; an enum is recorded for name resolution but its specialized C is
// deferred to a later iteration. Registration is idempotent, keyed by the type's
// mangled fragment.
func (w *worker) collectType(t types.Type) {
	switch x := t.(type) {
	case *types.Struct:
		for _, a := range x.Args {
			w.collectType(a)
		}
		w.registerStruct(x)
	case *types.List:
		w.collectType(x.Elem)
	case *types.Set:
		w.collectType(x.Elem)
	case *types.Array:
		w.collectType(x.Elem)
	case *types.Map:
		w.collectType(x.Key)
		w.collectType(x.Val)
	case *types.Opt:
		w.collectType(x.Elem)
	}
}

// registerStruct records the specialized C struct for a use-site struct type. It
// marks the key before computing fields so a recursive type terminates, and appends
// the instance after its field types, so a struct is emitted after the types it
// depends on.
func (w *worker) registerStruct(x *types.Struct) {
	if x.Def == nil || x.Def.Struct == nil {
		return
	}
	key := typeFrag(x.Def, x.Args)
	if _, ok := w.prog.typeByKey[key]; ok {
		return
	}
	ti := &TypeInstance{Mangled: mangle(key), Def: x.Def, Args: x.Args}
	w.prog.typeByKey[key] = ti

	subT := map[string]types.Type{}
	for i, p := range x.Def.Params {
		if i < len(x.Args) {
			subT[p.Name] = x.Args[i]
		}
	}
	for _, f := range x.Def.Struct.Fields {
		ft := sema.Substitute(f.Type, subT, nil)
		w.collectType(ft)
		ti.Fields = append(ti.Fields, FieldInst{Name: f.Name, Type: ft})
	}
	w.prog.Types = append(w.prog.Types, ti)
}

// --- name mangling (FORK-B) ---------------------------------------------------

// mangle prefixes a fragment with the reserved 'zg_' so no emitted C name collides
// with a keyword or the runtime. A non-generic function keeps its historic
// 'zg_<name>' spelling, so the examples' C is unchanged.
func mangle(frag string) string { return "zg_" + frag }

// mangleFn is the structured, deterministic name of a function instance
// (DESIGN-1c §4.5): a non-generic function is 'zg_<fn>'; a generic instance appends
// one fragment per parameter in declaration order — '__<type>' for a type argument,
// '__n<value>' for a value argument — so distinct instantiations get distinct
// names.
func mangleFn(name string, env *sema.GenericEnv, subT map[string]types.Type, subV map[string]types.ConstVal) string {
	if env == nil {
		return mangle(name)
	}
	s := mangle(name)
	for _, pn := range env.Names {
		switch {
		case subV[pn].Known || subV[pn].Name != "":
			s += "__n" + valueFrag(subV[pn])
		case subT[pn] != nil:
			s += "__" + mangleType(subT[pn])
		}
	}
	return s
}

// mangleType encodes a concrete type as a C-identifier-safe fragment (DESIGN-1c
// §4.5): a primitive is one letter, a fixed-width numeric its spelling, a container
// its constructor and element fragments, and a nominal type its name with argument
// fragments.
func mangleType(t types.Type) string {
	switch x := t.(type) {
	case *types.Fixed:
		return x.String()
	case *types.List:
		return "list_" + mangleType(x.Elem)
	case *types.Set:
		return "set_" + mangleType(x.Elem)
	case *types.Map:
		return "map_" + mangleType(x.Key) + "_" + mangleType(x.Val)
	case *types.Array:
		return "arr_" + mangleType(x.Elem) + "_n" + valueFrag(x.N)
	case *types.Opt:
		return "opt_" + mangleType(x.Elem)
	case *types.Struct:
		return typeFrag(x.Def, x.Args)
	case *types.Enum:
		return typeFrag(x.Def, x.Args)
	}
	switch t.Kind() {
	case types.KInt:
		return "i"
	case types.KUint:
		return "u"
	case types.KFloat:
		return "f"
	case types.KBool:
		return "b"
	case types.KStr:
		return "s"
	case types.KRune:
		return "r"
	case types.KByte:
		return "y"
	case types.KNil:
		return "nil"
	}
	return "t"
}

// typeFrag is a nominal type's fragment: its name, then one '__<arg>' per type
// argument. A non-generic type is just its name, so a plain 'struct Foo' mangles to
// 'zg_Foo'.
func typeFrag(def *types.TypeDef, args []types.Type) string {
	s := def.Name
	for _, a := range args {
		s += "__" + mangleType(a)
	}
	return s
}

// valueFrag renders a value-generic argument as a fragment: a boolean by name, any
// other compile-time value by its integer form.
func valueFrag(v types.ConstVal) string {
	if v.Kind == types.KBool {
		if v.B {
			return "true"
		}
		return "false"
	}
	return strconv.FormatInt(v.I, 10)
}
