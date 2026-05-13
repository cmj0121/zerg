// v0.9 Unit 3 — codegen for std/os primitives.
//
// v0.14 T2 retired the coupled os_env / os_argv / os_exit shims in
// favour of atomic accessor primitives:
//
//   - os_argv_len / os_argv_at(i)  — read process-globals __zerg_argc /
//     __zerg_argv[i]. The pure-Zerg argv() in src/std/os.zg builds a
//     list[str] over these.
//   - os_envp_len / os_envp_at(i)  — count and index extern char **environ.
//     The pure-Zerg env(name) walks them and matches on "name=" prefix.
//   - exit(code) lives in pure-Zerg src/std/os.zg as a wrapper over
//     sys.syscall.exit(code), so no os_exit __builtin remains.
//
// main() signature swap: programUsesArgv reports whether the program
// reaches os_argv_len or os_argv_at. When true, main is rewritten as
// `int main(int argc, char **argv)` and the first two statements seed
// the __zerg_argc / __zerg_argv globals.

package build

import (
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// isV09ArgvExitBuiltin reports whether name was introduced in v0.9 Unit 3.
func isV09ArgvExitBuiltin(name string) bool {
	switch name {
	case "os_argv_len", "os_argv_at", "os_envp_len", "os_envp_at":
		return true
	}
	return false
}

// programUsesArgv reports whether any module references the argv
// primitives. Only programs hitting this gate get the
// main(int, char**) signature swap so __zerg_argc / __zerg_argv land.
func (g *cgen) programUsesArgv() bool {
	for i := range g.modules {
		if g.programUsesBuiltinWalk(g.modules[i].prog, "os_argv_len") {
			return true
		}
		if g.programUsesBuiltinWalk(g.modules[i].prog, "os_argv_at") {
			return true
		}
	}
	return false
}

// programUsesEnvp reports whether any module references the envp
// primitives. Drives the runtime emit of zerg_os_envp_* — separate
// gate so an argv-only program doesn't pull in environ-walking code.
func (g *cgen) programUsesEnvp() bool {
	for i := range g.modules {
		if g.programUsesBuiltinWalk(g.modules[i].prog, "os_envp_len") {
			return true
		}
		if g.programUsesBuiltinWalk(g.modules[i].prog, "os_envp_at") {
			return true
		}
	}
	return false
}

// skipBuiltinFn reports whether the trampoline for fn should be omitted
// from the emitted C source. v0.9 std/os accessor primitives and Unit 2
// std/time primitives are elided when their corresponding runtime block
// is not emitted — i.e. when the user program does not reach a call
// site for them. Without this gate, a program that imports std/os
// without calling argv() / env() would pull in trampolines referencing
// undefined zerg_os_argv_at / zerg_os_envp_at symbols (since the
// runtime is usage-gated for byte-identical pre-v0.9 emit).
//
// `needsArgv` is the cached programUsesArgv result; the other predicates
// are recomputed because emitFn is called from multiple sites.
func (g *cgen) skipBuiltinFn(fn *syntax.FnDecl, needsArgv bool) bool {
	if fn == nil || fn.BuiltinName == "" {
		return false
	}
	switch fn.BuiltinName {
	case "os_argv_len", "os_argv_at":
		return !needsArgv
	case "os_envp_len", "os_envp_at":
		return !g.programUsesEnvp()
	case "time_clock_us", "time_sleep_ns":
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
// std/os accessor primitive. Returns ok=true when fn is one of ours;
// ok=false otherwise. Body strings have no surrounding braces (same
// calling convention as builtinBodyStr).
func emitV09ArgvExitBuiltinBody(name string) (string, bool) {
	switch name {
	case "os_argv_len":
		return "    return zerg_os_argv_len();\n", true
	case "os_argv_at":
		return "    return zerg_os_argv_at(z_i);\n", true
	case "os_envp_len":
		return "    return zerg_os_envp_len();\n", true
	case "os_envp_at":
		return "    return zerg_os_envp_at(z_i);\n", true
	}
	return "", false
}

// runtimeV09ArgvExitC is the embedded C runtime for std/os accessor
// primitives. __zerg_argc / __zerg_argv are the process-global argv
// mirror seeded at the top of main; zerg_os_argv_len / _at index into
// them. zerg_os_envp_len / _at walk extern char **environ; the length
// loop runs each call, which is cheap (envp is short and stable).
//
// Each zerg_str returned by an _at helper points into the kernel-
// supplied argv / environ memory (read-only, process-lived). No malloc
// — saves a per-call copy and matches the "list of zerg_str" the user
// sees from os.argv() / os.env() being a snapshot that aliases
// process memory.
const runtimeV09ArgvExitC = `#include <stdlib.h>

extern char **environ;

/* ---------------- v0.9 std/os primitive runtime -------------------------- */

static int    __zerg_argc = 0;
static char **__zerg_argv = 0;

static int64_t zerg_os_argv_len(void) {
    return (int64_t)__zerg_argc;
}

static zerg_str zerg_os_argv_at(int64_t i) {
    const char *a = __zerg_argv[i];
    size_t n = 0;
    while (a[n]) n++;
    return (zerg_str){a, n};
}

static int64_t zerg_os_envp_len(void) {
    int64_t n = 0;
    if (environ) {
        while (environ[n]) n++;
    }
    return n;
}

static zerg_str zerg_os_envp_at(int64_t i) {
    const char *e = environ[i];
    size_t n = 0;
    while (e[n]) n++;
    return (zerg_str){e, n};
}
`
