// Package emit is the Phase 0 C backend. It lowers a type-checked ast.File to a
// single self-contained C translation unit — the shortest path to a binary, and
// the thesis the roadmap's Phase 0 proves (lex -> parse -> sema -> emit C -> cc).
//
// Every Zerg name is emitted with a 'zg_' prefix so it cannot collide with a C
// keyword or the C runtime; the Zerg 'main' becomes 'zg_main', wrapped by a real C
// 'main'. The type universe maps int->int64_t, float->double, bool->bool (stdbool),
// str->const char*. There is no runtime yet: strings are C string literals and
// nothing is refcounted (Phase 0 leaks, per the roadmap).
package emit

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Manifest records which C-runtime features a program's emitted code uses, so
// the driver can select how to compile it. An empty Manifest (NeedsRuntime
// false) means a value-only program: the emitted C neither includes nor links
// the runtime and is byte-identical to the Phase 0 backend. Finer-grained flags
// (UsesRef, UsesUnwind, …) can be added here as later slices land.
type Manifest struct {
	// NeedsRuntime reports whether the emitted C includes "zergrt.h" and must be
	// linked against the src/runtime tree.
	NeedsRuntime bool

	// Concurrency reports whether the program uses concurrency (a `spawn`, or a
	// channel value/op). When set, the driver additionally links the scheduler and
	// the per-arch context switch, and the entry runs main under the scheduler
	// (zrt_sched_main). It implies NeedsRuntime. A non-concurrent program leaves it
	// false, links nothing new, and stays byte-identical to the Phase 1d path.
	Concurrency bool

	// NeedsResult reports whether the program lowers a general Result/Either/optional
	// carrier (Phase 1f U0) — a construction, a `?`/`??`/`!`/`?.`/`guard`, or an
	// Either/optional signature. It implies NeedsRuntime (the carriers carry a
	// zrt_err). A program that uses no such value leaves it false, so its emitted C
	// stays byte-identical.
	NeedsResult bool

	// NeedsIO reports whether the program lowers a stdlib `io` write intrinsic
	// (`import "io"`, Phase 1f). It implies NeedsRuntime. The io primitives ride in
	// the always-linked sys.c, so NeedsRuntime is the effective link gate and this
	// flag records the dependency for the driver and later slices. A program that
	// never imports io leaves it false and stays byte-identical.
	NeedsIO bool

	// NeedsFormat reports whether the program lowers an f-string (Phase 1f U3): its
	// parts join through zrt_str_concat and its holes render through the display() /
	// Format helpers in fmt.c. It implies NeedsRuntime (those helpers ride in the
	// always-linked core units). A program with no f-string leaves it false and stays
	// byte-identical.
	NeedsFormat bool

	// NeedsFFI reports whether the program binds a foreign C symbol through an
	// `#[extern("c_symbol")]` function (Phase 1f U5): its emitted C includes the
	// standard headers that declare libc symbols and its link line adds the math
	// library. A program that binds no foreign symbol leaves it false and stays
	// byte-identical.
	NeedsFFI bool
}

// Emit lowers the monomorphized program to C source. It renders each instance in
// prog.Funcs, reading the type overlay from prog.Info, and returns a Manifest
// describing which runtime features the program uses. When the returned
// diagnostics are non-empty the source is empty and must not be compiled.
func Emit(prog *mono.Program) (string, Manifest, []diag.Diagnostic) {
	e := &emitter{prog: prog, info: prog.Info}
	e.program()
	if !e.diags.Empty() {
		return "", Manifest{}, e.diags.Items()
	}
	return e.sb.String(), Manifest{NeedsRuntime: e.needsRuntime, Concurrency: e.concurrency, NeedsResult: e.needsResult, NeedsIO: e.needsIO, NeedsFormat: e.needsFormat, NeedsFFI: e.needsFFI}, nil
}

type emitter struct {
	prog   *mono.Program
	info   *sema.Info
	cur    *mono.Instance // the instance whose body is being rendered (its type overlay)
	sb     strings.Builder
	indent int
	diags  diag.List

	// needsRuntime is set when this program uses a C-runtime feature (Iteration
	// 1: a 'fn main() -> Result[nil]' entry). It drives both the "#include
	// zergrt.h" line and the returned Manifest. Value-only programs leave it
	// false, so their emitted C is byte-identical to Phase 0.
	needsRuntime bool

	// concurrency is set when this program uses `spawn` or a channel (Phase 1e). It
	// drives the returned Manifest's Concurrency flag (the driver then links the
	// scheduler + context switch) and the scheduler entry in cMain, and implies
	// needsRuntime. spawnIdx numbers each `spawn f(args)` site so it shares one
	// generated env struct + trampoline (like deferIdx). Both empty/false for a
	// non-concurrent program, which therefore stays byte-identical to Phase 1d.
	concurrency bool
	spawnIdx    map[*ast.SpawnStmt]int

	// Result/Either/optional carriers (Phase 1f U0). carriers maps a type's spelling
	// to its generated C carrier (a monomorphized tagged struct generalizing the
	// channel recv carrier); needsResult gates their typedefs/helpers and the
	// NeedsResult manifest flag. Both empty/false for a program that uses no such
	// value, which therefore stays byte-identical.
	carriers    map[string]*carrier
	needsResult bool

	// needsIO is set when the program lowers a stdlib `io` write intrinsic (Phase
	// 1f). It drives the NeedsIO manifest flag and implies needsRuntime. False for a
	// program that never imports io, which therefore stays byte-identical.
	needsIO bool

	// needsFormat is set when the program lowers an f-string (Phase 1f U3): its parts
	// join through zrt_str_concat and its holes render through fmt.c's display()/Format
	// helpers. It drives the NeedsFormat manifest flag and implies needsRuntime. False
	// for a program with no f-string, which therefore stays byte-identical.
	needsFormat bool

	// needsFFI is set when the program binds a foreign C symbol through an
	// `#[extern]` function (Phase 1f U5): it drives the NeedsFFI manifest flag and the
	// standard-header includes. False for a program that binds no foreign symbol,
	// which therefore stays byte-identical.
	needsFFI bool

	// Channel state (Phase 1e slice C2). recvIdx numbers the distinct element types a
	// `<-ch` receives, so each gets a stable Result[T] carrier struct (zg_recv_<n>)
	// plus its recv/force helpers; recvElems is those element types in that order.
	// needChanDrop/needChanSenderDrop record whether the program drops any receive-only
	// / send-capable channel handle, gating the two drop thunks so a program that drops
	// neither emits neither. All empty/false for a program with no channels.
	recvIdx            map[string]int
	recvElems          []sema.Type
	needChanDrop       bool
	needChanSenderDrop bool

	// Ref[T] runtime state (Phase 1d iteration 2). refnewIdx numbers the distinct
	// Ref construction element types so each gets a stable zg_refnew_<n> helper;
	// refnewElems is those element types in that order. All empty for a value-only
	// program.
	refnewIdx   map[string]int
	refnewElems []sema.Type

	// Teardown state (Phase 1d iteration 3). drops is the stack of lexical scopes'
	// teardown frames (marks + droppable bindings), driving reverse-order release on
	// every exit path via the runtime cleanup stack. fnMark is the current function
	// root's cleanup mark ("" when the function owns no teardown), the height a
	// `return` unwinds to after copying the value out. deferIdx numbers each
	// `defer f(args)` site so it shares one generated env struct and thunk. All empty
	// for a value-only program, which therefore stays byte-identical to Phase 0.
	drops    []*scope
	fnMark   string
	deferIdx map[*ast.DeferStmt]int

	// Per-function name environment. Zerg allows a binding to shadow an outer
	// name (e.g. 'mut n := n'), but C parameters and the top-level body share one
	// scope, so every local gets a unique C name; 'scopes' maps a source name to
	// the C name currently in effect and 'used' guarantees uniqueness.
	scopes  []map[string]string
	used    map[string]bool
	counter int
}

