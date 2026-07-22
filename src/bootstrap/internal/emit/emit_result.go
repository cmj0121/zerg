package emit

// This file carries the Phase 1f U0 additions to the C backend: making the
// general Result/Either/optional VALUE real end to end. It generalizes the channel
// recv carrier (`zg_recv_<n>`, emit_chan.go) into per-type carriers for every
// Either/Result/optional a program actually uses, lowers construction (context-
// typed Ok/Left, and the guard/raise Right(err)), and lowers the group-8 operators
// `?` / `??` / `!` / `?.` / `guard`. `raise e (from c)` carries a runtime Err value
// (Decision D) so a surrounding `guard`/`?` recovers the actual error.
//
// The whole surface is additive and gated on carrier use: a program that never
// names an Either/optional and never uses one of these operators registers no
// carrier, so nothing here fires and its emitted C is byte-identical to Phase 0.
//
// Decision A: the carrier is an INTERNAL monomorphized struct, not an FFI-frozen
// layout (a Result is never FFI-safe), so a later phase may retune it freely.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// carrierKind tags the three carrier shapes: a Result[T] (Right is the built-in
// Err), a general Either[L, R] (an explicit non-Err Right), or an optional T?
// (whose Right is `nil`, carried as the tag alone).
type carrierKind int

const (
	carrierResult carrierKind = iota // { int32_t tag; <Lc> ok; zrt_err err; }
	carrierEither                    // { int32_t tag; <Lc> left; <Rc> right; }
	carrierOpt                       // { int32_t tag; <Lc> ok; }   (tag 1 = nil)
)

// carrier is one monomorphized Either/Result/optional C carrier: its generated C
// type name, its shape, and the concrete Left (and, for a general Either, Right)
// element type it wraps. tag 0 = Left/Ok, tag 1 = Right/Err/nil.
type carrier struct {
	name  string
	kind  carrierKind
	left  sema.Type
	right sema.Type // set only for carrierEither
}

// okField is the carrier's Left/value member name ("ok" for Result/optional,
// "left" for a general Either).
func (c *carrier) okField() string {
	if c.kind == carrierEither {
		return "left"
	}
	return "ok"
}

// --- pre-pass ----------------------------------------------------------------

