// v0.9 Unit 2 — codegen for std/time builtins.
//
// Trampolines for the two atomic time primitives (time_clock_us /
// time_sleep_ns) forward into a small embedded C runtime
// (runtimeV09TimeC). The primitives are intentionally narrow: clock_us
// reads CLOCK_REALTIME and returns walltime microseconds; sleep_ns
// calls nanosleep in an EINTR retry loop. All higher-level logic — the
// epoch-zero-on-first-call contract, ms math, negative-ms clamp —
// lives in pure-Zerg src/std/time.zg over the v0.14 module-level mut
// state surfaced by the P1 init walk.
//
// Gating: programUsesV09Time fires the runtime emit when either
// primitive is referenced, so v0.0–v0.8 programs preserve their byte-
// identical output.

package build

import (
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// isV09Builtin reports whether name was introduced in v0.9. Same idea as
// the implicit "v0.8 set" carried by the v08 registry, but extracted as a
// predicate so the v09 walker can disambiguate.
func isV09Builtin(name string) bool {
	switch name {
	case "time_clock_us", "time_sleep_ns":
		return true
	}
	return isV09ArgvExitBuiltin(name)
}

// programUsesV09 reports whether any module in the bundle references a
// v0.9-introduced __builtin. Mirrors programUsesV08 / programUsesV07.
func (g *cgen) programUsesV09() bool {
	for i := range g.modules {
		if g.programUsesV09Walk(g.modules[i].prog) {
			return true
		}
	}
	return false
}

// programUsesV09Time reports whether any module references a v0.9 time
// __builtin (time_clock_us / time_sleep_ns). Drives the time runtime
// emit independently of the argv/exit emit — programs that use only
// os.argv don't pull in <time.h> or the lazy-init epoch globals.
func (g *cgen) programUsesV09Time() bool {
	for i := range g.modules {
		if g.programUsesBuiltinWalk(g.modules[i].prog, "time_clock_us") {
			return true
		}
		if g.programUsesBuiltinWalk(g.modules[i].prog, "time_sleep_ns") {
			return true
		}
	}
	return false
}

func (g *cgen) programUsesV09Walk(prog *syntax.Program) bool {
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
			if fn := resolveCall(x.Callee); fn != nil && isV09Builtin(fn.BuiltinName) {
				found = true
				return
			}
			walkE(x.Callee)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.MethodCallExpr:
			if fn := resolveMethodCall(x); fn != nil && isV09Builtin(fn.BuiltinName) {
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

// emitV09TimeBuiltinBody emits the trampoline body for one v0.9 time
// __builtin. Returns ok=true when fn is a v0.9 builtin (caller skips the
// v0.8 dispatch); ok=false otherwise. Body strings have no surrounding
// braces — same calling convention as builtinBodyStr.
func emitV09TimeBuiltinBody(name string) (string, bool) {
	switch name {
	case "time_clock_us":
		return "    return zerg_time_clock_us();\n", true
	case "time_sleep_ns":
		return "    return zerg_time_sleep_ns(z_sec, z_nsec);\n", true
	}
	return "", false
}

// runtimeV09TimeC is the embedded C runtime for std/time's two atomic
// primitives. time_clock_us reads CLOCK_REALTIME and returns
// (sec * 1_000_000 + nsec / 1_000), giving walltime microseconds.
// time_sleep_ns calls nanosleep with an EINTR retry loop using the
// kernel's remaining-time fill to honour "blocks at least N ns" across
// signal delivery.
//
// All higher-level logic — the epoch-zero contract, ms math, negative
// clamp — lives in src/std/time.zg over P1's module-level mut state.
// Keeping the C primitives narrow means the build half's deviation
// from the run half is contained to the libc surface; the Zerg-level
// behaviour is identical because both halves implement the same
// pure-Zerg now_ms / sleep_ms on top of the same primitive contract.
const runtimeV09TimeC = `#include <time.h>
#include <errno.h>

/* ---------------- v0.9 std/time runtime --------------------------------- */

static int64_t zerg_time_clock_us(void) {
    struct timespec now;
    clock_gettime(CLOCK_REALTIME, &now);
    return (int64_t)now.tv_sec * 1000000 + (int64_t)now.tv_nsec / 1000;
}

static int64_t zerg_time_sleep_ns(int64_t sec, int64_t nsec) {
    if (sec < 0 || nsec < 0) return -((int64_t)EINVAL);
    struct timespec req;
    req.tv_sec = (time_t)sec;
    req.tv_nsec = (long)nsec;
    struct timespec rem;
    while (nanosleep(&req, &rem) == -1) {
        if (errno == EINTR) {
            req = rem;
            continue;
        }
        return -((int64_t)errno);
    }
    return 0;
}
`