func (e *emitter) program() {
	main, ok := e.info.Funcs["main"]
	switch {
	case !ok:
		e.diags.Add(token.Span{}, "no 'main' function to build a program")
		return
	case len(main.Params) != 0:
		e.diags.Add(main.Decl.Span(), "'main' must take no parameters in Phase 0")
	case main.Ret != sema.Nil && main.Ret != sema.Int && !isResultNil(main.Ret):
		e.diags.Add(main.Decl.Span(), "'main' must return nil, int, or Result[nil] in Phase 0")
	}

	// A 'Result[nil]' main is the additive runtime-entry path: it pulls in the C
	// runtime (header + link). A program that uses Ref[T] (or any non-POD value)
	// pulls it in too. Every other (value-only) main leaves needsRuntime false, so
	// no include is printed and the C stays byte-identical to Phase 0.
	e.needsRuntime = isResultNil(main.Ret)
	e.prepareRuntime()

	e.line("// Generated by zerg (Phase 0). Do not edit.")
	e.line("#include <stdio.h>")
	e.line("#include <stdint.h>")
	e.line("#include <stdbool.h>")
	e.line("#include <string.h>")
	if e.needsFFI {
		// declarations for the libc symbols an `#[extern]` binding may forward to.
		e.line("#include <math.h>")
		e.line("#include <stdlib.h>")
	}
	if e.needsRuntime {
		e.line("#include \"zergrt.h\"")
	}
	e.blank()

	// specialized nominal types, each before the functions that use it
	for _, ti := range e.prog.Types {
		e.typedef(ti)
	}

	// Result/Either/optional carriers, before any prototype that names one as a
	// return/parameter type (Phase 1f U0). Emits nothing for a program with none.
	e.emitResultTypedefs()

	// '#[dyn]' witness-table struct types, before any prototype that names one
	e.witnessStructs()

	// prototypes first, so declaration order does not constrain calls
	for _, inst := range e.prog.Funcs {
		e.line(e.prototype(inst) + ";")
	}
	e.blank()

	// concrete witness tables, after the impl-method prototypes their slots name
	e.witnessGlobals()

	// Ref[T] copy/drop and allocation helpers (Phase 1d), after the struct typedefs
	// they reference and before the function bodies that call them. Emits nothing
	// for a value-only program.
	e.emitRefHelpers()

	for _, inst := range e.prog.Funcs {
		e.function(inst)
		e.blank()
	}

	e.cMain(main)
}

// typedef emits a specialized nominal type: a struct as a plain C struct, or an
// enum as a tagged union (an integer tag plus a union of the payload variants).
func (e *emitter) typedef(ti *mono.TypeInstance) {
	if ti.IsEnum {
		e.enumTypedef(ti)
		return
	}
	e.line("typedef struct {")
	e.indent++
	for _, f := range ti.Fields {
		e.line(e.ctype(f.Type) + " zg_" + f.Name + ";")
	}
	e.indent--
	e.line("} " + ti.Mangled + ";")
	e.blank()
}

// enumTypedef emits a specialized enum as a tagged union: an 'int32_t tag' holding
// the variant's zero-based discriminant, and, when any variant carries a payload, a
// union 'u' of one anonymous struct per payload variant whose fields are 'f0, f1,
// …'. A wholly fieldless enum emits the tag alone.
func (e *emitter) enumTypedef(ti *mono.TypeInstance) {
	e.line("typedef struct {")
	e.indent++
	e.line("int32_t tag;")
	if enumHasPayload(ti) {
		e.line("union {")
		e.indent++
		for _, v := range ti.Variants {
			if len(v.Payload) == 0 {
				continue
			}
			var b strings.Builder
			b.WriteString("struct { ")
			for i, pt := range v.Payload {
				fmt.Fprintf(&b, "%s f%d; ", e.ctype(pt), i)
			}
			b.WriteString("} " + v.Name + ";")
			e.line(b.String())
		}
		e.indent--
		e.line("} u;")
	}
	e.indent--
	e.line("} " + ti.Mangled + ";")
	e.blank()
}

