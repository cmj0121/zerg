package mono

import (
	"strconv"
	"strings"

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
			Info:        info,
			byMangled:   map[string]*Instance{},
			typeByKey:   map[string]*TypeInstance{},
			witByGlobal: map[string]*Witness{},
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
		Origin:      fn,
		Mangled:     mangled,
		ParamNames:  sig.ParamNames,
		Params:      make([]types.Type, len(sig.Params)),
		Ret:         sema.Substitute(sig.Ret, subT, subV),
		subT:        subT,
		subV:        subV,
		Calls:       map[*ast.Call]string{},
		DynSites:    map[*ast.Call]*DynSite{},
		MethodCalls: map[*ast.Call]*MethodDispatch{},
		OpCalls:     map[*ast.Binary]*MethodDispatch{},
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
	case *ast.DeferStmt:
		w.walkExpr(in, n.Call)
	case *ast.SpawnStmt:
		w.walkExpr(in, n.Call)
	case *ast.SelectStmt:
		for i := range n.Arms {
			arm := &n.Arms[i]
			w.walkExpr(in, arm.Chan)
			w.walkExpr(in, arm.Value)
			// A '{ … }' arm body's statements are walked directly (walkExpr does not
			// descend into a block); any other body is an expression.
			if blk, ok := arm.Body.(*ast.Block); ok {
				w.walkBlock(in, blk)
			} else {
				w.walkExpr(in, arm.Body)
			}
		}
	case *ast.WithStmt:
		w.walkExpr(in, n.Resource)
		w.walkBlock(in, n.Body)
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
		w.lowerCompare(in, n)
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
	if w.walkMethodCall(in, n) {
		return
	}
	if br, ok := n.Callee.(*ast.Bracket); ok {
		w.explicitCall(in, br, n)
		return
	}
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return
	}
	sig, ok := w.info.Funcs[id.Name]
	if !ok || sig.Generic == nil {
		return
	}
	if sig.Dyn {
		w.dynCall(in, sig, n)
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
	exprs := pairArgs(sig, n)
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

// pairArgs aligns a call's arguments to a callee's parameters, positional first
// then named, returning one expression per parameter (nil for an unsupplied one).
func pairArgs(sig *sema.FuncSig, n *ast.Call) []ast.Expr {
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
	return exprs
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
	case *types.Enum:
		for _, a := range x.Args {
			w.collectType(a)
		}
		w.registerEnum(x)
	case *types.List:
		w.collectType(x.Elem)
	case *types.Set:
		w.collectType(x.Elem)
	case *types.Array:
		w.collectType(x.Elem)
	case *types.Map:
		w.collectType(x.Key)
		w.collectType(x.Val)
	case *types.Ref:
		w.collectType(x.Elem)
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
	key := typeKey(x.Def, x.Args)
	if _, ok := w.prog.typeByKey[key]; ok {
		return
	}
	ti := &TypeInstance{Mangled: typeInstanceName(x.Def, x.Args), Def: x.Def, Args: x.Args}
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

// registerEnum records the specialized C tagged union for a use-site enum type,
// finishing the generic-enum specialization deferred from iteration 3 (DESIGN-1c
// §4/§7). Like registerStruct it marks the key before computing payloads so a
// recursive enum terminates, specializes each variant's payload to the instance's
// type arguments, and appends the instance after the types it depends on.
func (w *worker) registerEnum(x *types.Enum) {
	if x.Def == nil || x.Def.Enum == nil {
		return
	}
	key := typeKey(x.Def, x.Args)
	if _, ok := w.prog.typeByKey[key]; ok {
		return
	}
	ti := &TypeInstance{Mangled: typeInstanceName(x.Def, x.Args), Def: x.Def, Args: x.Args, IsEnum: true}
	w.prog.typeByKey[key] = ti

	subT := map[string]types.Type{}
	for i, p := range x.Def.Params {
		if i < len(x.Args) {
			subT[p.Name] = x.Args[i]
		}
	}
	for tag, v := range x.Def.Enum.Variants {
		vi := VariantInst{Name: v.Name, Tag: tag}
		for _, pt := range v.Payload {
			st := sema.Substitute(pt, subT, nil)
			w.collectType(st)
			vi.Payload = append(vi.Payload, st)
		}
		ti.Variants = append(ti.Variants, vi)
	}
	w.prog.Types = append(w.prog.Types, ti)
}

// --- name mangling (FORK-B) ---------------------------------------------------

// Reserved name prefixes (DESIGN-1c §4.5, B3/B4). A non-generic function or type
// keeps its historic 'zg_<name>' spelling (so the examples are byte-identical),
// while every compiler-synthesized name takes a prefix whose third character is a
// letter, not '_' — a shape 'zg_<userident>' can never produce, so a user
// identifier (even one containing '__') can never collide with a synthesized name:
//
//	zgg_  generic function instance    zgm_  impl-method instance
//	zgt_  specialized nominal type      zgw_  concrete witness table
//	zgs_  witness struct type
//
// Within each namespace the type/value fragment encoding (typeCode) is
// self-delimiting, hence injective, so two distinct instantiations never collide.

// mangle prefixes a fragment with the reserved 'zg_' so no emitted C name collides
// with a keyword or the runtime. Used for non-generic functions and types, whose
// spelling is unchanged from the pre-generics backend.
func mangle(frag string) string { return "zg_" + frag }

// mangleFn is the name of a function instance: a non-generic function keeps its
// historic 'zg_<fn>' spelling; a generic instance takes the 'zgg_' prefix, a
// length-delimited function name, and one self-delimiting code per parameter (a
// type code for a type argument, a value code for a value argument), so distinct
// instantiations — and distinct functions — get distinct, collision-free names.
func mangleFn(name string, env *sema.GenericEnv, subT map[string]types.Type, subV map[string]types.ConstVal) string {
	if env == nil {
		return mangle(name)
	}
	var b strings.Builder
	b.WriteString("zgg_")
	b.WriteString(lenTag(name))
	for _, pn := range env.Names {
		switch {
		case subV[pn].Known || subV[pn].Name != "":
			b.WriteString(valCode(subV[pn]))
		case subT[pn] != nil:
			b.WriteString(typeCode(subT[pn]))
		default:
			b.WriteString("z")
		}
	}
	return b.String()
}

// typeInstanceName is the C name of a specialized nominal type: a non-generic type
// keeps 'zg_<name>' (byte-identical), a generic instance takes 'zgt_' and the
// injective type code, so 'Box[int]' can never alias a user 'struct Box__i'.
func typeInstanceName(def *types.TypeDef, args []types.Type) string {
	if len(args) == 0 {
		return mangle(def.Name)
	}
	return "zgt_" + nominalCode(def, args)
}

// methodMangle is the C name of an impl-method instance: 'zgm_' followed by the
// self-delimiting code of the receiver type and the method name.
func methodMangle(recv types.Type, name string) string {
	return "zgm_" + typeCode(recv) + "_" + name
}

// witnessStructName is the shared witness struct type of a spec ('zgs_<Spec>').
func witnessStructName(spec string) string { return "zgs_" + spec }

// witnessGlobalName is the concrete witness table of a (spec, type) pair.
func witnessGlobalName(spec string, recv types.Type) string {
	return "zgw_" + lenTag(spec) + typeCode(recv)
}

// lenTag length-delimits an identifier as '<len>_<ident>', so a name boundary is
// unambiguous even when the identifier or what follows contains '_'.
func lenTag(s string) string { return strconv.Itoa(len(s)) + "_" + s }

// typeKey is the injective map key for a specialized nominal type — the same code
// its instance name is built from, so two distinct types never share a key.
func typeKey(def *types.TypeDef, args []types.Type) string { return nominalCode(def, args) }

// typeCode encodes a type as a self-delimiting, C-identifier-safe code (B3/B4): a
// primitive is one letter, a fixed-width numeric 'W<bits><kind>', a container a tag
// letter and its element codes, a nominal type a length-delimited name and its
// argument codes. Concatenating codes is unambiguous, so the encoding is injective.
func typeCode(t types.Type) string {
	switch x := t.(type) {
	case *types.Fixed:
		return "W" + strconv.Itoa(x.Bits) + fixedKind(x)
	case *types.List:
		return "L" + typeCode(x.Elem)
	case *types.Set:
		return "X" + typeCode(x.Elem)
	case *types.Map:
		return "M" + typeCode(x.Key) + typeCode(x.Val)
	case *types.Array:
		return "Y" + typeCode(x.Elem) + valCode(x.N)
	case *types.Ref:
		return "R" + typeCode(x.Elem)
	case *types.Opt:
		return "O" + typeCode(x.Elem)
	case *types.Struct:
		return nominalCode(x.Def, x.Args)
	case *types.Enum:
		return nominalCode(x.Def, x.Args)
	case *types.Param:
		return "P" + lenTag(x.Name)
	case *types.ValParam:
		return "Q" + lenTag(x.Name)
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
		return "c"
	case types.KByte:
		return "y"
	case types.KNil:
		return "z"
	}
	return "e"
}

// nominalCode encodes a nominal type: 'N', a length-delimited name, a
// length-delimited argument count, then each argument's code — self-delimiting, so
// a bare 'struct Box__i' and 'Box[int]' encode differently.
func nominalCode(def *types.TypeDef, args []types.Type) string {
	var b strings.Builder
	b.WriteString("N")
	b.WriteString(lenTag(def.Name))
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteByte('_')
	for _, a := range args {
		b.WriteString(typeCode(a))
	}
	return b.String()
}

// fixedKind is the trailing letter of a fixed-width numeric code: 'g' float, 's'
// signed int, 'u' unsigned int.
func fixedKind(x *types.Fixed) string {
	switch {
	case x.Float:
		return "g"
	case x.Signed:
		return "s"
	default:
		return "u"
	}
}

// valCode encodes a value-generic argument as a self-delimiting code: 'Vt'/'Vf' for
// a boolean, 'Vi<digits>_' for an integer.
func valCode(v types.ConstVal) string {
	if v.Kind == types.KBool {
		if v.B {
			return "Vt"
		}
		return "Vf"
	}
	return "Vi" + strconv.FormatInt(v.I, 10) + "_"
}
