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
	// (zrt_sched_main). It implies NeedsRuntime. A program that touches no channel
	// and no `spawn` leaves it false, so it links none of the scheduler.
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
	return e.sb.String(), Manifest{NeedsRuntime: e.needsRuntime, Concurrency: e.concurrency, NeedsResult: e.needsResult, NeedsIO: e.needsIO, NeedsFormat: e.needsFormat}, nil
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

	// Concurrency state (GRAMMAR group 9). concurrency is set when the program touches a
	// channel or a `spawn`; it drives the Manifest flag the driver links the scheduler
	// from, and the scheduler entry in cMain, and implies needsRuntime. spawnIdx numbers
	// each `spawn f(args)` site so it shares one generated env struct + trampoline (like
	// deferIdx). recvIdx/recvElems number the distinct `<-ch` element types, each of which
	// gets a receive helper building its Result[T]; needChanDrop / needChanSenderDrop gate
	// the two scope-exit drop thunks, one per handle direction. All empty/false for a
	// program with no concurrency, which therefore links none of the scheduler.
	concurrency        bool
	spawnIdx           map[*ast.SpawnStmt]int
	recvIdx            map[string]int
	recvElems          []sema.Type
	needChanDrop       bool
	needChanSenderDrop bool

	// Result/Either/optional carriers (Phase 1f U0). carriers maps a type's spelling
	// to its generated C carrier (a monomorphized tagged struct generalizing the
	// channel recv carrier); needsResult gates their typedefs/helpers and the
	// NeedsResult manifest flag. Both empty/false for a program that uses no such
	// value, which therefore stays byte-identical.
	carriers    map[string]*carrier
	needsResult bool

	// needsRange is set when the program materializes a range VALUE (a range bound to
	// a name, or passed/returned as a value) — so the shared `zg_range` carrier typedef
	// is emitted. An inline membership `v in lo..hi` lowers to a bounds test and does not
	// set it, so a program that only tests membership stays free of the typedef.
	needsRange bool

	// needsFnPtr is set when the program HOLDS a function — bound to a name, stored in a
	// field, passed as an argument — so the shared generic function pointer typedef is
	// emitted. A program that only ever calls functions directly leaves it false and stays
	// byte-identical.
	needsFnPtr bool

	// Tuple value carriers (completeness iteration 2, U2). tuples maps a tuple type's
	// spelling to its generated per-shape C struct (`zg_tuple_<n>` with fields
	// `.f0, .f1, …`), mirroring the Result carrier: an INTERNAL monomorphized layout,
	// never FFI-frozen. Empty for a program that names no tuple value, which therefore
	// stays byte-identical.
	tuples map[string]*tupleCarrier

	// List instances (docs/code/collections.md). lists maps a list element type's spelling
	// to its generated per-instance helpers (the element vtable, the by-value copy, and
	// the drop-env thunk); every list is the same C header (zrt_list), only its element
	// copy/drop differ. Empty for a program that names no list value, which therefore
	// stays byte-identical.
	lists map[string]*listCarrier

	// needsIO is set when the program lowers a stdlib `io` write intrinsic (Phase
	// 1f). It drives the NeedsIO manifest flag and implies needsRuntime. False for a
	// program that never imports io, which therefore stays byte-identical.
	needsIO bool

	// needsFormat is set when the program lowers an f-string (Phase 1f U3): its parts
	// join through zrt_str_concat and its holes render through fmt.c's display()/Format
	// helpers. It drives the NeedsFormat manifest flag and implies needsRuntime. False
	// for a program with no f-string, which therefore stays byte-identical.
	needsFormat bool

	// strManaged is set when the program PRODUCES a heap string (S2): a concat, an
	// f-string with a hole/join, or a `str(bytes|runes)` conversion. When set, EVERY str
	// value in the program is a refcounted `[zrt_ref_hdr | bytes]` cell (a literal is an
	// immortal cell), str becomes non-POD (e.containsRef(str)==true), and its copy/drop
	// retain/release the cell; strLits maps each distinct literal value to its emitted
	// immortal-cell index. When UNSET, str stays a bare `const char*` literal with no
	// retain/release, so a program that produces no heap string (every numbered example)
	// is byte-identical. Implies needsRuntime.
	strManaged bool

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
	case len(main.Params) > 1:
		e.diags.Add(main.Decl.Span(), "'main' takes either no parameters or one 'args: list[str]'")
	case len(main.Params) == 1 && !isStrList(main.Params[0]):
		e.diags.Add(main.Decl.Span(), "'main' parameter must be 'list[str]' (the command-line arguments)")
	case main.Ret != sema.Nil && main.Ret != sema.Int && !isResultNil(main.Ret):
		e.diags.Add(main.Decl.Span(), "'main' must return nil, int, or Result[nil]")
	}

	// A 'Result[nil]' main is the additive runtime-entry path: it pulls in the C
	// runtime (header + link). A program that uses Ref[T] (or any non-POD value)
	// pulls it in too. A 'main(args)' likewise needs it — zrt_os_args builds the
	// list over the runtime. Every other (value-only) main leaves needsRuntime
	// false, so no include is printed and the C stays byte-identical to Phase 0.
	e.needsRuntime = isResultNil(main.Ret) || len(main.Params) == 1

	e.prepareRuntime()
	// prepareRuntime settles e.concurrency, so the args/scheduler conflict is judged only
	// now: the scheduler entry shims take a zero-argument function pointer, so threading
	// the args list through a concurrent main is not wired.
	if len(main.Params) == 1 && e.concurrency {
		e.diags.Add(main.Decl.Span(), "the bootstrap seed does not lower a 'main(args)' in a concurrent program")
	}
	// A prepare pass may reject the program with a clean diagnostic before any C is
	// written. Abort here so a rejected instance never reaches its helper emission or
	// the C backend; Emit already discards the buffer when diags is non-empty.
	if !e.diags.Empty() {
		return
	}

	e.line("// Generated by zerg (Phase 0). Do not edit.")
	e.line("#include <stdio.h>")
	e.line("#include <stdint.h>")
	e.line("#include <stdbool.h>")
	e.line("#include <string.h>")
	if e.needsRuntime {
		e.line("#include \"zergrt.h\"")
	}
	e.blank()

	// Struct/enum, tuple, and carrier typedefs, in one topological order: each is emitted
	// after every type it embeds BY VALUE (a struct after its non-boxed field types, a
	// carrier after its payload, a tuple after its elements), so an arbitrary nesting
	// (`Outer { kid: Inner? }` -> `Inner?` -> `Inner`) orders correctly. A boxed/pointer/
	// runtime member (str/Ref/list/map/boxed-optional/fn/channel) is a complete C type, so
	// it imposes no order.
	// The shared function pointer, BEFORE the struct typedefs rather than beside the range
	// carrier below: a struct field may hold a function, and that field's declaration names
	// `zg_fnptr`. Emits nothing for a program that holds no function.
	e.emitFnPtrTypedef()

	e.emitTypeTypedefs()

	// The shared range value carrier, before any prototype/body that names a range
	// value. Emits nothing for a program that materializes no range value.
	e.emitRangeTypedef()

	// module-constant globals, evaluated at init (Phase 1g S3); none for a program
	// with no module constant, which therefore stays byte-identical.
	e.emitModuleConstGlobals()

	// prototypes first, so declaration order does not constrain calls
	for _, inst := range e.prog.Funcs {
		if e.skipBundledConcurrency(inst) {
			continue
		}
		e.line(e.prototype(inst) + ";")
	}
	// per-module init function prototypes (Phase 1g S3); none for a no-init program.
	e.emitInitPrototypes()
	e.blank()

	// Ref[T] copy/drop and allocation helpers (Phase 1d), after the struct typedefs
	// they reference and before the function bodies that call them. Emits nothing
	// for a value-only program.
	e.emitRefHelpers()

	for _, inst := range e.prog.Funcs {
		if e.skipBundledConcurrency(inst) {
			continue
		}
		e.function(inst)
		e.blank()
	}

	// per-module init functions (Phase 1g S3), before the entry that calls them; none
	// for a program with no init and no module constant.
	e.emitInitFunctions()

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
		e.line(e.slotCtype(f.Type, f.Boxed) + " zg_" + f.Name + ";")
	}
	e.indent--
	e.line("} " + ti.Mangled + ";")
	e.blank()
}

// emitTypeTypedefs writes the struct/enum, tuple, and carrier typedefs in one topological
// order: a node is emitted only once every type it embeds BY VALUE is emitted, and among the
// ready nodes the lowest (rank, index) wins. The ranks — plain tuple (0), plain carrier (1),
// struct/enum (2), nominal-wrapping tuple (3), nominal-wrapping carrier (4) — make the order
// reproduce the historic plain-then-nominal two-pass whenever no struct embeds a
// nominal-wrapping carrier, so an existing program stays byte-identical; the dependency
// edges additionally satisfy an arbitrary nesting (Outer -> Inner? -> Inner) the two-pass
// could not. S1 boxing breaks every by-value cycle, so the graph is a DAG.
func (e *emitter) emitTypeTypedefs() {
	type node struct {
		key  string
		rank int
		idx  int
		deps []string
		emit func()
	}
	var nodes []*node
	byKey := map[string]*node{}
	add := func(n *node) {
		nodes = append(nodes, n)
		byKey[n.key] = n
	}
	for i, ti := range e.prog.Types {
		ti := ti
		add(&node{key: "T:" + ti.Mangled, rank: 2, idx: i, deps: e.nominalDepKeys(ti), emit: func() { e.typedef(ti) }})
	}
	for i, c := range e.orderedTuples() {
		c := c
		rank := 0
		if e.tupleDependsOnNominal(c) {
			rank = 3
		}
		add(&node{key: "U:" + c.name, rank: rank, idx: i, deps: e.tupleDepKeys(c), emit: func() { e.emitOneTuple(c); e.blank() }})
	}
	for i, c := range e.orderedCarriers() {
		c := c
		rank := 1
		if e.carrierDependsOnNominal(c) {
			rank = 4
		}
		add(&node{key: "C:" + c.name, rank: rank, idx: i, deps: e.carrierDepKeys(c), emit: func() { e.emitOneCarrier(c); e.blank() }})
	}

	emitted := map[string]bool{}
	// pick returns the lowest-(rank, idx) not-yet-emitted node; when readyOnly it considers
	// only nodes whose every in-graph dependency is already emitted.
	pick := func(readyOnly bool) *node {
		var best *node
		for _, n := range nodes {
			if emitted[n.key] {
				continue
			}
			if readyOnly {
				ready := true
				for _, d := range n.deps {
					if dn, ok := byKey[d]; ok && !emitted[dn.key] {
						ready = false
						break
					}
				}
				if !ready {
					continue
				}
			}
			if best == nil || n.rank < best.rank || (n.rank == best.rank && n.idx < best.idx) {
				best = n
			}
		}
		return best
	}
	for range nodes {
		n := pick(true)
		if n == nil {
			// A residual by-value cycle should not occur (S1 boxing breaks every back edge);
			// break it deterministically rather than drop a typedef.
			n = pick(false)
		}
		n.emit()
		emitted[n.key] = true
	}
}

// nominalDepKeys returns the type-graph node keys a struct/enum instance embeds BY VALUE:
// each non-boxed field (struct) or payload slot (enum) contributes its type's direct
// by-value dependencies. A boxed slot (S1) is a `void*` cell and imposes no order.
func (e *emitter) nominalDepKeys(ti *mono.TypeInstance) []string {
	var ks []string
	for _, f := range ti.Fields {
		if !f.Boxed {
			ks = append(ks, e.byValueDepKeys(f.Type)...)
		}
	}
	for _, v := range ti.Variants {
		for i, pt := range v.Payload {
			if !v.Boxed[i] {
				ks = append(ks, e.byValueDepKeys(pt)...)
			}
		}
	}
	return ks
}

// tupleDepKeys / carrierDepKeys return the by-value dependency node keys of a tuple or
// Result/optional/Either carrier — its element / payload types.
func (e *emitter) tupleDepKeys(c *tupleCarrier) []string {
	var ks []string
	for _, el := range c.elems {
		ks = append(ks, e.byValueDepKeys(el)...)
	}
	return ks
}

func (e *emitter) carrierDepKeys(c *carrier) []string {
	ks := e.byValueDepKeys(c.left)
	if c.kind == carrierEither {
		ks = append(ks, e.byValueDepKeys(c.right)...)
	}
	return ks
}

// byValueDepKeys returns the type-graph node keys a type embeds BY VALUE: a nominal
// struct/enum, or a tuple/optional/Either carrier. It mirrors dependsOnNominal's structural
// recursion but yields the DIRECT dependency node (which carries its own further deps)
// rather than a bool. A boxed-optional / str / Ref / list / map / channel / fn member is a
// pointer or a runtime type — always a complete C type — so it contributes no edge.
func (e *emitter) byValueDepKeys(t sema.Type) []string {
	switch x := t.(type) {
	case *types.Struct, *types.Enum:
		return []string{"T:" + e.ctype(t)}
	case *types.Tuple:
		if c, ok := e.tupleFor(t); ok {
			return []string{"U:" + c.name}
		}
		var ks []string
		for _, el := range x.Elems {
			ks = append(ks, e.byValueDepKeys(el)...)
		}
		return ks
	case *types.Opt:
		if e.isBoxedOpt(x) {
			return nil
		}
		if c, ok := e.carrierFor(t); ok {
			return []string{"C:" + c.name}
		}
		return e.byValueDepKeys(x.Elem)
	case *types.Either:
		if c, ok := e.carrierFor(t); ok {
			return []string{"C:" + c.name}
		}
		return append(e.byValueDepKeys(x.Left), e.byValueDepKeys(x.Right)...)
	}
	return nil
}