// enumHasPayload reports whether any variant of an enum instance carries a payload,
// so a wholly fieldless enum omits the (empty, and in C ill-formed) union.
func enumHasPayload(ti *mono.TypeInstance) bool {
	for _, v := range ti.Variants {
		if len(v.Payload) > 0 {
			return true
		}
	}
	return false
}

// paramList renders a C parameter list ("void" when empty), formatting each of
// the n parameters with render.
func paramList(n int, render func(i int) string) string {
	if n == 0 {
		return "void"
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(render(i))
	}
	return b.String()
}

// paramNames renders each of n parameters with render, returning them as a slice.
func paramNames(n int, render func(i int) string) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = render(i)
	}
	return out
}

// joinParams joins a receiver (0 or 1 entry) and the rest of the parameters into a
// C parameter list, spelling an empty list "void" — so a free function with no
// parameters is byte-identical to the pre-method backend.
func joinParams(recv, rest []string) string {
	all := append(recv, rest...)
	if len(all) == 0 {
		return "void"
	}
	return strings.Join(all, ", ")
}

// prototype renders a forward declaration with parameter types only, using the
// instance's mangled C name and its concrete (specialized) signature.
func (e *emitter) prototype(inst *mono.Instance) string {
	if inst.Dyn || inst.RecvErased {
		return e.erasedSignature(inst, false)
	}
	if inst.Recv != nil {
		var b strings.Builder
		b.WriteString(e.ctype(inst.Recv))
		for i := range inst.Params {
			b.WriteString(", ")
			b.WriteString(e.paramType(inst.Params[i]))
		}
		return fmt.Sprintf("%s %s(%s)", e.ctype(inst.Ret), inst.Mangled, b.String())
	}
	params := paramList(len(inst.Params), func(i int) string { return e.paramType(inst.Params[i]) })
	return fmt.Sprintf("%s %s(%s)", e.ctype(inst.Ret), inst.Mangled, params)
}

func (e *emitter) function(inst *mono.Instance) {
	if inst.Dyn {
		e.dynFunction(inst)
		return
	}
	if inst.RecvErased {
		e.methodFunction(inst)
		return
	}
	if sym, ok := sema.ExternSymbol(inst.Origin); ok {
		e.externFunction(inst, sym)
		return
	}
	fn := inst.Origin
	e.cur = inst
	e.resetUsed()
	e.counter = 0
	e.pushScope() // parameter scope

	// an impl-method instance takes its receiver by value as the first parameter,
	// bound to the source name 'this'; a free function has no receiver.
	var recv []string
	if inst.Recv != nil {
		recv = append(recv, e.ctype(inst.Recv)+" "+e.declareName("this"))
	}
	rest := paramNames(len(inst.Params), func(i int) string {
		return e.paramType(inst.Params[i]) + " " + e.declareName(inst.ParamNames[i])
	})
	e.line(fmt.Sprintf("%s %s(%s) {", e.ctype(inst.Ret), inst.Mangled, joinParams(recv, rest)))

	e.indent++
	e.pushScope() // body scope, nested so a body binding can shadow a parameter
	// One teardown frame spans the parameters and the top-level body: a by-value Ref
	// parameter is the callee's own holder and is released when the function returns
	// (the caller retained its argument copy). The mark is recorded only when the
	// function owns teardown, so a value-only function is byte-identical to Phase 0.
	need := e.anyRefParam(inst) || e.subtreeTeardown(fn.Body.Stmts)
	e.openScope(need, false)
	e.fnMark = e.curScope().markVar
	for i, p := range inst.Params {
		e.registerDrop(e.resolve(inst.ParamNames[i]), p)
	}
	for _, s := range fn.Body.Stmts {
		e.stmt(s)
	}
	// On fallthrough, unwind the function's cleanup stack (running param drops and any
	// top-level defers/drops) before the trailing return; an explicit final return
	// already unwound.
	if !endsWithReturn(fn.Body) {
		if e.fnMark != "" {
			e.line(fmt.Sprintf("zrt_unwind_to(%s);", e.fnMark))
		}
		if inst.Ret != sema.Nil {
			e.line("return " + e.zeroValueC(inst.Ret) + ";")
		}
	}
	e.popScopeRaw()
	e.fnMark = ""
	e.popScope()
	e.indent--
	e.line("}")

	e.popScope()
}

// externFunction emits the thunk for an `#[extern("c_symbol")]`-bound function
// (Phase 1f U5, the FFI import binder): a body-less `unsafe fn` whose compiler-
// supplied body forwards its parameters, in order, to the named C symbol taken
// verbatim (no mangling). The parameters already carry their FFI-safe C types
// (int→int64_t, float→double, str→const char*), so the forward is a direct call
// with the result cast to the Zerg-mapped return type; the standard headers that
// declare libc symbols (math.h/stdlib.h/string.h) ride in under the needsFFI gate.
func (e *emitter) externFunction(inst *mono.Instance, sym string) {
	e.cur = inst
	names := make([]string, len(inst.ParamNames))
	params := make([]string, len(inst.ParamNames))
	for i := range inst.ParamNames {
		names[i] = "a" + strconv.Itoa(i)
		params[i] = e.paramType(inst.Params[i]) + " " + names[i]
	}
	e.line(fmt.Sprintf("%s %s(%s) {", e.ctype(inst.Ret), inst.Mangled, strings.Join(params, ", ")))
	e.indent++
	call := fmt.Sprintf("%s(%s)", sym, strings.Join(names, ", "))
	if inst.Ret == sema.Nil {
		e.line(call + ";")
	} else {
		e.line(fmt.Sprintf("return (%s)%s;", e.ctype(inst.Ret), call))
	}
	e.indent--
	e.line("}")
}

// endsWithReturn reports whether a block's last statement is an unconditional
// return (a 'return ... if c' may fall through, so it does not count).
func endsWithReturn(b *ast.Block) bool {
	if len(b.Stmts) == 0 {
		return false
	}
	r, ok := b.Stmts[len(b.Stmts)-1].(*ast.ReturnStmt)
	return ok && r.Cond == nil
}

