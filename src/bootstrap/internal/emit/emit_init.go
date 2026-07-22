package emit

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
)

// Module initialization (Phase 1g S3, Decision F3). A module that owns an `init()`
// block or a module constant lowers to a per-module C function `__zerg_init_<tag>`
// guarded by a static once-flag, which evaluates the module's constants (into
// module-level C globals) and then runs its `init()` bodies in declaration order.
// The program entry calls each module's init function in dependency (topological)
// order before `main`'s body runs, so a dependency is initialized before a module
// that uses it. A program with no init and no module constant emits none of this,
// so its C stays byte-identical.

// hasInits reports whether the program owns any module init function.
func (e *emitter) hasInits() bool { return len(e.prog.Inits) > 0 }

// initFnName is the C name of a module's init function. The entry module (empty
// tag) uses a fixed spelling so its function name is stable and legal.
func initFnName(tag string) string {
	if tag == "" {
		return "__zerg_init_main"
	}
	return "__zerg_init_" + tag
}

// emitModuleConstGlobals declares one file-scope C global per module constant. The
// value is assigned at init time (not here), so a constant whose initializer is a
// runtime expression is computed inside its module's init function.
func (e *emitter) emitModuleConstGlobals() {
	any := false
	for _, g := range e.prog.Inits {
		for _, b := range g.Consts {
			t := e.prog.InitCtx.BindType(e.info, b)
			e.line(e.localDecl(t, "zg_"+b.Name) + ";")
			any = true
		}
	}
	if any {
		e.blank()
	}
}

// emitInitPrototypes forward-declares each module's init function so the entry can
// call them before their definitions appear.
func (e *emitter) emitInitPrototypes() {
	for _, g := range e.prog.Inits {
		e.line("void " + initFnName(g.Tag) + "(void);")
	}
	if e.hasInits() {
		e.blank()
	}
}

// emitInitFunctions renders each module's init function.
func (e *emitter) emitInitFunctions() {
	for _, g := range e.prog.Inits {
		e.initFunction(g)
		e.blank()
	}
}

// emitInitCalls runs every module's init function in dependency (topological) order,
// at the top of the C entry, before `main`'s body — the MVP eager trigger (F3). The
// once-guard makes each call idempotent, so a module reached along two paths still
// initializes exactly once.
func (e *emitter) emitInitCalls() {
	for _, g := range e.prog.Inits {
		e.line(initFnName(g.Tag) + "();")
	}
}

// initFunction lowers one module's init function: a static once-guard (with a
// poison flag reserved for the later abort-caching semantics), then the module
// constants' assignments, then the `init()` bodies in declaration order.
func (e *emitter) initFunction(g mono.InitGroup) {
	e.cur = e.prog.InitCtx
	e.resetUsed()
	e.counter = 0
	e.pushScope() // name scope

	e.line("void " + initFnName(g.Tag) + "(void) {")
	e.indent++
	e.line("static _Bool __done = 0;")
	e.line("static _Bool __poisoned = 0;")
	e.line("(void)__poisoned;")
	e.line("if (__done) { return; }")
	e.line("__done = 1;")

	e.pushScope() // body scope
	need := e.initNeedsTeardown(g)
	e.openScope(need, false)
	e.fnMark = e.curScope().markVar

	for _, b := range g.Consts {
		e.emitConstAssign(b)
	}
	for _, in := range g.Inits {
		if in.Body == nil {
			continue
		}
		for _, s := range in.Body.Stmts {
			e.stmt(s)
		}
	}

	if e.fnMark != "" {
		e.line("zrt_unwind_to(" + e.fnMark + ");")
	}
	e.popScopeRaw()
	e.fnMark = ""
	e.popScope()
	e.indent--
	e.line("}")

	e.popScope()
}

// emitConstAssign assigns a module constant's initializer to its C global, at init
// time. The value is copied (retain / deep copy) when it names existing storage, so
// a non-POD constant owns its own storage; a POD constant is a plain assignment.
func (e *emitter) emitConstAssign(b *ast.BindStmt) {
	if b.Value == nil {
		return
	}
	t := e.prog.InitCtx.BindType(e.info, b)
	rhs := e.wrapValue(t, e.prog.InitCtx.ExprType(e.info, b.Value), e.copyValue(t, b.Value))
	e.line("zg_" + b.Name + " = " + rhs + ";")
}

// initNeedsTeardown reports whether any of a module's init bodies owns teardown (a
// Ref local, a `defer`, a `with`), so the init function opens a cleanup frame only
// when one is needed — a teardown-free init stays plain C.
func (e *emitter) initNeedsTeardown(g mono.InitGroup) bool {
	for _, in := range g.Inits {
		if in.Body != nil && e.subtreeTeardown(in.Body.Stmts) {
			return true
		}
	}
	return false
}
