package emit

// This file carries the Phase 1f U3 additions to the C backend: lowering an
// f-string f"…{expr}…" (GRAMMAR group 5) to runtime-built text. A plain text part
// becomes a C string literal; a hole `{expr}` renders through the value's built-in
// `display()` and a `{expr:spec}` hole through its `Format` impl (fmt.c), with the
// rendering picked by the hole's static type — the per-type Format protocol of
// docs/format.md, realized as a compiler-recognized lowering for the MVP's scalar
// and str built-ins. The parts join through zrt_str_concat.
//
// The whole surface is additive and gated on f-string use (needsFormat): a program
// with no f-string registers nothing here, so its emitted C is byte-identical.

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// programUsesFormat reports whether the program lowers an f-string that needs the
// runtime — one with a hole, or with more than one part to join. A single text-only
// (or empty) f-string lowers to a bare C literal and needs no runtime, so it does not
// trip the gate.
func (e *emitter) programUsesFormat() bool {
	for node := range e.info.ExprTypes {
		if fstrNeedsRuntime(node) {
			return true
		}
	}
	return false
}

// fstrNeedsRuntime reports whether an f-string node lowers to runtime calls (a hole
// to render, or several parts to concatenate).
func fstrNeedsRuntime(node ast.Expr) bool {
	fs, ok := node.(*ast.FStr)
	if !ok {
		return false
	}
	if len(fs.Parts) > 1 {
		return true
	}
	for i := range fs.Parts {
		if fs.Parts[i].Expr != nil {
			return true
		}
	}
	return false
}

// fstrExpr lowers an f-string to a `const char*` value: each part renders to a
// fragment and the fragments fold right through zrt_str_concat. A lone text part is
// its literal (no concat); an empty f-string is the empty literal.
func (e *emitter) fstrExpr(n *ast.FStr) string {
	frags := make([]string, 0, len(n.Parts))
	for i := range n.Parts {
		frags = append(frags, e.fstrPart(n.Parts[i]))
	}
	if len(frags) == 0 {
		return `""`
	}
	out := frags[len(frags)-1]
	for i := len(frags) - 2; i >= 0; i-- {
		out = fmt.Sprintf("zrt_str_concat(%s, %s)", frags[i], out)
	}
	return out
}

// fstrPart renders one f-string part to a `const char*` fragment: a text part is a C
// string literal; a hole renders its expression through the Format `:spec` impl or
// its `display()` (with a `!s`/`!r`/`!a` conversion picking the view).
func (e *emitter) fstrPart(p ast.FStrPart) string {
	if p.Expr == nil {
		return cString(p.Text)
	}
	if p.Debug {
		// `{expr=}` self-documentation (reprint the source text, then '=', then the
		// value) is deferred to a later iteration; reject it rather than silently drop
		// the '=' prefix (DESIGN-1f §6 lists it as delay-able).
		e.diags.Add(p.Expr.Span(), "f-string self-documenting '{expr=}' is not supported yet")
		return `""`
	}
	t := e.cur.ExprType(e.info, p.Expr)
	code := e.expr(p.Expr)
	if p.HasSpec {
		return e.formatCall(p, t, code)
	}
	return e.displayCall(p.Expr, t, code)
}

// displayCall renders a hole's value through the built-in `display()` for its static
// type (str is its own display). The `!r`/`!a` conversions (developer debug / ASCII)
// are MVP-aliased to display; only `!s` is display proper. A type with no built-in
// display yields a diagnostic.
func (e *emitter) displayCall(x ast.Expr, t sema.Type, code string) string {
	switch numericDisplay(t) {
	case dispStr:
		return code
	case dispBool:
		return fmt.Sprintf("zrt_display_bool(%s)", code)
	case dispInt:
		return fmt.Sprintf("zrt_display_int((int64_t)(%s))", code)
	case dispUint:
		return fmt.Sprintf("zrt_display_uint((uint64_t)(%s))", code)
	case dispFloat:
		return fmt.Sprintf("zrt_display_float((double)(%s))", code)
	}
	e.diags.Add(x.Span(), "f-string cannot render a value of type %s yet (display() is built in for the scalar types and str)", t)
	return `""`
}

// formatCall renders a hole's value through its `Format` `:spec` impl for its static
// type. Numbers read the numeric spec, str the string spec, and bool formats its
// display text through the string spec. A type with no built-in Format yields a
// diagnostic.
func (e *emitter) formatCall(p ast.FStrPart, t sema.Type, code string) string {
	spec := cString(p.Spec)
	switch numericDisplay(t) {
	case dispStr:
		return fmt.Sprintf("zrt_fmt_str(%s, %s)", code, spec)
	case dispBool:
		return fmt.Sprintf("zrt_fmt_str(zrt_display_bool(%s), %s)", code, spec)
	case dispInt:
		return fmt.Sprintf("zrt_fmt_int((int64_t)(%s), %s)", code, spec)
	case dispUint:
		return fmt.Sprintf("zrt_fmt_uint((uint64_t)(%s), %s)", code, spec)
	case dispFloat:
		return fmt.Sprintf("zrt_fmt_float((double)(%s), %s)", code, spec)
	}
	e.diags.Add(p.Expr.Span(), "f-string cannot format a value of type %s yet (Format is built in for the scalar types and str)", t)
	return `""`
}

// display kinds an f-string hole renders through.
type dispKind int

const (
	dispNone dispKind = iota
	dispStr
	dispBool
	dispInt
	dispUint
	dispFloat
)

// numericDisplay classifies a scalar/str type into the display/format family it uses.
// A signed integer (int, i8..i64, rune, byte) is dispInt; an unsigned integer (uint,
// u8..u64) is dispUint; a float (float, f32, f64) is dispFloat.
func numericDisplay(t sema.Type) dispKind {
	if t == nil {
		return dispNone
	}
	switch t.Kind() {
	case types.KStr:
		return dispStr
	case types.KBool:
		return dispBool
	case types.KInt, types.KRune, types.KByte:
		return dispInt
	case types.KUint:
		return dispUint
	case types.KFloat:
		return dispFloat
	case types.KFixedInt:
		if f, ok := t.(*types.Fixed); ok {
			switch {
			case f.Float:
				return dispFloat
			case f.Signed:
				return dispInt
			default:
				return dispUint
			}
		}
	}
	return dispNone
}