// cMain wraps zg_main in a C entry point. A nil/int main keeps the Phase 0
// spelling exactly; a 'Result[nil]' main (additive) delegates to the runtime
// entry shim zrt_run, which installs the root abort handler, runs main under a
// root scope, and maps the Result to a process exit code.
func (e *emitter) cMain(main *sema.FuncSig) {
	e.line("int main(void) {")
	e.indent++
	switch {
	case e.concurrency:
		// A concurrent program runs main as the first coroutine under the scheduler
		// (Fork-F), one entry per main return shape; the scheduler drains the run queue
		// and maps main's outcome to the exit code.
		switch {
		case isResultNil(main.Ret):
			e.line("return zrt_sched_main(zg_main);")
		case main.Ret == sema.Int:
			e.line("return zrt_sched_main_int(zg_main);")
		default:
			e.line("return zrt_sched_main_nil(zg_main);")
		}
	case isResultNil(main.Ret):
		e.line("return zrt_run(zg_main);")
	case main.Ret == sema.Int:
		e.line("return (int)zg_main();")
	default:
		e.line("zg_main();")
		e.line("return 0;")
	}
	e.indent--
	e.line("}")
}

// isResultNil reports whether a type is Zerg's 'Result[nil]' (the sum
// 'Either[nil, Err]'), the one runtime-entry main return this iteration lowers;
// the backend spells it as the runtime's tag-only zrt_result_nil. A 'nil' type
// argument currently resolves to the Unknown primitive rather than the Nil
// singleton (a front-end quirk), so a nil-ish Left (Nil or Unknown) is accepted
// while a payload-carrying Result[int]/Result[str] main is not.
func isResultNil(t sema.Type) bool {
	e, ok := t.(*types.Either)
	if !ok {
		return false
	}
	return e.Left == types.Nil || e.Left.Kind() == types.KUnknown
}

// --- statements ---------------------------------------------------------------

func (e *emitter) stmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.NopStmt:
		e.line(";")
	case *ast.BindStmt:
		t := e.cur.BindType(e.info, n)
		// resolve the RHS before declaring the name, so 'mut n := n' reads the
		// outer binding (matching ':=' semantics). A non-POD RHS is copied (retain /
		// deep copy) when it names existing storage, else moved (byte-identical for
		// every POD binding). A T value bound to a Result/Either/optional binding is
		// wrapped as its Ok/Left (context-typed construction, Phase 1f U0).
		rhs := e.wrapValue(t, e.cur.ExprType(e.info, n.Value), e.copyValue(t, n.Value))
		cname := e.declareName(n.Name)
		e.line(e.localDecl(t, cname) + " = " + rhs + ";")
		e.registerDrop(cname, t)
	case *ast.Reassign:
		e.reassign(n)
	case *ast.PrintStmt:
		e.printStmt(n)
	case *ast.ReturnStmt:
		e.returnStmt(n)
	case *ast.BreakStmt:
		if lm := e.loopMark(); lm != "" {
			e.line(fmt.Sprintf("zrt_unwind_to(%s);", lm))
		}
		e.line("break;")
	case *ast.ContinueStmt:
		if lm := e.loopMark(); lm != "" {
			e.line(fmt.Sprintf("zrt_unwind_to(%s);", lm))
		}
		e.line("continue;")
	case *ast.IfStmt:
		e.ifStmt(n)
	case *ast.ForStmt:
		e.forStmt(n)
	case *ast.DelStmt:
		e.delStmt(n)
	case *ast.DeferStmt:
		e.deferStmt(n)
	case *ast.SpawnStmt:
		e.spawnStmt(n)
	case *ast.SendStmt:
		e.sendStmt(n)
	case *ast.SelectStmt:
		e.selectStmt(n)
	case *ast.WithStmt:
		e.withStmt(n)
	case *ast.RaiseStmt:
		e.raiseStmt(n)
	case *ast.ExprStmt:
		e.line(e.expr(n.X) + ";")
	}
}

// returnStmt lowers `return e (if c)?`. When the function owns no teardown (fnMark
// empty) it keeps the byte-identical Phase 0 spelling. Otherwise it copies the
// return value out to a temporary FIRST, unwinds the function's cleanup stack
// (running every pending defer/drop), then returns the temporary — so a Ref returned
// on an early path is retained before its scope releases, and no drop is skipped.
func (e *emitter) returnStmt(n *ast.ReturnStmt) {
	if e.fnMark == "" {
		ret := "return;"
		if n.Value != nil {
			vt := e.cur.ExprType(e.info, n.Value)
			ret = "return " + e.wrapValue(e.cur.Ret, vt, e.copyValue(vt, n.Value)) + ";"
		}
		if n.Cond != nil {
			e.line(fmt.Sprintf("if (%s) { %s }", e.expr(n.Cond), ret))
		} else {
			e.line(ret)
		}
		return
	}
	emit := func() {
		if n.Value == nil {
			e.line(fmt.Sprintf("zrt_unwind_to(%s);", e.fnMark))
			e.line("return;")
			return
		}
		tmp := e.freshName("ret")
		vt := e.cur.ExprType(e.info, n.Value)
		val := e.wrapValue(e.cur.Ret, vt, e.copyValue(vt, n.Value))
		e.line(fmt.Sprintf("%s = %s;", e.localDecl(e.cur.Ret, tmp), val))
		e.line(fmt.Sprintf("zrt_unwind_to(%s);", e.fnMark))
		e.line("return " + tmp + ";")
	}
	if n.Cond != nil {
		e.line(fmt.Sprintf("if (%s) {", e.expr(n.Cond)))
		e.indent++
		emit()
		e.indent--
		e.line("}")
		return
	}
	emit()
}

