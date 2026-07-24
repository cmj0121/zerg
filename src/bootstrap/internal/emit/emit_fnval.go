package emit

import (
	"fmt"
	"sort"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// First-class function values (docs/functions.md). A function value carries both code
// and, for a closure, its captured environment — "a closure is a scope-owned struct
// whose fields are its captures." So a `fn(P...) -> R` value lowers to a UNIFORM fat
// pair:
//
//	typedef struct { R (*code)(void *env, P...); void *env; } zg_fn_<n>;
//
// The code pointer always takes a leading `void *env`, so every function value is
// called the same way — `f.code(f.env, args)` — whether or not it captures:
//
//   - a NAMED function used as a value gets a generated env-ignoring thunk and env=NULL;
//   - a NON-CAPTURING closure is a lifted function that takes (and ignores) env, env=NULL;
//   - a CAPTURING closure is a lifted function that reads env, and env is a refcounted
//     box of its captures (a later iteration).
//
// env=NULL is the whole story for this iteration: no closure captures yet, so a
// function value is plain data and copies/drops trivially.

// fnCarrier is one generated function-value struct typedef: its C name and the function
// type it spells.
type fnCarrier struct {
	name string
	fn   *types.Fn
}

// fnThunk is one generated env-ignoring adapter for a named function used as a value.
type fnThunk struct {
	name    string    // the thunk's C name
	target  string    // the named function's mangled C name
	fn      *types.Fn // the function's value type (for the signature)
	carrier string    // the fn-value struct typedef name
}

// prepareFnTypes numbers every distinct function TYPE the program uses as a value, so
// each gets a stable `zg_fn_<n>` typedef. It scans the same recorded type positions as
// prepareTuples. A program that names no function value registers nothing and stays
// byte-identical.
func (e *emitter) prepareFnTypes() {
	e.fntypes = map[string]*fnCarrier{}
	seen := map[string]*types.Fn{}
	var consider func(t sema.Type)
	consider = func(t sema.Type) {
		fn, ok := t.(*types.Fn)
		if !ok || fn == nil {
			return
		}
		if mentionsParam(fn) {
			return
		}
		if _, dup := seen[fn.String()]; dup {
			return
		}
		seen[fn.String()] = fn
		for _, p := range fn.Params {
			consider(p.Type)
		}
		consider(fn.Ret)
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

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	reserved := e.reservedTopLevel()
	i := 0
	for _, k := range keys {
		name := e.freshCarrierName("zg_fn_%d", &i, reserved)
		e.fntypes[k] = &fnCarrier{name: name, fn: seen[k]}
	}

	e.prepareLambdaDecls()
	e.prepareFnThunks(reserved, &i)
}

// prepareLambdaDecls records the synthesized FuncDecl of each lifted closure, so the
// backend can give a lambda instance the leading `void *env` parameter of the uniform
// closure calling convention, and assigns each CAPTURING closure an environment struct
// name.
func (e *emitter) prepareLambdaDecls() {
	e.lambdaDecls = map[*ast.FuncDecl]bool{}
	e.lambdaByDecl = map[*ast.FuncDecl]*sema.Lambda{}
	e.envTypes = map[string]string{}
	for _, lam := range e.info.Lambdas {
		e.lambdaDecls[lam.Decl] = true
		e.lambdaByDecl[lam.Decl] = lam
		if len(lam.Captures) > 0 {
			e.envTypes[lam.Name] = "zg_env_" + lam.Name
		}
	}
}

// lambdaOf returns the lifted closure an instance came from, if any.
func (e *emitter) lambdaOf(inst *mono.Instance) (*sema.Lambda, bool) {
	if inst == nil {
		return nil, false
	}
	lam, ok := e.lambdaByDecl[inst.Origin]
	return lam, ok
}

// emitEnvTypedefs writes the environment struct of each capturing closure — one field
// per capture — after the nominal types (a capture may be a user struct) and before the
// functions that read it. Emits nothing when no closure captures.
func (e *emitter) emitEnvTypedefs() {
	for _, lam := range e.orderedCaptureLambdas() {
		var b string
		for _, cap := range lam.Captures {
			b += fmt.Sprintf("%s zg_%s; ", e.ctype(cap.Type), cap.Name)
		}
		e.line(fmt.Sprintf("typedef struct { %s} %s;", b, e.envTypes[lam.Name]))
	}
}

// orderedCaptureLambdas returns the capturing closures in a deterministic order.
func (e *emitter) orderedCaptureLambdas() []*sema.Lambda {
	out := make([]*sema.Lambda, 0, len(e.envTypes))
	for _, lam := range e.info.Lambdas {
		if len(lam.Captures) > 0 {
			out = append(out, lam)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// prepareFnThunks assigns a thunk name to each named function used as a value, keyed by
// the function name (one thunk per function, regardless of how many use sites).
func (e *emitter) prepareFnThunks(reserved map[string]bool, i *int) {
	e.fnthunks = map[string]*fnThunk{}
	for _, name := range e.info.FuncValues {
		if _, done := e.fnthunks[name]; done {
			continue
		}
		sig, ok := e.info.Funcs[name]
		if !ok {
			continue
		}
		fn := fnTypeOfSig(sig)
		carrier, ok := e.fnTypeFor(fn)
		if !ok {
			continue
		}
		thunk := e.freshCarrierName("zg_fnthunk_%d", i, reserved)
		e.fnthunks[name] = &fnThunk{name: thunk, target: e.prog.CallTarget(name), fn: fn, carrier: carrier.name}
	}
}

// fnTypeOfSig builds the function-value type of a signature (its parameters, each with
// its `mut &` marker, and its result) — the same shape sema.funcValueType produces.
func fnTypeOfSig(sig *sema.FuncSig) *types.Fn {
	fn := &types.Fn{Ret: sig.Ret}
	for i, pt := range sig.Params {
		fn.Params = append(fn.Params, types.Param0{Type: pt, ByRef: sigParamByRef(sig, i)})
	}
	return fn
}

// sigParamByRef reports whether a signature's i-th parameter is a `mut &` reference.
func sigParamByRef(sig *sema.FuncSig, i int) bool {
	if sig.Decl == nil || i >= len(sig.Decl.Params) {
		return false
	}
	return sig.Decl.Params[i].Ref
}

// emitFnTypedefs writes each function-value struct typedef, before the prototypes that
// may name one. An inner function-typed parameter/result is emitted first, so C sees the
// complete type. Emits nothing when the program registered none.
func (e *emitter) emitFnTypedefs() {
	done := map[string]bool{}
	var emit func(c *fnCarrier)
	emit = func(c *fnCarrier) {
		if done[c.name] {
			return
		}
		done[c.name] = true
		for _, p := range c.fn.Params {
			if inner, ok := e.fnTypeFor(p.Type); ok {
				emit(inner)
			}
		}
		if inner, ok := e.fnTypeFor(c.fn.Ret); ok {
			emit(inner)
		}
		e.line(fmt.Sprintf("typedef struct { %s (*code)(%s); void *env; } %s;",
			e.fnRetType(c.fn.Ret), e.fnCodeParamList(c.fn), c.name))
	}
	for _, c := range e.orderedFnTypes() {
		emit(c)
	}
}

// emitFnThunks writes each named-function value thunk: an env-ignoring adapter that
// forwards to the function, giving it the uniform closure calling convention. Emitted
// after the function prototypes it forwards to.
func (e *emitter) emitFnThunks() {
	for _, t := range e.orderedFnThunks() {
		names := make([]string, len(t.fn.Params))
		params := make([]string, len(t.fn.Params))
		for i, p := range t.fn.Params {
			names[i] = "a" + fmt.Sprint(i)
			ct := e.paramType(p.Type)
			if p.ByRef {
				ct += "*"
			}
			params[i] = ct + " " + names[i]
		}
		sig := "void *env"
		for _, p := range params {
			sig += ", " + p
		}
		call := fmt.Sprintf("%s(%s)", t.target, joinArgs(names))
		e.line(fmt.Sprintf("static %s %s(%s) {", e.fnRetType(t.fn.Ret), t.name, sig))
		e.indent++
		e.line("(void)env;")
		if t.fn.Ret == nil || t.fn.Ret == sema.Nil {
			e.line(call + ";")
		} else {
			e.line("return " + call + ";")
		}
		e.indent--
		e.line("}")
	}
}

// fnCodeParamList renders the parameter list of a function value's code pointer: the
// leading `void *env`, then each declared parameter (a `mut &` one as a pointer).
func (e *emitter) fnCodeParamList(fn *types.Fn) string {
	out := "void *"
	for _, p := range fn.Params {
		ct := e.paramType(p.Type)
		if p.ByRef {
			ct += "*"
		}
		out += ", " + ct
	}
	return out
}

// fnRetType renders a function type's C return type; a nil (no `-> type`) result is
// `void`.
func (e *emitter) fnRetType(ret sema.Type) string {
	if ret == nil || ret == sema.Nil {
		return "void"
	}
	return e.ctype(ret)
}

// namedFnValue lowers a named top-level function used as a value to a closure literal:
// its env-ignoring thunk as code, and a NULL environment.
func (e *emitter) namedFnValue(name string) (string, bool) {
	t, ok := e.fnthunks[name]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("((%s){ %s, (void *)0 })", t.carrier, t.name), true
}

// lambdaValue lowers a lifted closure used as a value to a closure literal. A
// non-capturing closure has a NULL environment; a capturing one allocates an
// environment box, copies each capture into it, and carries it as the environment.
func (e *emitter) lambdaValue(lam *sema.Lambda) string {
	carrier := "zg_fn_0"
	if c, ok := e.fnTypeFor(fnTypeOfSig(e.info.Funcs[lam.Name])); ok {
		carrier = c.name
	}
	code := e.prog.CallTarget(lam.Name)
	if len(lam.Captures) == 0 {
		return fmt.Sprintf("((%s){ %s, (void *)0 })", carrier, code)
	}
	// Capture by copy into a fresh environment box. The box is plain (leaked) heap, like
	// a string's, since captures are POD; the closure-environment ownership story frees
	// it later. Each capture is read from the enclosing scope by its source name.
	env := e.envTypes[lam.Name]
	var b string
	b += fmt.Sprintf("%s *_e = (%s *)zrt_alloc(sizeof(%s)); ", env, env, env)
	for _, cap := range lam.Captures {
		b += fmt.Sprintf("_e->zg_%s = %s; ", cap.Name, e.resolve(cap.Name))
	}
	return fmt.Sprintf("({ %s((%s){ %s, (void *)_e }); })", b, carrier, code)
}

// isLambdaInst reports whether an instance is a lifted closure, which the uniform
// calling convention gives a leading `void *env` parameter.
func (e *emitter) isLambdaInst(inst *mono.Instance) bool {
	return inst != nil && e.lambdaDecls[inst.Origin]
}

// indirectCallEmit lowers a call through a function VALUE to a call through its code
// pointer, passing the environment first. The callee is bound to a temporary so a
// callee with side effects (a call that returns a closure) is evaluated once. It reports
// false for a direct named call and for a non-function callee.
func (e *emitter) indirectCallEmit(n *ast.Call) (string, bool) {
	if id, ok := n.Callee.(*ast.Ident); ok {
		if _, isFn := e.info.Funcs[id.Name]; isFn {
			return "", false
		}
	}
	fn, ok := e.cur.ExprType(e.info, n.Callee).(*types.Fn)
	if !ok {
		return "", false
	}
	carrier := e.ctype(fn)
	args := []string{"_cl.env"}
	for i, a := range n.Args {
		if i < len(fn.Params) && fn.Params[i].ByRef {
			args = append(args, e.addressOf(a.Value))
			continue
		}
		args = append(args, e.copyValue(e.cur.ExprType(e.info, a.Value), a.Value))
	}
	return fmt.Sprintf("({ %s _cl = %s; _cl.code(%s); })", carrier, e.expr(n.Callee), joinArgs(args)), true
}

// joinArgs joins a comma-separated argument list.
func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += ", "
		}
		out += a
	}
	return out
}

// fnTypeFor returns the typedef registered for a function type, if any.
func (e *emitter) fnTypeFor(t sema.Type) (*fnCarrier, bool) {
	if t == nil || len(e.fntypes) == 0 {
		return nil, false
	}
	c, ok := e.fntypes[t.String()]
	return c, ok
}

// orderedFnTypes returns the function-type carriers in a deterministic order.
func (e *emitter) orderedFnTypes() []*fnCarrier {
	out := make([]*fnCarrier, 0, len(e.fntypes))
	for _, c := range e.fntypes {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// orderedFnThunks returns the value thunks in a deterministic order.
func (e *emitter) orderedFnThunks() []*fnThunk {
	out := make([]*fnThunk, 0, len(e.fnthunks))
	for _, t := range e.fnthunks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
