package emit

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// The seed's whole answer to concurrency, and it is a refusal.
//
// Tier 2 of the contract in ../../README.md: the self-host chain contains no `spawn`, no
// `chan[T]`, no `select` and no `<-`, so the seed has nothing to build with them. What used
// to be here — 878 lines lowering a send, a receive, a select's descriptor array, a spawn's
// captured environment, and the per-direction refcount that closes a channel — was a SECOND
// implementation of a chapter the seed is not the authority on, and every gap closed on that
// chapter was the two compilers disagreeing. A refusal cannot disagree.
//
// prepareChannels keeps its name and its place in the prepare sequence: it is where the
// refusal lands, before any C is written, and prepareRuntime aborts on a non-empty diag
// list right after it.

// skipBundledConcurrency drops a BUNDLED module's function whose body touches concurrency,
// without a word.
//
// The whole-program flatten hands the seed every member of every module a program imports,
// reachable or not — so `import "time"` for `now()` also brings `after` and `ticker`, which
// are channels by definition ("a timer is a channel"). Refusing those would stop a program
// that never asked for one; lowering them is impossible now. Skipping is the third answer,
// and it is safe because a program that actually CALLS one names a channel in its own
// module — as a binding, an argument or a receive — where rejectConcurrency is waiting.
//
// It is the same reasoning rejectDirectionalChans has always used to scope itself to the
// entry module, one step further: not only "do not judge it", but "do not emit it".
func (e *emitter) skipBundledConcurrency(inst *mono.Instance) bool {
	if inst.Origin == nil || inst.Origin.Module == "" {
		return false
	}
	if containsChan(inst.Ret) {
		return true
	}
	for _, p := range inst.Params {
		if containsChan(p) {
			return true
		}
	}
	if inst.Origin.Body == nil {
		return false
	}
	_, what := firstConcurrency(inst.Origin.Body)
	return what != ""
}

func (e *emitter) prepareChannels() {
	if !e.concurrency {
		return
	}
	e.rejectConcurrency()
}

// programUsesConcurrency reports whether the program uses concurrency: any function body
// contains a `spawn`/send/`select`, any binding, expression, or signature type transitively
// holds a channel, or the program lowers a scheduler-floor intrinsic. It is the single
// trigger for Manifest.Concurrency (mirroring programUsesRef), so a value-only program links
// none of the scheduler and stays byte-identical.
func (e *emitter) programUsesConcurrency() bool {
	// A scheduler-floor intrinsic is a use of the scheduler even in a program that names no
	// channel and no `spawn`: its primitive lives in sched.c, which only Concurrency links.
	if e.programUsesSchedFloor() {
		return true
	}
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

// rejectConcurrency refuses every concurrency form, by name, reporting whether it did.
//
// Tier 2 of the contract in ../../README.md: the self-host chain contains no `spawn`, no
// `chan[T]`, no `select` and no `<-`, so the seed has nothing to build with them and
// carries no opinion about them. That silence is the point — a compiler that lowers a
// feature it is not the authority on is a second implementation that can DISAGREE, and
// every gap closed on the concurrency chapter was one of those disagreements.
//
// One refusal per program rather than per site: a concurrent program is concurrent
// throughout, and a reader who writes one wants to be told where the line is, not handed
// forty diagnostics. It is scoped to the program's OWN module, like the directional-type
// refusal below and for the same reason — a bundled stdlib module may name a channel in a
// signature the program never reaches.
func (e *emitter) rejectConcurrency() bool {
	for _, inst := range e.prog.Funcs {
		if inst.Origin == nil || inst.Origin.Module != "" || inst.Origin.Body == nil {
			continue
		}
		if at, what := firstConcurrency(inst.Origin.Body); what != "" {
			e.diags.Add(at, "the bootstrap seed does not lower %s: concurrency belongs to the "+
				"self-hosting compiler, which implements the whole chapter", what)
			return true
		}
	}
	return false
}

// firstConcurrency finds the first concurrency form in a body and names it the way the
// source writes it, so the diagnostic points at what the programmer typed.
func firstConcurrency(b *ast.Block) (token.Span, string) {
	var at token.Span
	var what string
	walkStmts(b, func(s ast.Stmt) {
		if what != "" {
			return
		}
		switch n := s.(type) {
		case *ast.SpawnStmt:
			at, what = n.Span(), "`spawn`"
		case *ast.SelectStmt:
			at, what = n.Span(), "`select`"
		case *ast.SendStmt:
			at, what = n.Span(), "a send `ch <- v`"
		}
	})
	if what != "" {
		return at, what
	}
	walkBlockExprs(b, func(x ast.Expr) {
		if what != "" {
			return
		}
		switch n := x.(type) {
		case *ast.ChanNew:
			at, what = n.Span(), "`chan[T]`"
		case *ast.Recv:
			at, what = n.Span(), "a receive `<-ch`"
		}
	})
	return at, what
}

// containsChan reports whether a type transitively holds a channel, the value form that
// pulls in the scheduler. It mirrors containsRef's structural walk, with a visited set so
// a recursive (S1) type terminates instead of looping forever — a nominal already on the
// walk contributes no new channel, so revisiting it yields false. The set is allocated only
// once a nominal is actually reached, since this is asked of every recorded type in the
// program and the overwhelming majority are scalars.
func containsChan(t sema.Type) bool {
	return containsChanSeen(t, nil)
}

func containsChanSeen(t sema.Type, seen map[string]bool) bool {
	nominal := func(name string, parts []sema.Type) bool {
		if seen == nil {
			seen = map[string]bool{}
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		for _, p := range parts {
			if containsChanSeen(p, seen) {
				return true
			}
		}
		return false
	}
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
	case *types.List:
		return containsChanSeen(x.Elem, seen)
	case *types.Struct:
		return nominal(x.String(), structFieldTypes(x))
	case *types.Enum:
		return nominal(x.String(), enumPayloadTypes(x))
	}
	return false
}