// reassign lowers 'x = e'. For a POD target it is the historic bare assignment. For
// a non-POD (Ref-holding) target the old value is dropped first, then the new one
// bound with copy/move semantics — the memory model's declare-del-declare, done
// eagerly (full flow tracking is U4/U5).
func (e *emitter) reassign(n *ast.Reassign) {
	target := e.assignTarget(n.Target)
	t := e.cur.ExprType(e.info, targetExpr(n.Target))
	if !containsRef(t) {
		e.line(fmt.Sprintf("%s = %s;", target, e.expr(n.Value)))
		return
	}
	// Release the old value before overwriting, so a Ref (or Ref-holding) target does
	// not leak or, if the new value aliases the old, double-free. A whole-binding
	// target (a name) is found in the scope's drop items and released through the
	// binding; a sub-place target (a field or index) is not tracked as a binding, so
	// the old value occupying that place is released directly in place.
	if it, ok := e.findDrop(target); ok {
		e.emitInlineDrop(it)
	} else if targetIsPlace(n.Target) {
		e.line(e.fieldDrop(t, target))
	}
	e.line(fmt.Sprintf("%s = %s;", target, e.copyValue(t, n.Value)))
}

// targetIsPlace reports whether an assignment target is a sub-place — a field,
// index, or tuple element — rather than a whole binding, so a reassignment can
// release the old value that currently occupies that place.
func targetIsPlace(t ast.AssignTarget) bool {
	lv, ok := t.(*ast.LValueTarget)
	if !ok {
		return false
	}
	switch lv.X.(type) {
	case *ast.Field, *ast.Bracket, *ast.TupleIndex:
		return true
	}
	return false
}

// targetExpr returns the expression underlying a simple assignment target (the
// lvalue), or nil for the destructuring shapes not yet lowered.
func targetExpr(t ast.AssignTarget) ast.Expr {
	if lv, ok := t.(*ast.LValueTarget); ok {
		return lv.X
	}
	return nil
}

func (e *emitter) printStmt(n *ast.PrintStmt) {
	v := e.expr(n.Value)
	switch e.cur.ExprType(e.info, n.Value) {
	case sema.Int:
		e.line(fmt.Sprintf("printf(\"%%lld\\n\", (long long)(%s));", v))
	case sema.Float:
		e.line(fmt.Sprintf("printf(\"%%g\\n\", %s);", v))
	case sema.Bool:
		e.line(fmt.Sprintf("printf(\"%%s\\n\", (%s) ? \"true\" : \"false\");", v))
	case sema.Str:
		e.line(fmt.Sprintf("printf(\"%%s\\n\", %s);", v))
	}
}

func (e *emitter) ifStmt(n *ast.IfStmt) {
	for i, br := range n.Branches {
		kw := "if"
		if i > 0 {
			kw = "} else if"
		}
		e.line(fmt.Sprintf("%s (%s) {", kw, e.expr(br.Cond)))
		e.body(br.Body, false)
	}
	if n.Else != nil {
		e.line("} else {")
		e.body(n.Else, false)
	}
	e.line("}")
}

func (e *emitter) forStmt(n *ast.ForStmt) {
	if n.Cond == nil {
		e.line("for (;;) {")
	} else {
		e.line(fmt.Sprintf("while (%s) {", e.expr(n.Cond)))
	}
	e.body(n.Body, true)
	e.line("}")
}

// body emits a block's statements at one deeper indent (the surrounding braces are
// emitted by the caller so 'else' can share a line with the closing brace). It opens
// a nested name scope so the block's bindings do not leak, and a teardown frame that
// records a cleanup mark when the block owns teardown. A loop body records its mark
// whenever its subtree owns teardown (a `break`/`continue` unwinds to it); a plain
// block only when its own statements do.
func (e *emitter) body(b *ast.Block, isLoop bool) {
	e.indent++
	e.pushScope()
	need := e.directTeardown(b.Stmts)
	if isLoop {
		need = e.subtreeTeardown(b.Stmts)
	}
	e.openScope(need, isLoop)
	for _, s := range b.Stmts {
		e.stmt(s)
	}
	e.closeScope()
	e.popScope()
	e.indent--
}

// --- name environment ---------------------------------------------------------

func (e *emitter) pushScope() { e.scopes = append(e.scopes, map[string]string{}) }
func (e *emitter) popScope()  { e.scopes = e.scopes[:len(e.scopes)-1] }

// resetUsed starts a fresh per-function C-name environment and pre-seeds it with
// every top-level mangled name (functions and specialized types), so a body-local
// binding or a compiler temporary can never pick a name that collides with — and so
// shadows — a top-level function or type in C. Without this, a local like `x :=
// guard { gr() }` whose temp is `zg_gr`, or a teardown mark `zg_mk` beside a `fn
// mk`, would shadow the function of the same mangled name and miscompile the call.
// The examples hold no such collision, so their emitted names are unchanged.
func (e *emitter) resetUsed() {
	e.used = map[string]bool{}
	for _, inst := range e.prog.Funcs {
		e.used[inst.Mangled] = true
	}
	for _, ti := range e.prog.Types {
		e.used[ti.Mangled] = true
	}
}

// declareName maps a source name to a fresh, function-unique C name (plain
// 'zg_<name>' unless that is already taken, in which case a numeric suffix is
// appended) and records it in the current scope.
func (e *emitter) declareName(name string) string {
	unique := "zg_" + name
	for e.used[unique] {
		e.counter++
		unique = fmt.Sprintf("zg_%s__%d", name, e.counter)
	}
	e.used[unique] = true
	e.scopes[len(e.scopes)-1][name] = unique
	return unique
}

// freshName returns a fresh, function-unique C name with the given prefix (not tied
// to any source name), for a compiler temporary such as a cleanup mark, a
// copied-out return value, or a deferred call's captured environment.
func (e *emitter) freshName(prefix string) string {
	name := "zg_" + prefix
	for e.used[name] {
		e.counter++
		name = fmt.Sprintf("zg_%s__%d", prefix, e.counter)
	}
	e.used[name] = true
	return name
}

// resolve returns the C name a source name currently refers to.
func (e *emitter) resolve(name string) string {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if u, ok := e.scopes[i][name]; ok {
			return u
		}
	}
	return "zg_" + name // unreachable for a checked program
}

// --- expressions --------------------------------------------------------------

