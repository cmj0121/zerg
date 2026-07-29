package emit

// The spawn half of the concurrency backend (GRAMMAR group 9). It is `defer`'s marshaling
// with one difference: a spawned coroutine outlives the spawn site, so its captured
// arguments live in a HEAP env (zrt_alloc) that the coroutine's thunk frees, rather than a
// stack env the enclosing scope owns. Each argument is copied with the same copyValue rule
// (a Ref is retained, a struct-of-Ref deep-copied); the thunk hands those copies to the
// callee, which releases them on return exactly as any by-value call does, so refcounts
// balance — and, for a channel argument, the sender count the callee gives back is what
// eventually closes the channel.
//
// The seed lowers a DIRECT function call and nothing else. capturedCall (emit_scope.go)
// would also resolve a method and a namespaced function for `defer`, but a coroutine is
// not a deferred call: its receiver outlives the spawner and its module-merged target
// needs machinery the seed does not carry, so those are refused by name here.

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// errSpawnCallee is the seed's line on a `spawn` callee. It is one string because the two
// guards below reject the same set for the same reason, and a test matches on it.
const errSpawnCallee = "the bootstrap seed lowers 'spawn' only on a direct function call — " +
	"not a method, a namespaced function, or a closure"

// spawnStmt lowers `spawn f(args)`: it copies the call's argument values into a heap env
// (so the coroutine owns them past the spawn site) and enqueues the pre-generated thunk on
// the scheduler with zrt_spawn — fire-and-forget, no handle. A no-argument call enqueues
// the thunk with a NULL env. A site the prepass refused is silent here: it already said why.
func (e *emitter) spawnStmt(n *ast.SpawnStmt) {
	idx, ok := e.spawnIdx[n]
	if !ok {
		return
	}
	call, _ := n.Call.(*ast.Call)
	if len(call.Args) == 0 {
		e.line(fmt.Sprintf("zrt_spawn(zg_spawnthunk_%d, NULL);", idx))
		return
	}
	env := e.freshName("senv")
	b := e.captureValues(nil, call.Args) // a coroutine OWNS its arguments: it may outlive the spawner's scope
	e.line(fmt.Sprintf("zg_spawnenv_%d *%s = zrt_alloc(sizeof(zg_spawnenv_%d));", idx, env, idx))
	e.line(fmt.Sprintf("*%s = (zg_spawnenv_%d){ %s };", env, idx, joinComma(b)))
	e.line(fmt.Sprintf("zrt_spawn(zg_spawnthunk_%d, %s);", idx, env))
}

// emitSpawnHelpers generates, before any function body, the env struct and trampoline for
// every `spawn f(args)` in the program (numbered per distinct site, like defers), and
// refuses the sites past the seed's line. The trampoline unpacks the captured args, makes
// the call, and frees the heap env. It emits nothing for a program with no spawns.
func (e *emitter) emitSpawnHelpers() {
	e.spawnIdx = map[*ast.SpawnStmt]int{}
	if !e.concurrency {
		return // no `spawn` and no channel: walking every body would find nothing
	}
	freed := false
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
				return // sema already rejected a `spawn` whose operand is not a call
			}
			target, ok := e.spawnTarget(call)
			if !ok {
				return
			}
			// The shared env-free thunk goes ahead of the first trampoline that names it,
			// and not at all when every spawn is nullary — those allocate no env.
			if len(call.Args) > 0 && !freed {
				freed = true
				e.line("static void zg_spawn_free(void *p) { zrt_free(p); }")
				e.blank()
			}
			idx := len(e.spawnIdx)
			e.spawnIdx[sp] = idx
			e.emitSpawnThunk(idx, target, call.Args)
		})
	}
	e.cur = nil
}

// spawnTarget resolves a `spawn f(args)` callee to its mangled C target, refusing every
// callee shape past a direct function call and every `mut &` argument. A `mut &` parameter
// binds the CALLER's variable, and a coroutine may still be running after that variable's
// scope is gone — so the borrow has no owner to point at. Today it reaches cc and dies
// there, which tells the reader nothing about the program.
func (e *emitter) spawnTarget(call *ast.Call) (string, bool) {
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		e.diags.Add(call.Span(), "%s", errSpawnCallee)
		return "", false
	}
	sym, found := e.info.Refs[id]
	if !found || sym == nil {
		return "", false // an unresolved callee; sema already reported it
	}
	if _, isFn := sym.Decl.(*ast.FuncDecl); !isFn {
		e.diags.Add(call.Span(), "%s", errSpawnCallee)
		return "", false
	}
	byref := e.calleeByRefArgs(id)
	for i := range call.Args {
		if i < len(byref) && byref[i] {
			e.diags.Add(call.Args[i].Value.Span(), "the bootstrap seed cannot pass a 'mut &' argument across a 'spawn': the coroutine captures its arguments by value and may outlive the borrowed variable")
			return "", false
		}
	}
	return e.callTarget(call, id), true
}

// emitSpawnThunk writes one spawned call's env struct (when it captures arguments) and its
// coroutine trampoline. The trampoline runs on the coroutine's own stack: it makes the call
// with the captured arguments — released by the callee on return, as any by-value call —
// then frees the heap env.
func (e *emitter) emitSpawnThunk(idx int, target string, args []ast.Arg) {
	if len(args) == 0 {
		e.line(fmt.Sprintf("static void zg_spawnthunk_%d(void *p) { (void)p; %s(); }", idx, target))
		e.blank()
		return
	}
	e.line(fmt.Sprintf("typedef struct { %s } zg_spawnenv_%d;", e.captureFields(nil, args), idx))
	e.line(fmt.Sprintf("static void zg_spawnthunk_%d(void *p) {", idx))
	e.indent++
	// The env is freed from the coroutine's cleanup stack, not after the call: a coroutine
	// that aborts never returns from it, and the scheduler unwinds that stack on the abort
	// path as well as the normal one. The callee's own parameter drops sit above this on
	// the same stack, so a captured channel's sender release still runs first — which is
	// what lets a crashing producer close the channel instead of hanging its consumer.
	e.line("zrt_defer(zg_spawn_free, p);")
	e.line(fmt.Sprintf("zg_spawnenv_%d *zg_e = (zg_spawnenv_%d *)p;", idx, idx))
	e.line(fmt.Sprintf("%s(%s);", target, captureCallArgs("zg_e", false, len(args))))
	e.indent--
	e.line("}")
	e.blank()
}
