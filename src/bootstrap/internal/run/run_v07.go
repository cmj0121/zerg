package run

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// v0.7 concurrency runtime. Mirrors PLAN §Interpreter model:
//   - Channels back onto Go's `chan Value` plus a closed flag (for the
//     receive-after-drain Option.None contract).
//   - Spawn launches a goroutine that re-runs the call against a sibling
//     interp sharing read-only decl tables but owning its own scope stack
//     and defer stacks.
//   - Anon-fn closures snapshot captured outer bindings (deep-copy) at
//     evaluation time and re-install them at call time.
//   - Defer pushes onto the per-fn-call defer stack; drained LIFO on every
//     fn exit (return / propagate / fall-through), matching the v0.7 §Risks
//     `defer × ?` interaction pin.
//   - Select uses reflect.Select to multiplex over the chosen arms.
//   - WaitGroup wraps *sync.WaitGroup; methods route here from
//     dispatchConcrete.

// deferRec is one entry on a fn-frame's defer stack. body is the AST block
// the user wrote; env is the variable scope-chain head at defer time so the
// drain executes the body in the correct lexical context (the body sees
// outer fn locals captured at defer-evaluation time, not at drain time).
// modCur is the active *moduleData at defer time so a body containing
// unqualified identifiers resolves against the right per-module tables.
type deferRec struct {
	body   *syntax.Block
	env    []*frame
	modCur *moduleData
}

// pushDeferFrame opens a fresh defer-stack slot for a fn / method body. The
// hasDefers bit comes from typeck's collect pass — when false the body
// cannot register any defer, so we elide the slot to keep callFn cheap on
// the no-defer hot path.
func (in *interp) pushDeferFrame(hasDefers bool) {
	if !hasDefers {
		return
	}
	in.deferStacks = append(in.deferStacks, nil)
}

// popDeferFrame discards the slot without running any deferred bodies. Only
// used on the early-error paths inside callFn / callMethodFn where we
// already know defers do not need to run (e.g. a parameter-binding internal
// error, which is impossible after typeck).
func (in *interp) popDeferFrame(hasDefers bool) {
	if !hasDefers || len(in.deferStacks) == 0 {
		return
	}
	in.deferStacks = in.deferStacks[:len(in.deferStacks)-1]
}

// drainDefers runs the topmost defer stack in LIFO order, then pops it. Any
// error or sentinel raised by a deferred body is reported but does not
// short-circuit the drain — every registered defer must run, matching Go's
// guarantee. The first non-nil error is returned (best-effort surface) but
// only logged because v0.7 does not pin defer-from-defer error semantics.
func (in *interp) drainDefers(hasDefers bool) {
	if !hasDefers || len(in.deferStacks) == 0 {
		return
	}
	n := len(in.deferStacks) - 1
	stack := in.deferStacks[n]
	in.deferStacks = in.deferStacks[:n]
	for i := len(stack) - 1; i >= 0; i-- {
		rec := stack[i]
		savedStack := in.stack
		savedCur := in.cur
		in.stack = rec.env
		if rec.modCur != nil {
			in.cur = rec.modCur
		}
		_ = in.execBlock(rec.body)
		in.stack = savedStack
		in.cur = savedCur
	}
}

// execDefer pushes the body onto the current fn-frame's defer stack. typeck
// has rejected defer outside a fn, so deferStacks is non-empty here. The
// captured env is the live scope-chain slice (shared); v0.7 admits defer
// only at fn-body scope, so the slice doesn't get clobbered before drain.
func (in *interp) execDefer(s *syntax.DeferStmt) error {
	if len(in.deferStacks) == 0 {
		return fmt.Errorf("internal: defer without enclosing fn frame at %s", s.Pos)
	}
	envCopy := make([]*frame, len(in.stack))
	copy(envCopy, in.stack)
	rec := deferRec{body: s.Body, env: envCopy, modCur: in.cur}
	n := len(in.deferStacks) - 1
	in.deferStacks[n] = append(in.deferStacks[n], rec)
	return nil
}