func (e *emitter) expr(x ast.Expr) string {
	switch n := x.(type) {
	case *ast.IntLit:
		return strconv.FormatInt(n.Value, 10)
	case *ast.FloatLit:
		return floatLiteral(n.Value)
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.StrLit:
		return cString(n.Value)
	case *ast.NilLit:
		return "0"
	case *ast.Ident:
		if sym, ok := e.info.Refs[n]; ok && sym.Kind == sema.SymVariant {
			return e.constructVariant(n, nil, sym.Variant.Name)
		}
		return e.resolve(n.Name)
	case *ast.Unary:
		return fmt.Sprintf("(%s%s)", unaryOp(n.Op), e.expr(n.X))
	case *ast.Binary:
		if md, ok := e.cur.OpCalls[n]; ok {
			return fmt.Sprintf("%s(%s, %s)", md.Mangled, e.expr(n.L), e.expr(n.R))
		}
		return fmt.Sprintf("(%s %s %s)", e.expr(n.L), binaryOp(n.Op), e.expr(n.R))
	case *ast.Call:
		return e.call(n)
	case *ast.Field:
		return e.expr(n.X) + ".zg_" + n.Name
	case *ast.Bracket:
		if e.info.Brackets[n].Kind == sema.BracketIndex && len(n.Elems) == 1 {
			return e.expr(n.Base) + "[" + e.expr(n.Elems[0]) + "]"
		}
		return "0"
	case *ast.ListLit:
		return "{" + e.exprList(n.Elems) + "}"
	case *ast.MatchExpr:
		return e.lowerMatch(n)
	case *ast.ChanNew:
		return e.chanNew(n)
	case *ast.Recv:
		return e.recvExpr(n)
	case *ast.Force:
		return e.forceExpr(n)
	case *ast.Try:
		return e.tryExpr(n)
	case *ast.Coalesce:
		return e.coalesceExpr(n)
	case *ast.OptChain:
		return e.optChainExpr(n)
	case *ast.GuardExpr:
		return e.guardExpr(n)
	case *ast.FStr:
		return e.fstrExpr(n)
	case *ast.UnsafeExpr:
		return e.unsafeExpr(n)
	default:
		return "0"
	}
}

// unsafeExpr lowers the function-body `unsafe { block }` block-expression (GRAMMAR
// group 12): the unsafe marker guides the front-end only (a foreign call is legal
// inside), so the backend simply yields the block's value. It renders as a GNU
// statement-expression running the block's statements and yielding its trailing
// expression, mirroring guardExpr's inline-block shape.
func (e *emitter) unsafeExpr(n *ast.UnsafeExpr) string {
	stmts := n.Body.Stmts
	var last ast.Expr
	if k := len(stmts); k > 0 {
		if es, ok := stmts[k-1].(*ast.ExprStmt); ok {
			last, stmts = es.X, stmts[:k-1]
		}
	}
	body := e.capture(func() {
		e.pushScope()
		for _, s := range stmts {
			e.stmt(s)
		}
		e.popScope()
	})
	value := "0"
	if last != nil {
		value = e.expr(last)
	}
	return fmt.Sprintf("({ %s%s; })", body, value)
}

// exprList renders a comma-separated list of expressions.
func (e *emitter) exprList(xs []ast.Expr) string {
	var b strings.Builder
	for i, x := range xs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(e.expr(x))
	}
	return b.String()
}

// lowerMatch lowers a match to a nested C ternary. The subject is a name or
// literal (a Phase 0 restriction), so it is safe to reference in each arm without
// hoisting. The last arm is an unguarded catch-all, forming the final else.
func (e *emitter) lowerMatch(m *ast.MatchExpr) string {
	subj := e.expr(m.Subject)
	subjT := e.cur.ExprType(e.info, m.Subject)
	n := len(m.Arms)
	result := e.armValue(m.Arms[n-1], subj, subjT)
	for i := n - 2; i >= 0; i-- {
		test, body := e.armTestAndValue(m.Arms[i], subj, subjT)
		result = fmt.Sprintf("(%s ? %s : %s)", test, body, result)
	}
	return result
}

// armValue emits an arm's body with the pattern's names bound to the subject.
func (e *emitter) armValue(arm ast.MatchArm, subj string, subjT sema.Type) string {
	pop := e.bindArm(arm.Pat, subj)
	v := e.expr(arm.Body)
	pop()
	return v
}

// armTestAndValue emits an arm's match test and body, with the pattern's names in
// scope for both the guard and the body.
func (e *emitter) armTestAndValue(arm ast.MatchArm, subj string, subjT sema.Type) (test, body string) {
	pop := e.bindArm(arm.Pat, subj)
	test = e.patternTest(arm.Pat, subj, subjT)
	if arm.Guard != nil {
		g := e.expr(arm.Guard)
		if test == "" {
			test = g
		} else {
			test = fmt.Sprintf("(%s && %s)", test, g)
		}
	}
	body = e.expr(arm.Body)
	pop()
	if test == "" {
		test = "1" // a non-last unguarded catch-all is rejected by sema
	}
	return test, body
}

// bindArm opens a scope and binds an arm pattern's names to the subject: a
// binding NamePattern binds the whole subject, and a variant pattern binds each
// payload name to its union field 'subj.u.<Variant>.f<i>'. It returns the scope's
// pop, to be deferred after the body is emitted.
func (e *emitter) bindArm(pat ast.Pattern, subj string) func() {
	e.pushScope()
	scope := e.scopes[len(e.scopes)-1]
	switch p := pat.(type) {
	case *ast.NamePattern:
		if res, ok := e.info.Patterns[p]; !ok || res.Kind != sema.NameVariant {
			scope[p.Name] = subj
		}
	case *ast.VariantPattern:
		for i, el := range p.Elems {
			if np, ok := el.(*ast.NamePattern); ok {
				scope[np.Name] = fmt.Sprintf("%s.u.%s.f%d", subj, p.Name, i)
			}
		}
	}
	return e.popScope
}

