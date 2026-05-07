// v0.9 Unit 3 — codegen for std/os.argv and std/os.exit.
//
// Trampolines:
//   - os_argv: forwards into zerg_os_argv() which builds a zerg_list_zerg_str
//     from process-globals __zerg_argc / __zerg_argv. The list shape is
//     force-monomorphised in the prelude (same as strings_split).
//   - os_exit: forwards into zerg_os_exit(code) which calls libc exit.
//     The trampoline body is `zerg_os_exit(z_code);` — no return statement
//     because writeFnSig already stamps `__attribute__((noreturn))` on
//     `-> never` fn-decls (Unit 1).
//
// main() signature swap: programUsesArgv reports whether any reachable
// builtin reference is os_argv. When true, main is rewritten as
// `int main(int argc, char **argv)` and the first two statements seed
// the __zerg_argc / __zerg_argv globals. Programs that reference only
// os_exit keep `int main(void)` — the byte-identical guarantee for
// non-argv programs holds.

package build

import (
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// isV09ArgvExitBuiltin reports whether name was introduced in v0.9 Unit 3.
func isV09ArgvExitBuiltin(name string) bool {
	switch name {
	case "os_argv", "os_exit":
		return true
	}
	return false
}

// programUsesArgv reports whether any module references os_argv. Only
// programs hitting this gate get the main(int, char**) signature swap.
func (g *cgen) programUsesArgv() bool {
	for i := range g.modules {
		if g.programUsesBuiltinWalk(g.modules[i].prog, "os_argv") {
			return true
		}
	}
	return false
}

// programUsesOsExit reports whether any module references os_exit.
// Drives the runtime emit (the argv/exit C runtime can be partially
// emitted: programs that use only exit don't need the argv globals
// initialised but the runtime block emits both for cohesion).
func (g *cgen) programUsesOsExit() bool {
	for i := range g.modules {
		if g.programUsesBuiltinWalk(g.modules[i].prog, "os_exit") {
			return true
		}
	}
	return false
}

// skipBuiltinFn reports whether the trampoline for fn should be omitted
// from the emitted C source. v0.9 Unit 3 builtins (os_argv, os_exit) and
// Unit 2 builtins (time_now_ms, time_sleep_ms) are elided when their
// corresponding runtime block is not emitted — i.e. when the user
// program does not reach a call site for them. Without this gate, a
// v0.8 program that imports std/os solely for os.env would pull in
// trampolines referencing undefined zerg_os_argv / zerg_os_exit symbols
// (since the runtime is now usage-gated per Phase 4 Fix 4).
//
// `needsArgv` is the cached programUsesArgv result; the other predicates
// are recomputed because emitFn is called from multiple sites.
func (g *cgen) skipBuiltinFn(fn *syntax.FnDecl, needsArgv bool) bool {
	if fn == nil || fn.BuiltinName == "" {
		return false
	}
	switch fn.BuiltinName {
	case "os_argv":
		return !needsArgv
	case "os_exit":
		return !g.programUsesOsExit()
	case "time_now_ms", "time_sleep_ms":
		return !g.programUsesV09Time()
	}
	return false
}

// programUsesBuiltinWalk returns true if the named __builtin is referenced
// anywhere in prog (top-level statements, MonoFns, MonoImpls). Generic
// walker shared with the v09 argv predicate; structurally identical to
// programUsesV09Walk but parameterised on a single name.
func (g *cgen) programUsesBuiltinWalk(prog *syntax.Program, name string) bool {
	if prog == nil {
		return false
	}
	found := false
	resolveCall := func(callee syntax.Expr) *syntax.FnDecl {
		switch c := callee.(type) {
		case *syntax.IdentExpr:
			return g.lookupFnByName(c.Name, prog)
		}
		return nil
	}
	resolveMethodCall := func(e *syntax.MethodCallExpr) *syntax.FnDecl {
		id, ok := e.Receiver.(*syntax.IdentExpr)
		if !ok {
			return nil
		}
		host := g.findModuleForProg(prog)
		if host == nil {
			return nil
		}
		foreignMangle, ok := host.imports[id.Name]
		if !ok {
			return nil
		}
		return g.lookupModuleFn(foreignMangle, e.Method)
	}
	var walkE func(syntax.Expr)
	var walkS func(syntax.Stmt)
	walkE = func(e syntax.Expr) {
		if e == nil || found {
			return
		}
		switch x := e.(type) {
		case *syntax.CallExpr:
			if fn := resolveCall(x.Callee); fn != nil && fn.BuiltinName == name {
				found = true
				return
			}
			walkE(x.Callee)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.MethodCallExpr:
			if fn := resolveMethodCall(x); fn != nil && fn.BuiltinName == name {
				found = true
				return
			}
			walkE(x.Receiver)
			for _, a := range x.Args {
				walkE(a)
			}
			if x.Lowered != nil {
				walkE(x.Lowered)
			}
			if x.LoweredCall != nil {
				walkE(x.LoweredCall)
			}
		case *syntax.UnaryExpr:
			walkE(x.Operand)
		case *syntax.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.ParenExpr:
			walkE(x.Inner)
		case *syntax.IndexExpr:
			walkE(x.Receiver)
			walkE(x.Index)
		case *syntax.FieldAccessExpr:
			walkE(x.Receiver)
			if x.Lowered != nil {
				walkE(x.Lowered)
			}
		case *syntax.ListLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.TupleLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.StructLit:
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *syntax.EnumLit:
			for _, sub := range x.Payload {
				walkE(sub)
			}
		case *syntax.PropagateExpr:
			walkE(x.Inner)
		case *syntax.CoalesceExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.AnonFnExpr:
			walkBlock(x.Body, walkS)
		}
	}
	walkS = func(s syntax.Stmt) {
		if s == nil || found {
			return
		}
		switch n := s.(type) {
		case *syntax.PrintStmt:
			walkE(n.Expr)
		case *syntax.ExprStmt:
			walkE(n.Expr)
		case *syntax.LetStmt:
			walkE(n.Value)
		case *syntax.MutStmt:
			walkE(n.Value)
		case *syntax.ConstStmt:
			walkE(n.Value)
		case *syntax.AssignStmt:
			walkE(n.Target)
			walkE(n.Value)
		case *syntax.IfStmt:
			walkE(n.Cond)
			walkBlock(n.Then, walkS)
			for _, ec := range n.Elifs {
				walkE(ec.Cond)
				walkBlock(ec.Body, walkS)
			}
			if n.Else != nil {
				walkBlock(n.Else, walkS)
			}
		case *syntax.ForStmt:
			if n.Iter != nil {
				walkE(n.Iter)
			}
			if n.Cond != nil {
				walkE(n.Cond)
			}
			if n.Range != nil {
				walkE(n.Range.Start)
				walkE(n.Range.End)
			}
			walkBlock(n.Body, walkS)
		case *syntax.ReturnStmt:
			if n.Value != nil {
				walkE(n.Value)
			}
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.MatchStmt:
			walkE(n.Subject)
			for _, arm := range n.Arms {
				if arm.Guard != nil {
					walkE(arm.Guard)
				}
				walkBlock(arm.Body, walkS)
			}
		case *syntax.FnDecl:
			walkBlock(n.Body, walkS)
		case *syntax.SpawnStmt:
			walkE(n.Call)
		case *syntax.SendStmt:
			walkE(n.Chan)
			walkE(n.Value)
		case *syntax.DeferStmt:
			walkBlock(n.Body, walkS)
		case *syntax.SelectStmt:
			for _, arm := range n.Arms {
				if arm.Chan != nil {
					walkE(arm.Chan)
				}
				if arm.Value != nil {
					walkE(arm.Value)
				}
				walkBlock(arm.Body, walkS)
			}
		case *syntax.BreakStmt:
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.ContinueStmt:
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.ImplDecl:
			for _, m := range n.Methods {
				if m != nil {
					walkBlock(m.Body, walkS)
				}
			}
		}
	}
	for _, st := range prog.Statements {
		walkS(st)
		if found {
			return true
		}
	}
	for _, fn := range prog.MonoFns {
		if fn == nil || found {
			continue
		}
		walkBlock(fn.Body, walkS)
	}
	for _, im := range prog.MonoImpls {
		if im == nil || found {
			continue
		}
		for _, m := range im.Methods {
			if m != nil {
				walkBlock(m.Body, walkS)
			}
		}
	}
	return found
}

// emitV09ArgvExitBuiltinBody emits the trampoline body for one v0.9
// Unit 3 builtin. Returns ok=true when fn is one of ours; ok=false
// otherwise. Body strings have no surrounding braces (same calling
// convention as builtinBodyStr).
func emitV09ArgvExitBuiltinBody(name string) (string, bool) {
	switch name {
	case "os_argv":
		return "    return zerg_os_argv();\n", true
	case "os_exit":
		return "    zerg_os_exit(z_code);\n", true
	}
	return "", false
}

// runtimeV09ArgvExitC is the embedded C runtime for std/os.argv and
// std/os.exit. __zerg_argc / __zerg_argv are the process-global
// argv mirror seeded at the top of main; zerg_os_argv builds a
// zerg_list_zerg_str from them (one zerg_str per argv entry).
//
// _exit (in zerg_os_exit) needs <unistd.h>; programs that use os.exit
// without any v0.7 concurrency primitive don't pull the v0.12 runtime
// preamble (which would already include it via coroRuntimeC) so we
// include it locally here. Including twice is harmless — both headers
// are idempotent on every supported platform.
const runtimeV09ArgvExitC = `#include <stdlib.h>
#include <unistd.h>

/* ---------------- v0.9 std/os argv + exit runtime ----------------------- */

static int    __zerg_argc = 0;
static char **__zerg_argv = 0;

static zerg_list_zerg_str zerg_os_argv(void) {
    zerg_list_zerg_str out;
    out.len = 0;
    out.cap = 0;
    out.data = 0;
    for (int i = 0; i < __zerg_argc; i++) {
        const char *a = __zerg_argv[i];
        size_t n = 0;
        while (a[n]) n++;
        char *p = (char *)malloc(n + 1);
        if (n) memcpy(p, a, n);
        p[n] = 0;
        zerg_list_zerg_str_push(&out, (zerg_str){p, n});
    }
    return out;
}

/* zerg_os_exit terminates the process with the given code. We flush
   stdout/stderr first so any prints made before the exit call reach
   the user, then use _exit rather than exit. _exit avoids running
   atexit handlers and libc teardown — important under the v0.12 M:N
   runtime, where the caller is a coroutine running on one worker
   pthread while other workers are still in zerg_worker_main; libc
   teardown would race the workers' access to scheduler globals
   (calloc'd worker pool, runqueues, mutexes). _exit terminates the
   entire process immediately; the workers' mmap'd stacks and locked
   mutexes are released by the kernel. v0.9's semantics ("os.exit
   bypasses defers") are preserved either way — _exit doesn't run
   cleanup paths. */
__attribute__((noreturn)) static void zerg_os_exit(int64_t code) {
    fflush(stdout);
    fflush(stderr);
    _exit((int)code);
}
`
