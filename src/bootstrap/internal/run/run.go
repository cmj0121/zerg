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
	"strings"

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
		switch stmt.(type) {
		case *syntax.FnDecl, *syntax.SpecDecl, *syntax.ImplDecl:
			// Decls are collected into per-program tables by newInterp. At
			// top level they are declarations, not executable statements.
			continue
		case *syntax.ImportDecl:
			// v0.5 Unit 1b: imports are resolved by the loader before Run
			// sees the merged program. A stray ImportDecl at this layer is
			// a no-op so existing single-file programs keep running unchanged.
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
//
// enums maps every enum name declared at top level to its canonical *Type,
// so FieldAccessExpr can disambiguate `Color.Red` (enum variant access) from
// `p.x` (struct field access) by checking the receiver-name against this
// table. typeck has already validated each enum's variant set; we just
// mirror the lookup structure here so the runtime path can produce a
// variant Value without re-walking the AST.
//
// v0.4 adds spec/impl tables. specDecls is the per-name SpecDecl AST so
// default-method bodies are accessible at dispatch time. inherentImpls
// aggregates every inherent impl method per receiver type. specImpls is keyed
// by (Type, Spec) so vtable construction and method dispatch resolve in O(1).
type interp struct {
	w   io.Writer
	fns map[string]*syntax.FnDecl

	enums map[string]*syntax.Type

	// v0.4: spec / impl tables. Mirror typeck's structure but reuse the AST
	// nodes for body walking. Method dispatch precedence at the interpreter
	// is the same as typeck: inherent first, then unique spec impl, then
	// vtable when the receiver is a fat-pointer specValue.
	specDecls     map[string]*syntax.SpecDecl
	inherentImpls map[string]map[string]*syntax.FnDecl                  // type → method → FnDecl
	specImpls     map[implKey]map[string]*syntax.FnDecl                 // (type, spec) → method → FnDecl
	specImplPos   map[implKey]bool                                      // (type, spec) presence (the impl block exists)

	// stack[0] is the top-level frame; the active frame is stack[len(stack)-1].
	// We keep the slice rather than a parent-pointer linked list because
	// pushing/popping a Go slice is allocation-light and the depth stays small
	// in practice.
	stack []*frame
}

// implKey deduplicates impls by (type, spec). Spec is "" for inherent impls.
type implKey struct {
	typeName string
	specName string
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
	in := &interp{
		w:             w,
		fns:           map[string]*syntax.FnDecl{},
		enums:         map[string]*syntax.Type{},
		specDecls:     map[string]*syntax.SpecDecl{},
		inherentImpls: map[string]map[string]*syntax.FnDecl{},
		specImpls:     map[implKey]map[string]*syntax.FnDecl{},
		specImplPos:   map[implKey]bool{},
	}
	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *syntax.FnDecl:
			in.fns[s.Name] = s
		case *syntax.EnumDecl:
			variants := make([]string, len(s.Variants))
			for i, v := range s.Variants {
				variants[i] = v.Name
			}
			in.enums[s.Name] = syntax.NewEnumType(s.Name, variants)
		case *syntax.SpecDecl:
			in.specDecls[s.Name] = s
		case *syntax.ImplDecl:
			if s.Spec == "" {
				m, ok := in.inherentImpls[s.Type]
				if !ok {
					m = map[string]*syntax.FnDecl{}
					in.inherentImpls[s.Type] = m
				}
				for _, fn := range s.Methods {
					m[fn.Name] = fn
				}
			} else {
				key := implKey{typeName: s.Type, specName: s.Spec}
				m, ok := in.specImpls[key]
				if !ok {
					m = map[string]*syntax.FnDecl{}
					in.specImpls[key] = m
				}
				for _, fn := range s.Methods {
					m[fn.Name] = fn
				}
				in.specImplPos[key] = true
			}
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
		if s.Tuple != nil {
			return in.execTupleDestructure(s.Tuple, s.Value)
		}
		return in.execDecl(s.Name, s.Type, s.Value)
	case *syntax.MutStmt:
		if s.Tuple != nil {
			return in.execTupleDestructure(s.Tuple, s.Value)
		}
		return in.execDecl(s.Name, s.Type, s.Value)
	case *syntax.ConstStmt:
		// At v0.1 a const is just an immutable binding. The type checker has
		// already enforced that the rhs is a constant expression; runtime
		// evaluation is the same as let. The destructure form is rejected by
		// typeck so s.Tuple is always nil here.
		return in.execDecl(s.Name, s.Type, s.Value)
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
	case *syntax.StructDecl:
		// Top-level type declarations are registered in newInterp; nothing
		// to execute at statement-walk time. typeck rejects nested decls.
		return nil
	case *syntax.EnumDecl:
		// Same as StructDecl — registration happens once at interp init.
		return nil
	case *syntax.MatchStmt:
		return in.execMatch(s)
	case *syntax.SpecDecl, *syntax.ImplDecl:
		// v0.4: spec / impl declarations are processed at interp init
		// (newInterp aggregates inherentImpls / specImpls). At statement-walk
		// time they are no-ops — like StructDecl / EnumDecl.
		_ = s
		return nil
	case *syntax.ImportDecl:
		// v0.5 Unit 1b: imports are resolved by the loader before Run sees
		// the merged program. A stray ImportDecl at this layer is a no-op.
		_ = s
		return nil
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
//
// v0.2 extensions (PLAN lines 153-160):
//   - byte: decimal of the unsigned 0..255 value.
//   - rune: decimal of the Unicode codepoint.
//   - list[T]: "[ e1, e2, e3 ]" — comma+space between elements; empty list
//     prints "[]" with no inner spaces.
//   - tuple: "( e1, e2 )" — same comma+space rule; tuples have ≥ 2 elements
//     so the empty-pair guard does not apply.
//   - struct: "Name { field1: e1, field2: e2 }" — declaration field order.
//   - enum: "Name.VariantName".
//
// Inner element formatting recurses through formatValue, so a list of
// structs prints with the struct format inline.
func formatValue(v Value) string {
	if v.Type == nil {
		return fmt.Sprintf("<unprintable %s>", v.Type)
	}
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
	case syntax.TByte():
		// PLAN: decimal of the unsigned value. Token/typeck guarantee
		// 0 <= v.Int < 128 for byte (ASCII range), but we mask defensively.
		return strconv.FormatUint(uint64(uint8(v.Int)), 10)
	case syntax.TRune():
		return strconv.FormatInt(v.Int, 10)
	}
	switch v.Type.Kind {
	case syntax.TypeList:
		if len(v.List) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[ ")
		for i, e := range v.List {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(formatValue(e))
		}
		b.WriteString(" ]")
		return b.String()
	case syntax.TypeTuple:
		var b strings.Builder
		b.WriteString("( ")
		for i, e := range v.Tuple {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(formatValue(e))
		}
		b.WriteString(" )")
		return b.String()
	case syntax.TypeStruct:
		var b strings.Builder
		b.WriteString(v.Type.Name)
		b.WriteString(" { ")
		for i, f := range v.Type.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Name)
			b.WriteString(": ")
			b.WriteString(formatValue(v.Fields[i]))
		}
		b.WriteString(" }")
		return b.String()
	case syntax.TypeEnum:
		if len(v.Payload) == 0 {
			return v.Type.Name + "." + v.VariantName
		}
		// PLAN: payload variants print as "Name.Variant(arg1, arg2)" with
		// recursive formatValue per position. No leading/trailing spaces
		// inside the parens — matches the literal source-form construction.
		var b strings.Builder
		b.WriteString(v.Type.Name)
		b.WriteString(".")
		b.WriteString(v.VariantName)
		b.WriteString("(")
		for i, p := range v.Payload {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(formatValue(p))
		}
		b.WriteString(")")
		return b.String()
	}
	// typeck rejects anything else for `print`; reaching here is an internal
	// error rather than user-visible.
	return fmt.Sprintf("<unprintable %s>", v.Type)
}