// patternTest renders the boolean C test for a pattern, or "" when the pattern
// matches unconditionally (wildcard or a bare binding). A literal compares by value
// (or strcmp for a string); a variant pattern — spelled 'V(...)' or a nullary 'V'
// name — compares the subject's tag.
func (e *emitter) patternTest(pat ast.Pattern, subj string, subjT sema.Type) string {
	switch p := pat.(type) {
	case *ast.LitPattern:
		lit := e.expr(p.Lit)
		if p.Neg {
			lit = "(-" + lit + ")"
		}
		if subjT == sema.Str {
			return fmt.Sprintf("(strcmp(%s, %s) == 0)", subj, lit)
		}
		return fmt.Sprintf("(%s == %s)", subj, lit)
	case *ast.VariantPattern:
		return fmt.Sprintf("(%s.tag == %d)", subj, e.variantTag(subjT, p.Name))
	case *ast.NamePattern:
		if res, ok := e.info.Patterns[p]; ok && res.Kind == sema.NameVariant {
			return fmt.Sprintf("(%s.tag == %d)", subj, e.variantTag(subjT, p.Name))
		}
	}
	return ""
}

// variantTag returns the discriminant of a named variant of an enum subject, read
// from the specialized enum instance (0 when the type or variant is not found, a
// case a checked program does not reach).
func (e *emitter) variantTag(subjT sema.Type, name string) int {
	if ti := e.prog.EnumInstance(subjT); ti != nil {
		if v, ok := ti.Variant(name); ok {
			return v.Tag
		}
	}
	return 0
}

func (e *emitter) call(n *ast.Call) string {
	if s, ok := e.builtinCallEmit(n); ok {
		return s
	}
	if s, ok := e.namespaceCallEmit(n); ok {
		return s
	}
	if site, ok := e.cur.DynSites[n]; ok {
		return e.dynCallSite(n, site)
	}
	if e.cur.Dyn {
		if s := e.dynDispatch(n); s != "" {
			return s
		}
	}
	if md, ok := e.cur.MethodCalls[n]; ok {
		return e.methodCall(n, md)
	}
	id, _ := n.Callee.(*ast.Ident)
	if id != nil {
		if sym, ok := e.info.Refs[id]; ok {
			switch sym.Kind {
			case sema.SymType:
				return e.construct(n)
			case sema.SymVariant:
				return e.constructVariant(n, n.Args, sym.Variant.Name)
			}
		}
	}
	var args strings.Builder
	for i, a := range n.Args {
		if i > 0 {
			args.WriteString(", ")
		}
		// a by-value argument is copied (retain / deep copy) when it names existing
		// storage, so the callee's own holder is independent; POD args are unchanged.
		args.WriteString(e.copyValue(e.cur.ExprType(e.info, a.Value), a.Value))
	}
	return fmt.Sprintf("%s(%s)", e.callTarget(n, id), args.String())
}

// namespaceCallEmit lowers a single-level imported-module member call
// `ns.member(args)` (the bundle-import MVP, Decision C): when the callee's base names
// an imported namespace, the member is the bundled module's public function under its
// mangled name `ns__member`, called by value with the ordinary argument copies. It is
// tried before the method/dyn/plain paths, which never match a namespace callee.
func (e *emitter) namespaceCallEmit(n *ast.Call) (string, bool) {
	fld, ok := n.Callee.(*ast.Field)
	if !ok {
		return "", false
	}
	id, ok := fld.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	sym, ok := e.info.Refs[id]
	if !ok || sym.Kind != sema.SymNamespace {
		return "", false
	}
	var args strings.Builder
	for i, a := range n.Args {
		if i > 0 {
			args.WriteString(", ")
		}
		args.WriteString(e.copyValue(e.cur.ExprType(e.info, a.Value), a.Value))
	}
	// A non-generic member calls the bundled top-level function directly; a generic
	// member (e.g. `testing.assert_eq`) dispatches to the per-instance mangled name
	// mono recorded for this call site.
	target := e.prog.CallTarget(sema.NamespaceMemberName(sym, id.Name, fld.Name))
	if m, ok := e.cur.Calls[n]; ok {
		target = m
	}
	return fmt.Sprintf("%s(%s)", target, args.String()), true
}

// callTarget is the mangled C name a call resolves to: a generic call site is
// recorded per instance (so the same call in two specializations dispatches to two
// callees); a non-generic Ident call falls back to the program-level 'zg_<name>'.
// A callee that is neither a recorded generic call nor a plain identifier (e.g. an
// unresolved indirect call) yields a safe placeholder rather than a nil deref.
func (e *emitter) callTarget(n *ast.Call, id *ast.Ident) string {
	if m, ok := e.cur.Calls[n]; ok {
		return m
	}
	if id == nil {
		return "0"
	}
	return e.prog.CallTarget(id.Name)
}

// methodCall lowers a bound-method call 'recv.method(args)' to a by-value call of
// the resolved impl-method instance, passing the receiver first (B1).
func (e *emitter) methodCall(n *ast.Call, md *mono.MethodDispatch) string {
	field, _ := n.Callee.(*ast.Field)
	args := e.expr(field.X)
	for _, a := range n.Args {
		args += ", " + e.expr(a.Value)
	}
	return fmt.Sprintf("%s(%s)", md.Mangled, args)
}

// construct lowers a struct construction 'T(...)' to a C compound literal of the
// specialized struct type, with arguments in field-declaration order.
func (e *emitter) construct(n *ast.Call) string {
	name := e.ctype(e.cur.ExprType(e.info, n))
	return "((" + name + "){" + e.constructArgs(n.Args) + "})"
}