// prepareResults numbers every distinct Either/Result/optional carrier the program
// uses, so each gets a stable typedef and force helper. It scans every recorded
// type position (function signatures, bindings, expression types), skipping the
// two shapes already lowered elsewhere — `Result[nil]` (the tag-only
// zrt_result_nil) and a channel recv's Result[T] (`zg_recv_<n>`). It sets
// needsResult (and hence needsRuntime) only when at least one carrier is found, so
// a program with none stays byte-identical.
func (e *emitter) prepareResults() {
	e.carriers = map[string]*carrier{}
	seen := map[string]sema.Type{}
	consider := func(t sema.Type) {
		if t == nil || !e.isCarrierType(t) {
			return
		}
		seen[t.String()] = t
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
	var nres, neither, nopt int
	for _, k := range keys {
		t := seen[k]
		c := &carrier{left: leftOf(t)}
		switch x := t.(type) {
		case *types.Opt:
			c.kind, c.name = carrierOpt, fmt.Sprintf("zg_opt_%d", nopt)
			nopt++
		case *types.Either:
			if isErrType(x.Right) {
				c.kind, c.name = carrierResult, fmt.Sprintf("zg_result_%d", nres)
				nres++
			} else {
				c.kind, c.right, c.name = carrierEither, x.Right, fmt.Sprintf("zg_either_%d", neither)
				neither++
			}
		}
		e.carriers[k] = c
	}
	if len(e.carriers) > 0 {
		e.needsResult = true
		e.needsRuntime = true
	}
}

// isCarrierType reports whether a type needs a generated carrier: an optional, or an
// Either that is neither the tag-only `Result[nil]` nor a channel recv's Result[T]
// (kept on their existing paths), and whose Left has a real C type.
func (e *emitter) isCarrierType(t sema.Type) bool {
	switch x := t.(type) {
	case *types.Opt:
		return e.ctype(x.Elem) != "void"
	case *types.Either:
		if isResultNil(t) {
			return false
		}
		if _, ok := e.recvIdx[x.Left.String()]; ok {
			return false // a channel recv carrier already covers this Left type
		}
		return e.ctype(x.Left) != "void"
	}
	return false
}

// carrierFor returns the carrier registered for a type, if any.
func (e *emitter) carrierFor(t sema.Type) (*carrier, bool) {
	if t == nil || e.carriers == nil {
		return nil, false
	}
	c, ok := e.carriers[t.String()]
	return c, ok
}

// isErrType reports whether a type is the built-in `Err` (the Right of a Result).
func isErrType(t sema.Type) bool {
	s, ok := t.(*types.Struct)
	return ok && s.Def != nil && s.Def.Name == "Err"
}

// leftOf returns the Left/Ok/element type of an Either or optional (nil otherwise).
func leftOf(t sema.Type) sema.Type {
	switch x := t.(type) {
	case *types.Opt:
		return x.Elem
	case *types.Either:
		return x.Left
	}
	return nil
}

// isBadType reports whether a type is the Unknown/Invalid placeholder, which never
// takes part in carrier construction.
func isBadType(t sema.Type) bool {
	if t == nil {
		return true
	}
	k := t.Kind()
	return k == types.KInvalid || k == types.KUnknown
}

// --- generated typedefs / helpers --------------------------------------------

// emitResultTypedefs writes every carrier's struct typedef, before the function
// prototypes that name one as a return/parameter type. Emits nothing when the
// program registered no carrier.
func (e *emitter) emitResultTypedefs() {
	for _, c := range e.orderedCarriers() {
		lc := e.ctype(c.left)
		switch c.kind {
		case carrierResult:
			e.line(fmt.Sprintf("typedef struct { int32_t tag; %s ok; zrt_err err; } %s;", lc, c.name))
		case carrierOpt:
			e.line(fmt.Sprintf("typedef struct { int32_t tag; %s ok; } %s;", lc, c.name))
		case carrierEither:
			e.line(fmt.Sprintf("typedef struct { int32_t tag; %s left; %s right; } %s;", lc, e.ctype(c.right), c.name))
		}
	}
	if len(e.carriers) > 0 {
		e.blank()
	}
}

// emitResultHelpers writes each carrier's force-unwrap helper `zg_force_<name>`,
// which yields the Left value or, on a Right/nil, raises an Err (aborts). Emitted
// among the other generated helpers, before any function body.
func (e *emitter) emitResultHelpers() {
	for _, c := range e.orderedCarriers() {
		lc := e.ctype(c.left)
		e.line(fmt.Sprintf("static %s zg_force_%s(%s r) {", lc, c.name, c.name))
		e.indent++
		switch c.kind {
		case carrierResult:
			e.line("if (r.tag != 0) { zrt_raise_err(zrt_err_with_cause(\"force-unwrap of an Err value\", r.err)); }")
		case carrierOpt:
			e.line("if (r.tag != 0) { zrt_raise_err(zrt_err_new(\"force-unwrap of nil\")); }")
		case carrierEither:
			e.line("if (r.tag != 0) { zrt_raise_err(zrt_err_new(\"force-unwrap of a Right value\")); }")
		}
		e.line("return r." + c.okField() + ";")
		e.indent--
		e.line("}")
		e.blank()
	}
}

// orderedCarriers returns the carriers sorted by generated name, so the emitted
// typedefs and helpers are deterministic run to run.
func (e *emitter) orderedCarriers() []*carrier {
	out := make([]*carrier, 0, len(e.carriers))
	for _, c := range e.carriers {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// --- construction (context-typed Ok/Left) ------------------------------------

// zeroValueC renders the zero value of a return type for a synthesized fall-through
// return. A carrier type needs a zeroed struct literal — `(zg_result_<n>){0}` — not
// the scalar `0`, whose int type would not match the struct return; every other type
// keeps the plain zeroValue, so a value-only function stays byte-identical. The
// synthesized return sits on an unreachable path (the body diverged or a checked
// program always returns), so its tag-0 payload is never observed.
func (e *emitter) zeroValueC(t sema.Type) string {
	if c, ok := e.carrierFor(t); ok {
		return fmt.Sprintf("(%s){0}", c.name)
	}
	if c, ok := e.tupleFor(t); ok {
		return fmt.Sprintf("(%s){0}", c.name)
	}
	return zeroValue(t)
}

// wrapValue coerces a value of type vt into a target carrier type: a T value builds
// the Ok/Left case, and `nil` builds the empty optional or the Result[nil] Ok. When
// the value already has the carrier type (or the target is not a carrier) it is
// returned unchanged, so every existing bind/return stays byte-identical.
func (e *emitter) wrapValue(target, vt sema.Type, code string) string {
	if target == nil {
		return code
	}
	// A `Result[nil]` target uses the tag-only zrt_result_nil representation, so it is
	// reconciled here before the structural short-circuit below — `Result[nil]`'s Left
	// is `<unknown>`, which Identical treats as compatible with any Left, so a concrete
	// `Result[T]` value (e.g. a `guard { … }` whose block yields a value) would
	// otherwise slip through unwrapped and be returned with a mismatched carrier type.
	// A value already tag-only passes through; a concrete Result/optional/Either carrier
	// projects its ok/err tag (dropping the payload the nil result does not carry); a
	// bare value or `nil` is Ok.
	if isResultNil(target) {
		if isResultNil(vt) {
			return code
		}
		if c, ok := e.carrierFor(vt); ok {
			tmp := e.freshName("rn")
			return fmt.Sprintf("({ %s %s = %s; (zrt_result_nil){ .tag = %s.tag }; })", c.name, tmp, code, tmp)
		}
		return "zrt_result_ok()"
	}
	if isBadType(vt) || types.Identical(target, vt) {
		return code
	}
	c, ok := e.carrierFor(target)
	if !ok {
		return code
	}
	if c.kind == carrierOpt && vt == sema.Nil {
		return fmt.Sprintf("(%s){ .tag = 1 }", c.name)
	}
	return fmt.Sprintf("(%s){ .tag = 0, .%s = %s }", c.name, c.okField(), code)
}

// --- operators ---------------------------------------------------------------

// tryExpr lowers `x?` (Try): it unwraps the Left value, or early-returns the Right
// re-wrapped into the enclosing function's own carrier. The early return first
// unwinds the function's cleanup stack (running its defers/drops), exactly as a
// plain `return` does. Lowered as a GNU statement-expression so the operator is a
// value even though it carries control flow.
func (e *emitter) tryExpr(n *ast.Try) string {
	opT := e.cur.ExprType(e.info, n.X)
	c, ok := e.carrierFor(opT)
	if !ok {
		return e.expr(n.X) // an un-modelled operand: leave it rather than emit broken C
	}
	tmp := e.freshName("try")
	unwind := ""
	if e.fnMark != "" {
		unwind = fmt.Sprintf("zrt_unwind_to(%s); ", e.fnMark)
	}
	right := e.rightLiteral(e.cur.Ret, tmp+".err", c)
	return fmt.Sprintf("({ %s %s = %s; if (%s.tag != 0) { %sreturn %s; } %s.%s; })",
		c.name, tmp, e.expr(n.X), tmp, unwind, right, tmp, c.okField())
}

// rightLiteral builds the Right/Err value of the enclosing return carrier for a `?`
// propagation: it re-wraps the operand's error (errExpr) into ret's own shape. A
// Result[nil] or optional return carries only the tag; a general Either propagation
// is outside iteration 1 (it carries a default-tagged Right).
func (e *emitter) rightLiteral(ret sema.Type, errExpr string, op *carrier) string {
	errVal := "zrt_err_new(0)"
	if op.kind == carrierResult {
		errVal = errExpr
	}
	if isResultNil(ret) {
		return "(zrt_result_nil){ .tag = 1 }"
	}
	c, ok := e.carrierFor(ret)
	if !ok {
		return "0"
	}
	if c.kind == carrierResult {
		return fmt.Sprintf("(%s){ .tag = 1, .err = %s }", c.name, errVal)
	}
	return fmt.Sprintf("(%s){ .tag = 1 }", c.name)
}

// coalesceExpr lowers `a ?? b` (Coalesce): the Left value of a, or b when a is
// Right/nil. b may DIVERGE (break/continue/return/raise), which produces no value,
// so that form is a statement-expression running the escape; a plain default is a
// short-circuiting ternary that evaluates a once.
func (e *emitter) coalesceExpr(n *ast.Coalesce) string {
	opT := e.cur.ExprType(e.info, n.X)
	acc, cname := e.carrierAccess(opT)
	tmp := e.freshName("co")
	if div, ok := n.Y.(*ast.Diverge); ok {
		leftC := e.ctype(leftOf(opT))
		val := e.freshName("cov")
		body := e.capture(func() { e.divergeStmt(div) })
		return fmt.Sprintf("({ %s %s = %s; %s %s; if (%s.tag == 0) { %s = %s.%s; } else { %s } %s; })",
			cname, tmp, e.expr(n.X), leftC, val, tmp, val, tmp, acc, body, val)
	}
	return fmt.Sprintf("({ %s %s = %s; %s.tag == 0 ? %s.%s : (%s); })",
		cname, tmp, e.expr(n.X), tmp, tmp, acc, e.expr(n.Y))
}

// optChainExpr lowers `x?.f` (OptChain): when the optional x is present, read field
// f and re-wrap it as an optional; otherwise the result is the empty optional.
func (e *emitter) optChainExpr(n *ast.OptChain) string {
	opT := e.cur.ExprType(e.info, n.X)
	resT := e.cur.ExprType(e.info, n)
	oc, ok := e.carrierFor(opT)
	rc, rok := e.carrierFor(resT)
	if !ok || !rok {
		return e.expr(n.X)
	}
	tmp := e.freshName("oc")
	return fmt.Sprintf("({ %s %s = %s; %s.tag == 0 ? (%s){ .tag = 0, .ok = %s.ok.zg_%s } : (%s){ .tag = 1 }; })",
		oc.name, tmp, e.expr(n.X), tmp, rc.name, tmp, n.Name, rc.name)
}

// forceExpr lowers `x!` (Force). A channel recv's carrier keeps its existing helper;
// a general Result/Either/optional carrier uses its generated `zg_force_<name>`,
// which raises an Err on a Right/nil. Any other operand is left unchanged.
func (e *emitter) forceExpr(n *ast.Force) string {
	opT := e.cur.ExprType(e.info, n.X)
	if ei, ok := opT.(*types.Either); ok {
		if idx, ok := e.recvIdx[ei.Left.String()]; ok {
			return fmt.Sprintf("zg_force_%d(%s)", idx, e.expr(n.X))
		}
	}
	if c, ok := e.carrierFor(opT); ok {
		return fmt.Sprintf("zg_force_%s(%s)", c.name, e.expr(n.X))
	}
	return e.expr(n.X)
}

// guardExpr lowers `guard { block }`: it runs the block under a fresh abort handler
// and demotes an abort inside it to the Right(err) of a Result[T] (Decision D's
// carried Err), while a normal completion becomes Left(block value). Modelled on
// zrt_run's setjmp handler, inline as a statement-expression so its setjmp lands in
// the enclosing function's own activation.
func (e *emitter) guardExpr(n *ast.GuardExpr) string {
	gt := e.cur.ExprType(e.info, n)
	frame := e.freshName("gf")
	res := e.freshName("gr")
	stmts := n.Body.Stmts
	var last ast.Expr
	if k := len(stmts); k > 0 {
		if es, ok := stmts[k-1].(*ast.ExprStmt); ok {
			last, stmts = es.X, stmts[:k-1]
		}
	}
	okBody := e.capture(func() {
		e.pushScope()
		for _, s := range stmts {
			e.stmt(s)
		}
		e.emitGuardOk(gt, res, last)
		e.popScope()
	})
	errBody := e.guardErr(gt, res)
	return fmt.Sprintf("({ zrt_frame %s; %s %s; zrt_handler_push_catch(&%s); "+
		"if (setjmp(%s.buf) == 0) { %s zrt_unwind_to(%s.mark); zrt_handler_pop(&%s); } "+
		"else { zrt_handler_pop(&%s); %s } %s; })",
		frame, e.ctype(gt), res, frame, frame, okBody, frame, frame, frame, errBody, res)
}

// emitGuardOk writes the guard's Ok assignment: the Left(block value) case. A
// Result[nil] guard evaluates the block for effect and stores the tag alone.
func (e *emitter) emitGuardOk(gt sema.Type, res string, last ast.Expr) {
	if isResultNil(gt) {
		if last != nil {
			e.line(e.expr(last) + ";")
		}
		e.line(fmt.Sprintf("%s = (zrt_result_nil){ .tag = 0 };", res))
		return
	}
	c, ok := e.carrierFor(gt)
	if !ok {
		return
	}
	val := "0"
	if last != nil {
		val = e.wrapValue(c.left, e.cur.ExprType(e.info, last), e.expr(last))
	}
	e.line(fmt.Sprintf("%s = (%s){ .tag = 0, .%s = %s };", res, c.name, c.okField(), val))
}

// guardErr renders the guard's abort-landing assignment: the Right(taken Err) case,
// demoting the in-flight abort to a value.
func (e *emitter) guardErr(gt sema.Type, res string) string {
	if isResultNil(gt) {
		return fmt.Sprintf("%s = (zrt_result_nil){ .tag = 1 };", res)
	}
	if c, ok := e.carrierFor(gt); ok {
		return fmt.Sprintf("%s = (%s){ .tag = 1, .err = zrt_taken_err() };", res, c.name)
	}
	return ""
}

// carrierAccess returns a carrier operand's Left member accessor and its C type
// name, covering both a general carrier and a channel recv carrier (`.val`).
func (e *emitter) carrierAccess(t sema.Type) (accessor, cname string) {
	if c, ok := e.carrierFor(t); ok {
		return c.okField(), c.name
	}
	if ei, ok := t.(*types.Either); ok {
		if idx, ok := e.recvIdx[ei.Left.String()]; ok {
			return "val", fmt.Sprintf("zg_recv_%d", idx)
		}
	}
	return "ok", e.ctype(t)
}

// divergeStmt lowers a `??`-right-side control-flow escape (break/continue/return/
// raise) to its statement form, reusing the existing statement lowerings so the
// escape's teardown/abort behaviour matches a stand-alone one.
func (e *emitter) divergeStmt(n *ast.Diverge) {
	switch n.Kw {
	case token.Break:
		if lm := e.loopMark(); lm != "" {
			e.line(fmt.Sprintf("zrt_unwind_to(%s);", lm))
		}
		e.line("break;")
	case token.Continue:
		if lm := e.loopMark(); lm != "" {
			e.line(fmt.Sprintf("zrt_unwind_to(%s);", lm))
		}
		e.line("continue;")
	case token.Return:
		e.returnStmt(&ast.ReturnStmt{Value: n.Value})
	case token.Raise:
		e.raiseStmt(&ast.RaiseStmt{Value: n.Value, From: n.From})
	}
}

// capture runs fn with output redirected to a fresh buffer and returns what it
// wrote, so a statement-carrying operator (`?`, a diverging `??`, `guard`) can embed
// real statements inside a GNU statement-expression. Indentation is reset for the
// captured fragment; the surrounding line supplies placement.
func (e *emitter) capture(fn func()) string {
	savedSB, savedIndent := e.sb, e.indent
	e.sb, e.indent = strings.Builder{}, 0
	fn()
	out := e.sb.String()
	e.sb, e.indent = savedSB, savedIndent
	return out
}