// execDecl evaluates the rhs and binds the name in the current frame. The
// value is deep-copied so any composite payload is independent of the source
// — this is the v0.2 value-semantics rule for lists / tuples / structs.
// Primitives copy trivially through the same helper; the cost is negligible
// for small composite shapes the corpus exercises.
func (in *interp) execDecl(name string, ref *syntax.TypeRef, value syntax.Expr) error {
	v, err := in.evalExpr(value)
	if err != nil {
		return err
	}
	// v0.4: when the binding is annotated with a spec type (or a composite
	// containing a spec position), wrap the rhs at the bind site so method
	// dispatch can reach the right (concrete, spec) impl.
	if ref != nil && ref.Resolved != nil {
		v = in.coerceToType(v, ref.Resolved)
	}
	// v0.3: no implicit deep-copy on bind. The borrow checker has
	// invalidated the source binding at the move site, so sharing the
	// underlying Go slice/struct is safe. clone(xs) is the explicit
	// opt-in for the v0.2-style deep copy.
	return in.declare(name, v)
}

// execTupleDestructure evaluates `let (a, b, ...) := expr` (and the mut
// form). The RHS must yield a tuple value of matching arity — typeck has
// already enforced this, so a mismatch here is an internal error rather
// than user-facing. Each name is bound to a deep copy of the matching
// element so the new bindings are independent of the source tuple.
func (in *interp) execTupleDestructure(tb *syntax.TupleBinding, value syntax.Expr) error {
	v, err := in.evalExpr(value)
	if err != nil {
		return err
	}
	if v.Type == nil || v.Type.Kind != syntax.TypeTuple {
		return fmt.Errorf("internal: destructure rhs is not a tuple at %s", tb.Pos)
	}
	if len(v.Tuple) != len(tb.Names) {
		return fmt.Errorf("internal: destructure arity mismatch at %s: %d names vs %d elements", tb.Pos, len(tb.Names), len(v.Tuple))
	}
	for i, name := range tb.Names {
		// v0.3: no implicit deep-copy on tuple destructure bind.
		if err := in.declare(name, v.Tuple[i]); err != nil {
			return err
		}
	}
	return nil
}