// constructVariant lowers an enum variant used as a value — a payload
// construction 'V(a, b)' or a bare nullary value 'V' — to a C compound literal of
// the specialized tagged union: the variant's tag, and, for a payload variant, its
// arguments in the union member 'u.<V>' as fields 'f0, f1, …'. node is the
// construction expression (used to read its concrete enum type).
func (e *emitter) constructVariant(node ast.Expr, args []ast.Arg, name string) string {
	t := e.cur.ExprType(e.info, node)
	cname := e.ctype(t)
	tag := 0
	if ti := e.prog.EnumInstance(t); ti != nil {
		if v, ok := ti.Variant(name); ok {
			tag = v.Tag
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "((%s){.tag = %d", cname, tag)
	if len(args) > 0 {
		fmt.Fprintf(&b, ", .u.%s = {", name)
		for i, a := range args {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, ".f%d = %s", i, e.expr(a.Value))
		}
		b.WriteString("}")
	}
	b.WriteString("})")
	return b.String()
}

// constructArgs renders a construction's positional argument values in order.
func (e *emitter) constructArgs(args []ast.Arg) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(e.expr(a.Value))
	}
	return b.String()
}

// assignTarget lowers a reassignment target. The Phase 0 backend only lowers the
// bare-identifier lvalue that the checked examples use; richer shapes (tuple,
// struct, field, index) are outside the Phase 0 subset.
func (e *emitter) assignTarget(t ast.AssignTarget) string {
	if lv, ok := t.(*ast.LValueTarget); ok {
		return e.expr(lv.X)
	}
	return "0"
}

// --- lowering helpers ---------------------------------------------------------

// ctype renders a type in type-only position (a return type, a cast, a field, a
// struct-typed value): a specialized nominal type spells its mangled C name, and
// every other type falls to the primitive mapping — so a non-generic program's C is
// unchanged.
func (e *emitter) ctype(t sema.Type) string {
	if isResultNil(t) {
		return "zrt_result_nil"
	}
	if ei, ok := t.(*types.Either); ok {
		// the Result[T] a `<-ch` yields: a generated tagged carrier struct keyed by the
		// received element type (tag 0 = Left(value), 1 = Right(closed/crash)).
		if idx, ok := e.recvIdx[ei.Left.String()]; ok {
			return fmt.Sprintf("zg_recv_%d", idx)
		}
	}
	// a general Result/Either/optional value: its monomorphized carrier (Phase 1f U0).
	if c, ok := e.carrierFor(t); ok {
		return c.name
	}
	if _, ok := t.(*types.Chan); ok {
		// a channel handle is an opaque runtime pointer (chan.c owns the layout).
		return "zrt_chan*"
	}
	if _, ok := t.(*types.Ref); ok {
		// a Ref[T] value is a pointer to its zrt_ref_alloc'd header+payload.
		return "void*"
	}
	if name, ok := e.prog.TypeName(t); ok {
		return name
	}
	return cType(t)
}

// paramType renders a parameter's C type. A fixed-size array parameter decays to a
// pointer to its element (C cannot pass an array by value); every other type is its
// ctype.
func (e *emitter) paramType(t sema.Type) string {
	if a, ok := t.(*types.Array); ok {
		return e.ctype(a.Elem) + "*"
	}
	return e.ctype(t)
}

// localDecl renders a local declaration 'type name'. A fixed-size array places its
// bound after the name ('int64_t name[N]'), the C array declarator; every other
// type is 'ctype name', identical to the pre-generics backend.
func (e *emitter) localDecl(t sema.Type, name string) string {
	if a, ok := t.(*types.Array); ok && a.N.Known {
		return e.ctype(a.Elem) + " " + name + "[" + strconv.FormatInt(a.N.I, 10) + "]"
	}
	return e.ctype(t) + " " + name
}

func cType(t sema.Type) string {
	switch t {
	case sema.Int:
		return "int64_t"
	case sema.Float:
		return "double"
	case sema.Bool:
		return "bool"
	case sema.Str:
		return "const char*"
	default:
		return "void"
	}
}

func zeroValue(t sema.Type) string {
	if isResultNil(t) {
		return "zrt_result_ok()"
	}
	switch t {
	case sema.Int:
		return "0"
	case sema.Float:
		return "0.0"
	case sema.Bool:
		return "false"
	case sema.Str:
		return "\"\""
	default:
		return "0"
	}
}

func unaryOp(k token.Kind) string {
	switch k {
	case token.Minus, token.MinusMod:
		return "-"
	case token.Not:
		return "!"
	case token.Tilde:
		return "~"
	}
	return "?"
}

func binaryOp(k token.Kind) string {
	switch k {
	case token.Plus, token.PlusMod:
		return "+"
	case token.Minus, token.MinusMod:
		return "-"
	case token.Star, token.StarMod:
		return "*"
	case token.Slash:
		return "/"
	case token.Percent:
		return "%"
	case token.Amp:
		return "&"
	case token.Pipe:
		return "|"
	case token.Caret:
		return "^"
	case token.Shl:
		return "<<"
	case token.Shr:
		return ">>"
	case token.EqEq:
		return "=="
	case token.Ne:
		return "!="
	case token.Lt:
		return "<"
	case token.Gt:
		return ">"
	case token.Le:
		return "<="
	case token.Ge:
		return ">="
	case token.And:
		return "&&"
	case token.Or:
		return "||"
	}
	return "?"
}

// floatLiteral renders a double literal that C reads as floating-point (always
// carrying a '.' or exponent).
func floatLiteral(v float64) string {
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eEnN") { // n/N guards inf/nan spellings
		s += ".0"
	}
	return s
}

// cString renders a Go string as a C string literal, escaping conservatively with
// octal for bytes outside printable ASCII so no '\x' run-on can occur.
func cString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString("\\\"")
		case c == '\\':
			b.WriteString("\\\\")
		case c == '\n':
			b.WriteString("\\n")
		case c == '\t':
			b.WriteString("\\t")
		case c == '\r':
			b.WriteString("\\r")
		case c >= 0x20 && c < 0x7f:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "\\%03o", c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// --- output helpers -----------------------------------------------------------

func (e *emitter) line(s string) {
	e.writeIndent()
	e.sb.WriteString(s)
	e.sb.WriteByte('\n')
}

func (e *emitter) blank() { e.sb.WriteByte('\n') }

func (e *emitter) writeIndent() {
	for i := 0; i < e.indent; i++ {
		e.sb.WriteString("    ")
	}
}
