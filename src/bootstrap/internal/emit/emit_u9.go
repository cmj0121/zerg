package emit

// This file carries the Phase 1e slice-C1 additions to the C backend: `spawn`
// lowering and the concurrency build gate. The whole surface is additive and gated
// on concurrency use: a program with no `spawn` (and no channel) sets no flag, links
// no scheduler, and emits byte-identical C to Phase 1d.
//
// The model mirrors 1d's `defer` marshaling exactly, with one difference: a spawned
// coroutine outlives the spawn site, so its captured arguments live in a HEAP env
// (zrt_alloc) that the coroutine's thunk frees, rather than a stack env the enclosing
// scope owns. Each argument is copied with the same copyValue rule (a Ref is retained,
// a struct-of-Ref deep-copied); the thunk hands those copies to the callee, which
// releases them on return exactly as any by-value call does, so refcounts balance.

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// --- concurrency build gate ---------------------------------------------------

// programUsesConcurrency reports whether the program uses concurrency: any function
// body contains a `spawn`/send/`select`, or any binding, expression, or signature
// type transitively holds a channel. It is the single trigger for Manifest.Concurrency
// (mirroring programUsesRuntimeStmt / programUsesRef), so a program with none of these
// links no scheduler and stays byte-identical.
func (e *emitter) programUsesConcurrency() bool {
	for _, inst := range e.prog.Funcs {
		found := false
		walkStmts(inst.Origin.Body, func(s ast.Stmt) {
			switch s.(type) {
			case *ast.SpawnStmt, *ast.SendStmt, *ast.SelectStmt:
				found = true
			}
		})
		if found {
			return true
		}
	}
	for _, t := range e.info.BindTypes {
		if containsChan(t) {
			return true
		}
	}
	for _, t := range e.info.ExprTypes {
		if containsChan(t) {
			return true
		}
	}
	for _, sig := range e.info.Funcs {
		if containsChan(sig.Ret) {
			return true
		}
		for _, p := range sig.Params {
			if containsChan(p) {
				return true
			}
		}
	}
	return false
}

// containsChan reports whether a type transitively holds a channel (chan[T]), the
// value form that pulls in the scheduler. It mirrors containsRef's structural walk, with
// a visited set so a recursive (S1) type terminates instead of looping forever — a
// nominal already on the walk contributes no new channel, so revisiting it yields false.
func containsChan(t sema.Type) bool {
	return containsChanSeen(t, map[string]bool{})
}

func containsChanSeen(t sema.Type, seen map[string]bool) bool {
	switch x := t.(type) {
	case *types.Chan:
		return true
	case *types.Tuple:
		for _, el := range x.Elems {
			if containsChanSeen(el, seen) {
				return true
			}
		}
	case *types.Array:
		return containsChanSeen(x.Elem, seen)
	case *types.Opt:
		return containsChanSeen(x.Elem, seen)
	case *types.Struct:
		if seen[x.String()] {
			return false
		}
		seen[x.String()] = true
		for _, ft := range structFieldTypes(x) {
			if containsChanSeen(ft, seen) {
				return true
			}
		}
	case *types.Enum:
		if seen[x.String()] {
			return false
		}
		seen[x.String()] = true
		for _, pt := range enumPayloadTypes(x) {
			if containsChanSeen(pt, seen) {
				return true
			}
		}
	}
	return false
}

// --- spawn --------------------------------------------------------------------

// spawnStmt lowers `spawn f(args)` (GRAMMAR group 9): it copies the call's argument
// values into a heap env (so the coroutine owns them past the spawn site) and enqueues
// the pre-generated thunk on the scheduler with zrt_spawn — fire-and-forget, no handle.
// A no-argument call enqueues the thunk with a NULL env.
func (e *emitter) spawnStmt(n *ast.SpawnStmt) {
	idx, ok := e.spawnIdx[n]
	if !ok {
		e.diags.Add(n.Span(), "spawn supports only a direct function call in Phase 1e")
		return
	}
	call, _ := n.Call.(*ast.Call)
	if len(call.Args) == 0 {
		e.line(fmt.Sprintf("zrt_spawn(zg_spawnthunk_%d, NULL);", idx))
		return
	}
	env := e.freshName("senv")
	var b []string
	for _, a := range call.Args {
		b = append(b, e.copyValue(e.cur.ExprType(e.info, a.Value), a.Value))
	}
	e.line(fmt.Sprintf("zg_spawnenv_%d *%s = zrt_alloc(sizeof(zg_spawnenv_%d));", idx, env, idx))
	e.line(fmt.Sprintf("*%s = (zg_spawnenv_%d){ %s };", env, idx, joinComma(b)))
	e.line(fmt.Sprintf("zrt_spawn(zg_spawnthunk_%d, %s);", idx, env))
}

// emitSpawnHelpers generates, before any function body, the env struct and trampoline
// for every `spawn f(args)` in the program (numbered per distinct site, like defers).
// The trampoline unpacks the captured args, makes the call, and frees the heap env. It
// emits nothing for a program with no spawns.
func (e *emitter) emitSpawnHelpers() {
	e.spawnIdx = map[*ast.SpawnStmt]int{}
	for _, inst := range e.prog.Funcs {
		e.cur = inst
		walkStmts(inst.Origin.Body, func(s ast.Stmt) {
			sp, ok := s.(*ast.SpawnStmt)
			if !ok {
				return
			}
			if _, seen := e.spawnIdx[sp]; seen {
				return
			}
			call, ok := sp.Call.(*ast.Call)
			if !ok {
				return
			}
			id, ok := call.Callee.(*ast.Ident)
			if !ok {
				return
			}
			idx := len(e.spawnIdx)
			e.spawnIdx[sp] = idx
			e.emitSpawnThunk(idx, e.callTarget(call, id), call.Args)
		})
	}
	e.cur = nil
}

// emitSpawnThunk writes one spawned call's env struct (when it has captured args) and
// its coroutine trampoline. The trampoline runs on the coroutine's own stack: it makes
// the call with the captured arguments (which the callee releases on return, as any
// by-value call does), then frees the heap env.
func (e *emitter) emitSpawnThunk(idx int, target string, args []ast.Arg) {
	if len(args) == 0 {
		e.line(fmt.Sprintf("static void zg_spawnthunk_%d(void *p) { (void)p; %s(); }", idx, target))
		e.blank()
		return
	}
	e.line(fmt.Sprintf("typedef struct { %s } zg_spawnenv_%d;", e.deferFields(args), idx))
	e.line(fmt.Sprintf("static void zg_spawnthunk_%d(void *p) {", idx))
	e.indent++
	e.line(fmt.Sprintf("zg_spawnenv_%d *zg_e = (zg_spawnenv_%d *)p;", idx, idx))
	var call []string
	for i := range args {
		call = append(call, fmt.Sprintf("zg_e->f%d", i))
	}
	e.line(fmt.Sprintf("%s(%s);", target, joinComma(call)))
	e.line("zrt_free(p);")
	e.indent--
	e.line("}")
	e.blank()
}