// execAssign mutates an existing binding. typeck has already checked the
// target is mut and the rhs type matches; here we just do the operation.
//
// Two LHS shapes are admitted: a bare IdentExpr (`x = v`) which writes the
// named slot in scope, and an IndexExpr (`xs[i] = v`) which writes through a
// list slot's slice header at the given position. typeck and the borrow
// checker have already verified mutability and aliasing; the interpreter
// only does the work.
func (in *interp) execAssign(s *syntax.AssignStmt) error {
	if idx, ok := s.Target.(*syntax.IndexExpr); ok {
		return in.execIndexAssign(s, idx)
	}
	target, ok := s.Target.(*syntax.IdentExpr)
	if !ok {
		return fmt.Errorf("internal: unsupported assignment target %T at %s", s.Target, s.Pos)
	}
	slot, ok := in.lookup(target.Name)
	if !ok {
		return fmt.Errorf("internal: undefined name %q at %s", target.Name, s.Pos)
	}
	rhs, err := in.evalExpr(s.Value)
	if err != nil {
		return err
	}
	switch s.Op {
	case syntax.AssignSet:
		// v0.3: plain `x = v` is only meaningful for primitive targets
		// (composite mut bindings rebind via `:=` or write through
		// `xs[i] = v`); no implicit deep-copy.
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

// execIndexAssign handles `xs[i] = v`. The receiver must be a bare named
// list (typeck and the borrow checker have already enforced this); we look
// up the slot, evaluate the index, range-check it, and write a deep copy of
// the rhs through the slice header at the indexed position.
//
// Only AssignSet (`=`) is admitted on a list element — compound assigns
// (`xs[i] += 1`) are out of scope at v0.3 because typeck doesn't yet plumb
// them through the IndexExpr LHS path. Any compound op that reaches here
// is a typeck bug; we report it rather than guess.
func (in *interp) execIndexAssign(s *syntax.AssignStmt, idx *syntax.IndexExpr) error {
	id, ok := idx.Receiver.(*syntax.IdentExpr)
	if !ok {
		return fmt.Errorf("internal: list-element assignment requires a named list at %s", s.Pos)
	}
	slot, ok := in.lookup(id.Name)
	if !ok {
		return fmt.Errorf("internal: undefined name %q at %s", id.Name, s.Pos)
	}
	iv, err := in.evalExpr(idx.Index)
	if err != nil {
		return err
	}
	rhs, err := in.evalExpr(s.Value)
	if err != nil {
		return err
	}
	if s.Op != syntax.AssignSet {
		return fmt.Errorf("internal: list-element compound assign %s not supported at %s", s.Op, s.Pos)
	}
	n := int64(len(slot.List))
	i := iv.Int
	if i < 0 || i >= n {
		return fmt.Errorf("runtime error at %s: list index %d out of range [0..%d)", s.Pos, i, n)
	}
	// v0.3: no implicit deep-copy on element write — the borrow checker
	// has invalidated the rhs's source binding at the move site.
	slot.List[i] = rhs
	return nil
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
	case syntax.ForIter:
		// `for x in xs { ... }` — list iteration. Evaluate the iterable
		// once; deep-copy each element on bind so the loop body sees a
		// snapshot independent of any later mutation of xs (no list
		// mutation at v0.2 keeps this academic, but the contract holds).
		iterV, err := in.evalExpr(s.Iter)
		if err != nil {
			return err
		}
		if iterV.Type == nil || iterV.Type.Kind != syntax.TypeList {
			return fmt.Errorf("internal: for-in iterable is not a list at %s", s.Pos)
		}
		for _, elem := range iterV.List {
			cont, err := in.runListIter(s, elem)
			if err != nil {
				return err
			}
			if !cont {
				return nil
			}
		}
		return nil
	}
	return fmt.Errorf("internal: unknown for kind at %s", s.Pos)
}

// runListIter executes one iteration of a `for x in xs` body with the loop
// variable bound to a deep copy of elem. Mirrors runRangeIter's contract:
// returns (continueLoop, err) where false means break, true means proceed.
func (in *interp) runListIter(s *syntax.ForStmt, elem Value) (bool, error) {
	in.pushFrame()
	defer in.popFrame()
	// v0.3: no implicit deep-copy on for-iter element bind. The borrow
	// checker has BorrowedShared the iterable for the body's duration
	// and rejects mutation of it inside, so shared backing is safe.
	if err := in.declare(s.Var, elem); err != nil {
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
	case *syntax.RuneLit:
		// typeck has classified the literal as TByte or TRune via Type();
		// reuse that decision so the print path picks the right format.
		if e.Type() == syntax.TByte() {
			return byteVal(e.Value), nil
		}
		return runeVal(e.Value), nil
	case *syntax.ListLit:
		return in.evalListLit(e)
	case *syntax.TupleLit:
		return in.evalTupleLit(e)
	case *syntax.StructLit:
		return in.evalStructLit(e)
	case *syntax.IndexExpr:
		return in.evalIndex(e)
	case *syntax.SliceExpr:
		return in.evalSlice(e)
	case *syntax.FieldAccessExpr:
		return in.evalFieldAccess(e)
	case *syntax.MethodCallExpr:
		return in.evalMethodCall(e)
	case *syntax.ThisExpr:
		slot, ok := in.lookup("this")
		if !ok {
			return Value{}, fmt.Errorf("internal: 'this' is only valid inside an impl method body at %s", e.Pos)
		}
		return *slot, nil
	case *syntax.EnumLit:
		return in.evalEnumLit(e)
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
		eq, err := eqValues(lv, rv)
		if err != nil {
			return Value{}, err
		}
		return boolVal(eq), nil
	case syntax.BinNE:
		eq, err := eqValues(lv, rv)
		if err != nil {
			return Value{}, err
		}
		return boolVal(!eq), nil
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
// Byte and rune compare on their integer codepoint — same as the codegen
// side which lowers to a plain `==` on uint8_t / int32_t.
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
	case syntax.TByte(), syntax.TRune():
		return lv.Int == rv.Int
	}
	return false
}

// valueLT is < over typed values. typeck guarantees same-typed numeric/str
// operands; bool ordering is rejected at check time. Byte and rune order on
// codepoint, mirroring the codegen's int compare.
func valueLT(lv, rv Value) bool {
	switch lv.Type {
	case syntax.TInt():
		return lv.Int < rv.Int
	case syntax.TFloat():
		return lv.Float < rv.Float
	case syntax.TStr():
		return lv.Str < rv.Str
	case syntax.TByte(), syntax.TRune():
		return lv.Int < rv.Int
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
//
// The built-in `len` is dispatched here before the user-fn lookup. typeck
// has already enforced that `len` accepts exactly one list argument and
// returns int — at v0.2 it's the only generic intrinsic, so a single-name
// switch is the right shape; future built-ins will append.
func (in *interp) evalCall(e *syntax.CallExpr) (Value, error) {
	ident, ok := e.Callee.(*syntax.IdentExpr)
	if !ok {
		return Value{}, fmt.Errorf("internal: non-ident callee at %s", e.Pos)
	}
	if ident.Name == "len" {
		return in.evalLen(e)
	}
	// v0.3 builtins. `clone(xs)` returns a deep copy of its composite argument
	// — the borrow checker already enforces that primitives are rejected. The
	// interpreter implementation reuses copyValue, which is the same logic v0.2
	// applied implicitly on every bind. `push(xs, v)` mutates the named mut
	// list in place; the borrow checker has already validated mut and state.
	if ident.Name == "clone" {
		return in.evalClone(e)
	}
	if ident.Name == "push" {
		return in.evalPush(e)
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
		// v0.4: spec widening at the call boundary. typeck's typeEq path
		// rejects concrete-into-spec at the surface, but the lowered list-
		// builtin path lets typeck-valid spec arguments through; safe to call
		// unconditionally because coerceToType is identity when the param
		// type doesn't reach for a spec.
		if i < len(fn.Params) && fn.Params[i].Type != nil && fn.Params[i].Type.Resolved != nil {
			v = in.coerceToType(v, fn.Params[i].Type.Resolved)
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
		// v0.3: fn-call composite args are implicit shared borrows. No
		// deep copy at the call boundary — the borrow checker enforces
		// that fn parameters of composite type are BorrowedShared and
		// cannot be moved/mutated inside the body, so sharing the
		// underlying value with the caller's binding is safe.
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
			retVal := ret.value
			if fn.Return != nil && fn.Return.Resolved != nil {
				retVal = in.coerceToType(retVal, fn.Return.Resolved)
			}
			return retVal, nil
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

// evalLen implements the `len` built-in. typeck has validated argument count
// and type (one list[T]). For str the codepoint-count rule is also pinned in
// PLAN line 233; we accept str defensively even though typeck currently
// rejects str arguments to len at v0.2 — the dispatch is harmless and lines
// run.go up for a future PLAN tweak without code churn.
func (in *interp) evalLen(e *syntax.CallExpr) (Value, error) {
	if len(e.Args) != 1 {
		return Value{}, fmt.Errorf("internal: len expects 1 arg, got %d at %s", len(e.Args), e.Pos)
	}
	v, err := in.evalExpr(e.Args[0])
	if err != nil {
		return Value{}, err
	}
	if v.Type == nil {
		return Value{}, fmt.Errorf("internal: len argument has nil type at %s", e.Pos)
	}
	switch v.Type.Kind {
	case syntax.TypeList:
		return intVal(int64(len(v.List))), nil
	case syntax.TypeStr:
		// PLAN: count of runes, not bytes. []rune(s) decodes UTF-8.
		return intVal(int64(len([]rune(v.Str)))), nil
	}
	return Value{}, fmt.Errorf("internal: len cannot accept %s at %s", v.Type, e.Pos)
}

// evalClone implements `clone(xs)`. The argument has already been validated by
// typeck (composite, exactly one) and by the borrow checker (the receiver is a
// shared borrow — caller retains ownership). The runtime is purely a fresh
// deep copy via copyValue.
func (in *interp) evalClone(e *syntax.CallExpr) (Value, error) {
	if len(e.Args) != 1 {
		return Value{}, fmt.Errorf("internal: clone expects 1 arg, got %d at %s", len(e.Args), e.Pos)
	}
	v, err := in.evalExpr(e.Args[0])
	if err != nil {
		return Value{}, err
	}
	return copyValue(v), nil
}

// evalPush implements `push(xs, v)`. typeck has already required xs to be a
// mut-bound list ident and v's type to match the list's element type; the
// borrow checker has further verified xs is in Owned state. The runtime
// appends to the named binding's slice in place — we look up the slot, take
// the current list value, append a deep copy of v, and store the new header
// back so the slice grows independently of any other view.
func (in *interp) evalPush(e *syntax.CallExpr) (Value, error) {
	if len(e.Args) != 2 {
		return Value{}, fmt.Errorf("internal: push expects 2 args, got %d at %s", len(e.Args), e.Pos)
	}
	id, ok := e.Args[0].(*syntax.IdentExpr)
	if !ok {
		return Value{}, fmt.Errorf("internal: push first arg must be ident at %s", e.Pos)
	}
	slot, ok := in.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("internal: push undefined name %q at %s", id.Name, e.Pos)
	}
	v, err := in.evalExpr(e.Args[1])
	if err != nil {
		return Value{}, err
	}
	// v0.3: no implicit deep-copy on push — the borrow checker has
	// invalidated v's source binding at the move site if v is a name.
	slot.List = append(slot.List, v)
	return Value{}, nil
}

// ---------------------------------------------------------------------------
// v0.2 composite-data evaluators.
// ---------------------------------------------------------------------------

// evalListLit evaluates `[e1, e2, ...]` to a list Value. Each element is
// deep-copied as it goes into the list so the source bindings stay
// independent of the constructed list (a later mutation of an element source
// — none today, but the contract holds — cannot leak).
func (in *interp) evalListLit(e *syntax.ListLit) (Value, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeList {
		return Value{}, fmt.Errorf("internal: list literal has non-list type %s at %s", t, e.Pos)
	}
	elems := make([]Value, len(e.Elements))
	for i, sub := range e.Elements {
		ev, err := in.evalExpr(sub)
		if err != nil {
			return Value{}, err
		}
		// v0.4: when the literal's element type is a spec, each element is
		// wrapped at this construction point so list[Printable] holds fat
		// pointers regardless of which concrete types the user wrote.
		if t.Element != nil {
			ev = in.coerceToType(ev, t.Element)
		}
		elems[i] = ev
	}
	return Value{Type: t, List: elems}, nil
}

// evalTupleLit evaluates `(e1, e2, ...)`. The tuple length is fixed at parse
// time; element values are deep-copied as they enter the tuple so any
// composite element is independent of its source binding.
func (in *interp) evalTupleLit(e *syntax.TupleLit) (Value, error) {
	elems := make([]Value, len(e.Elements))
	for i, sub := range e.Elements {
		ev, err := in.evalExpr(sub)
		if err != nil {
			return Value{}, err
		}
		// v0.3: no implicit deep-copy at tuple-literal construction.
		elems[i] = ev
	}
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeTuple {
		return Value{}, fmt.Errorf("internal: tuple literal has non-tuple type %s at %s", t, e.Pos)
	}
	return Value{Type: t, Tuple: elems}, nil
}

// evalStructLit evaluates `Name { f1: v1, f2: v2 }`. Field order in the
// runtime Value follows declaration order (PLAN-pinned for print
// determinism), regardless of the order the user wrote field initialisers.
// typeck has already validated completeness and uniqueness.
func (in *interp) evalStructLit(e *syntax.StructLit) (Value, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeStruct {
		return Value{}, fmt.Errorf("internal: struct literal has non-struct type %s at %s", t, e.Pos)
	}
	// Walk the user's FieldInits, but write into a slice indexed by the
	// declared field order so print order stays deterministic. typeck
	// guarantees every declared field appears exactly once.
	values := make([]Value, len(t.Fields))
	provided := make([]bool, len(t.Fields))
	for _, init := range e.Fields {
		idx := -1
		for i, f := range t.Fields {
			if f.Name == init.Name {
				idx = i
				break
			}
		}
		if idx == -1 {
			return Value{}, fmt.Errorf("internal: struct %q has no field %q at %s", t.Name, init.Name, init.Pos)
		}
		v, err := in.evalExpr(init.Value)
		if err != nil {
			return Value{}, err
		}
		// v0.4: coerce when the field's declared type is a spec (or
		// composite containing a spec). For other field types this is a
		// no-op — coerceToType returns v unchanged.
		v = in.coerceToType(v, t.Fields[idx].Type)
		values[idx] = v
		provided[idx] = true
	}
	for i, ok := range provided {
		if !ok {
			return Value{}, fmt.Errorf("internal: struct %q literal missing field %q at %s", t.Name, t.Fields[i].Name, e.Pos)
		}
	}
	return structVal(t, values), nil
}

// evalIndex evaluates `xs[i]`. List indexing returns a deep copy of the
// element so a later mutation of the index target cannot leak into the
// source list. String indexing returns a rune Value (Unicode codepoint at
// position i over the rune-decoded string). Out-of-range indices are
// runtime errors — typeck cannot prove bounds at v0.2.
func (in *interp) evalIndex(e *syntax.IndexExpr) (Value, error) {
	rv, err := in.evalExpr(e.Receiver)
	if err != nil {
		return Value{}, err
	}
	iv, err := in.evalExpr(e.Index)
	if err != nil {
		return Value{}, err
	}
	idx := iv.Int
	if rv.Type == nil {
		return Value{}, fmt.Errorf("internal: index receiver has nil type at %s", e.Pos)
	}
	switch rv.Type.Kind {
	case syntax.TypeList:
		n := int64(len(rv.List))
		if idx < 0 || idx >= n {
			return Value{}, fmt.Errorf("runtime error at %s: list index %d out of range [0..%d)", e.Pos, idx, n)
		}
		// v0.3: index read aliases the element rather than deep-copying.
		return rv.List[idx], nil
	case syntax.TypeStr:
		runes := []rune(rv.Str)
		n := int64(len(runes))
		if idx < 0 || idx >= n {
			return Value{}, fmt.Errorf("runtime error at %s: string index %d out of range [0..%d)", e.Pos, idx, n)
		}
		return runeVal(int64(runes[idx])), nil
	}
	return Value{}, fmt.Errorf("internal: cannot index %s at %s", rv.Type, e.Pos)
}

// evalSlice evaluates list-slicing forms: `xs[lo..hi]`, `xs[..hi]`,
// `xs[lo..]`, `xs[..]`, `xs[lo..=hi]`. The result is a NEW list that
// deep-copies the selected range so the source list is unaffected by later
// mutations of the slice (and vice-versa). String slicing is rejected by
// typeck so this path only ever sees lists.
func (in *interp) evalSlice(e *syntax.SliceExpr) (Value, error) {
	rv, err := in.evalExpr(e.Receiver)
	if err != nil {
		return Value{}, err
	}
	if rv.Type == nil || rv.Type.Kind != syntax.TypeList {
		return Value{}, fmt.Errorf("internal: cannot slice %s at %s", rv.Type, e.Pos)
	}
	n := int64(len(rv.List))
	lo := int64(0)
	hi := n
	if e.Low != nil {
		v, err := in.evalExpr(e.Low)
		if err != nil {
			return Value{}, err
		}
		lo = v.Int
	}
	if e.High != nil {
		v, err := in.evalExpr(e.High)
		if err != nil {
			return Value{}, err
		}
		hi = v.Int
		if e.Inclusive {
			hi++
		}
	} else if e.Inclusive {
		// `xs[lo..=]` is a parse error (the parser requires `=`'s rhs);
		// reaching here would be an internal bug.
		return Value{}, fmt.Errorf("internal: inclusive slice without high bound at %s", e.Pos)
	}
	if lo < 0 || hi > n || lo > hi {
		return Value{}, fmt.Errorf("runtime error at %s: slice [%d..%d] out of range [0..%d]", e.Pos, lo, hi, n)
	}
	out := make([]Value, hi-lo)
	for i := lo; i < hi; i++ {
		// v0.3: slice copies the OUTER list header but aliases each
		// element. Primitives are value-copied by Go assignment anyway.
		out[i-lo] = rv.List[i]
	}
	// Reuse the receiver's list type so the constructed Value's Type pointer
	// matches the receiver's (consistent with the rest of the interpreter's
	// "return the same list[T] *Type" contract).
	return Value{Type: rv.Type, List: out}, nil
}

// evalFieldAccess evaluates `receiver.field`. Three paths:
//
//  1. typeck has stashed a lowered EnumLit (bare-variant enum construction
//     such as `Token.Eof`) — evaluate that.
//  2. Receiver is a bare IdentExpr naming a known enum type — produce the
//     variant Value via the enum table. typeck has validated the variant.
//  3. Otherwise the receiver is a struct value; look up the field by name
//     in the struct's declared field order and return the field value.
func (in *interp) evalFieldAccess(e *syntax.FieldAccessExpr) (Value, error) {
	if e.Lowered != nil {
		return in.evalEnumLit(e.Lowered)
	}
	if id, ok := e.Receiver.(*syntax.IdentExpr); ok {
		if en, isEnum := in.enums[id.Name]; isEnum {
			for i, v := range en.Variants {
				if v == e.FieldName {
					return enumVal(en, i, v, nil), nil
				}
			}
			return Value{}, fmt.Errorf("internal: enum %q has no variant %q at %s", id.Name, e.FieldName, e.NamePos)
		}
	}
	rv, err := in.evalExpr(e.Receiver)
	if err != nil {
		return Value{}, err
	}
	if rv.Type == nil || rv.Type.Kind != syntax.TypeStruct {
		return Value{}, fmt.Errorf("internal: field access on non-struct %s at %s", rv.Type, e.Pos)
	}
	for i, f := range rv.Type.Fields {
		if f.Name == e.FieldName {
			// v0.3: field read aliases the field rather than deep-copying.
			return rv.Fields[i], nil
		}
	}
	return Value{}, fmt.Errorf("internal: struct %q has no field %q at %s", rv.Type.Name, e.FieldName, e.NamePos)
}

// ---------------------------------------------------------------------------
// match.
// ---------------------------------------------------------------------------

// execMatch evaluates a match statement. PLAN-pinned semantics:
//   - arms tested top-to-bottom, first match wins
//   - guards evaluate against pattern bindings; on false, fall through
//   - if no arm matches, the statement is a runtime error (no silent
//     fall-through, per the tenth-man revision in PLAN.md)
//
// Each arm runs in a fresh frame populated with the pattern's bindings; the
// body itself is a Block whose execBlock pushes another frame, so an arm
// body is free to redeclare a name without clobbering the pattern binding.
func (in *interp) execMatch(s *syntax.MatchStmt) error {
	subj, err := in.evalExpr(s.Subject)
	if err != nil {
		return err
	}
	for i := range s.Arms {
		arm := &s.Arms[i]
		in.pushFrame()
		bound, perr := in.bindPattern(arm.Pattern, subj)
		if perr != nil {
			in.popFrame()
			return perr
		}
		if !bound {
			in.popFrame()
			continue
		}
		if arm.Guard != nil {
			gv, err := in.evalExpr(arm.Guard)
			if err != nil {
				in.popFrame()
				return err
			}
			if !gv.Bool {
				in.popFrame()
				continue
			}
		}
		err := in.execBlock(arm.Body)
		in.popFrame()
		return err
	}
	return fmt.Errorf("match: no arm matched at %s", s.Pos)
}

// bindPattern attempts to match pat against v, recording any bindings in the
// current frame. Returns (matched, err). A pattern that fails to match
// without a runtime error returns (false, nil); typeck rules out shape
// mismatches (e.g. tuple-pat against non-tuple), so this path only fires on
// value-disagreement (literal mismatch, enum variant mismatch, ...).
func (in *interp) bindPattern(pat syntax.Pattern, v Value) (bool, error) {
	switch p := pat.(type) {
	case *syntax.WildcardPat:
		return true, nil
	case *syntax.BindPat:
		// v0.3: bind shares the value. The borrow checker has flagged
		// the scrutinee as Moved at the BindPat site, so the user
		// can't observe the alias.
		if err := in.declare(p.Name, v); err != nil {
			return false, err
		}
		return true, nil
	case *syntax.LitPat:
		// Evaluate the literal expression in the current scope; typeck has
		// constrained it to a primitive literal (optionally negated).
		lv, err := in.evalExpr(p.Lit)
		if err != nil {
			return false, err
		}
		return litEq(lv, v), nil
	case *syntax.TuplePat:
		if v.Type == nil || v.Type.Kind != syntax.TypeTuple {
			return false, fmt.Errorf("internal: tuple pattern against non-tuple at %s", p.Pos)
		}
		if len(p.Elements) != len(v.Tuple) {
			return false, nil
		}
		for i, sub := range p.Elements {
			ok, err := in.bindPattern(sub, v.Tuple[i])
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case *syntax.StructPat:
		if v.Type == nil || v.Type.Kind != syntax.TypeStruct {
			return false, fmt.Errorf("internal: struct pattern against non-struct at %s", p.Pos)
		}
		// typeck has validated that each named field exists on the struct
		// and that all declared fields are covered when `..` is absent.
		// Field order in the pattern doesn't have to match decl order — we
		// look each field up by name. The struct value's Fields slice is
		// ordered by declaration so we use the type's Fields[i].Name to
		// find the right slot.
		for _, f := range p.Fields {
			idx := -1
			for i, df := range v.Type.Fields {
				if df.Name == f.Name {
					idx = i
					break
				}
			}
			if idx == -1 {
				return false, fmt.Errorf("internal: struct %q has no field %q at %s", v.Type.Name, f.Name, f.Pos)
			}
			ok, err := in.bindPattern(f.Pattern, v.Fields[idx])
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case *syntax.EnumPat:
		if v.Type == nil || v.Type.Kind != syntax.TypeEnum {
			return false, fmt.Errorf("internal: enum pattern against non-enum at %s", p.Pos)
		}
		// typeck rejects mismatched type names; here we compare variants.
		if v.VariantName != p.VariantName {
			return false, nil
		}
		// v0.4: bare patterns short-circuit; payload patterns recurse over the
		// runtime payload slice. typeck has already validated arity, so a
		// mismatch here would be an internal error.
		if len(p.Payload) == 0 {
			return true, nil
		}
		if len(v.Payload) != len(p.Payload) {
			return false, fmt.Errorf("internal: enum pattern arity mismatch (%d vs %d) at %s", len(p.Payload), len(v.Payload), p.Pos)
		}
		for i, sub := range p.Payload {
			ok, err := in.bindPattern(sub, v.Payload[i])
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("internal: unhandled pattern %T at %s", pat, pat.PatPos())
}

// litEq compares a literal-pattern value against the scrutinee using v0.1
// primitive equality semantics, plus byte/rune compared by codepoint. typeck
// ensures the types match, so we just dispatch on Type.
func litEq(lit, v Value) bool {
	if lit.Type == nil || v.Type == nil {
		return false
	}
	switch lit.Type {
	case syntax.TInt():
		return lit.Int == v.Int
	case syntax.TFloat():
		return lit.Float == v.Float
	case syntax.TBool():
		return lit.Bool == v.Bool
	case syntax.TStr():
		return lit.Str == v.Str
	case syntax.TByte(), syntax.TRune():
		return lit.Int == v.Int
	}
	return false
}

// ---------------------------------------------------------------------------
// v0.4 — enum payloads, method calls, vtable dispatch, composite ==.
// ---------------------------------------------------------------------------

// evalEnumLit evaluates a typeck-lowered EnumLit. The lowering walks any
// payload arguments in order and produces a runtime enum value with the
// variant tag and the per-position payload slice. typeck has validated arity
// and per-position type so this path only needs to evaluate sub-expressions.
func (in *interp) evalEnumLit(e *syntax.EnumLit) (Value, error) {
	en := e.Type()
	if en == nil || en.Kind != syntax.TypeEnum {
		return Value{}, fmt.Errorf("internal: enum literal has non-enum type %s at %s", en, e.Pos)
	}
	idx := -1
	for i, v := range en.Variants {
		if v == e.Variant {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Value{}, fmt.Errorf("internal: enum %q has no variant %q at %s", en.Name, e.Variant, e.VariantPos)
	}
	if len(e.Payload) == 0 {
		return enumVal(en, idx, e.Variant, nil), nil
	}
	payload := make([]Value, len(e.Payload))
	// The variant's declared payload types drive any spec coercion at element
	// position so a `Token.Wrap(c)` where `Wrap(Printable)` was declared
	// widens the concrete arg to a fat pointer.
	declared := en.VariantPayloads[idx]
	for i, arg := range e.Payload {
		v, err := in.evalExpr(arg)
		if err != nil {
			return Value{}, err
		}
		if i < len(declared) {
			v = in.coerceToType(v, declared[i])
		}
		payload[i] = v
	}
	return enumVal(en, idx, e.Variant, payload), nil
}

// evalMethodCall is the runtime dispatcher for `receiver.method(args)`.
// Resolution mirrors typeck's precedence:
//   1. typeck has lowered the call to an EnumLit (`Token.Ident("foo")`) →
//      delegate to evalEnumLit.
//   2. typeck has lowered the call to a list builtin (`xs.push(v)`) →
//      delegate to evalCall on the synthetic CallExpr.
//   3. Receiver is a spec-typed fat pointer → vtable dispatch via
//      ConcreteType + spec name.
//   4. Receiver is a struct/enum value → dispatch by inherent first, then
//      unique spec impl by method name.
func (in *interp) evalMethodCall(e *syntax.MethodCallExpr) (Value, error) {
	if e.Lowered != nil {
		return in.evalEnumLit(e.Lowered)
	}
	if e.LoweredCall != nil {
		return in.evalCall(e.LoweredCall)
	}
	rv, err := in.evalExpr(e.Receiver)
	if err != nil {
		return Value{}, err
	}
	if rv.Type == nil {
		return Value{}, fmt.Errorf("internal: method call receiver has nil type at %s", e.Pos)
	}
	// Spec-typed receiver — vtable dispatch.
	if rv.Type.Kind == syntax.TypeSpec {
		return in.dispatchSpec(e, rv)
	}
	// Concrete receiver — typeck has narrowed to struct or enum.
	return in.dispatchConcrete(e, rv)
}

// dispatchSpec routes a method call through a spec-typed fat pointer's
// (ConcreteType, Spec) pair. Resolution: concrete impl override > spec
// default > NotImplemented panic.
func (in *interp) dispatchSpec(e *syntax.MethodCallExpr, rv Value) (Value, error) {
	specName := rv.Type.Name
	concreteType := rv.ConcreteType
	if rv.Data == nil {
		return Value{}, fmt.Errorf("internal: spec value has nil data at %s", e.Pos)
	}
	this := *rv.Data
	fn, sm := in.resolveSpecMethod(concreteType, specName, e.Method)
	if fn != nil {
		return in.callMethodFn(e, fn, this)
	}
	if sm != nil {
		return in.callSpecDefault(e, sm, this)
	}
	return Value{}, fmt.Errorf("not implemented: %s.%s (declared in spec %s at %s)",
		concreteType, e.Method, specName, e.MethodPos)
}

// resolveSpecMethod looks up the (Type, Spec) override; returns the impl's
// FnDecl if one is supplied, the spec's default method AST if not. Both nil
// means the method is signature-only with no override — NotImplemented.
func (in *interp) resolveSpecMethod(typeName, specName, methodName string) (*syntax.FnDecl, *syntax.SpecMethod) {
	key := implKey{typeName: typeName, specName: specName}
	if impl, ok := in.specImpls[key]; ok {
		if fn, ok := impl[methodName]; ok {
			return fn, nil
		}
	}
	// Fall through to spec default body if present.
	if sd, ok := in.specDecls[specName]; ok {
		for _, m := range sd.Methods {
			if m.Name == methodName {
				if m.Body != nil {
					return nil, m
				}
				return nil, nil
			}
		}
	}
	return nil, nil
}

// dispatchConcrete routes a method call against a struct- or enum-typed
// receiver. Inherent methods take precedence over spec impls; if no inherent
// exists, the unique spec impl exposing the method wins. typeck has already
// rejected ambiguity, so the first matching spec impl is sufficient.
func (in *interp) dispatchConcrete(e *syntax.MethodCallExpr, rv Value) (Value, error) {
	typeName := rv.Type.Name
	// 1. Inherent.
	if methods, ok := in.inherentImpls[typeName]; ok {
		if fn, ok := methods[e.Method]; ok {
			return in.callMethodFn(e, fn, rv)
		}
	}
	// 2. Spec impls. Walk every (typeName, specName) in the spec impl table to
	// find one that exposes the method (override or via default). typeck has
	// rejected ambiguity so we take the first match.
	for key := range in.specImpls {
		if key.typeName != typeName {
			continue
		}
		fn, sm := in.resolveSpecMethod(typeName, key.specName, e.Method)
		if fn != nil {
			return in.callMethodFn(e, fn, rv)
		}
		if sm != nil {
			return in.callSpecDefault(e, sm, rv)
		}
		// (Type, Spec) impl exists but the method has no override or default.
		// Only return NotImplemented if the spec actually declares this
		// method — otherwise keep searching (other specs might have it).
		if sd, ok := in.specDecls[key.specName]; ok {
			for _, m := range sd.Methods {
				if m.Name == e.Method {
					return Value{}, fmt.Errorf("not implemented: %s.%s (declared in spec %s at %s)",
						typeName, e.Method, key.specName, e.MethodPos)
				}
			}
		}
	}
	return Value{}, fmt.Errorf("internal: method %q not resolvable on %s at %s", e.Method, typeName, e.MethodPos)
}

// callMethodFn binds `this` plus declared params, walks the impl method's
// body, and catches the return sentinel.
func (in *interp) callMethodFn(e *syntax.MethodCallExpr, fn *syntax.FnDecl, this Value) (Value, error) {
	// Evaluate args in caller scope first.
	args := make([]Value, len(e.Args))
	for i, a := range e.Args {
		v, err := in.evalExpr(a)
		if err != nil {
			return Value{}, err
		}
		// Coerce to declared param type so spec-typed params widen.
		if i < len(fn.Params) && fn.Params[i].Type != nil && fn.Params[i].Type.Resolved != nil {
			v = in.coerceToType(v, fn.Params[i].Type.Resolved)
		}
		args[i] = v
	}
	// Method bodies are like fn calls — a fresh frame stack rooted at one
	// frame. Save and restore.
	savedStack := in.stack
	in.stack = []*frame{newFrame()}
	defer func() { in.stack = savedStack }()
	if err := in.declare("this", this); err != nil {
		return Value{}, err
	}
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
			retVal := ret.value
			if fn.Return != nil && fn.Return.Resolved != nil {
				retVal = in.coerceToType(retVal, fn.Return.Resolved)
			}
			return retVal, nil
		}
		if errors.Is(err, errBreak) || errors.Is(err, errContinue) {
			return Value{}, fmt.Errorf("internal: %v escaped method %s", err, fn.Name)
		}
		return Value{}, err
	}
	if e.Type() != nil && e.Type() != syntax.TVoid() {
		return Value{}, fmt.Errorf("method %q ended without return at %s", fn.Name, fn.Pos)
	}
	return Value{}, nil
}

// callSpecDefault is the SpecMethod analogue of callMethodFn — spec defaults
// store their body on a SpecMethod, not a FnDecl, but the receiver-binding
// machinery is otherwise identical.
func (in *interp) callSpecDefault(e *syntax.MethodCallExpr, sm *syntax.SpecMethod, this Value) (Value, error) {
	args := make([]Value, len(e.Args))
	for i, a := range e.Args {
		v, err := in.evalExpr(a)
		if err != nil {
			return Value{}, err
		}
		if i < len(sm.Params) && sm.Params[i].Type != nil && sm.Params[i].Type.Resolved != nil {
			v = in.coerceToType(v, sm.Params[i].Type.Resolved)
		}
		args[i] = v
	}
	savedStack := in.stack
	in.stack = []*frame{newFrame()}
	defer func() { in.stack = savedStack }()
	if err := in.declare("this", this); err != nil {
		return Value{}, err
	}
	for i, p := range sm.Params {
		if err := in.declare(p.Name, args[i]); err != nil {
			return Value{}, err
		}
	}
	for _, st := range sm.Body.Statements {
		err := in.execStmt(st)
		if err == nil {
			continue
		}
		var ret *errReturn
		if errors.As(err, &ret) {
			retVal := ret.value
			if sm.Return != nil && sm.Return.Resolved != nil {
				retVal = in.coerceToType(retVal, sm.Return.Resolved)
			}
			return retVal, nil
		}
		if errors.Is(err, errBreak) || errors.Is(err, errContinue) {
			return Value{}, fmt.Errorf("internal: %v escaped spec default %s", err, sm.Name)
		}
		return Value{}, err
	}
	if e.Type() != nil && e.Type() != syntax.TVoid() {
		return Value{}, fmt.Errorf("spec default %q ended without return at %s", sm.Name, sm.Pos)
	}
	return Value{}, nil
}

// coerceToType narrows the runtime widening rule that typeck encodes via
// assignableTo: a concrete struct / enum value flowing into a spec-typed
// slot is wrapped in a fat pointer; list[Spec] / tuple[..., Spec] / struct
// fields of spec type recurse element-wise. Same shape on both sides → no-op.
//
// The wrap point matters because spec method dispatch reads ConcreteType off
// the wrapper; without coercion at let / arg / return / list-elem / struct-
// field sites, vtable lookup would fail.
func (in *interp) coerceToType(v Value, target *syntax.Type) Value {
	if target == nil || v.Type == nil {
		return v
	}
	switch target.Kind {
	case syntax.TypeSpec:
		if v.Type.Kind == syntax.TypeSpec {
			// Already wrapped — typically the same spec; assume so since
			// typeck rejects spec-to-different-spec.
			return v
		}
		// Wrap concrete value.
		return specVal(target, v)
	case syntax.TypeList:
		if v.Type.Kind != syntax.TypeList || target.Element == nil {
			return v
		}
		// Only descend when the target element type differs (e.g. Spec) —
		// recursion into pure-primitive lists would be an unnecessary copy.
		if target.Element.Equals(v.Type.Element) && target.Element.Kind != syntax.TypeSpec {
			return v
		}
		out := make([]Value, len(v.List))
		for i, e := range v.List {
			out[i] = in.coerceToType(e, target.Element)
		}
		return Value{Type: target, List: out}
	case syntax.TypeTuple:
		if v.Type.Kind != syntax.TypeTuple || len(target.Tuple) != len(v.Tuple) {
			return v
		}
		needs := false
		for _, t := range target.Tuple {
			if t != nil && t.Kind == syntax.TypeSpec {
				needs = true
				break
			}
		}
		if !needs {
			return v
		}
		out := make([]Value, len(v.Tuple))
		for i, e := range v.Tuple {
			out[i] = in.coerceToType(e, target.Tuple[i])
		}
		return Value{Type: target, Tuple: out}
	}
	return v
}

// eqValues recursively compares two runtime values structurally. typeck has
// validated that comparable shapes match; this walker handles primitives via
// the existing valueEq plus composite kinds (list, tuple, struct, enum with
// payload). Spec-typed bindings on either side are typeck-rejected; defensive
// here so a slip-through fails loudly.
func eqValues(a, b Value) (bool, error) {
	if a.Type == nil || b.Type == nil {
		return false, fmt.Errorf("internal: eqValues on untyped value")
	}
	if a.Type.Kind == syntax.TypeSpec || b.Type.Kind == syntax.TypeSpec {
		return false, fmt.Errorf("internal: spec == reached runtime")
	}
	switch a.Type.Kind {
	case syntax.TypeInt, syntax.TypeFloat, syntax.TypeBool, syntax.TypeStr,
		syntax.TypeByte, syntax.TypeRune:
		return valueEq(a, b), nil
	case syntax.TypeList:
		if len(a.List) != len(b.List) {
			return false, nil
		}
		for i := range a.List {
			eq, err := eqValues(a.List[i], b.List[i])
			if err != nil {
				return false, err
			}
			if !eq {
				return false, nil
			}
		}
		return true, nil
	case syntax.TypeTuple:
		if len(a.Tuple) != len(b.Tuple) {
			return false, nil
		}
		for i := range a.Tuple {
			eq, err := eqValues(a.Tuple[i], b.Tuple[i])
			if err != nil {
				return false, err
			}
			if !eq {
				return false, nil
			}
		}
		return true, nil
	case syntax.TypeStruct:
		// typeck guarantees same struct type → same field count.
		if len(a.Fields) != len(b.Fields) {
			return false, nil
		}
		for i := range a.Fields {
			eq, err := eqValues(a.Fields[i], b.Fields[i])
			if err != nil {
				return false, err
			}
			if !eq {
				return false, nil
			}
		}
		return true, nil
	case syntax.TypeEnum:
		if a.VariantIndex != b.VariantIndex {
			return false, nil
		}
		if len(a.Payload) != len(b.Payload) {
			return false, nil
		}
		for i := range a.Payload {
			eq, err := eqValues(a.Payload[i], b.Payload[i])
			if err != nil {
				return false, err
			}
			if !eq {
				return false, nil
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("internal: eqValues unhandled kind %d", int(a.Type.Kind))
}
