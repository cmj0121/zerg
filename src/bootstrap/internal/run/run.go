// Package run is the v0.1 tree-walking interpreter for `zerg run`.
//
// Run.Run takes the parser's AST, calls syntax.Check internally to annotate
// types and reject ill-formed programs, then walks the typed AST to produce
// stdout. The interpreter is the parity reference: its bytes-on-stdout for
// any v0.1 program must match the C codegen's bytes-on-stdout for the same
// program (Unit 4). The print format and numeric semantics are pinned in
// PLAN.md and reproduced here without freelancing.
package run

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// Run executes prog, sending program output to w. It first calls syntax.Check
// so callers (CLI, tests) do not need to remember to do so — the interpreter
// will not walk an un-typechecked tree because nearly every node-walker
// relies on Expr.Type() being non-nil.
//
// The returned error is for type errors (propagated verbatim from Check) or
// runtime failures. v0.1 has very few runtime failures: a short write on w,
// a call to a function that fails to return when its declared return type is
// non-void, or one of the documented "undefined" cases (div-by-zero on int,
// INT64_MIN/-1) that PLAN.md says is not exercised by the corpus.
func Run(prog *syntax.Program, w io.Writer) error {
	if err := syntax.Check(prog); err != nil {
		return err
	}
	in := newInterp(prog, w)
	for _, stmt := range prog.Statements {
		if _, ok := stmt.(*syntax.FnDecl); ok {
			// Fn decls are collected into the function table by newInterp.
			// At top level they are declarations, not executable statements.
			continue
		}
		if err := in.execStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Interpreter state.
// ---------------------------------------------------------------------------

// interp holds the per-Run mutable state. Functions are looked up by name in
// fns; variables live on a stack of frames. Each call site, each block, and
// each for-range iteration push a fresh frame. A frame holds the names
// introduced inside its scope only; lookup walks toward the root.
type interp struct {
	w   io.Writer
	fns map[string]*syntax.FnDecl

	// stack[0] is the top-level frame; the active frame is stack[len(stack)-1].
	// We keep the slice rather than a parent-pointer linked list because
	// pushing/popping a Go slice is allocation-light and the depth stays small
	// in practice.
	stack []*frame
}

// frame is one rung of the variable scope stack. Names live here only as
// long as the enclosing block or call is active.
type frame struct {
	vars map[string]*Value
}

func newFrame() *frame { return &frame{vars: map[string]*Value{}} }

// newInterp builds an interpreter with the program's fn table populated.
// Type-check has already validated function uniqueness, so a duplicate name
// here would be an internal error.
func newInterp(prog *syntax.Program, w io.Writer) *interp {
	in := &interp{w: w, fns: map[string]*syntax.FnDecl{}}
	for _, stmt := range prog.Statements {
		if fn, ok := stmt.(*syntax.FnDecl); ok {
			in.fns[fn.Name] = fn
		}
	}
	in.pushFrame()
	return in
}

func (in *interp) pushFrame() { in.stack = append(in.stack, newFrame()) }
func (in *interp) popFrame()  { in.stack = in.stack[:len(in.stack)-1] }

// declare binds name in the current (innermost) frame. typeck has already
// rejected same-block redeclarations, so we do not re-validate here — but we
// guard against the impossible case to fail loudly rather than silently.
func (in *interp) declare(name string, v Value) error {
	top := in.stack[len(in.stack)-1]
	if _, dup := top.vars[name]; dup {
		return fmt.Errorf("internal: %q already bound in current frame", name)
	}
	val := v
	top.vars[name] = &val
	return nil
}

// lookup walks frames from innermost to outermost. Returns the storage slot
// (so assignment can mutate it) plus a found bool.
func (in *interp) lookup(name string) (*Value, bool) {
	for i := len(in.stack) - 1; i >= 0; i-- {
		if slot, ok := in.stack[i].vars[name]; ok {
			return slot, true
		}
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Control-flow sentinels.
//
// We use sentinel errors to unwind the stack on return / break / continue.
// Carrying the value (for return) on a struct field of the unwinding error
// keeps the call-site signature uniform: every execStmt returns error, and
// the enclosing fn / loop catches the right sentinel kind.
// ---------------------------------------------------------------------------

// errReturn carries a returning value out of a function body. The Value field
// is the zero Value when the function declares no return type and uses bare
// `return`. callFn() recognises this and unwinds.
type errReturn struct{ value Value }

func (e *errReturn) Error() string { return "return" }

// errBreak unwinds out of the innermost loop. The enclosing for-loop catches
// it and exits cleanly.
var errBreak = errors.New("break")

// errContinue unwinds to the top of the innermost loop. The enclosing for-loop
// catches it and proceeds to the next iteration.
var errContinue = errors.New("continue")

// ---------------------------------------------------------------------------
// Statement execution.
// ---------------------------------------------------------------------------

func (in *interp) execStmt(stmt syntax.Stmt) error {
	switch s := stmt.(type) {
	case *syntax.NopStmt:
		return nil
	case *syntax.PrintStmt:
		return in.execPrint(s)
	case *syntax.LetStmt:
		return in.execDecl(s.Name, s.Value)
	case *syntax.MutStmt:
		return in.execDecl(s.Name, s.Value)
	case *syntax.ConstStmt:
		// At v0.1 a const is just an immutable binding. The type checker has
		// already enforced that the rhs is a constant expression; runtime
		// evaluation is the same as let.
		return in.execDecl(s.Name, s.Value)
	case *syntax.AssignStmt:
		return in.execAssign(s)
	case *syntax.ExprStmt:
		_, err := in.evalExpr(s.Expr)
		return err
	case *syntax.IfStmt:
		return in.execIf(s)
	case *syntax.ForStmt:
		return in.execFor(s)
	case *syntax.ReturnStmt:
		return in.execReturn(s)
	case *syntax.BreakStmt:
		ok, err := in.guardTrue(s.Guard)
		if err != nil {
			return err
		}
		if ok {
			return errBreak
		}
		return nil
	case *syntax.ContinueStmt:
		ok, err := in.guardTrue(s.Guard)
		if err != nil {
			return err
		}
		if ok {
			return errContinue
		}
		return nil
	case *syntax.FnDecl:
		// Nested fn decls are rejected by typeck; reaching this from a top-
		// level walk is handled in Run() by the FnDecl skip. A FnDecl seen
		// elsewhere is an internal error.
		return fmt.Errorf("internal: unexpected FnDecl at %s", s.Pos)
	}
	return fmt.Errorf("internal: unhandled statement %T at %s", stmt, stmt.StmtPos())
}

// execPrint formats per the v0.1 print table: trailing '\n' always, no quotes
// around str, decimal int, %g float, "true"/"false" for bool.
func (in *interp) execPrint(s *syntax.PrintStmt) error {
	v, err := in.evalExpr(s.Expr)
	if err != nil {
		return err
	}
	out := formatValue(v)
	// Append '\n' once — every output line gets it. fmt.Fprintln would also
	// work but introduces a Fprintln-specific space-between-args behaviour
	// that does not matter here yet may surprise a reader; explicit is safer.
	if _, err := io.WriteString(in.w, out); err != nil {
		return err
	}
	_, err = io.WriteString(in.w, "\n")
	return err
}

// formatValue is the print-format spec. C codegen MUST emit the same bytes;
// see PLAN.md "print format spec (pinned)".
func formatValue(v Value) string {
	switch v.Type {
	case syntax.TInt():
		return strconv.FormatInt(v.Int, 10)
	case syntax.TFloat():
		return strconv.FormatFloat(v.Float, 'g', 17, 64)
	case syntax.TBool():
		if v.Bool {
			return "true"
		}
		return "false"
	case syntax.TStr():
		return v.Str
	}
	// typeck rejects anything else for `print`; reaching here is an internal
	// error rather than user-visible.
	return fmt.Sprintf("<unprintable %s>", v.Type)
}

// execDecl evaluates the rhs and binds the name in the current frame.
func (in *interp) execDecl(name string, value syntax.Expr) error {
	v, err := in.evalExpr(value)
	if err != nil {
		return err
	}
	return in.declare(name, v)
}

// execAssign mutates an existing binding. typeck has already checked the
// target is mut and the rhs type matches; here we just do the operation.
func (in *interp) execAssign(s *syntax.AssignStmt) error {
	slot, ok := in.lookup(s.Target.Name)
	if !ok {
		return fmt.Errorf("internal: undefined name %q at %s", s.Target.Name, s.Pos)
	}
	rhs, err := in.evalExpr(s.Value)
	if err != nil {
		return err
	}
	switch s.Op {
	case syntax.AssignSet:
		*slot = rhs
	case syntax.AssignAdd:
		*slot, err = applyBin(syntax.BinAdd, *slot, rhs)
	case syntax.AssignSub:
		*slot, err = applyBin(syntax.BinSub, *slot, rhs)
	case syntax.AssignMul:
		*slot, err = applyBin(syntax.BinMul, *slot, rhs)
	case syntax.AssignDiv:
		*slot, err = applyBin(syntax.BinDiv, *slot, rhs)
	case syntax.AssignMod:
		*slot, err = applyBin(syntax.BinMod, *slot, rhs)
	case syntax.AssignAnd:
		*slot, err = applyBin(syntax.BinBitAnd, *slot, rhs)
	case syntax.AssignOr:
		*slot, err = applyBin(syntax.BinBitOr, *slot, rhs)
	case syntax.AssignXor:
		*slot, err = applyBin(syntax.BinBitXor, *slot, rhs)
	case syntax.AssignShl:
		*slot, err = applyBin(syntax.BinShl, *slot, rhs)
	case syntax.AssignShr:
		*slot, err = applyBin(syntax.BinShr, *slot, rhs)
	default:
		return fmt.Errorf("internal: unknown assign op %s at %s", s.Op, s.Pos)
	}
	return err
}

// execIf walks the if-elif-else chain. A matched branch executes its block
// in a fresh frame, then the chain ends.
func (in *interp) execIf(s *syntax.IfStmt) error {
	cond, err := in.evalExpr(s.Cond)
	if err != nil {
		return err
	}
	if cond.Bool {
		return in.execBlock(s.Then)
	}
	for i := range s.Elifs {
		ec := &s.Elifs[i]
		c, err := in.evalExpr(ec.Cond)
		if err != nil {
			return err
		}
		if c.Bool {
			return in.execBlock(ec.Body)
		}
	}
	if s.Else != nil {
		return in.execBlock(s.Else)
	}
	return nil
}

// execBlock pushes a frame, walks statements, pops on the way out.
func (in *interp) execBlock(b *syntax.Block) error {
	in.pushFrame()
	defer in.popFrame()
	for _, st := range b.Statements {
		if err := in.execStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// execFor handles all three for-loop shapes. break/continue are caught here.
func (in *interp) execFor(s *syntax.ForStmt) error {
	switch s.Kind {
	case syntax.ForInfinite:
		for {
			err := in.execBlock(s.Body)
			if errors.Is(err, errBreak) {
				return nil
			}
			if errors.Is(err, errContinue) {
				continue
			}
			if err != nil {
				return err
			}
		}
	case syntax.ForCond:
		for {
			c, err := in.evalExpr(s.Cond)
			if err != nil {
				return err
			}
			if !c.Bool {
				return nil
			}
			err = in.execBlock(s.Body)
			if errors.Is(err, errBreak) {
				return nil
			}
			if errors.Is(err, errContinue) {
				continue
			}
			if err != nil {
				return err
			}
		}
	case syntax.ForRange:
		startV, err := in.evalExpr(s.Range.Start)
		if err != nil {
			return err
		}
		endV, err := in.evalExpr(s.Range.End)
		if err != nil {
			return err
		}
		start, end := startV.Int, endV.Int
		if s.Range.Inclusive {
			// For closed ranges we walk start..end inclusive. If end < start
			// the loop body never runs — same as half-open with reversed
			// bounds. We don't iterate downward at v0.1; PLAN.md doesn't
			// pin reverse iteration semantics so we keep it forward-only.
			for i := start; i <= end; i++ {
				if cont, err := in.runRangeIter(s, i); err != nil {
					return err
				} else if !cont {
					return nil
				}
			}
		} else {
			for i := start; i < end; i++ {
				if cont, err := in.runRangeIter(s, i); err != nil {
					return err
				} else if !cont {
					return nil
				}
			}
		}
		return nil
	}
	return fmt.Errorf("internal: unknown for kind at %s", s.Pos)
}

// runRangeIter executes one iteration of a for-in body with the loop var
// bound to i. Returns (continueLoop, err): false means break, true means
// proceed (whether or not continue fired). Errors not-equal-to break/continue
// propagate.
func (in *interp) runRangeIter(s *syntax.ForStmt, i int64) (bool, error) {
	in.pushFrame()
	defer in.popFrame()
	if err := in.declare(s.Var, intVal(i)); err != nil {
		return false, err
	}
	for _, st := range s.Body.Statements {
		err := in.execStmt(st)
		if errors.Is(err, errBreak) {
			return false, nil
		}
		if errors.Is(err, errContinue) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

// execReturn unwinds to the enclosing call. typeck has validated the value
// type; the guard form returns only when the guard is true.
func (in *interp) execReturn(s *syntax.ReturnStmt) error {
	ok, err := in.guardTrue(s.Guard)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if s.Value == nil {
		return &errReturn{value: Value{}}
	}
	v, err := in.evalExpr(s.Value)
	if err != nil {
		return err
	}
	return &errReturn{value: v}
}

// guardTrue evaluates a break/continue/return guard. A nil guard means
// "unconditional", so the result is true.
func (in *interp) guardTrue(g syntax.Expr) (bool, error) {
	if g == nil {
		return true, nil
	}
	v, err := in.evalExpr(g)
	if err != nil {
		return false, err
	}
	return v.Bool, nil
}

// ---------------------------------------------------------------------------
// Expression evaluation.
// ---------------------------------------------------------------------------

func (in *interp) evalExpr(expr syntax.Expr) (Value, error) {
	switch e := expr.(type) {
	case *syntax.IntLit:
		return intVal(e.Int), nil
	case *syntax.FloatLit:
		return floatVal(e.Float), nil
	case *syntax.StringLit:
		return strVal(e.Value), nil
	case *syntax.BoolLit:
		return boolVal(e.Value), nil
	case *syntax.IdentExpr:
		slot, ok := in.lookup(e.Name)
		if !ok {
			return Value{}, fmt.Errorf("internal: undefined name %q at %s", e.Name, e.Pos)
		}
		return *slot, nil
	case *syntax.ParenExpr:
		return in.evalExpr(e.Inner)
	case *syntax.UnaryExpr:
		return in.evalUnary(e)
	case *syntax.BinaryExpr:
		return in.evalBinary(e)
	case *syntax.CallExpr:
		return in.evalCall(e)
	}
	return Value{}, fmt.Errorf("internal: unhandled expression %T at %s", expr, expr.ExprPos())
}

func (in *interp) evalUnary(e *syntax.UnaryExpr) (Value, error) {
	v, err := in.evalExpr(e.Operand)
	if err != nil {
		return Value{}, err
	}
	switch e.Op {
	case syntax.UnaryNeg:
		if v.Type == syntax.TInt() {
			return intVal(-v.Int), nil
		}
		// typeck restricts unary - to numeric, so the only other case is float.
		return floatVal(-v.Float), nil
	case syntax.UnaryBitNot:
		return intVal(^v.Int), nil
	case syntax.UnaryNot:
		return boolVal(!v.Bool), nil
	}
	return Value{}, fmt.Errorf("internal: unknown unary op %s at %s", e.Op, e.Pos)
}

// evalBinary handles short-circuit `and`/`or`; everything else delegates to
// applyBin so the assignment path can share the implementation.
func (in *interp) evalBinary(e *syntax.BinaryExpr) (Value, error) {
	switch e.Op {
	case syntax.BinAnd:
		// Short-circuit: skip the rhs when lhs is false.
		l, err := in.evalExpr(e.Left)
		if err != nil {
			return Value{}, err
		}
		if !l.Bool {
			return boolVal(false), nil
		}
		r, err := in.evalExpr(e.Right)
		if err != nil {
			return Value{}, err
		}
		return boolVal(r.Bool), nil
	case syntax.BinOr:
		// Short-circuit: skip the rhs when lhs is true.
		l, err := in.evalExpr(e.Left)
		if err != nil {
			return Value{}, err
		}
		if l.Bool {
			return boolVal(true), nil
		}
		r, err := in.evalExpr(e.Right)
		if err != nil {
			return Value{}, err
		}
		return boolVal(r.Bool), nil
	}
	// All non-short-circuit ops evaluate both sides eagerly.
	lv, err := in.evalExpr(e.Left)
	if err != nil {
		return Value{}, err
	}
	rv, err := in.evalExpr(e.Right)
	if err != nil {
		return Value{}, err
	}
	return applyBin(e.Op, lv, rv)
}

// applyBin performs op on already-evaluated lv, rv. Shared by direct binary
// expressions and compound assignments. typeck has guaranteed the operand
// types match the operator's expectations, so the dispatch is type-safe.
//
// Numeric semantics (pinned in PLAN.md):
//   - int arithmetic wraps via Go's int64 (matches C `-fwrapv`).
//   - int / and // both truncate toward zero (Go and C99+ agree).
//   - float / produces IEEE 754 quotient.
//   - float // produces math.Floor(quotient) as a float — PLAN.md does not
//     pin float floor-division, but the codegen will emit the same lowering
//     so v0.1 parity holds. Document here so Unit 4 follows suit.
//   - int % follows the dividend's sign (Go and C99+ agree).
//   - String + concatenates.
func applyBin(op syntax.BinaryOp, lv, rv Value) (Value, error) {
	switch op {
	case syntax.BinAdd:
		if lv.Type == syntax.TStr() {
			return strVal(lv.Str + rv.Str), nil
		}
		if lv.Type == syntax.TInt() {
			return intVal(lv.Int + rv.Int), nil
		}
		return floatVal(lv.Float + rv.Float), nil
	case syntax.BinSub:
		if lv.Type == syntax.TInt() {
			return intVal(lv.Int - rv.Int), nil
		}
		return floatVal(lv.Float - rv.Float), nil
	case syntax.BinMul:
		if lv.Type == syntax.TInt() {
			return intVal(lv.Int * rv.Int), nil
		}
		return floatVal(lv.Float * rv.Float), nil
	case syntax.BinDiv:
		if lv.Type == syntax.TInt() {
			// PLAN.md: "Division by zero on int: runtime-undefined; not
			// exercised." We don't synthesise a dedicated error; Go panics on
			// integer division by zero and that is acceptable parity with C
			// undefined behaviour for the v0.1 corpus.
			return intVal(lv.Int / rv.Int), nil
		}
		return floatVal(lv.Float / rv.Float), nil
	case syntax.BinFloorDiv:
		// On int: identical to BinDiv (truncating toward zero). PLAN.md does
		// not split `//` from `/` for int at v0.1; we choose to make them
		// identical because (a) the parity codegen will lower both to the
		// same C expression for ints, (b) any user who reaches for `//` on
		// ints gets the answer they expect for non-negative operands.
		// On float: math.Floor of the quotient — see Note above applyBin.
		if lv.Type == syntax.TInt() {
			return intVal(lv.Int / rv.Int), nil
		}
		// We avoid pulling in math just for Floor here; the float64 trick
		// `q := a/b; if (q != int64(q)) && (signMismatch) { q-- }` is more
		// fragile than just using math.Floor. Use math.Floor.
		return floatVal(floorFloat(lv.Float / rv.Float)), nil
	case syntax.BinMod:
		if lv.Type == syntax.TInt() {
			return intVal(lv.Int % rv.Int), nil
		}
		// Go has no float64 % at the language level; we are not required to
		// support it (typeck rejects float % at parse-or-check time? Actually
		// it does not — see typeck.go BinSub/...,BinMod accepts numeric.
		// PLAN.md does not exercise it, but the codegen should match. Use
		// math.Mod equivalent via the standard "a - b*trunc(a/b)" identity.
		return floatVal(floatMod(lv.Float, rv.Float)), nil
	case syntax.BinBitAnd:
		return intVal(lv.Int & rv.Int), nil
	case syntax.BinBitOr:
		return intVal(lv.Int | rv.Int), nil
	case syntax.BinBitXor:
		return intVal(lv.Int ^ rv.Int), nil
	case syntax.BinShl:
		// Shift by negative amounts is undefined in C; typeck does not catch
		// it. Go panics on negative shift count in some Go versions; we let
		// the runtime decide rather than synthesising a specific error.
		return intVal(lv.Int << uint64(rv.Int)), nil
	case syntax.BinShr:
		return intVal(lv.Int >> uint64(rv.Int)), nil
	case syntax.BinEq:
		return boolVal(valueEq(lv, rv)), nil
	case syntax.BinNE:
		return boolVal(!valueEq(lv, rv)), nil
	case syntax.BinLT:
		return boolVal(valueLT(lv, rv)), nil
	case syntax.BinGT:
		return boolVal(valueLT(rv, lv)), nil
	case syntax.BinLE:
		return boolVal(!valueLT(rv, lv)), nil
	case syntax.BinGE:
		return boolVal(!valueLT(lv, rv)), nil
	case syntax.BinXor:
		// Logical xor — non-short-circuit per PLAN.md.
		return boolVal(lv.Bool != rv.Bool), nil
	case syntax.BinAnd, syntax.BinOr:
		// Short-circuit forms are handled in evalBinary; if we land here it's
		// because applyBin was called from a compound-assign path (which
		// never targets bool ops) — that's an internal error.
		return Value{}, fmt.Errorf("internal: %s reached applyBin", op)
	}
	return Value{}, fmt.Errorf("internal: unhandled binary op %s", op)
}

// valueEq is == over typed values. typeck guarantees lv.Type == rv.Type.
func valueEq(lv, rv Value) bool {
	switch lv.Type {
	case syntax.TInt():
		return lv.Int == rv.Int
	case syntax.TFloat():
		return lv.Float == rv.Float
	case syntax.TBool():
		return lv.Bool == rv.Bool
	case syntax.TStr():
		return lv.Str == rv.Str
	}
	return false
}

// valueLT is < over typed values. typeck guarantees same-typed numeric/str
// operands; bool ordering is rejected at check time.
func valueLT(lv, rv Value) bool {
	switch lv.Type {
	case syntax.TInt():
		return lv.Int < rv.Int
	case syntax.TFloat():
		return lv.Float < rv.Float
	case syntax.TStr():
		return lv.Str < rv.Str
	}
	return false
}

// floorFloat returns math.Floor(x). Wrapped in a helper so the few call
// sites read "this is float floor-division semantics" rather than reaching
// for the math package directly.
func floorFloat(x float64) float64 { return math.Floor(x) }

// floatMod implements a - b*trunc(a/b) for float64 operands. typeck currently
// admits float % even though the corpus does not exercise it. The codegen
// will emit fmod(a,b) for parity; we use math.Mod here to match.
func floatMod(a, b float64) float64 { return math.Mod(a, b) }

// evalCall executes a function call. typeck has verified the callee is a
// declared fn and the argument types match. We push a fresh frame, bind
// parameters, walk the body, and catch errReturn to extract the value.
func (in *interp) evalCall(e *syntax.CallExpr) (Value, error) {
	ident, ok := e.Callee.(*syntax.IdentExpr)
	if !ok {
		return Value{}, fmt.Errorf("internal: non-ident callee at %s", e.Pos)
	}
	fn, ok := in.fns[ident.Name]
	if !ok {
		return Value{}, fmt.Errorf("internal: undefined function %q at %s", ident.Name, e.Pos)
	}

	// Evaluate args in left-to-right order BEFORE pushing the call frame,
	// so the args are evaluated in the caller's scope (matters for nested
	// calls or self-recursion).
	args := make([]Value, len(e.Args))
	for i, a := range e.Args {
		v, err := in.evalExpr(a)
		if err != nil {
			return Value{}, err
		}
		args[i] = v
	}

	// Calls do NOT inherit the caller's scope: a fresh frame stack rooted at
	// just the new frame. Without this a fn could accidentally see the
	// caller's locals — typeck would catch most cases, but at v0.1 with
	// only top-level fns the rule is "fn body sees parameters and globals
	// of nothing else". We achieve it by saving and replacing the stack.
	savedStack := in.stack
	in.stack = []*frame{newFrame()}
	defer func() { in.stack = savedStack }()

	for i, p := range fn.Params {
		if err := in.declare(p.Name, args[i]); err != nil {
			return Value{}, err
		}
	}

	for _, st := range fn.Body.Statements {
		err := in.execStmt(st)
		if err == nil {
			continue
		}
		var ret *errReturn
		if errors.As(err, &ret) {
			return ret.value, nil
		}
		// break/continue must NOT escape a function: typeck rejects them
		// outside loops, and a function body without an enclosing loop in
		// scope means any `break` is in a loop strictly inside the body and
		// is caught by execFor before reaching us. Defensive check.
		if errors.Is(err, errBreak) || errors.Is(err, errContinue) {
			return Value{}, fmt.Errorf("internal: %v escaped fn %s", err, ident.Name)
		}
		return Value{}, err
	}
	// Fall-through end of body. typeck rejects falling off a non-void fn,
	// so reaching here for a void fn is fine; for a non-void fn it is an
	// internal error.
	if e.Type() != nil && e.Type() != syntax.TVoid() {
		return Value{}, fmt.Errorf("function %q ended without return at %s", ident.Name, e.Pos)
	}
	return Value{}, nil
}