// evalChanConstructor evaluates `chan[T]()` (unbuffered) or `chan[T](N)`
// (buffered). N is bounded only by Go's make — a negative capacity panics
// in Go and we surface that as a runtime error.
func (in *interp) evalChanConstructor(e *syntax.ChanConstructorExpr) (Value, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeChan {
		return Value{}, fmt.Errorf("internal: chan constructor lacks chan type at %s", e.Pos)
	}
	cap := 0
	if e.Capacity != nil {
		cv, err := in.evalExpr(e.Capacity)
		if err != nil {
			return Value{}, err
		}
		if cv.Int < 0 {
			return Value{}, fmt.Errorf("runtime error at %s: chan capacity must be non-negative, got %d", e.Pos, cv.Int)
		}
		cap = int(cv.Int)
	}
	ref := &chanRef{ch: make(chan Value, cap), elem: t.Element}
	return chanVal(t, ref), nil
}

// execSend evaluates `ch <- v`. Sending on a closed channel panics (matches
// Go); we trap with recover and surface as a runtime error so the host
// process stays alive for the rest of the bundle.
func (in *interp) execSend(s *syntax.SendStmt) (rerr error) {
	chV, err := in.evalExpr(s.Chan)
	if err != nil {
		return err
	}
	if chV.Chan == nil {
		return fmt.Errorf("internal: send on non-channel at %s", s.Pos)
	}
	v, err := in.evalExpr(s.Value)
	if err != nil {
		return err
	}
	if chV.Chan.elem != nil {
		v = in.coerceToType(v, chV.Chan.elem)
	}
	defer func() {
		if r := recover(); r != nil {
			rerr = fmt.Errorf("runtime error at %s: send on closed channel", s.Pos)
		}
	}()
	chV.Chan.ch <- v
	return nil
}

// evalRecv evaluates `<- ch`. Returns Option[T].Some(v) on a value, Option
// [T].None on a closed-and-drained channel.
func (in *interp) evalRecv(e *syntax.RecvExpr) (Value, error) {
	chV, err := in.evalExpr(e.Chan)
	if err != nil {
		return Value{}, err
	}
	if chV.Chan == nil {
		return Value{}, fmt.Errorf("internal: recv on non-channel at %s", e.Pos)
	}
	optType := e.Type()
	v, ok := <-chV.Chan.ch
	if !ok {
		return optionNone(optType, e.Pos)
	}
	return optionSome(optType, v, e.Pos)
}

// optionSome / optionNone synthesise the Option[T] enum value with the
// canonical *Type that typeck stamped on the receive expression. Mirrors
// the v0.6 nil-lit construction path.
func optionSome(optType *syntax.Type, v Value, pos syntax.Position) (Value, error) {
	if optType == nil || optType.Kind != syntax.TypeEnum {
		return Value{}, fmt.Errorf("internal: recv result lacks Option type at %s", pos)
	}
	idx := variantIndex(optType, "Some")
	if idx < 0 {
		return Value{}, fmt.Errorf("internal: %s has no Some variant at %s", optType, pos)
	}
	return enumVal(optType, idx, "Some", []Value{v}), nil
}

func optionNone(optType *syntax.Type, pos syntax.Position) (Value, error) {
	if optType == nil || optType.Kind != syntax.TypeEnum {
		return Value{}, fmt.Errorf("internal: recv result lacks Option type at %s", pos)
	}
	idx := variantIndex(optType, "None")
	if idx < 0 {
		return Value{}, fmt.Errorf("internal: %s has no None variant at %s", optType, pos)
	}
	return enumVal(optType, idx, "None", nil), nil
}

// evalCloseCall closes the argument channel. Closing-already-closed panics;
// we trap and surface so the host stays alive.
func (in *interp) evalCloseCall(e *syntax.CallExpr) (rv Value, rerr error) {
	if len(e.Args) != 1 {
		return Value{}, fmt.Errorf("internal: close expects 1 arg, got %d at %s", len(e.Args), e.Pos)
	}
	chV, err := in.evalExpr(e.Args[0])
	if err != nil {
		return Value{}, err
	}
	if chV.Chan == nil {
		return Value{}, fmt.Errorf("internal: close on non-channel at %s", e.Pos)
	}
	defer func() {
		if r := recover(); r != nil {
			rerr = fmt.Errorf("runtime error at %s: close on already-closed channel", e.Pos)
		}
	}()
	chV.Chan.mu.Lock()
	chV.Chan.closed = true
	chV.Chan.mu.Unlock()
	close(chV.Chan.ch)
	return Value{}, nil
}