// slotCtype renders a struct field or enum payload slot's C type. A Boxed slot (S1) is
// heap-indirected through a refcounted cell, so it is a `void*` regardless of its
// nominal payload type; every other slot keeps its ordinary ctype.
func (e *emitter) slotCtype(t sema.Type, boxed bool) string {
	if boxed {
		return "void*"
	}
	return e.ctype(t)
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
				fmt.Fprintf(&b, "%s f%d; ", e.slotCtype(pt, v.Boxed[i]), i)
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
	if inst.Recv != nil {
		var b strings.Builder
		b.WriteString(e.ctype(inst.Recv))
		for i := range inst.Params {
			b.WriteString(", ")
			b.WriteString(e.declParamType(inst, i))
		}
		return fmt.Sprintf("%s %s(%s)", e.ctype(inst.Ret), inst.Mangled, b.String())
	}
	params := paramList(len(inst.Params), func(i int) string { return e.declParamType(inst, i) })
	return fmt.Sprintf("%s %s(%s)", e.ctype(inst.Ret), inst.Mangled, params)
}

func (e *emitter) function(inst *mono.Instance) {
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
		return e.declParamType(inst, i) + " " + e.declareName(inst.ParamNames[i])
	})
	sig := joinParams(recv, rest)
	e.line(fmt.Sprintf("%s %s(%s) {", e.ctype(inst.Ret), inst.Mangled, sig))

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
		// A by-ref parameter is a BORROW: the caller keeps the variable, so ending this
		// call must not free it (docs/core/memory.md's lifetime table).
		if isByRefParam(inst, i) {
			continue
		}
		e.registerDrop(e.resolve(inst.ParamNames[i]), p, fn)
	}
	for _, s := range fn.Body.Stmts {
		e.stmt(s)
	}
	// On fallthrough, unwind the function's cleanup stack (running param drops and any
	// top-level defers/drops) before the trailing return; an explicit final return
	// already unwound.
	if !endsWithDiverge(fn.Body) {
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

// endsWithDiverge reports whether a block's last statement leaves the function
// without falling through — an unconditional 'return', or a 'raise', which aborts.
// A 'return ... if c' may fall through, so it does not count; a postfix-guarded
// 'raise e if c' is desugared into an IfStmt by the parser and so is not one either.
//
// The raise case matters because the fallthrough return below is typed from the
// signature: a function whose body ENDS in a raise needs no trailing return at all,
// and emitting one spelled `return 0;` is a C type error for every non-scalar result.
func endsWithDiverge(b *ast.Block) bool {
	if len(b.Stmts) == 0 {
		return false
	}
	switch s := b.Stmts[len(b.Stmts)-1].(type) {
	case *ast.ReturnStmt:
		return s.Cond == nil
	case *ast.RaiseStmt:
		return true
	}
	return false
}

// cMain wraps zg_main in a C entry point. A nil/int main keeps the Phase 0
// spelling exactly; a 'Result[nil]' main (additive) delegates to the runtime
// entry shim zrt_run, which installs the root abort handler, runs main under a
// root scope, and maps the Result to a process exit code.
func (e *emitter) cMain(main *sema.FuncSig) {
	takesArgs := len(main.Params) == 1
	if takesArgs {
		// A main that takes the command-line args needs argc/argv, so the C entry grows
		// its parameters and builds the `list[str]` main receives by value.
		e.line("int main(int argc, char **argv) {")
	} else {
		e.line("int main(void) {")
	}
	e.indent++
	// Run every module's init in dependency order before main's body (Phase 1g S3);
	// nothing is emitted for a program with no init and no module constant, so its
	// entry stays byte-identical.
	e.emitInitCalls()
	if takesArgs {
		// concurrency + args is rejected above, so only the non-scheduler shapes reach
		// here. main owns the list as its by-value parameter and frees it at scope exit.
		e.line("zrt_list zg_args = zrt_os_args(argc, argv);")
		switch {
		case isResultNil(main.Ret):
			e.line("return zrt_run_args(zg_main, zg_args);")
		case main.Ret == sema.Int:
			e.line("return (int)zg_main(zg_args);")
		default:
			e.line("zg_main(zg_args);")
			e.line("return 0;")
		}
		e.indent--
		e.line("}")
		return
	}
	switch {
	case e.concurrency:
		// A concurrent program runs main as the first coroutine under the scheduler; the
		// scheduler drains the run queue and maps main's outcome to the exit code. One shim
		// per return shape, named from the same three-way question the plain entries ask.
		e.line(fmt.Sprintf("return %s(zg_main);", schedEntry(main.Ret)))
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

// schedEntry names the scheduler program-entry shim for a main return shape (sched.c).
func schedEntry(ret sema.Type) string {
	switch {
	case isResultNil(ret):
		return "zrt_sched_main"
	case ret == sema.Int:
		return "zrt_sched_main_int"
	default:
		return "zrt_sched_main_nil"
	}
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

// isStrList reports whether a type is `list[str]` — the one shape a `main` parameter
// may take (the command-line arguments).
func isStrList(t sema.Type) bool {
	l, ok := t.(*types.List)
	return ok && l.Elem == types.Str
}

// --- statements ---------------------------------------------------------------

func (e *emitter) stmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.NopStmt:
		e.line(";")
	case *ast.BindStmt:
		if n.Target != nil {
			e.destructureBind(n)
			return
		}
		t := e.cur.BindType(e.info, n)
		// resolve the RHS before declaring the name, so 'mut n := n' reads the
		// outer binding (matching ':=' semantics). A non-POD RHS is copied (retain /
		// deep copy) when it names existing storage, else moved (byte-identical for
		// every POD binding). A T value bound to a Result/Either/optional binding is
		// wrapped as its Ok/Left (context-typed construction, Phase 1f U0).
		rhs := e.wrapValue(t, e.cur.ExprType(e.info, n.Value), e.copyValue(t, n.Value))
		cname := e.declareName(n.Name)
		e.line(e.localDecl(t, cname) + " = " + rhs + ";")
		e.registerDrop(cname, t, n)
	case *ast.Reassign:
		e.reassign(n)
	case *ast.PrintStmt:
		e.printStmt(n)
	case *ast.ReturnStmt:
		e.returnStmt(n)
	case *ast.BreakStmt:
		e.loopExit("break", n.Cond)
	case *ast.ContinueStmt:
		e.loopExit("continue", n.Cond)
	case *ast.IfStmt:
		e.ifStmt(n)
	case *ast.ForStmt:
		e.forStmt(n)
	case *ast.DelStmt:
		e.delStmt(n)
	case *ast.DeferStmt:
		e.deferStmt(n)
	case *ast.WithStmt:
		e.withStmt(n)
	case *ast.RaiseStmt:
		e.raiseStmt(n)
	case *ast.ExprStmt:
		// `close(ch)` is a statement rather than a value (closeCallStmt says why), so it is
		// taken here before the expression dispatch ever sees the call.
		e.line(e.expr(n.X) + ";")
	default:
		// The statement half of the anti-silence net the expression dispatch already
		// carries: every statement the backend lowers has an explicit case above, so a
		// node reaching here is one the seed does not implement. Emitting nothing for it
		// would silently drop the statement from the program — fail loudly instead, since
		// Emit discards its output while diags are non-empty.
		e.diags.Add(s.Span(), "statement not supported by the bootstrap seed: %T", s)
	}
}

// loopExit lowers a `break`/`continue` statement, optionally guarded by a trailing
// `if cond`. The loop teardown (zrt_unwind_to to the loop mark, run before leaving
// the loop body) MUST sit inside the same guard so it only fires when the jump is
// actually taken. An unconditional exit keeps the byte-identical teardown+keyword
// spelling. (The `??`-RHS break/continue is a separate, always-unconditional path in
// divergeStmt and is unaffected.)
func (e *emitter) loopExit(kw string, cond ast.Expr) {
	if cond == nil {
		if lm := e.loopMark(); lm != "" {
			e.line(fmt.Sprintf("zrt_unwind_to(%s);", lm))
		}
		e.line(kw + ";")
		return
	}
	e.line(fmt.Sprintf("if (%s) {", e.expr(cond)))
	e.indent++
	if lm := e.loopMark(); lm != "" {
		e.line(fmt.Sprintf("zrt_unwind_to(%s);", lm))
	}
	e.line(kw + ";")
	e.indent--
	e.line("}")
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
	// `xs[i] = v` on a list target sets in place through the runtime (drop-old +
	// store-new), before the ordinary place-assignment paths.
	if s, ok := e.listIndexAssign(n); ok {
		e.line(s + ";")
		return
	}
	target := e.assignTarget(n.Target)
	t := e.cur.ExprType(e.info, targetExpr(n.Target))
	if !e.containsRef(t) {
		// A POD optional target (`int?`/`bool?`/`float?`) is a value CARRIER (`{tag, ok}`)
		// even though containsRef is false, so the new value must be wrapped into the carrier
		// (Some -> `{.tag=0,.ok=v}` / nil -> `{.tag=1}`) exactly like its bind initializer —
		// a raw store would land the value in the carrier's `tag` slot and read back as absent
		// (a silent miscompile), mirroring fieldSlot. A plain (non-carrier) POD target stays
		// the byte-identical raw assignment.
		rhs := e.expr(n.Value)
		if e.isOptCarrierType(t) {
			rhs = e.wrapValue(t, e.cur.ExprType(e.info, n.Value), rhs)
		}
		e.line(fmt.Sprintf("%s = %s;", target, rhs))
		return
	}
	// Materialize the new value into a temp BEFORE releasing the old one. The RHS may
	// read the target itself — `s = s + x` str accumulation, `xs = xs.tail` — and the
	// inline drop below does NOT null the slot, so releasing first would free the very
	// cell the RHS then reads (a use-after-free that silently loses the accumulator).
	// The temp holds a retained reference across the release, so the old cell stays live
	// until the RHS has consumed it.
	vt := e.cur.ExprType(e.info, n.Value)
	newVal := e.freshName("as")
	e.line(fmt.Sprintf("%s = %s;", e.localDecl(t, newVal), e.wrapValue(t, vt, e.copyValue(vt, n.Value))))
	// Release the old value before overwriting, so a Ref (or Ref-holding) target does
	// not leak. A whole-binding target (a name) is found in the scope's drop items and
	// released through the binding; a sub-place target (a field or index) is not tracked
	// as a binding, so the old value occupying that place is released directly in place.
	if it, ok := e.findDrop(target); ok {
		e.emitInlineDrop(it)
	} else if targetIsPlace(n.Target) {
		e.line(e.fieldDrop(t, target))
	}
	// Move the already-retained new value into the (now released) slot. The
	// retain-new → release-old → store order is load-bearing: for a boxed `Opt` field
	// (no slot guard) storing before releasing would leak the old cell, and for a
	// self-referential RHS releasing before materializing would free it early.
	e.line(fmt.Sprintf("%s = %s;", target, newVal))
}

// destructureBind lowers a destructuring bind '(a, b) := e' / 'P{x, y} := e'. It
// materializes the RHS once into a temp of the tuple/struct type, then binds each leaf
// name to its sub-place inside the temp (`tmp.f<i>` for a tuple element, `tmp.zg_<f>`
// for a struct field) — reusing the match pattern walk, so a nested '(a, (b, c))'
// destructures too. Every subsequent read of a leaf resolves to its sub-place, so no
// per-component copy is made; the temp stays live for the enclosing block.
func (e *emitter) destructureBind(n *ast.BindStmt) {
	t := e.cur.ExprType(e.info, n.Value)
	tmp := e.freshName("db")
	e.line(fmt.Sprintf("%s = %s;", e.localDecl(t, tmp), e.copyValue(t, n.Value)))
	// The RHS temp owns any non-POD leaves it holds; schedule their teardown so a
	// destructured tuple/struct of Refs/managed strs is released at scope exit (mirroring
	// the normal bind path's registerDrop). Every leaf name is only an ALIAS of a temp
	// sub-place, so a leaf that is later copied retains independently and the temp's own
	// drop still releases each field exactly once — no double-free.
	e.registerDestructureDrops(tmp, t, n)
	e.patternWalk(n.Target, tmp, t, e.scopes[len(e.scopes)-1])
}

// registerDestructureDrops schedules teardown for a destructuring bind's RHS temp. A
// nominal struct/enum temp registers its own generated drop-env thunk (like any bound
// name), but a tuple carrier has no such thunk, so it recurses into each element place
// (`place.f<i>`) and registers each non-POD leaf directly — a bare Ref/str/list leaf
// through its slot guard, a nested struct/enum through its drop-env. A wholly-POD temp
// owns nothing and registers nothing (byte-identical for a POD destructure).
func (e *emitter) registerDestructureDrops(place string, t sema.Type, at ast.Node) {
	if !e.containsRef(t) {
		return
	}
	if tup, ok := t.(*types.Tuple); ok {
		for i, el := range tup.Elems {
			e.registerDestructureDrops(fmt.Sprintf("%s.f%d", place, i), el, at)
		}
		return
	}
	e.registerDrop(place, t, at)
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
	// The display family drives the printf conversion: a signed integer (int, byte,
	// rune, i8..i64) prints with %lld, an unsigned integer (uint, u8..u64) with %llu,
	// and a float with %g. The int/float/bool/str forms are unchanged, so a program
	// that prints only those stays byte-identical.
	switch numericDisplay(e.cur.ExprType(e.info, n.Value)) {
	case dispInt:
		e.line(fmt.Sprintf("printf(\"%%lld\\n\", (long long)(%s));", v))
	case dispUint:
		e.line(fmt.Sprintf("printf(\"%%llu\\n\", (unsigned long long)(%s));", v))
	case dispFloat:
		e.line(fmt.Sprintf("printf(\"%%g\\n\", %s);", v))
	case dispBool:
		e.line(fmt.Sprintf("printf(\"%%s\\n\", (%s) ? \"true\" : \"false\");", v))
	case dispStr:
		// A managed print of an OWNED str temporary (a concat/f-string/conversion result, or
		// a literal) releases it after the write so it does not leak; a borrowed variable is
		// left to its binding's drop. Unmanaged, this is the plain byte-identical printf.
		if e.strManaged && e.strOwned(n.Value) {
			p := e.freshName("ps")
			e.line(fmt.Sprintf("{ const char *%s = %s; printf(\"%%s\\n\", %s); zrt_str_release(%s); }", p, v, p, p))
		} else {
			e.line(fmt.Sprintf("printf(\"%%s\\n\", %s);", v))
		}
	}
}

func (e *emitter) ifStmt(n *ast.IfStmt) {
	// A plain if/else-if/else chain keeps the flat `} else if` spelling (byte-identical).
	// A chain with a binding head `if x := opt` nests, since each binding head must
	// evaluate its optional into a temp before the presence test.
	if !anyIfBind(n.Branches) {
		e.ifStmtFlat(n)
		return
	}
	e.ifChain(n.Branches, n.Else)
}

func (e *emitter) ifStmtFlat(n *ast.IfStmt) {
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

// anyIfBind reports whether any branch of a chain is a binding head `if x := opt`.
func anyIfBind(branches []ast.IfBranch) bool {
	for _, br := range branches {
		if br.Bind != "" {
			return true
		}
	}
	return false
}

// ifChain emits an if/else-if/else statement chain that contains at least one binding
// head. A plain branch keeps `if (cond) { body }`; a binding head `if x := opt`
// evaluates the optional into a temp and, when it is present, binds x to the unwrapped
// value for the then-body only. Later branches form the `else` tail recursively, so a
// later branch's optional is evaluated only when the earlier tests fail.
func (e *emitter) ifChain(branches []ast.IfBranch, elseB *ast.Block) {
	br := branches[0]
	tail := func() {
		switch {
		case len(branches) > 1:
			e.line("} else {")
			e.indent++
			e.ifChain(branches[1:], elseB)
			e.indent--
			e.line("}")
		case elseB != nil:
			e.line("} else {")
			e.body(elseB, false)
			e.line("}")
		default:
			e.line("}")
		}
	}
	if br.Bind == "" {
		e.line(fmt.Sprintf("if (%s) {", e.expr(br.Cond)))
		e.body(br.Body, false)
		tail()
		return
	}
	optT := e.cur.ExprType(e.info, br.Cond)
	e.line("{")
	e.indent++
	e.pushScope()
	// The evaluated optional temp OWNS its value: a non-POD optional is copyValue'd (a named
	// source is retained/deep-copied, a fresh producer is moved) and its teardown scheduled
	// on a local frame, so its payload is released exactly once at the block's exit whether
	// present or absent. The bound name below is a BORROW of that payload (a plain read, never
	// registered), so the then-block uses it without a second owner — no double-free. A POD
	// optional owns nothing: copyValue is a bare `=`, no mark and no drop are emitted, and the
	// block stays byte-identical.
	e.openScope(e.containsRef(optT), false)
	tmp := e.freshName("ifopt")
	e.line(fmt.Sprintf("%s = %s;", e.localDecl(optT, tmp), e.copyValue(optT, br.Cond)))
	e.registerDrop(tmp, optT, br.Cond)
	e.line(fmt.Sprintf("if (%s) {", e.optPresentTest(optT, tmp)))
	e.pushScope()
	e.indent++
	cname := e.declareName(br.Bind)
	e.line(fmt.Sprintf("%s = %s;", e.localDecl(optElem(optT), cname), e.optUnwrapValue(optT, tmp)))
	e.indent--
	e.body(br.Body, false)
	e.popScope()
	tail()
	e.closeScope()
	e.popScope()
	e.indent--
	e.line("}")
}

// optPresentTest renders the presence test of an evaluated optional temp: a carrier
// optional is present when its tag is 0; a boxed nullable optional when its cell is
// non-NULL.
func (e *emitter) optPresentTest(optT sema.Type, tmp string) string {
	if e.isBoxedOpt(optT) {
		return tmp + " != NULL"
	}
	return tmp + ".tag == 0"
}

// optUnwrapValue renders the unwrapped element value of an evaluated optional temp: a
// carrier optional reads its `.ok` field; a boxed nullable optional reads through the
// cell's payload.
func (e *emitter) optUnwrapValue(optT sema.Type, tmp string) string {
	if e.isBoxedOpt(optT) {
		elem := optT.(*types.Opt).Elem
		return fmt.Sprintf("(*(%s*)zrt_ref_payload(%s))", e.ctype(elem), tmp)
	}
	return tmp + ".ok"
}

// optElem returns the element type of an optional, or Unknown when the type is not an
// optional (a checked binding head always yields an optional).
func optElem(optT sema.Type) sema.Type {
	if o, ok := optT.(*types.Opt); ok {
		return o.Elem
	}
	// A `Result[T]` head binds its Left, which lives in the same tag-0 slot an optional's
	// value does — so the present-test and the unwrap above need no case of their own.
	if ei, ok := optT.(*types.Either); ok {
		return ei.Left
	}
	return types.Unknown
}

func (e *emitter) forStmt(n *ast.ForStmt) {
	if n.Iter != nil {
		e.forInStmt(n)
		return
	}
	if n.Cond == nil {
		e.line("for (;;) {")
	} else {
		e.line(fmt.Sprintf("while (%s) {", e.expr(n.Cond)))
	}
	e.body(n.Body, true)
	e.line("}")
}

// forInStmt lowers the iterate form `for v in iter { body }`. An integer range
// `a..b` (`a..=b`) becomes a counted C `for` over a real `int64_t` loop var the body
// reads: `for (int64_t v = LO; v < HI; v++)` (`<= HI` for an inclusive range). A fixed
// array `[T; N]` becomes an index loop that copies each element into a `T v` local
// before the body: `for (size_t i = 0; i < N; i++) { T v = arr[i]; body }`. The loop
// var is a fresh C name registered in a scope enclosing the body, so a `v` in the body
// resolves to it. Sema rejects every other iterable, so no other shape reaches here.
func (e *emitter) forInStmt(n *ast.ForStmt) {
	if rng, ok := n.Iter.(*ast.Range); ok {
		e.pushScope()
		cv := e.declareName(n.Var)
		op := "<"
		if rng.Inclusive {
			op = "<="
		}
		e.line(fmt.Sprintf("for (int64_t %s = %s; %s %s %s; %s++) {",
			cv, e.expr(rng.Lo), cv, op, e.expr(rng.Hi), cv))
		e.body(n.Body, true)
		e.line("}")
		e.popScope()
		return
	}
	// A list[T]: index it through the runtime and copy each element into a `T v` local
	// the body reads (a `for mut x` writes the edited element back to its slot at the
	// end of each iteration). The loop var and body share one name scope and teardown
	// frame, so a non-POD element copy is released per iteration.
	if lt, ok := e.cur.ExprType(e.info, n.Iter).(*types.List); ok {
		e.forInList(n, lt)
		return
	}
	// A channel: the loop IS the receive, and the close is what ends it. A channel is the
	// other thing iterated without being a container (docs/code/coroutine.md).
	// A str: iterate its code points. Materialize the runes into a temporary list and
	// walk it, so the body's loop variable binds each rune (docs/code/collections.md).
	if e.cur.ExprType(e.info, n.Iter) == sema.Str {
		e.forInStr(n)
		return
	}
	// A fixed array [T; N]: index it and copy each element into a `T v` local the body
	// reads. The loop var and the body share one name scope and teardown frame.
	//
	// Anything else reaching here is a shape sema TYPED but this pass does not lower — a
	// map is the one that exists today (`for k in m` binds the key). The assertion below
	// used to be unchecked, so it was a nil dereference: the seed CRASHED with a Go panic
	// and a stack trace instead of reporting anything. A form the compiler cannot lower is
	// refused by name, which is the whole contract; it is never a crash.
	arr, ok := e.cur.ExprType(e.info, n.Iter).(*types.Array)
	if !ok {
		e.diags.Add(n.Iter.Span(), "NotImplemented: the bootstrap seed does not lower a for-in over %s: "+
			"it builds the self-hosting compiler, which does", e.cur.ExprType(e.info, n.Iter))
		return
	}
	iv := e.freshName("i")
	e.line(fmt.Sprintf("for (size_t %s = 0; %s < %d; %s++) {", iv, iv, arr.N.I, iv))
	e.indent++
	e.pushScope()
	cv := e.declareName(n.Var)
	e.openScope(e.subtreeTeardown(n.Body.Stmts), true)
	e.line(fmt.Sprintf("%s = %s[%s];", e.localDecl(arr.Elem, cv), e.expr(n.Iter), iv))
	for _, s := range n.Body.Stmts {
		e.stmt(s)
	}
	e.closeScope()
	e.popScope()
	e.indent--
	e.line("}")
}

// forInStr lowers `for c in s` over a str: it decodes the str's code points into a
// temporary list[rune], walks it binding each rune into an `int32_t c` local, and frees
// the temporary after the loop. A str is immutable, so `for mut c` is meaningless here
// and the loop var is always a fresh read.
func (e *emitter) forInStr(n *ast.ForStmt) {
	runes := e.freshName("runes")
	iv := e.freshName("i")
	e.line("{")
	e.indent++
	e.line(fmt.Sprintf("zrt_list %s = zrt_str_runes(%s);", runes, e.expr(n.Iter)))
	e.line(fmt.Sprintf("for (size_t %s = 0; %s < zrt_list_len(&%s); %s++) {", iv, iv, runes, iv))
	e.indent++
	e.pushScope()
	cv := e.declareName(n.Var)
	e.openScope(e.subtreeTeardown(n.Body.Stmts), true)
	e.line(fmt.Sprintf("int32_t %s = *(int32_t*)zrt_list_at(&%s, %s);", cv, runes, iv))
	for _, s := range n.Body.Stmts {
		e.stmt(s)
	}
	e.closeScope()
	e.popScope()
	e.indent--
	e.line("}")
	e.line(fmt.Sprintf("zrt_list_drop(&%s);", runes))
	e.indent--
	e.line("}")
}

// forInList lowers `for (mut)? v in xs` over a list. Each iteration copies element i
// into a `T v` local: a plain `for` copies (retain/deep-copy) a value the body only
// reads, dropped per iteration when non-POD; a `for mut` copies the element, lets the
// body edit it, then writes it back to slot i (drop-old + store-new via zrt_list_set).
// The iterated list is frozen against structural change (sema), so the length read
// each turn is stable.
func (e *emitter) forInList(n *ast.ForStmt, lt *types.List) {
	nonPOD := e.containsRef(lt.Elem)
	if n.Mut && nonPOD {
		// A `for mut x` over a non-POD element needs move/ownership tracking on the
		// per-iteration write-back that the MVP loop-var machinery does not model; gate it
		// rather than leak or double-free. A `for x` read and a POD `for mut` are supported.
		e.diags.Add(n.Span(), "`for mut x` over a list of non-POD elements is not yet supported")
		return
	}
	// The iterable must be an addressable place to index in place. A fresh list (a
	// literal, a call result) is materialized into an owned temp, dropped on every exit
	// path through the cleanup stack, then iterated.
	if !isLValueExpr(n.Iter) {
		e.line("{")
		e.indent++
		e.pushScope()
		e.openScope(true, false)
		tmp := e.freshName("foriter")
		e.line(fmt.Sprintf("zrt_list %s = %s;", tmp, e.copyValue(lt, n.Iter)))
		e.registerDrop(tmp, lt, n.Iter)
		e.forInListBody(n, lt, tmp)
		e.closeScope()
		e.popScope()
		e.indent--
		e.line("}")
		return
	}
	e.forInListBody(n, lt, e.expr(n.Iter))
}

// forInListBody emits the counted list iteration loop over the list named by `base`
// (an addressable place or a materialized temp), copying each element into the loop
// var. Split from forInList so a fresh iterable can be materialized and dropped around
// it.
func (e *emitter) forInListBody(n *ast.ForStmt, lt *types.List, base string) {
	nonPOD := e.containsRef(lt.Elem)
	iv := e.freshName("i")
	ct := e.ctype(lt.Elem)
	e.line(fmt.Sprintf("for (size_t %s = 0; %s < zrt_list_len(&(%s)); %s++) {", iv, iv, base, iv))
	e.indent++
	e.pushScope()
	cv := e.declareName(n.Var)
	// A non-POD loop var registers a per-iteration drop (registerDrop below), so the
	// scope must record a mark even when the body owns no teardown — otherwise the
	// element copy's release is deferred to function end (the same stack slot re-pushed
	// each iteration → multi-release of the last element and a leak of the earlier ones).
	e.openScope(nonPOD || e.subtreeTeardown(n.Body.Stmts), true)
	slot := fmt.Sprintf("(*(%s*)zrt_list_at(&(%s), %s))", ct, base, iv)
	rhs := slot
	if nonPOD {
		rhs = e.fieldCopy(lt.Elem, slot)
	}
	e.line(fmt.Sprintf("%s = %s;", e.localDecl(lt.Elem, cv), rhs))
	if nonPOD {
		e.registerDrop(cv, lt.Elem, n)
	}
	for _, s := range n.Body.Stmts {
		e.stmt(s)
	}
	if n.Mut {
		// a POD element edited in place is written straight back to its slot (a bit copy;
		// no element teardown, so no drop/leak concern).
		e.line(fmt.Sprintf("%s = %s;", slot, cv))
	}
	e.closeScope()
	e.popScope()
	e.indent--
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
	e.used = e.topLevelNames()
}

// topLevelNames is the set of every top-level mangled C name: function and type
// instances (their Mangled name), plus module-constant globals ("zg_"+Name). It is
// the one source shared by resetUsed's per-function seeding and reservedTopLevel's
// carrier-allocation reservation. Module-constant globals share the file scope with
// every body, so a body-local binding must not pick one of their names (Phase 1g S3).
func (e *emitter) topLevelNames() map[string]bool {
	r := map[string]bool{}
	for _, inst := range e.prog.Funcs {
		r[inst.Mangled] = true
	}
	for _, ti := range e.prog.Types {
		r[ti.Mangled] = true
	}
	for _, g := range e.prog.Inits {
		for _, b := range g.Consts {
			r["zg_"+b.Name] = true
		}
	}
	return r
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

// systemError reports a state the seed cannot classify — not the programmer's mistake but
// the SEED's. It is tier 3 of the contract in README.md, and it exists because the
// alternative is what these sites used to do: fall through to "0" and emit C naming
// something nothing declared, after which cc reports a real error against a file under
// .zerg-cache that the programmer cannot open.
//
// It returns a placeholder so the pass keeps walking and one run reports everything it
// found; the DIAGNOSTIC is what stops the C from being written. The rule it enforces: the
// seed may refuse a program, and it may fail, but it may never be wrong quietly.
func (e *emitter) systemError(at ast.Node, format string, args ...any) string {
	span := token.Span{}
	if at != nil {
		span = at.Span()
	}
	e.diags.Add(span, "SystemError: "+format, args...)
	return "0"
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
		return e.strLiteral(n.Value)
	case *ast.RawStrLit:
		// A raw string r"…" carries no escapes; lower its DECODED content (never the
		// surface .Text, which keeps the r"…" delimiters for fmt only) as a C string.
		return e.strLiteral(n.Value)
	case *ast.RuneLit:
		// A rune is an int32_t code point (cType maps types.Rune). Lower the DECODED
		// scalar, never the surface lexeme, so 'a' becomes 97 not "'a'".
		return strconv.Itoa(int(n.Value))
	case *ast.ByteLit:
		// A byte is a uint8_t octet (cType maps types.Byte); lower the decoded value.
		return strconv.Itoa(int(n.Value))
	case *ast.NilLit:
		return "0"
	case *ast.Ident:
		if sym, ok := e.info.Refs[n]; ok && sym.Kind == sema.SymVariant {
			return e.constructVariant(n, nil, sym.Variant.Name)
		}
		// A bare function name used as a VALUE (sema records it here) is a function
		// value, which the seed does not carry — same boundary as a closure below.
		// Without this the name would spell its own mangled C symbol and bind into a
		// `void` local, which only fails later as cc noise about generated code.
		if name, ok := e.info.FuncValues[n]; ok {
			return e.fnValue(name)
		}
		// a `mut &x` parameter is pointer storage: every mention reads through it.
		if e.identIsByRef(n) {
			return "(*" + e.resolve(n.Name) + ")"
		}
		return e.resolve(n.Name)
	case *ast.Unary:
		if n.Op == token.Minus {
			if s, ok := e.checkedNeg(e.cur.ExprType(e.info, n), n.X, e.expr(n.X)); ok {
				return s
			}
		}
		return fmt.Sprintf("(%s%s)", unaryOp(n.Op), e.expr(n.X))
	case *ast.Binary:
		if md, ok := e.cur.OpCalls[n]; ok {
			// The right operand is a by-value ARGUMENT the impl method consumes (drops), so an
			// lvalue operand is copied (retain/deep-copy) exactly as the plain call path copies
			// its args — otherwise a non-POD/boxed operand is double-freed when the callee
			// releases its param (DESIGN-refcount §7 risk 8). The left operand is the borrowed
			// receiver (never dropped by the callee), so it is passed raw. POD operands copy with
			// a bare `=`, so an existing derived comparison stays byte-identical.
			return fmt.Sprintf("%s(%s, %s)", md.Mangled, e.expr(n.L), e.copyValue(e.cur.ExprType(e.info, n.R), n.R))
		}
		if n.Op == token.In {
			// `v in lo..hi` range membership lowers to an inline bounds test.
			if s, ok := e.rangeMembership(n); ok {
				return s
			}
		}
		// `str` is not a native C operand: '+' concatenates through the runtime and a
		// comparison goes through strcmp (see emit_str.go).
		if s, ok := e.strBinary(n); ok {
			return s
		}
		if s, ok := e.checkedArith(e.cur.ExprType(e.info, n), n.Op, e.expr(n.L), e.expr(n.R)); ok {
			return s
		}
		return fmt.Sprintf("(%s %s %s)", e.expr(n.L), binaryOp(n.Op), e.expr(n.R))
	case *ast.Call:
		return e.call(n)
	case *ast.Field:
		// a qualified nullary enum variant value `E.Green`: the enum name is a value
		// namespace, so this is the variant value, not a struct field access. The base
		// identifier resolves to the enum TYPE symbol, which a struct-typed value never does.
		if id, ok := n.X.(*ast.Ident); ok {
			if sym, ok := e.info.Refs[id]; ok && sym.Kind == sema.SymType {
				return e.constructVariant(n, nil, n.Name)
			}
		}
		// `mod.f` taken as a value rather than called: the Field analogue of the bare
		// function name above, and the same value — a module flattens into one program, so
		// the resolved merged name is what it holds.
		if key, ok := e.info.NsFuncValues[n]; ok {
			return e.fnValue(key)
		}
		if s, ok := e.namespaceMemberValue(n); ok {
			return s
		}
		return e.expr(n.X) + ".zg_" + n.Name
	case *ast.Bracket:
		if e.info.Brackets[n].Kind == sema.BracketIndex && len(n.Elems) == 1 {
			// a list[T] index reads the element through the runtime (aborting on OOB =
			// IndexError) and copies it out to an owned value; an array/ptr base keeps the
			// native `base[idx]`.
			if lt, ok := e.cur.ExprType(e.info, n.Base).(*types.List); ok {
				return e.listIndex(n, lt)
			}
			return e.expr(n.Base) + "[" + e.expr(n.Elems[0]) + "]"
		}
		// sizeof[T] / alignof[T]: a compile-time uint from C's sizeof / _Alignof of the
		// type argument (recorded on the bracket by sema).
		if id, ok := n.Base.(*ast.Ident); ok && sema.IsSizeofBuiltin(id.Name) {
			if args := e.info.Brackets[n].Args; len(args) == 1 {
				op := "sizeof"
				if id.Name == "alignof" {
					op = "_Alignof"
				}
				return fmt.Sprintf("((uint64_t)%s(%s))", op, e.ctype(args[0]))
			}
		}
		return e.systemError(n, "no lowering for this call")
	case *ast.ListLit:
		// A list literal in fixed-array position ([int; N] = [a, b, …]) lowers to a C
		// array initializer, which is what the surrounding binding's array type consumes.
		// A list[T] value lowers to a statement-expression that builds a zrt_list; a
		// set[T] value has no runtime yet and is gated.
		if t, ok := e.cur.ExprType(e.info, n).(*types.List); ok {
			return e.listLit(n, t)
		}
		if t, ok := e.info.ExprTypes[n]; ok {
			if _, ok := t.(*types.Set); ok {
				e.diags.Add(n.Span(), "a set value is not yet supported")
				return "0"
			}
		}
		return "{" + e.exprList(n.Elems) + "}"
	case *ast.ListFill:
		// The fill form [v; N] in list position builds a zrt_list of N copies; an array
		// (or set) target has no runtime lowering yet and stays gated.
		if t, ok := e.cur.ExprType(e.info, n).(*types.List); ok {
			return e.listFill(n, t)
		}
		e.diags.Add(n.Span(), "a fill-form list literal is not yet supported")
		return "0"
	case *ast.MapLit:
		// map literals are not supported by the minimized bootstrap (the self-host
		// compiler uses none).
		e.diags.Add(n.Span(), "a map literal is not yet supported")
		return "0"
	case *ast.Range:
		return e.rangeValue(n)
	case *ast.CmdLit:
		e.diags.Add(n.Span(), "a command literal is not yet supported")
		return "0"
	case *ast.FCmd:
		e.diags.Add(n.Span(), "an interpolating command literal is not yet supported")
		return "0"
	case *ast.MatchExpr:
		return e.lowerMatch(n)
	case *ast.IfExpr:
		return e.ifExprValue(n)
	case *ast.Block:
		return e.blockExprValue(n)
	case *ast.TupleLit:
		return e.tupleLit(n)
	case *ast.TupleIndex:
		return e.tupleIndex(n)
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
	case *ast.IsExpr:
		// `err is ValueError` (and the other built-in error kinds) dispatches on the Err's
		// kind tag — the documented way to distinguish an erased error (docs/code/errors.md,
		// "Handling errors by type"). It lowers to a kind comparison on the runtime zrt_err.
		if kind, ok := sema.ErrKind(n.TypeName); ok && isErrType(e.cur.ExprType(e.info, n.X)) {
			return fmt.Sprintf("((%s).kind == %d)", e.expr(n.X), kind)
		}
		// Any other `x is T` type test parses and type-checks (yielding bool) but has no
		// backend lowering yet — a general runtime type test needs type tags the MVP does
		// not carry. Gate it as a normal user-facing Phase diagnostic rather than letting it
		// fall to the internal-error backstop below.
		e.diags.Add(x.Span(), "the `is` type test is not yet supported")
		return "0"
	case *ast.FnExpr:
		// A closure used as a value: first-class function values are not supported by the
		// minimized bootstrap (the self-host compiler uses none).
		e.diags.Add(n.Span(), "a closure used as a value is not yet supported")
		return "0"
	default:
		// The load-bearing anti-silence net (SLICE 8): every expression node the backend
		// can lower has an explicit case above. Anything reaching here is a node the emit
		// pass does not handle, which previously lowered to a silent "0" that miscompiles.
		// Fail loudly instead — Emit discards the output while diags are non-empty. A
		// working example/golden that trips this means a real case was lost; add it back.
		e.diags.Add(x.Span(), "internal: unsupported expression node %T", x)
		return "0"
	}
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

// lowerMatch lowers a match to a nested C ternary. A trivial subject — a name/field or
// a scalar/string literal — is side-effect-free and cheap to repeat, so it is spliced
// verbatim into every arm (the historic byte-identical lowering, 07_match). A
// non-trivial subject (a call, a composite producer) is hoisted into a single temp so
// it is evaluated exactly once and, when non-POD, owned and released once instead of
// re-evaluated and leaked. The last arm is an unguarded catch-all, forming the final
// else.
func (e *emitter) lowerMatch(m *ast.MatchExpr) string {
	subjT := e.cur.ExprType(e.info, m.Subject)
	gt := e.cur.ExprType(e.info, m)
	if isTrivialMatchSubject(m.Subject) {
		return e.lowerMatchArms(m, e.expr(m.Subject), subjT, gt)
	}
	return e.lowerMatchHoisted(m, subjT, gt)
}

// lowerMatchArms builds the nested ternary over a match's arms, referencing subj (a
// name, a literal, or a hoisted temp) in every arm's test and body. Each arm body flows
// through copyValue against the match's result type gt, so a body that names still-owned
// storage (an enum payload binding, a borrowed variable, a field) is retained before it
// is consumed as owned — exactly as blockValueInto does for an if/block expression.
func (e *emitter) lowerMatchArms(m *ast.MatchExpr, subj string, subjT, gt sema.Type) string {
	n := len(m.Arms)
	result := e.armValue(m.Arms[n-1], subj, subjT, gt)
	for i := n - 2; i >= 0; i-- {
		test, body := e.armTestAndValue(m.Arms[i], subj, subjT, gt)
		result = fmt.Sprintf("(%s ? %s : %s)", test, body, result)
	}
	return result
}

// lowerMatchHoisted evaluates a non-trivial subject ONCE into a temp, then references
// the temp in every arm — so a side-effecting subject (a call) runs exactly once and a
// non-POD producer subject is owned by the temp and released at scope exit rather than
// leaking. It wraps the arms in a GNU statement-expression, the same value mechanism the
// if-/block-expression lowerings use; because every example match has a name subject,
// none reaches this path and their C stays byte-identical.
func (e *emitter) lowerMatchHoisted(m *ast.MatchExpr, subjT, gt sema.Type) string {
	res := ""
	if e.ctype(gt) != "void" {
		res = e.freshName("match")
	}
	need := e.containsRef(subjT)
	body := e.capture(func() {
		e.openScope(need, false)
		tmp := e.freshName("subj")
		e.line(fmt.Sprintf("%s = %s;", e.localDecl(subjT, tmp), e.copyValue(subjT, m.Subject)))
		e.registerDrop(tmp, subjT, m.Subject)
		arms := e.lowerMatchArms(m, tmp, subjT, gt)
		if res != "" {
			e.line(fmt.Sprintf("%s = %s;", res, arms))
		} else {
			e.line(arms + ";")
		}
		e.closeScope()
	})
	return e.wrapStmtExpr(gt, res, body)
}

// isTrivialMatchSubject reports whether a match subject is side-effect-free and cheap to
// reference repeatedly, so it can be spliced into every arm rather than hoisted: a bare
// name, a field access over such a subject, or a scalar/string literal. A call or a
// composite producer (a tuple/list/map literal) is NOT trivial — repeating it would
// re-run its effects and re-build its value — and is hoisted instead.
func isTrivialMatchSubject(x ast.Expr) bool {
	switch t := x.(type) {
	case *ast.Ident,
		*ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.NilLit,
		*ast.StrLit, *ast.RawStrLit, *ast.RuneLit, *ast.ByteLit:
		return true
	case *ast.Field:
		return isTrivialMatchSubject(t.X)
	}
	return false
}

// --- expression-as-value (if-expr / block-expr) -------------------------------

// blockValueInto emits a value-producing block's statements, assigning its value —
// the last statement when it is an expression, coerced into the target type ty — to
// tmp. Every leading statement is emitted for effect. A block that ends in a
// non-expression statement leaves tmp untouched (its zero value). It is the shared
// core of the if-expression and block-expression lowerings.
func (e *emitter) blockValueInto(tmp string, ty sema.Type, b *ast.Block) {
	// A void-typed block/branch (ctype→void) carries no value: emit EVERY statement,
	// including a trailing expression, for effect only — never assign into tmp (the
	// tmp is not even declared on the void path, see blockExprValue/ifExprValue). This
	// is the shared degrade for a statement-position bare block (completeness iteration
	// 3, F1).
	void := e.ctype(ty) == "void"
	stmts := b.Stmts
	var last ast.Expr
	if !void {
		if k := len(stmts); k > 0 {
			if es, ok := stmts[k-1].(*ast.ExprStmt); ok {
				last, stmts = es.X, stmts[:k-1]
			}
		}
	}
	e.pushScope()
	for _, s := range stmts {
		e.stmt(s)
	}
	if last != nil {
		val := e.wrapValue(ty, e.cur.ExprType(e.info, last), e.copyValue(ty, last))
		e.line(fmt.Sprintf("%s = %s;", tmp, val))
	}
	e.popScope()
}

// wrapStmtExpr wraps a captured body as a GNU statement-expression; an empty res
// means a void body that yields no value.
func (e *emitter) wrapStmtExpr(gt sema.Type, res, body string) string {
	if res == "" {
		return fmt.Sprintf("({ %s})", body)
	}
	return fmt.Sprintf("({ %s %s; %s%s; })", e.ctype(gt), res, body, res)
}

// ifExprValue lowers an if-EXPRESSION to a GNU statement-expression: a fresh temp
// receives the taken branch's value, since each branch body and the mandatory else
// is a value-producing block (sema requires them all one type). This is the same
// value mechanism guard/`??` use, so `x := if c { a } else { b }` — and a `return`
// of an if-expression — becomes a value without a new AST shape.
//
// A void-typed if in statement position produces no value: declaring `void res;`
// is uncompilable, so with res empty we degrade to a plain if that runs each branch
// for effect with no temp and no trailing value (completeness iteration 3, F1).
func (e *emitter) ifExprValue(n *ast.IfExpr) string {
	gt := e.cur.ExprType(e.info, n)
	res := ""
	if e.ctype(gt) != "void" {
		res = e.freshName("if")
	}
	body := e.capture(func() {
		if anyIfBind(n.Branches) {
			e.ifExprChain(n.Branches, n.Else, res, gt)
			return
		}
		for i, br := range n.Branches {
			kw := "if"
			if i > 0 {
				kw = "} else if"
			}
			e.line(fmt.Sprintf("%s (%s) {", kw, e.expr(br.Cond)))
			e.blockValueInto(res, gt, br.Body)
		}
		e.line("} else {")
		e.blockValueInto(res, gt, n.Else)
		e.line("}")
	})
	return e.wrapStmtExpr(gt, res, body)
}

// ifExprChain lowers an if-EXPRESSION chain that contains a binding head into the
// captured body of ifExprValue. It mirrors ifChain but assigns each taken branch's
// value into res (via blockValueInto) rather than running the body for effect; an
// if-expression always has a trailing else, so the tail always terminates in one.
func (e *emitter) ifExprChain(branches []ast.IfBranch, elseB *ast.Block, res string, gt sema.Type) {
	br := branches[0]
	tail := func() {
		e.line("} else {")
		if len(branches) > 1 {
			e.indent++
			e.ifExprChain(branches[1:], elseB, res, gt)
			e.indent--
		} else {
			e.blockValueInto(res, gt, elseB)
		}
		e.line("}")
	}
	if br.Bind == "" {
		e.line(fmt.Sprintf("if (%s) {", e.expr(br.Cond)))
		e.blockValueInto(res, gt, br.Body)
		tail()
		return
	}
	optT := e.cur.ExprType(e.info, br.Cond)
	e.line("{")
	e.indent++
	e.pushScope()
	// The evaluated optional temp OWNS its value (see ifChain): a non-POD optional is
	// copyValue'd and its teardown scheduled locally, and the bound name is a BORROW of the
	// payload. The taken branch's value escapes into `res`, but blockValueInto copyValue's it
	// (retains/deep-copies a borrowed payload), so `res` owns an independent copy that survives
	// the temp's own drop at the block's exit — no double-free, no use-after-free. A POD
	// optional owns nothing, so no mark/drop is emitted and the block stays byte-identical.
	e.openScope(e.containsRef(optT), false)
	tmp := e.freshName("ifopt")
	e.line(fmt.Sprintf("%s = %s;", e.localDecl(optT, tmp), e.copyValue(optT, br.Cond)))
	e.registerDrop(tmp, optT, br.Cond)
	e.line(fmt.Sprintf("if (%s) {", e.optPresentTest(optT, tmp)))
	e.pushScope()
	cname := e.declareName(br.Bind)
	e.line(fmt.Sprintf("%s = %s;", e.localDecl(optElem(optT), cname), e.optUnwrapValue(optT, tmp)))
	e.blockValueInto(res, gt, br.Body)
	e.popScope()
	tail()
	e.closeScope()
	e.popScope()
	e.indent--
	e.line("}")
}

// blockExprValue lowers a block-EXPRESSION to a GNU statement-expression whose value
// is the block's last statement, so `x := { s1; s2; v }` is a value (GRAMMAR: a
// block's value is its last statement).
//
// A void-typed block in statement position produces no value: declaring `void res;`
// is uncompilable, so with res empty we degrade to a bare statement-expression that
// runs the block's statements for effect with no temp and no trailing value. This
// covers a bare `{ … }` (and a nested one) used as a statement (completeness
// iteration 3, F1).
func (e *emitter) blockExprValue(b *ast.Block) string {
	gt := e.cur.ExprType(e.info, b)
	res := ""
	if e.ctype(gt) != "void" {
		res = e.freshName("blk")
	}
	body := e.capture(func() {
		e.blockValueInto(res, gt, b)
	})
	return e.wrapStmtExpr(gt, res, body)
}

// armValue emits an arm's body with the pattern's names bound to the subject. The body
// is copied against the match's result type gt, so an arm yielding borrowed non-POD
// storage is retained before it is consumed as owned (no double-free / UAF).
func (e *emitter) armValue(arm ast.MatchArm, subj string, subjT, gt sema.Type) string {
	e.pushScope()
	e.patternWalk(arm.Pat, subj, subjT, e.scopes[len(e.scopes)-1])
	v := e.copyValue(gt, arm.Body)
	e.popScope()
	return v
}

// armTestAndValue emits an arm's match test and body, with the pattern's names in
// scope for both the guard and the body. The body is copied against the match's result
// type gt (see armValue).
func (e *emitter) armTestAndValue(arm ast.MatchArm, subj string, subjT, gt sema.Type) (test, body string) {
	e.pushScope()
	test = e.patternWalk(arm.Pat, subj, subjT, e.scopes[len(e.scopes)-1])
	if arm.Guard != nil {
		g := e.expr(arm.Guard)
		if test == "" {
			test = g
		} else {
			test = fmt.Sprintf("(%s && %s)", test, g)
		}
	}
	body = e.copyValue(gt, arm.Body)
	e.popScope()
	if test == "" {
		test = "1" // a non-last unguarded catch-all is rejected by sema
	}
	return test, body
}

// patternWalk recursively lowers a pattern against the C place-expression `place`
// (which locates the sub-value of type placeT within the subject), recording each
// name binding into `scope` and returning the boolean C test — "" when the pattern is
// irrefutable. It descends structurally: a tuple element is `place.f<i>`, a struct
// field is `place.zg_<field>`, an array element is `place[i]`, and a variant payload
// is `place.u.<V>.f<i>`. It allocates NO compiler temporary, so a program's `used`
// name counts are unchanged and the historic single-level lowerings (07_match)
// stay byte-identical.
func (e *emitter) patternWalk(pat ast.Pattern, place string, placeT sema.Type, scope map[string]string) string {
	switch p := pat.(type) {
	case *ast.LitPattern:
		lit := e.expr(p.Lit)
		if p.Neg {
			lit = "(-" + lit + ")"
		}
		if placeT == sema.Str {
			return fmt.Sprintf("(strcmp(%s, %s) == 0)", place, lit)
		}
		return fmt.Sprintf("(%s == %s)", place, lit)
	case *ast.VariantPattern:
		// `Left(v)` / `Right(e)` on an Either/Result carrier: the tag selects the side and
		// the sub-pattern binds it (a Result's `Right(e)` binds the erased Err for an
		// `is`-dispatch). Handled before the enum path since an Either is not an enum.
		if _, ok := placeT.(*types.Either); ok {
			return e.eitherPatternWalk(p, place, placeT, scope)
		}
		// The arm matches when the discriminant equals this variant's tag AND every
		// payload sub-pattern matches. Each payload element i is located at the union
		// place `place.u.<V>.f<i>` and typed from the specialized enum instance, then
		// walked recursively — so a literal (`Leaf(0)`) or nested (`Wrap(A(v))`) payload
		// sub-pattern contributes its own test and bindings instead of being dropped.
		tests := []string{fmt.Sprintf("(%s.tag == %d)", place, e.variantTag(placeT, p.Name))}
		var payload []sema.Type
		var boxed []bool
		if ti := e.prog.EnumInstance(placeT); ti != nil {
			if v, ok := ti.Variant(p.Name); ok {
				payload, boxed = v.Payload, v.Boxed
			}
		}
		for i, el := range p.Elems {
			et := sema.Type(types.Unknown)
			if i < len(payload) {
				et = payload[i]
			}
			sub := fmt.Sprintf("%s.u.%s.f%d", place, p.Name, i)
			// A BOXED payload slot (S1) holds a `void*` cell: bind/match reads THROUGH the box
			// (zrt_ref_payload), so a nested pattern (`Add(Add(_,_), _)`) recurses through
			// another deref and a name binding borrows the payload (no retain — copyValue at a
			// use site retains if needed).
			if i < len(boxed) && boxed[i] {
				sub = e.boxDeref(sub, et)
			}
			if t := e.patternWalk(el, sub, et, scope); t != "" {
				tests = append(tests, t)
			}
		}
		return strings.Join(tests, " && ")
	case *ast.NamePattern:
		if res, ok := e.info.Patterns[p]; ok && res.Kind == sema.NameVariant {
			return fmt.Sprintf("(%s.tag == %d)", place, e.variantTag(placeT, p.Name))
		}
		scope[p.Name] = place
		return ""
	case *ast.TuplePattern:
		return e.tuplePatternWalk(p, place, placeT, scope)
	case *ast.StructPattern:
		return e.structPatternWalk(p, place, placeT, scope)
	case *ast.AsPattern:
		test := e.patternWalk(p.Inner, place, placeT, scope)
		scope[p.Name] = place
		return test
	case *ast.RangeArm:
		lo := e.expr(p.Lo)
		if p.Hi == nil {
			return fmt.Sprintf("(%s >= %s)", place, lo)
		}
		op := "<"
		if p.Inclusive {
			op = "<="
		}
		return fmt.Sprintf("(%s >= %s && %s %s %s)", place, lo, place, op, e.expr(p.Hi))
	case *ast.ListPattern:
		return e.listPatternWalk(p, place, placeT, scope)
	case *ast.OrPattern:
		return e.orPatternWalk(p, place, placeT, scope)
	case *ast.WildPattern:
		// '_' matches anything and binds nothing: an irrefutable test.
		return ""
	}
	// The load-bearing anti-silence net (mirrors expr's): every pattern node the
	// backend lowers has an explicit case above. A node reaching here would formerly
	// fall to an empty test — an always-true match that silently miscompiles the arm.
	// Fail loudly instead so Emit discards the output while diags are non-empty.
	e.diags.Add(pat.Span(), "internal: unsupported pattern node %T", pat)
	return ""
}

// orPatternWalk lowers an or-pattern 'a | b | c' (GRAMMAR group 6): the arm matches
// when any alternative does, so the test is the alternatives' tests joined by '||'.
// Every alternative is walked against the same place, so a variant/literal or-pattern
// without bindings (Red|Green, 1|2|3) lowers to a plain disjunction and stays
// byte-identical to a hand-written guard. An alternative that would bind a name is
// gated cleanly: with different alternatives binding different carriers, a single
// scope entry cannot describe the value, so it is not yet supported. An irrefutable
// alternative makes the whole or-pattern always match (empty test).
func (e *emitter) orPatternWalk(p *ast.OrPattern, place string, placeT sema.Type, scope map[string]string) string {
	var tests []string
	for _, alt := range p.Alts {
		probe := map[string]string{}
		t := e.patternWalk(alt, place, placeT, probe)
		if len(probe) > 0 {
			e.diags.Add(p.Span(), "an or-pattern with bindings is not yet supported")
			return ""
		}
		if t == "" {
			return "" // an irrefutable alternative subsumes the rest
		}
		tests = append(tests, t)
	}
	if len(tests) == 0 {
		return ""
	}
	return "(" + strings.Join(tests, " || ") + ")"
}

// tuplePatternWalk destructures a tuple pattern: each element i is located at the
// carrier field `place.f<i>` and typed from the subject tuple's element types. The
// arm matches when every element sub-test does (all-irrefutable yields "").
func (e *emitter) tuplePatternWalk(p *ast.TuplePattern, place string, placeT sema.Type, scope map[string]string) string {
	tup, _ := placeT.(*types.Tuple)
	var tests []string
	for i, sub := range p.Elems {
		et := sema.Type(types.Unknown)
		if tup != nil && i < len(tup.Elems) {
			et = tup.Elems[i]
		}
		if t := e.patternWalk(sub, fmt.Sprintf("%s.f%d", place, i), et, scope); t != "" {
			tests = append(tests, t)
		}
	}
	return strings.Join(tests, " && ")
}

// structPatternWalk destructures a struct pattern: each field is located at
// `place.zg_<field>` (the typedef field naming) and typed from the specialized struct
// instance (so a generic field reads its concrete type). A shorthand `{x}` binds the
// field's value under its own name; a `{x: sub}` recurses. A trailing `..` ignores the
// rest and imposes no test.
func (e *emitter) structPatternWalk(p *ast.StructPattern, place string, placeT sema.Type, scope map[string]string) string {
	si := e.prog.StructInstance(placeT)
	st, _ := placeT.(*types.Struct)
	fieldType := func(name string) sema.Type {
		if si != nil {
			for _, f := range si.Fields {
				if f.Name == name {
					return f.Type
				}
			}
		}
		if st != nil && st.Def != nil && st.Def.Struct != nil {
			for i := range st.Def.Struct.Fields {
				if st.Def.Struct.Fields[i].Name == name {
					return st.Def.Struct.Fields[i].Type
				}
			}
		}
		return types.Unknown
	}
	var tests []string
	for _, f := range p.Fields {
		fplace := place + ".zg_" + f.Name
		if f.Pat == nil {
			scope[f.Name] = fplace
			continue
		}
		if t := e.patternWalk(f.Pat, fplace, fieldType(f.Name), scope); t != "" {
			tests = append(tests, t)
		}
	}
	return strings.Join(tests, " && ")
}

// listPatternWalk destructures a list pattern. A fixed array `[T; N]` subject lowers
// each positional element to `place[i]`; a rest element, or a general `list[T]`
// subject (whose runtime is not yet implemented — the A8 cut), is failed cleanly so
// it can never silently mis-index.
func (e *emitter) listPatternWalk(p *ast.ListPattern, place string, placeT sema.Type, scope map[string]string) string {
	arr, ok := placeT.(*types.Array)
	if !ok {
		e.diags.Add(p.Span(), "a list pattern is not yet supported")
		return ""
	}
	var tests []string
	for i, el := range p.Elems {
		if el.Rest || el.Pat == nil {
			e.diags.Add(p.Span(), "a rest '..' element in a list pattern is not yet supported")
			return ""
		}
		if t := e.patternWalk(el.Pat, fmt.Sprintf("%s[%d]", place, i), arr.Elem, scope); t != "" {
			tests = append(tests, t)
		}
	}
	return strings.Join(tests, " && ")
}

// variantTag returns the discriminant of a named variant of an enum subject, read
// from the specialized enum instance (0 when the type or variant is not found, a
// case a checked program does not reach).
// eitherPatternWalk lowers a `Left(v)` / `Right(e)` pattern against an Either/Result
// carrier place: tag 0 is the Left/Ok side, tag 1 the Right/Err side. It binds the
// sub-pattern at the carrier's corresponding member — `.ok`/`.left` for Left, `.err`
// (Result) or `.right` (Either) for Right — so a Result's `Right(e)` binds the erased
// Err ready for an `is`-dispatch on its kind (docs/code/errors.md).
func (e *emitter) eitherPatternWalk(p *ast.VariantPattern, place string, placeT sema.Type, scope map[string]string) string {
	ei := placeT.(*types.Either)
	c, ok := e.carrierFor(placeT)
	if !ok {
		return e.systemError(p, "no carrier for %s in a pattern", placeT)
	}
	var tag int
	var member string
	var elemT sema.Type
	if p.Name == "Left" {
		tag, member, elemT = 0, c.okField(), ei.Left
	} else {
		tag, elemT = 1, ei.Right
		if c.kind == carrierResult {
			member = "err"
		} else {
			member = "right"
		}
	}
	test := fmt.Sprintf("(%s.tag == %d)", place, tag)
	if len(p.Elems) == 1 {
		if t := e.patternWalk(p.Elems[0], place+"."+member, elemT, scope); t != "" {
			test = fmt.Sprintf("(%s && %s)", test, t)
		}
	}
	return test
}

func (e *emitter) variantTag(subjT sema.Type, name string) int {
	if ti := e.prog.EnumInstance(subjT); ti != nil {
		if v, ok := ti.Variant(name); ok {
			return v.Tag
		}
	}
	return 0
}

func (e *emitter) call(n *ast.Call) string {
	// `close(ch)` reaching the expression dispatch is a `close` where a statement cannot go.
	// a primitive conversion `T(x)`.
	if s, ok := e.convCallEmit(n); ok {
		return s
	}
	// the enum discriminant reverse `E.of(n) -> E?` (GRAMMAR group 7).
	if s, ok := e.enumOfEmit(n); ok {
		return s
	}
	// a built-in error constructor `ValueError("msg")` and the `err.message()` accessor
	// (GRAMMAR group 8, docs/code/errors.md).
	if s, ok := e.errCallEmit(n); ok {
		return s
	}
	// a str<->list bridge: `str(bytes)`, `list[byte](s)`, etc.
	if s, ok := e.strBridgeEmit(n); ok {
		return s
	}
	if s, ok := e.listMethodEmit(n); ok {
		return s
	}
	if s, ok := e.builtinCallEmit(n); ok {
		return s
	}
	// a call THROUGH a value that holds a function, rather than a call OF a function
	if s, ok := e.fnValueCall(n); ok {
		return s
	}
	if s, ok := e.namespaceCallEmit(n); ok {
		return s
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
	byref := e.calleeByRefArgs(id)
	// A NAMED argument selects its parameter by name (docs/code/functions.md), so the
	// emitted order is the PARAMETER's and not the source's. This loop read `n.Args`
	// positionally and never looked at `a.Name`, so `f(b: 1, a: 5)` emitted `zg_f(1, 5)`
	// — the arguments swapped, silently, in a form sema had already bound correctly.
	callArgs := n.Args
	reordered := e.namedArgSlots(id, n)
	if reordered != nil {
		callArgs = reordered
	}
	var args strings.Builder
	for i, a := range callArgs {
		if i > 0 {
			args.WriteString(", ")
		}
		// a `mut &` parameter binds the caller's variable itself — pass its address, and
		// never a copy, which is the whole point of the writeback.
		if byref != nil && i < len(byref) && byref[i] {
			args.WriteString(e.addressOf(a.Value))
			continue
		}
		// a by-value argument is copied (retain / deep copy) when it names existing
		// storage, so the callee's own holder is independent; POD args are unchanged. The
		// copy is then coerced into the declared parameter type, so a value passed to an
		// optional-carrier parameter is wrapped into the carrier (Some/None) rather than
		// stored raw in the `tag` slot. wrapValue passes an equal-typed argument through
		// unchanged, so a non-coercing call stays byte-identical.
		argT := e.cur.ExprType(e.info, a.Value)
		args.WriteString(e.wrapValue(e.calleeParamType(id, i), argT, e.copyValue(argT, a.Value)))
	}
	// A5: backfill trailing omitted parameters with their (constant) default
	// expressions, in declaration order, so a `fn f(a, b = 10)` called as `f(1)`
	// still passes the default. A fully-applied call has no trailing defaults and
	// stays byte-identical.
	// namedArgSlots has already filled every parameter, defaults included, so there is
	// nothing trailing to backfill for a call that used one.
	provided := len(callArgs)
	if reordered == nil {
		for i, def := range e.trailingDefaults(id, provided) {
			if args.Len() > 0 || i > 0 {
				args.WriteString(", ")
			}
			args.WriteString(e.paramDefaultArg(id, provided+i, def))
		}
	}
	return fmt.Sprintf("%s(%s)", e.callTarget(n, id), args.String())
}

// namedArgSlots reorders a call's arguments into PARAMETER order, filling each omitted
// defaulted parameter, and returns nil for a call that names none — which is every call
// the seed emitted before this existed, so a positional call stays byte-identical.
//
// Sema already binds names to parameters (matchArgs) and reports a duplicate, an unknown
// name or a missing argument, so this only has to follow the same rule: positional
// arguments fill left to right, a named one goes to its own parameter, and what is left
// takes its declared default.
func (e *emitter) namedArgSlots(id *ast.Ident, n *ast.Call) []ast.Arg {
	if id == nil || !hasNamedArg(n.Args) {
		return nil
	}
	sym, ok := e.info.Refs[id]
	if !ok {
		return nil
	}
	fd, ok := sym.Decl.(*ast.FuncDecl)
	if !ok {
		return nil
	}
	return slotsByName(n.Args, len(fd.Params), func(i int) (string, ast.Expr) {
		return fd.Params[i].Name, fd.Params[i].Default
	})
}

// slotsByName is the rule both of them follow (docs/code/functions.md): positional
// arguments fill left to right, a named one goes to its own slot, and what is left takes
// its declared default. `decl(i)` answers the i-th slot's name and default.
//
// It answers nil when a slot ends up with neither an argument nor a default — sema has
// already reported that call, and emitting a hole for it would be worse than emitting the
// original order for code that is about to be discarded.
func slotsByName(args []ast.Arg, n int, decl func(i int) (string, ast.Expr)) []ast.Arg {
	slots := make([]ast.Arg, n)
	pos := 0
	for _, a := range args {
		if a.Name == "" {
			if pos < n {
				slots[pos] = a
			}
			pos++
			continue
		}
		for i := 0; i < n; i++ {
			if nm, _ := decl(i); nm == a.Name {
				slots[i] = ast.Arg{Value: a.Value}
			}
		}
	}
	for i := range slots {
		if slots[i].Value != nil {
			continue
		}
		_, def := decl(i)
		if def == nil {
			return nil
		}
		slots[i] = ast.Arg{Value: def}
	}
	return slots
}

// hasNamedArg reports whether any argument was passed by name. sema has its own copy for
// the same question; this package cannot see it.
func hasNamedArg(args []ast.Arg) bool {
	for _, a := range args {
		if a.Name != "" {
			return true
		}
	}
	return false
}

// trailingDefaults returns the default expressions for the parameters a call omits —
// the callee's declared parameters at index >= provided that carry a default (A5,
// FORK-A5: trailing positional constants). It returns nil for anything but a
// resolved function identifier call, or when no parameter is omitted, so a
// fully-applied or indirect call is unaffected and byte-identical.
func (e *emitter) trailingDefaults(id *ast.Ident, provided int) []ast.Expr {
	if id == nil {
		return nil
	}
	sym, ok := e.info.Refs[id]
	if !ok {
		return nil
	}
	fd, ok := sym.Decl.(*ast.FuncDecl)
	if !ok {
		return nil
	}
	return defaultsFrom(fd, provided)
}

// defaultsFrom returns the default expressions for the parameters at index >= provided.
// It stops at the first gap without one, because that is a sema error rather than
// something to backfill, and it answers nil when nothing is omitted — so a fully-applied
// call is unaffected. A direct call and a method call read it off the same node.
func defaultsFrom(fd *ast.FuncDecl, provided int) []ast.Expr {
	if provided >= len(fd.Params) {
		return nil
	}
	var out []ast.Expr
	for i := provided; i < len(fd.Params); i++ {
		if fd.Params[i].Default == nil {
			return nil
		}
		out = append(out, fd.Params[i].Default)
	}
	return out
}

// calleeParamType returns the i-th declared parameter type of a direct function call's
// callee, or nil when the callee is not a resolved plain function or has no such
// parameter (an indirect/method/namespace call, or an out-of-range index). The emitter
// coerces each argument into this type through wrapValue, so a value passed to an
// optional-carrier parameter is wrapped into the carrier. A generic callee's parameter is
// still an unsubstituted type variable here, which is not a carrier — wrapValue passes it
// through, so a generic call is byte-identical.
func (e *emitter) calleeParamType(id *ast.Ident, i int) sema.Type {
	if id == nil {
		return nil
	}
	sym, ok := e.info.Refs[id]
	if !ok || sym == nil {
		return nil
	}
	fd, ok := sym.Decl.(*ast.FuncDecl)
	if !ok {
		return nil
	}
	sig, ok := e.info.Funcs[fd.Name]
	if !ok || i >= len(sig.Params) {
		return nil
	}
	return sig.Params[i]
}

// paramDefaultArg renders a backfilled trailing parameter default (FORK-A5) coerced into
// the callee's declared parameter type. A defaulted optional parameter
// (`fn f(p: int? = 8080)`) must wrap its constant default into the carrier, exactly like a
// struct field default — a raw value in an optional-carrier parameter slot is a loud cc
// type error / silent miscompile. A non-carrier parameter (or an unresolved callee) is the
// byte-identical raw expression.
func (e *emitter) paramDefaultArg(id *ast.Ident, i int, def ast.Expr) string {
	pt := e.calleeParamType(id, i)
	if pt == nil {
		return e.expr(def)
	}
	return e.defaultWrap(pt, def)
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
	// The resolved merged name (which follows a one-level `import pub` re-export, Phase 1g
	// S2) is read from sema's NsMembers table when present, falling back to the direct
	// spelling; it keys both the call target and the function's by-ref parameter shape.
	key := sema.NamespaceMemberName(sym, id.Name, fld.Name)
	if k, ok := e.info.NsMembers[fld]; ok {
		key = k
	}
	// A `mut &` parameter of the resolved function takes the argument's ADDRESS, exactly
	// as a direct call does — sema already checked each such argument is a mut lvalue.
	byref := e.namespaceByRefArgs(key)
	var args strings.Builder
	for i, a := range n.Args {
		if i > 0 {
			args.WriteString(", ")
		}
		if i < len(byref) && byref[i] {
			args.WriteString(e.addressOf(a.Value))
		} else {
			args.WriteString(e.copyValue(e.cur.ExprType(e.info, a.Value), a.Value))
		}
	}
	// Backfill the trailing parameters the call omitted, from the member's own declared
	// defaults — the third and last call shape to read them off the declaration, beside a
	// direct call and a method call. Without it `cli.argument("O", "off", "help")` reached
	// cc as a three-argument call to a four-parameter function.
	if sig, ok := e.info.Funcs[key]; ok && sig != nil && sig.Decl != nil {
		for _, def := range defaultsFrom(sig.Decl, len(n.Args)) {
			if args.Len() > 0 {
				args.WriteString(", ")
			}
			args.WriteString(e.expr(def))
		}
	}
	// A non-generic member calls the bundled top-level function directly; a generic
	// member (e.g. `testing.assert_eq`) dispatches to the per-instance mangled name mono
	// recorded for this call site.
	target := e.prog.CallTarget(key)
	if m, ok := e.cur.Calls[n]; ok {
		target = m
	}
	return fmt.Sprintf("%s(%s)", target, args.String()), true
}

// namespaceByRefArgs reports, per argument position of a resolved namespace member call
// keyed by its merged name, whether the parameter is a `mut &` reference (so the argument
// passes by address, like a direct call). It returns nil when the function is unknown or
// declares no such parameter, leaving those calls byte-identical.
func (e *emitter) namespaceByRefArgs(key string) []bool {
	sig, ok := e.info.Funcs[key]
	if !ok || sig == nil || sig.Decl == nil {
		return nil
	}
	return byRefOf(sig.Decl)
}

// namespaceMemberValue lowers a non-call namespace member access `ns.member` used as
// a value — a cross-module module constant or binding — to the merged file-scope C
// global sema resolved it to (honoring a one-level `import pub` re-export). It
// returns false for an ordinary struct-field access, which the caller spells as
// `x.zg_field`.
func (e *emitter) namespaceMemberValue(n *ast.Field) (string, bool) {
	id, ok := n.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	sym, ok := e.info.Refs[id]
	if !ok || sym.Kind != sema.SymNamespace {
		return "", false
	}
	key, ok := e.info.NsMembers[n]
	if !ok {
		key = sema.NamespaceMemberName(sym, id.Name, n.Name)
	}
	return "zg_" + key, true
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
		return e.systemError(n, "unresolved call target")
	}
	return e.prog.CallTarget(id.Name)
}

// methodCall lowers a bound-method call 'recv.method(args)' to a by-value call of
// the resolved impl-method instance, passing the receiver first (B1).
func (e *emitter) methodCall(n *ast.Call, md *mono.MethodDispatch) string {
	field, _ := n.Callee.(*ast.Field)
	recv := e.expr(field.X)
	args := recv
	byref := e.methodByRefArgs(md)
	for i, a := range n.Args {
		// A `mut &` parameter binds the caller's variable itself — pass its address, and
		// never a copy, which is the whole point of the writeback. Passing the value here
		// handed a `zrt_list` to a `zrt_list*` parameter, which cc rejected.
		if i < len(byref) && byref[i] {
			args += ", " + e.addressOf(a.Value)
			continue
		}
		// A by-value argument is CONSUMED by the callee (its body registers the param's drop),
		// so an lvalue argument must be copied (retain/deep-copy) here or the callee's release
		// double-frees the caller's value — the enum analogue of the struct discipline, newly
		// load-bearing for a boxed/recursive arg (DESIGN-refcount §7 risk 8). A POD arg copies
		// with a bare `=`, so an existing method call stays byte-identical.
		args += ", " + e.copyValue(e.cur.ExprType(e.info, a.Value), a.Value)
	}
	// Backfill the trailing parameters the call omitted, from the method's own declared
	// defaults — the same rule a free call gets, read off the same node. Without it a
	// method could declare a default that no call site was ever allowed to leave out.
	for _, def := range e.methodTrailingDefaults(md, len(n.Args)) {
		args += ", " + e.expr(def)
	}
	return fmt.Sprintf("%s(%s)", md.Mangled, args)
}

// A method call reads its by-ref flags and its parameter defaults off the SAME node a
// direct call does — the declaration — through the same two helpers. It reaches that node
// differently, because a method call has no callee identifier to resolve: mono carries the
// declaration on the dispatch. The receiver is implicit and takes no slot in the
// declaration, so the indices line up with the call's own arguments and need no offset.
func (e *emitter) methodByRefArgs(md *mono.MethodDispatch) []bool {
	if md.Decl == nil {
		return nil
	}
	return byRefOf(md.Decl)
}

func (e *emitter) methodTrailingDefaults(md *mono.MethodDispatch, provided int) []ast.Expr {
	if md.Decl == nil {
		return nil
	}
	return defaultsFrom(md.Decl, provided)
}

// construct lowers a struct construction 'T(...)' to a C compound literal of the
// specialized struct type, with arguments in field-declaration order.
func (e *emitter) construct(n *ast.Call) string {
	t := e.cur.ExprType(e.info, n)
	name := e.ctype(t)
	si := e.prog.StructInstance(t)
	// A named argument selects its FIELD by name, exactly as it selects a parameter in a
	// call, so the emitted order is the declaration's. This read `n.Args` positionally, so
	// `P(y: 2, x: 1)` built `{2, 1}` — the fields swapped with no diagnostic.
	ctorArgs := n.Args
	reordered := e.namedFieldSlots(n)
	if reordered != nil {
		ctorArgs = reordered
	}
	var parts []string
	for i, a := range ctorArgs {
		parts = append(parts, e.fieldSlot(si, i, a.Value))
	}
	// A5: backfill trailing omitted fields with their (constant) default expressions,
	// in field-declaration order. A fully-specified construction has none and stays
	// byte-identical; a construction that named a field has every slot filled already.
	provided := len(ctorArgs)
	if reordered == nil {
		for j, def := range e.trailingFieldDefaults(n, provided) {
			parts = append(parts, e.fieldDefaultSlot(si, provided+j, def))
		}
	}
	return "((" + name + "){" + strings.Join(parts, ", ") + "})"
}

// namedFieldSlots is namedArgSlots for a struct construction: it reorders the arguments
// into FIELD-declaration order and fills each omitted defaulted field, answering nil for
// a construction that names none — so a positional one stays byte-identical.
func (e *emitter) namedFieldSlots(n *ast.Call) []ast.Arg {
	if !hasNamedArg(n.Args) {
		return nil
	}
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return nil
	}
	sym, ok := e.info.Refs[id]
	if !ok {
		return nil
	}
	sd, ok := sym.Decl.(*ast.StructDecl)
	if !ok {
		return nil
	}
	return slotsByName(n.Args, len(sd.Fields), func(i int) (string, ast.Expr) {
		return sd.Fields[i].Name, sd.Fields[i].Default
	})
}

// fieldSlot renders one struct-construction field value. A non-POD or boxed field
// (S1/S2) is copied (retain/deep-copy an lvalue, move a fresh producer) and coerced to
// the field type — for a boxed `Opt` field this allocates the nullable box (Some) or
// NULL (None) via wrapValue. This closes the pre-existing gap where a struct built from
// a BORROWED str/Ref variable did not retain (leak/double-free). A POD field stays the
// byte-identical raw expression, so an existing value struct construction is unchanged.
func (e *emitter) fieldSlot(si *mono.TypeInstance, i int, arg ast.Expr) string {
	ft, boxed := e.fieldTypeBoxed(si, i)
	if ft == nil {
		return e.expr(arg)
	}
	// A POD optional field (`int?`/`bool?`/`float?`) is a value CARRIER (`{tag, ok}`) even
	// though containsRef is false, so its argument must be wrapped (Some -> `{.tag=0,.ok=v}`
	// / nil -> `{.tag=1}`) exactly like a non-POD (`str?`) or boxed field — emitting the raw
	// value would land it in the carrier's `tag` slot and read back as absent. A non-POD or
	// boxed field already takes this branch via containsRef/boxed; a plain (non-carrier) POD
	// field stays the byte-identical raw expression.
	if boxed || e.containsRef(ft) || e.isOptCarrierType(ft) {
		et := e.cur.ExprType(e.info, arg)
		return e.wrapValue(ft, et, e.copyValue(et, arg))
	}
	return e.expr(arg)
}

// fieldDefaultSlot renders a backfilled struct-field default (FORK-A5) coerced into the
// field type. It mirrors fieldSlot, but because a default expression carries no recorded
// ExprType (checkConstDefault validates its shape only, so e.cur.ExprType is bad), it
// derives the value type through defaultWrap — so a defaulted optional field
// (`port: int? = 8080`, docs/surface/grammar.md's headline example) wraps its default into the
// carrier (`{tag, ok}`) instead of dropping the raw value into the `tag` slot, where it
// would read back as absent (a silent miscompile). A plain (non-carrier) POD field default
// stays the byte-identical raw expression.
func (e *emitter) fieldDefaultSlot(si *mono.TypeInstance, i int, def ast.Expr) string {
	ft, boxed := e.fieldTypeBoxed(si, i)
	if ft == nil {
		return e.expr(def)
	}
	if boxed || e.containsRef(ft) || e.isOptCarrierType(ft) {
		return e.defaultWrap(ft, def)
	}
	return e.expr(def)
}

// defaultWrap coerces a constant DEFAULT expression (a struct field or a function
// parameter default, FORK-A5) into a target slot type. A default carries no recorded
// ExprType, so wrapValue cannot read the value type from e.info; this derives it — `nil`
// is the empty optional / NULL box, and any other constant is the target optional's bare
// element (its Some payload) or, for a non-optional target, the target itself. wrapValue
// passes a non-carrier target through unchanged, so a plain POD default is byte-identical.
func (e *emitter) defaultWrap(target sema.Type, def ast.Expr) string {
	vt := target
	if _, isNil := def.(*ast.NilLit); isNil {
		vt = sema.Nil
	} else if o, ok := target.(*types.Opt); ok {
		vt = o.Elem
	}
	return e.wrapValue(target, vt, e.copyValue(vt, def))
}

// isOptCarrierType reports whether a type is a non-boxed Optional that lowers to a value
// carrier (`{tag, ok}`) — the shape a struct-field construction must route through
// wrapValue. A boxed optional (`Node?`, a `void*` cell) is not a value carrier and takes
// the boxed slot path instead.
func (e *emitter) isOptCarrierType(t sema.Type) bool {
	o, ok := t.(*types.Opt)
	if !ok || e.isBoxedOpt(o) {
		return false
	}
	_, has := e.carrierFor(o)
	return has
}

// fieldTypeBoxed returns the i-th field's concrete type and boxed mark from a struct
// instance, or (nil, false) when the instance or index is unavailable (a defensive
// fallback that keeps the raw expression).
func (e *emitter) fieldTypeBoxed(si *mono.TypeInstance, i int) (sema.Type, bool) {
	if si == nil || i >= len(si.Fields) {
		return nil, false
	}
	return si.Fields[i].Type, si.Fields[i].Boxed
}

// trailingFieldDefaults returns the default expressions for the struct fields a
// construction omits — declared fields at index >= provided that carry a default
// (A5, FORK-A5: trailing positional constants). It returns nil unless the callee is a
// resolved struct type with every omitted trailing field defaulted, so a
// fully-specified construction is unaffected and byte-identical.
func (e *emitter) trailingFieldDefaults(n *ast.Call, provided int) []ast.Expr {
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return nil
	}
	sym, ok := e.info.Refs[id]
	if !ok {
		return nil
	}
	sd, ok := sym.Decl.(*ast.StructDecl)
	if !ok || provided >= len(sd.Fields) {
		return nil
	}
	var out []ast.Expr
	for i := provided; i < len(sd.Fields); i++ {
		if sd.Fields[i].Default == nil {
			return nil // a gap without a default is a sema error, not our backfill
		}
		out = append(out, sd.Fields[i].Default)
	}
	return out
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
	var payload []sema.Type
	var boxed []bool
	if ti := e.prog.EnumInstance(t); ti != nil {
		if v, ok := ti.Variant(name); ok {
			tag, payload, boxed = v.Tag, v.Payload, v.Boxed
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
			fmt.Fprintf(&b, ".f%d = %s", i, e.payloadSlot(payload, boxed, i, a.Value))
		}
		b.WriteString("}")
	}
	b.WriteString("})")
	return b.String()
}

// enumOfEmit lowers the discriminant reverse `E.of(n) -> E?` (GRAMMAR group 7): given
// an int, it yields `Some(variant)` when n equals a C-style variant's discriminant,
// else `None`. It is the inverse of `int(v)`. Lowered as a statement-expression whose
// switch tests n against each distinct discriminant; a matched value builds the enum
// (its tag IS the discriminant) into the optional's Ok, and the default is the empty
// optional. Reports false for any other call so `call` falls through.
func (e *emitter) enumOfEmit(n *ast.Call) (string, bool) {
	fld, ok := n.Callee.(*ast.Field)
	if !ok || fld.Name != "of" || len(n.Args) != 1 {
		return "", false
	}
	id, ok := fld.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	if sym, ok := e.info.Refs[id]; !ok || sym.Kind != sema.SymType {
		return "", false
	}
	opt, ok := e.cur.ExprType(e.info, n).(*types.Opt)
	if !ok {
		return "", false
	}
	c, ok := e.carrierFor(opt)
	if !ok {
		return "", false
	}
	ti := e.prog.EnumInstance(opt.Elem)
	if ti == nil {
		return "", false
	}
	enumC := e.ctype(opt.Elem)
	nv := e.freshName("ofn")
	rv := e.freshName("ofr")
	var labels strings.Builder
	seen := map[int]bool{}
	for _, v := range ti.Variants {
		if seen[v.Tag] {
			continue
		}
		seen[v.Tag] = true
		fmt.Fprintf(&labels, "case %d: ", v.Tag)
	}
	return fmt.Sprintf("({ int64_t %s = (%s); %s %s; switch (%s) { %s"+
		"%s = (%s){ .tag = 0, .%s = (%s){ .tag = (int32_t)%s } }; break; "+
		"default: %s = (%s){ .tag = 1 }; break; } %s; })",
		nv, e.expr(n.Args[0].Value), c.name, rv, nv, labels.String(),
		rv, c.name, c.okField(), enumC, nv,
		rv, c.name, rv), true
}

// errCallEmit lowers the built-in error surface (GRAMMAR group 8, docs/code/errors.md): a
// kind constructor `ValueError("msg")` builds an Err value carrying the kind and message
// (the runtime `zrt_err`, the same value a guard-recovered abort yields), and
// `err.message()` reads its message string. Reports false for any other call so `call`
// falls through. The Err is the erased common carrier — a fixed kind tag on one value —
// so the Result/Either lowering is unchanged.
func (e *emitter) errCallEmit(n *ast.Call) (string, bool) {
	switch callee := n.Callee.(type) {
	case *ast.Ident:
		if _, shadowed := e.info.Refs[callee]; shadowed {
			return "", false
		}
		if kind, ok := sema.ErrCtorKind(callee.Name); ok && len(n.Args) == 1 {
			return fmt.Sprintf("zrt_err_new_kind(%d, %s)", kind, e.expr(n.Args[0].Value)), true
		}
	case *ast.Field:
		if callee.Name == "message" && len(n.Args) == 0 && isErrType(e.cur.ExprType(e.info, callee.X)) {
			return fmt.Sprintf("(%s).msg", e.expr(callee.X)), true
		}
	}
	return "", false
}

// payloadSlot renders one enum-variant payload argument at construction. A BOXED slot
// (S1) allocates a fresh cell and MOVES/RETAINS the copied payload into it via
// zg_refnew_<n>(copyValue(...)); a non-POD inline slot copies (retain/deep-copy) so the
// new value owns it; a POD slot stays the byte-identical raw expression, so an existing
// payload-enum program is unchanged (DESIGN-refcount §7 risk 2/6). The copyValue is what
// keeps the refcount balanced: a fresh rvalue is moved, an lvalue is retained.
func (e *emitter) payloadSlot(payload []sema.Type, boxed []bool, i int, arg ast.Expr) string {
	if i >= len(payload) {
		return e.expr(arg)
	}
	pt := payload[i]
	if i < len(boxed) && boxed[i] {
		return fmt.Sprintf("%s(%s)", e.refnewName(boxPayloadType(pt)), e.copyValue(pt, arg))
	}
	// An optional-carrier payload (`Some(int?)` — POD `int?` or non-POD `str?`/`Inner?`) is
	// a value CARRIER (`{tag, ok}`), so the argument must be wrapped (Some -> `{.tag=0,.ok=v}`
	// / nil -> `{.tag=1}`) exactly like a struct field (fieldSlot). Emitting the raw value
	// would land it in the carrier's `tag` slot and match back as absent (silent miscompile),
	// or fail cc for a non-POD payload (loud). A boxed (cyclic) optional payload took the
	// boxed branch above; a non-optional payload is unaffected.
	if e.isOptCarrierType(pt) {
		et := e.cur.ExprType(e.info, arg)
		return e.wrapValue(pt, et, e.copyValue(et, arg))
	}
	if e.containsRef(pt) {
		return e.copyValue(pt, arg)
	}
	return e.expr(arg)
}

// assignTarget lowers a reassignment target. The Phase 0 backend only lowers the
// bare-identifier lvalue that the checked examples use; richer shapes (tuple,
// struct, field, index) are outside the Phase 0 subset.
func (e *emitter) assignTarget(t ast.AssignTarget) string {
	if lv, ok := t.(*ast.LValueTarget); ok {
		return e.expr(lv.X)
	}
	return e.systemError(nil, "no lowering for this assignment target")
}

// --- lowering helpers ---------------------------------------------------------

// ctype renders a type in type-only position (a return type, a cast, a field, a
// struct-typed value): a specialized nominal type spells its mangled C name, and
// every other type falls to the primitive mapping — so a non-generic program's C is
// unchanged.
func (e *emitter) ctype(t sema.Type) string {
	if _, ok := t.(*types.Named); ok {
		// a strong typedef lowers to its underlying representation — no distinct C type.
		return e.ctype(types.Underlying(t))
	}
	// the built-in erased error `Err` is the runtime carrier value (GRAMMAR group 8): a
	// bound error (`e := ValueError("x")`) and a Result's Right side are both the C
	// `zrt_err`, so a fixed kind tag on one value distinguishes them.
	if isErrType(t) {
		return "zrt_err"
	}
	if isResultNil(t) {
		return "zrt_result_nil"
	}
	// An `Opt[T]` whose T is a cyclic nominal is a nullable heap box, not the optional
	// carrier (S1 §5): None≡NULL, Some≡a Ref[T] cell, so its C type is `void*`. This keeps
	// a standalone `x: Node? = n.next` the same representation as the boxed field read.
	if o, ok := t.(*types.Opt); ok && e.isCyclicNominal(o.Elem) {
		return "void*"
	}
	// a range value: one shared carrier struct (int64 bounds + inclusive flag), so a
	// `r := lo..hi` bound name and a membership `v in r` read the same shape.
	if _, ok := t.(*types.Range); ok {
		return "zg_range"
	}
	// a general Result/Either/optional value: its monomorphized carrier (Phase 1f U0).
	if c, ok := e.carrierFor(t); ok {
		return c.name
	}
	// a tuple value: its monomorphized per-shape carrier (completeness iteration 2 U2).
	if c, ok := e.tupleFor(t); ok {
		return c.name
	}
	if _, ok := t.(*types.List); ok {
		// a list[T] value is a by-value header (list.c owns the buffer); its element type
		// rides in the per-instance vtable, so every list is the same C header type.
		return "zrt_list"
	}
	if _, ok := t.(*types.Map); ok {
		// a map[K, V] value is a by-value header (map.c owns the storage); its key/value
		// types ride in the per-instance vtable, so every map is the same C header type.
		return "zrt_map"
	}
	if _, ok := t.(*types.Chan); ok {
		// a channel handle is an opaque runtime pointer (chan.c owns the layout).
		return "zrt_chan*"
	}
	if _, ok := t.(*types.Ref); ok {
		// a Ref[T] value is a pointer to its zrt_ref_alloc'd header+payload.
		return "void*"
	}
	if p, ok := t.(*types.Ptr); ok {
		// a raw pointer (group 12): bare `ptr` is `void*`; `ptr[T]` is `T*`, so the
		// pointee type drives load/store deref width and offset stride (FORK-PTR-C).
		if p.Elem == nil {
			return "void*"
		}
		return e.ctype(p.Elem) + "*"
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

// cType maps a primitive type to its C spelling. The built-in numeric types beyond
// int/float lower to their <stdint.h> fixed-width integers — uint to uint64_t, byte
// to uint8_t, rune to a 32-bit code point — and an explicit fixed-width type (i8..i64,
// u8..u64, f32/f64) to the matching stdint/float type; every other type is void.
func cType(t sema.Type) string {
	if f, ok := t.(*types.Fixed); ok {
		return fixedCType(f)
	}
	// a held function is the one generic function pointer, cast back at the call site
	if _, ok := t.(*types.Fn); ok {
		return "zg_fnptr"
	}
	switch t {
	case sema.Int:
		return "int64_t"
	case types.Uint:
		return "uint64_t"
	case sema.Float:
		return "double"
	case sema.Bool:
		return "bool"
	case sema.Str:
		return "const char*"
	case types.Byte:
		return "uint8_t"
	case types.Rune:
		return "int32_t"
	default:
		return "void"
	}
}

// fixedCType maps a fixed-width numeric type to its C spelling: a float picks
// float/double by width, and an integer picks (u)intN_t by sign and width.
func fixedCType(f *types.Fixed) string {
	if f.Float {
		if f.Bits == 32 {
			return "float"
		}
		return "double"
	}
	prefix := "int"
	if !f.Signed {
		prefix = "uint"
	}
	return fmt.Sprintf("%s%d_t", prefix, f.Bits)
}

func zeroValue(t sema.Type) string {
	// a strong typedef zero-inits as its underlying representation.
	t = types.Underlying(t)
	if isResultNil(t) {
		return "zrt_result_ok()"
	}
	if f, ok := t.(*types.Fixed); ok && f.Float {
		return "0.0"
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