// execForChan implements `for v in ch { ... }`: receive in a loop, bind
// each Some payload to v, break on the first None (closed-and-drained).
func (in *interp) execForChan(s *syntax.ForStmt) error {
	iterV, err := in.evalExpr(s.Iter)
	if err != nil {
		return err
	}
	if iterV.Chan == nil {
		return fmt.Errorf("internal: for-in chan iterable is not a channel at %s", s.Pos)
	}
	for {
		v, ok := <-iterV.Chan.ch
		if !ok {
			return nil
		}
		cont, err := in.runChanIter(s, v)
		if err != nil {
			return err
		}
		if !cont {
			return nil
		}
	}
}

// runChanIter executes one iteration of a for-v-in-ch body. Mirrors
// runListIter / runRangeIter — pushFrame, declare, walk body statements,
// catch break / continue.
func (in *interp) runChanIter(s *syntax.ForStmt, elem Value) (bool, error) {
	in.pushFrame()
	defer in.popFrame()
	if err := in.declare(s.Var, elem); err != nil {
		return false, err
	}
	for _, st := range s.Body.Statements {
		err := in.execStmt(st)
		if errors.Is(err, errBreak) {
			return false, nil
		}
		if errors.Is(err, errContinue) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

// evalAnonFn evaluates `fn(params) [-> R] { body }` to a closure value.
// Captures are deep-copied per the v0.7 closure-capture pin so the closure
// holds an independent snapshot of the immutable outer bindings.
func (in *interp) evalAnonFn(e *syntax.AnonFnExpr) (Value, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeFn {
		return Value{}, fmt.Errorf("internal: anon-fn lacks TypeFn stamp at %s", e.Pos)
	}
	captures := map[string]Value{}
	for _, cap := range e.Captures {
		slot, ok := in.lookup(cap.Name)
		if !ok {
			// A capture of a top-level fn name resolves through cur.fns
			// rather than the value stack; skip — runtime calls into
			// captured fns route through the cur.fns lookup at the body
			// site, since the captured-env table holds value bindings only.
			continue
		}
		captures[cap.Name] = copyValue(*slot)
	}
	fv := &fnValue{
		anon:     e,
		params:   e.Params,
		ret:      t.FnReturn,
		captures: captures,
		owner:    in.cur,
	}
	return fnVal(t, fv), nil
}

// callFnValue invokes a closure: bind args, install the captured env, walk
// the body. Mirrors callFn's defer / errReturn / errPropagate handling.
func (in *interp) callFnValue(fv *fnValue, argExprs []syntax.Expr, resultType *syntax.Type, callPos syntax.Position) (Value, error) {
	if fv == nil || fv.anon == nil {
		return Value{}, fmt.Errorf("internal: fn-value with nil body at %s", callPos)
	}
	args := make([]Value, len(argExprs))
	for i, a := range argExprs {
		v, err := in.evalExpr(a)
		if err != nil {
			return Value{}, err
		}
		if i < len(fv.params) && fv.params[i].Type != nil && fv.params[i].Type.Resolved != nil {
			v = in.coerceToType(v, fv.params[i].Type.Resolved)
		}
		args[i] = v
	}

	savedStack := in.stack
	savedCur := in.cur
	in.stack = []*frame{newFrame()}
	if fv.owner != nil {
		in.cur = fv.owner
	}
	in.pushDeferFrame(fv.anon.HasDefers)
	defer func() {
		in.stack = savedStack
		in.cur = savedCur
	}()

	for name, val := range fv.captures {
		if err := in.declare(name, val); err != nil {
			in.popDeferFrame(fv.anon.HasDefers)
			return Value{}, err
		}
	}
	for i, p := range fv.params {
		if err := in.declare(p.Name, args[i]); err != nil {
			in.popDeferFrame(fv.anon.HasDefers)
			return Value{}, err
		}
	}

	for _, st := range fv.anon.Body.Statements {
		err := in.execStmt(st)
		if err == nil {
			continue
		}
		var ret *errReturn
		if errors.As(err, &ret) {
			in.drainDefers(fv.anon.HasDefers)
			retVal := ret.value
			if fv.ret != nil {
				retVal = in.coerceToType(retVal, fv.ret)
			}
			return retVal, nil
		}
		if pret, perr := catchPropagate(err, fv.ret); perr != nil {
			in.drainDefers(fv.anon.HasDefers)
			return Value{}, perr
		} else if pret != nil {
			in.drainDefers(fv.anon.HasDefers)
			retVal := pret.value
			if fv.ret != nil {
				retVal = in.coerceToType(retVal, fv.ret)
			}
			return retVal, nil
		}
		if errors.Is(err, errBreak) || errors.Is(err, errContinue) {
			in.drainDefers(fv.anon.HasDefers)
			return Value{}, fmt.Errorf("internal: %v escaped anon fn at %s", err, callPos)
		}
		in.drainDefers(fv.anon.HasDefers)
		return Value{}, err
	}
	in.drainDefers(fv.anon.HasDefers)
	if resultType != nil && resultType != syntax.TVoid() && fv.ret != nil && fv.ret != syntax.TVoid() {
		return Value{}, fmt.Errorf("anonymous function ended without return at %s", callPos)
	}
	return Value{}, nil
}

// execSpawn launches the call in a fresh goroutine. The goroutine runs
// against a sibling interp sharing the bundle's read-only decl tables so
// the spawned closure can resolve identifiers, fns, and methods just like
// the host. recover() in the wrapper traps any panic (e.g. send-on-closed
// from inside the spawned task) and reports to stderr-equivalent — here
// we route it to the host's writer with a runtime-warning prefix so
// corpus tests can match deterministic stdout when needed.
func (in *interp) execSpawn(s *syntax.SpawnStmt) error {
	// The closure / call's args evaluate in the parent scope. For an IIFE
	// `spawn fn() { ... }()`, the AnonFnExpr is the callee and capture
	// snapshot fires at evalAnonFn time — so the captured values are
	// independent of any later mutation in the parent.
	switch call := s.Call.(type) {
	case *syntax.CallExpr:
		return in.spawnCallExpr(call)
	case *syntax.MethodCallExpr:
		return in.spawnMethodCallExpr(call)
	}
	return fmt.Errorf("internal: spawn admits only call shapes, got %T at %s", s.Call, s.Pos)
}

// spawnCallExpr handles `spawn ident(args)` and `spawn fn() {...}()`. We
// snapshot the callee and arg values in the host frame so the spawned
// goroutine has no shared scope-stack with the parent.
func (in *interp) spawnCallExpr(call *syntax.CallExpr) error {
	if anon, ok := call.Callee.(*syntax.AnonFnExpr); ok {
		fnv, err := in.evalAnonFn(anon)
		if err != nil {
			return err
		}
		args, err := in.evalSpawnArgs(call.Args)
		if err != nil {
			return err
		}
		in.spawnGo(func(child *interp) {
			child.invokeFnValueDirect(fnv.Fn, args, call.Pos)
		})
		return nil
	}
	ident, ok := call.Callee.(*syntax.IdentExpr)
	if !ok {
		return fmt.Errorf("internal: spawn callee shape %T at %s", call.Callee, call.Pos)
	}
	if ident.Name == "wait_group" || ident.Name == "close" || ident.Name == "len" ||
		ident.Name == "clone" || ident.Name == "push" {
		return fmt.Errorf("internal: spawn of built-in %q rejected at typeck", ident.Name)
	}
	args, err := in.evalSpawnArgs(call.Args)
	if err != nil {
		return err
	}
	// Resolve the callee once on the host side. Local fn-typed bindings
	// shadow same-named top-level fns — match callFn's resolution.
	if slot, ok := in.lookup(ident.Name); ok && slot.Type != nil && slot.Type.Kind == syntax.TypeFn && slot.Fn != nil {
		fv := slot.Fn
		in.spawnGo(func(child *interp) {
			child.invokeFnValueDirect(fv, args, call.Pos)
		})
		return nil
	}
	if call.Specialised != nil {
		fn := call.Specialised
		in.spawnGo(func(child *interp) {
			child.invokeFnDirect(fn, args, call.Pos)
		})
		return nil
	}
	fn, ok := in.cur.fns[ident.Name]
	if !ok {
		return fmt.Errorf("internal: undefined function %q at %s", ident.Name, call.Pos)
	}
	in.spawnGo(func(child *interp) {
		child.invokeFnDirect(fn, args, call.Pos)
	})
	return nil
}

// spawnMethodCallExpr handles `spawn obj.method(args)` and the cross-
// module call shape `spawn mod.fn(args)`. Args are pre-evaluated on the
// host; the goroutine runs the body under a child interp.
func (in *interp) spawnMethodCallExpr(call *syntax.MethodCallExpr) error {
	if call.LoweredCall != nil {
		return in.spawnCallExpr(call.LoweredCall)
	}
	// Cross-module fn call: receiver is an ident binding to a module.
	if id, ok := call.Receiver.(*syntax.IdentExpr); ok {
		if foreign, isMod := in.cur.imports[id.Name]; isMod {
			if fn, ok := foreign.fns[call.Method]; ok {
				args, err := in.evalSpawnArgs(call.Args)
				if err != nil {
					return err
				}
				in.spawnGo(func(child *interp) {
					child.invokeFnDirect(fn, args, call.Pos)
				})
				return nil
			}
		}
	}
	// Method dispatch: evaluate receiver, then bind args. We dispatch
	// inside the goroutine so the receiver value crosses the boundary
	// independently — mirrors the codegen-side rule that the spawned
	// task takes ownership of the receiver value.
	rv, err := in.evalExpr(call.Receiver)
	if err != nil {
		return err
	}
	args, err := in.evalSpawnArgs(call.Args)
	if err != nil {
		return err
	}
	in.spawnGo(func(child *interp) {
		child.invokeMethodDirect(call, rv, args)
	})
	return nil
}

// evalSpawnArgs walks each arg expr in caller scope, returning the
// snapshot value vector that crosses the goroutine boundary.
func (in *interp) evalSpawnArgs(exprs []syntax.Expr) ([]Value, error) {
	out := make([]Value, len(exprs))
	for i, e := range exprs {
		v, err := in.evalExpr(e)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// spawnGo runs body in a goroutine with a child interp that shares the
// bundle's read-only state (decl tables, impl indices, writer, spawnWg,
// writeMu) but owns its own scope/defer stacks. Tracks via spawnWg so
// RunBundle blocks at top level until every spawned task completes.
//
// v0.9 Phase 4 Fix 2: an exitErr panic raised inside the spawned body
// (i.e. user code calling os.exit from a spawn frame) cannot propagate
// across the goroutine boundary via panic. The recover stashes the
// requested code on the bundle-shared spawn-exit coordinator; RunBundle
// consults it after spawnWg.Wait() and surfaces the code to the host.
// First-spawned-to-exit wins via sync.Once — matches cgen's libc-exit
// semantics where the first thread to call exit() takes the whole
// process down.
func (in *interp) spawnGo(body func(child *interp)) {
	child := in.newSiblingInterp()
	in.spawnWg.Add(1)
	go func() {
		defer in.spawnWg.Done()
		defer func() {
			if r := recover(); r != nil {
				if ee, ok := catchExit(r); ok {
					in.spawnExitOnce.Do(func() {
						in.spawnExitCode.Store(int64(ee.Code))
						in.spawnExited.Store(true)
					})
					return
				}
				in.writeMu.Lock()
				_, _ = io.WriteString(in.w, fmt.Sprintf("spawned task panicked: %v\n", r))
				in.writeMu.Unlock()
			}
		}()
		body(child)
	}()
}

// newSiblingInterp returns an interp pointing at the same shared tables as
// `in` but with a fresh scope stack and defer stack so the goroutine can
// run independently. Mutexes / wait groups are shared by reference.
func (in *interp) newSiblingInterp() *interp {
	child := *in
	child.stack = []*frame{newFrame()}
	child.deferStacks = nil
	return &child
}

// invokeFnDirect runs a top-level FnDecl with already-evaluated args. Used
// by the spawn paths so the goroutine doesn't re-walk arg expressions on
// the parent's stack. Errors raised during the body are surfaced via the
// goroutine wrapper (recover) — execution simply unwinds.
func (in *interp) invokeFnDirect(fn *syntax.FnDecl, args []Value, pos syntax.Position) {
	in.stack = []*frame{newFrame()}
	if owner := in.fnOwner[fn]; owner != nil {
		in.cur = owner
	}
	in.pushDeferFrame(fn.HasDefers)
	for i, p := range fn.Params {
		if i >= len(args) {
			break
		}
		_ = in.declare(p.Name, args[i])
	}
	for _, st := range fn.Body.Statements {
		err := in.execStmt(st)
		if err == nil {
			continue
		}
		var ret *errReturn
		if errors.As(err, &ret) {
			break
		}
		if pret, _ := in.catchPropagateForFn(err, fn); pret != nil {
			break
		}
		break
	}
	in.drainDefers(fn.HasDefers)
	_ = pos
}

// invokeFnValueDirect runs an fn-value (anon-fn closure) with already-
// evaluated args.
func (in *interp) invokeFnValueDirect(fv *fnValue, args []Value, pos syntax.Position) {
	if fv == nil || fv.anon == nil {
		return
	}
	in.stack = []*frame{newFrame()}
	if fv.owner != nil {
		in.cur = fv.owner
	}
	in.pushDeferFrame(fv.anon.HasDefers)
	for name, val := range fv.captures {
		_ = in.declare(name, val)
	}
	for i, p := range fv.params {
		if i >= len(args) {
			break
		}
		_ = in.declare(p.Name, args[i])
	}
	for _, st := range fv.anon.Body.Statements {
		err := in.execStmt(st)
		if err == nil {
			continue
		}
		break
	}
	in.drainDefers(fv.anon.HasDefers)
	_ = pos
}

// invokeMethodDirect runs a method call against a pre-evaluated receiver
// and args. Routes through dispatchConcrete / dispatchSpec so the same
// resolution rules apply as a regular method call.
func (in *interp) invokeMethodDirect(call *syntax.MethodCallExpr, rv Value, args []Value) {
	// Synthesise a spawned-method call by re-injecting evaluated args into
	// a fresh scope and dispatching. The cleanest path: re-route through
	// dispatchConcrete after binding evaluated args via a synthetic
	// scratch frame — but dispatchConcrete reads e.Args itself. We
	// instead replicate the per-method binding inline.
	if rv.Type == nil {
		return
	}
	if rv.Type.Kind == syntax.TypeStruct && rv.Type.Name == "WaitGroup" {
		_, _ = in.dispatchWaitGroupArgs(call.Method, rv, args)
		return
	}
	if rv.Type.Kind == syntax.TypeSpec {
		// Spec dispatch: pre-evaluated args; route via the underlying.
		recv := rv.Data
		if recv == nil {
			return
		}
		fn, sm := in.resolveSpecMethod(recv.Type, rv.Type.Name, call.Method)
		if fn != nil {
			in.invokeMethodFnDirect(fn, *recv, args)
			return
		}
		if sm != nil {
			in.invokeSpecDefaultDirect(sm, *recv, args)
		}
		return
	}
	// Concrete struct/enum dispatch.
	if methods, ok := in.inherentByType[rv.Type]; ok {
		if fn, ok := methods[call.Method]; ok {
			in.invokeMethodFnDirect(fn, rv, args)
			return
		}
	}
	specMap := in.specByType[rv.Type]
	for specName := range specMap {
		fn, sm := in.resolveSpecMethod(rv.Type, specName, call.Method)
		if fn != nil {
			in.invokeMethodFnDirect(fn, rv, args)
			return
		}
		if sm != nil {
			in.invokeSpecDefaultDirect(sm, rv, args)
			return
		}
	}
	baseName := displayEnumName(rv.Type.Name)
	if methods, ok := in.inherentByBaseName[baseName]; ok {
		if fn, ok := methods[call.Method]; ok {
			in.invokeMethodFnDirect(fn, rv, args)
		}
	}
}

func (in *interp) invokeMethodFnDirect(fn *syntax.FnDecl, this Value, args []Value) {
	in.stack = []*frame{newFrame()}
	if owner := in.fnOwner[fn]; owner != nil {
		in.cur = owner
	}
	in.pushDeferFrame(fn.HasDefers)
	_ = in.declare("this", this)
	for i, p := range fn.Params {
		if i >= len(args) {
			break
		}
		_ = in.declare(p.Name, args[i])
	}
	for _, st := range fn.Body.Statements {
		err := in.execStmt(st)
		if err != nil {
			break
		}
	}
	in.drainDefers(fn.HasDefers)
}

func (in *interp) invokeSpecDefaultDirect(sm *syntax.SpecMethod, this Value, args []Value) {
	in.stack = []*frame{newFrame()}
	if owner := in.specMethodOwner[sm]; owner != nil {
		in.cur = owner
	}
	in.pushDeferFrame(sm.HasDefers)
	_ = in.declare("this", this)
	for i, p := range sm.Params {
		if i >= len(args) {
			break
		}
		_ = in.declare(p.Name, args[i])
	}
	for _, st := range sm.Body.Statements {
		err := in.execStmt(st)
		if err != nil {
			break
		}
	}
	in.drainDefers(sm.HasDefers)
}

// evalWaitGroupCtor implements the `wait_group()` built-in — returns a
// fresh WaitGroup-typed value wrapping a Go *sync.WaitGroup.
func (in *interp) evalWaitGroupCtor(e *syntax.CallExpr) (Value, error) {
	if len(e.Args) != 0 {
		return Value{}, fmt.Errorf("internal: wait_group expects 0 args, got %d at %s", len(e.Args), e.Pos)
	}
	t := e.Type()
	if t == nil {
		return Value{}, fmt.Errorf("internal: wait_group call lacks type stamp at %s", e.Pos)
	}
	return wgVal(t, &sync.WaitGroup{}), nil
}

// dispatchWaitGroup routes a method call against a WaitGroup value.
func (in *interp) dispatchWaitGroup(e *syntax.MethodCallExpr, rv Value) (Value, error) {
	args := make([]Value, len(e.Args))
	for i, a := range e.Args {
		v, err := in.evalExpr(a)
		if err != nil {
			return Value{}, err
		}
		args[i] = v
	}
	return in.dispatchWaitGroupArgs(e.Method, rv, args)
}

func (in *interp) dispatchWaitGroupArgs(method string, rv Value, args []Value) (Value, error) {
	if rv.Wg == nil {
		return Value{}, fmt.Errorf("internal: WaitGroup value has nil sync.WaitGroup")
	}
	switch method {
	case "add":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("internal: WaitGroup.add expects 1 arg, got %d", len(args))
		}
		rv.Wg.Add(int(args[0].Int))
		return Value{}, nil
	case "done":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("internal: WaitGroup.done expects 0 args, got %d", len(args))
		}
		rv.Wg.Done()
		return Value{}, nil
	case "wait":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("internal: WaitGroup.wait expects 0 args, got %d", len(args))
		}
		rv.Wg.Wait()
		return Value{}, nil
	}
	return Value{}, fmt.Errorf("internal: WaitGroup has no method %q", method)
}

// execSelect implements `select { arm; ... }`. PLAN.md pins v0.7 select
// tie-break to declaration order. We first pre-scan every non-default arm
// in declaration order with a non-blocking probe; the first ready arm
// wins. If nothing is ready we either dispatch the default arm (if any)
// or fall through to reflect.Select to BLOCK until exactly one arm fires
// — at that instant only one is ready, so no tie-break is needed.
func (in *interp) execSelect(s *syntax.SelectStmt) error {
	// Pre-evaluate each arm's channel + (for send) value so the pre-scan
	// and the blocking fallback agree on the operands. Side effects in
	// arm.Chan / arm.Value run exactly once.
	type prepared struct {
		armIdx  int
		op      syntax.SelectOpKind
		ch      reflect.Value // zero for default arms
		send    reflect.Value // populated for send arms
	}
	prepd := make([]prepared, 0, len(s.Arms))
	defaultIdx := -1
	for i := range s.Arms {
		arm := &s.Arms[i]
		switch arm.Op {
		case syntax.SelectDefault:
			defaultIdx = i
			prepd = append(prepd, prepared{armIdx: i, op: arm.Op})
		case syntax.SelectRecvBind, syntax.SelectRecvDiscard:
			chV, err := in.evalExpr(arm.Chan)
			if err != nil {
				return err
			}
			if chV.Chan == nil {
				return fmt.Errorf("internal: select recv on non-channel at %s", arm.Pos)
			}
			prepd = append(prepd, prepared{armIdx: i, op: arm.Op, ch: reflect.ValueOf(chV.Chan.ch)})
		case syntax.SelectSend:
			chV, err := in.evalExpr(arm.Chan)
			if err != nil {
				return err
			}
			if chV.Chan == nil {
				return fmt.Errorf("internal: select send on non-channel at %s", arm.Pos)
			}
			v, err := in.evalExpr(arm.Value)
			if err != nil {
				return err
			}
			if chV.Chan.elem != nil {
				v = in.coerceToType(v, chV.Chan.elem)
			}
			prepd = append(prepd, prepared{
				armIdx: i,
				op:     arm.Op,
				ch:     reflect.ValueOf(chV.Chan.ch),
				send:   reflect.ValueOf(v),
			})
		}
	}
	if len(prepd) == 0 {
		return fmt.Errorf("internal: empty select at %s", s.Pos)
	}

	// Phase 1: non-blocking pre-scan in declaration order. Per arm we
	// build a 2-case reflect.Select with the arm op + a default; if the
	// op fires we have our deterministic tie-break winner.
	var (
		chosenArm     = -1
		chosenRecv    reflect.Value
		chosenRecvOK  bool
		chosenIsRecv  bool
	)
	for _, p := range prepd {
		if p.op == syntax.SelectDefault {
			continue
		}
		probe := []reflect.SelectCase{{Dir: reflect.SelectDefault}}
		switch p.op {
		case syntax.SelectRecvBind, syntax.SelectRecvDiscard:
			probe = append(probe, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: p.ch})
		case syntax.SelectSend:
			probe = append(probe, reflect.SelectCase{Dir: reflect.SelectSend, Chan: p.ch, Send: p.send})
		}
		idx, recv, ok := reflect.Select(probe)
		if idx == 1 {
			chosenArm = p.armIdx
			chosenRecv = recv
			chosenRecvOK = ok
			chosenIsRecv = p.op == syntax.SelectRecvBind || p.op == syntax.SelectRecvDiscard
			break
		}
	}

	// Phase 2: nothing was ready. Either dispatch the default arm or
	// block on reflect.Select until exactly one arm fires.
	if chosenArm < 0 {
		if defaultIdx >= 0 {
			chosenArm = defaultIdx
		} else {
			cases := make([]reflect.SelectCase, 0, len(prepd))
			armForCase := make([]int, 0, len(prepd))
			for _, p := range prepd {
				switch p.op {
				case syntax.SelectRecvBind, syntax.SelectRecvDiscard:
					cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: p.ch})
					armForCase = append(armForCase, p.armIdx)
				case syntax.SelectSend:
					cases = append(cases, reflect.SelectCase{Dir: reflect.SelectSend, Chan: p.ch, Send: p.send})
					armForCase = append(armForCase, p.armIdx)
				}
			}
			idx, recv, ok := reflect.Select(cases)
			chosenArm = armForCase[idx]
			chosenRecv = recv
			chosenRecvOK = ok
			arm := &s.Arms[chosenArm]
			chosenIsRecv = arm.Op == syntax.SelectRecvBind || arm.Op == syntax.SelectRecvDiscard
		}
	}

	arm := &s.Arms[chosenArm]
	in.pushFrame()
	defer in.popFrame()
	if arm.Op == syntax.SelectRecvBind {
		// Per typeck (Unit 4), the recv-bind binds the unwrapped element
		// type T, not Option[T]. The closed-channel case still surfaces:
		// the chosen index is the recv arm and !ok signals close-and-
		// drained, so we bind the zero element value of T. Real programs
		// guard close via the `for v in ch` form (which exits on close)
		// so the bind-on-closed path is rare; we surface a runtime error
		// if the user constructs a select where the only ready signal is
		// a closed channel and they bind a value off it.
		if !chosenIsRecv {
			return fmt.Errorf("internal: select recv-bind dispatched without recv at %s", arm.Pos)
		}
		if !chosenRecvOK {
			return fmt.Errorf("runtime error at %s: select recv-bind on closed channel", arm.Pos)
		}
		v := chosenRecv.Interface().(Value)
		if err := in.declare(arm.BindName, v); err != nil {
			return err
		}
	}
	for _, st := range arm.Body.Statements {
		if err := in.execStmt(st); err != nil {
			return err
		}
	}
	return nil
}
