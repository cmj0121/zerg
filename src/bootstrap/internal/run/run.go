// Package run is the v0.1 tree-walking interpreter for `zerg run`.
//
// Run.Run takes the parser's AST, calls syntax.Check internally to annotate
// types and reject ill-formed programs, then walks the typed AST to produce
// stdout. The interpreter is the parity reference: its bytes-on-stdout for
// any v0.1 program must match the C codegen's bytes-on-stdout for the same
// program (Unit 4). The print format and numeric semantics are pinned in
// PLAN.md and reproduced here without freelancing.
package run

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// Run executes prog, sending program output to w. It first calls syntax.Check
// so callers (CLI, tests) do not need to remember to do so — the interpreter
// will not walk an un-typechecked tree because nearly every node-walker
// relies on Expr.Type() being non-nil.
//
// The returned error is for type errors (propagated verbatim from Check) or
// runtime failures. v0.1 has very few runtime failures: a short write on w,
// a call to a function that fails to return when its declared return type is
// non-void, or one of the documented "undefined" cases (div-by-zero on int,
// INT64_MIN/-1) that PLAN.md says is not exercised by the corpus.
func Run(prog *syntax.Program, w io.Writer) error {
	if err := syntax.Check(prog); err != nil {
		return err
	}
	return runChecked(prog, w)
}

// RunChecked walks an already-type-checked program. Use this when the
// caller has run syntax.CheckBundle on a multi-module bundle and wants
// to interpret the entry program without redundantly re-checking it.
//
// Backward compatible: builds a one-module bundle and forwards to
// RunBundle, so v0.0–v0.4 callers keep their single-program surface.
func RunChecked(prog *syntax.Program, w io.Writer) error {
	return runChecked(prog, w)
}

// RunBundle is the v0.5 entry: walk every module's decls, then execute the
// entry module's top-level statements. Cross-module fn calls, struct/enum
// construction, method dispatch, enum-payload match, and spec coercion all
// route through per-module decl tables and a bundle-wide impl index keyed
// by canonical *Type pointer.
//
// CheckBundle must have run before RunBundle — the interpreter reads
// typeck-stamped fields (StructLit.Module, EnumLit.Module, *Expr.Type(),
// MethodCallExpr.Lowered/LoweredCall) and trusts pub gating / orphan rule
// rejection upstream.
func RunBundle(bundle syntax.BundleView, w io.Writer) error {
	_, _, err := RunBundleWithOptions(bundle, w, Options{})
	return err
}

// Options carries v0.9 host-supplied process surface to the interpreter.
// Argv is the program's command-line arguments (index 0 is the
// executable / entry-file path; subsequent entries are caller args). The
// CLI driver populates from os.Args; the REPL hard-codes a single
// "<repl>" element; tests pass arbitrary lists.
type Options struct {
	Argv []string
}

// RunBundleWithOptions is the v0.9 entry point. It returns the program's
// exit code (0 unless `os.exit(code)` was invoked) plus an "exited" flag
// that distinguishes a clean termination (exited=false, code=0) from an
// explicit os.exit(0) (exited=true, code=0). Hosts that need either
// signal (CLI driver propagates exit code; REPL surfaces a message)
// consult both fields.
func RunBundleWithOptions(bundle syntax.BundleView, w io.Writer, opts Options) (exitCode int, exited bool, err error) {
	if bundle == nil {
		return 0, false, nil
	}
	in := newBundleInterp(bundle, w)
	in.argv = opts.Argv
	entry := bundle.BundleEntry()
	if entry == nil {
		return 0, false, nil
	}
	// An exit-style call (os.exit / sys.syscall.exit) raises an exitErr
	// panic that unwinds every fn-call frame to here. Recover and stash
	// the code on the interpreter so the host can read it. PLAN.md
	// §"Defer × exit": defers are intentionally NOT drained, spawned tasks
	// are NOT joined — we return immediately, mirroring Go's os.Exit.
	defer func() {
		if r := recover(); r != nil {
			if ee, ok := catchExit(r); ok {
				in.exitCode = ee.Code
				in.exited = true
				exitCode = ee.Code
				exited = true
				return
			}
			panic(r)
		}
	}()
	// v0.14 P1: initialize imported modules' top-level let / mut / const
	// bindings into their moduleVars frames before the entry runs. This
	// is what lets an imported module's fn body reference module-scope
	// state — the lookup() fall-through reaches in.cur.moduleVars only
	// if those bindings have been declared somewhere. The entry module
	// is skipped here; its top-level execution loop below handles its
	// own bindings (against in.stack[0] == entryMd.moduleVars).
	if err := in.initImportedModuleVars(bundle); err != nil {
		return 0, false, err
	}
	prog := entry.ModuleProgram()
	for _, stmt := range prog.Statements {
		switch stmt.(type) {
		case *syntax.FnDecl, *syntax.SpecDecl, *syntax.ImplDecl,
			*syntax.StructDecl, *syntax.EnumDecl, *syntax.ImportDecl:
			// Decls are collected into per-module tables at init. At top
			// level they are declarations, not executable statements.
			continue
		}
		if err := in.execStmt(stmt); err != nil {
			in.spawnWg.Wait()
			if in.spawnExited.Load() {
				return int(in.spawnExitCode.Load()), true, nil
			}
			return 0, false, err
		}
	}
	in.spawnWg.Wait()
	// v0.9 Phase 4 Fix 2: a spawned goroutine that called os.exit stashed
	// the code on the bundle-shared coordinator (the panic cannot cross
	// the goroutine boundary). Surface it now so the host sees the same
	// (exited, code) pair the cgen half would produce.
	if in.spawnExited.Load() {
		return int(in.spawnExitCode.Load()), true, nil
	}
	return 0, false, nil
}

func runChecked(prog *syntax.Program, w io.Writer) error {
	return RunBundle(singleProgramBundleAdapter{prog: prog}, w)
}

// singleProgramBundleAdapter wraps a *Program in the BundleView interface
// so single-program callers (Run, RunChecked) route through the same
// RunBundle entry as multi-module callers.
type singleProgramBundleAdapter struct {
	prog *syntax.Program
}

func (b singleProgramBundleAdapter) BundleEntry() syntax.ModuleView {
	return singleProgramModuleAdapter{prog: b.prog}
}
func (b singleProgramBundleAdapter) BundleModules() []syntax.ModuleView {
	return []syntax.ModuleView{singleProgramModuleAdapter{prog: b.prog}}
}

type singleProgramModuleAdapter struct {
	prog *syntax.Program
}

func (m singleProgramModuleAdapter) ModuleName() string             { return "main" }
func (m singleProgramModuleAdapter) ModuleProgram() *syntax.Program { return m.prog }
func (m singleProgramModuleAdapter) ModuleImports() []syntax.ImportView {
	return nil
}

// ---------------------------------------------------------------------------
// Interpreter state.
// ---------------------------------------------------------------------------

// interp holds the per-Run mutable state. Variables live on a stack of
// frames. Each call site, each block, and each for-range iteration push a
// fresh frame. A frame holds the names introduced inside its scope only;
// lookup walks toward the root.
//
// v0.5 multi-module: the interpreter holds per-module decl tables (one
// moduleData per module in the bundle) plus a bundle-wide impl index keyed
// by canonical *Type pointer. cur is the lexically-active module — when a
// fn or method is called, we switch cur to the fn's owning module so its
// body can resolve unqualified identifiers (its own structs, enums, fns,
// and import bindings) against the right tables. fnOwner / specMethodOwner
// stamp every FnDecl / SpecMethod with the module that declared it so the
// switch is O(1).
type interp struct {
	w io.Writer

	// modules holds per-module decl/import tables, keyed by *syntax.Program
	// pointer (every ModuleView has a unique Program).
	modules map[*syntax.Program]*moduleData
	// cur is the active module for unqualified identifier resolution.
	// Switched on every fn / method call to the callee's owning module.
	cur *moduleData

	// fnOwner and methodOwner map every FnDecl / spec-default body to the
	// module that declared it, so a cross-module call can find the callee's
	// lexical context in O(1).
	fnOwner         map[*syntax.FnDecl]*moduleData
	specMethodOwner map[*syntax.SpecMethod]*moduleData

	// Bundle-wide impl index keyed by canonical *Type pointer. typeck has
	// already validated cross-module rules, so a single union of every
	// module's impls is correct for runtime dispatch.
	//
	//   inherentByType[recv][methodName]  → FnDecl
	//   specByType[recv][specName][methodName] → FnDecl (override; absent
	//     entries fall through to the spec's default body)
	//   specByPair[(recv, specName)] = true when the impl block exists, so
	//     vtable lookup ("does this concrete-spec pair have an impl?") is O(1).
	inherentByType map[*syntax.Type]map[string]*syntax.FnDecl
	specByType     map[*syntax.Type]map[string]map[string]*syntax.FnDecl
	specByPair     map[specPairKey]bool

	// v0.6 generic-impl fallback tables. Generic impls (`impl[T] Box[T]`)
	// have no concrete receiver *Type at typeck time — id.Receiver is nil
	// because the impl applies to every monomorphisation of Box[T]. We
	// register their methods by base receiver name (`"Box"`) so dispatch
	// can fall through when the *Type-keyed lookup misses on a
	// monomorphized receiver (`recv.Name == "Box[int]"`).
	inherentByBaseName map[string]map[string]*syntax.FnDecl
	specByBaseName     map[string]map[string]map[string]*syntax.FnDecl
	// specDeclsByName indexes spec declarations across the whole bundle so
	// spec default bodies can be located by spec-name regardless of which
	// module declared the spec.
	specDeclsByName map[string]*syntax.SpecDecl

	// stack[0] is the top-level frame; the active frame is stack[len(stack)-1].
	// We keep the slice rather than a parent-pointer linked list because
	// pushing/popping a Go slice is allocation-light and the depth stays small
	// in practice.
	stack []*frame

	// v0.7: per-fn-call defer stacks (LIFO). callFn / callMethodFn /
	// callFnValue push a fresh slot; the body's DeferStmt appends; the
	// fn-call epilogue drains in reverse before returning. The slice grows
	// only during execution so ownership matches the call stack 1:1.
	deferStacks [][]deferRec

	// v0.7: synchronisation for spawned goroutines and stdout. spawnWg
	// tracks all live spawned tasks so Run blocks at the top-level until
	// every task has either returned or paniced — without this the v0.7
	// REPL behaviour ("synchronously wait for spawned tasks before
	// returning the next prompt") and corpus determinism would not hold.
	// writeMu guards in.w against concurrent writes from spawned tasks.
	// Both are pointers so a sibling interp built via shallow-copy in
	// newSiblingInterp can share them without violating sync.noCopy.
	spawnWg *sync.WaitGroup
	writeMu *sync.Mutex

	// v0.9 Unit 1: exit-sentinel state. exited is set when an exitErr
	// panic was caught at the RunBundle boundary; exitCode carries the
	// requested code. The fields are read-only by the host; the recover
	// hook in RunBundle is the sole writer.
	exited   bool
	exitCode int

	// v0.9 Phase 4 Fix 2: spawn × exit coordination. An os.exit call
	// inside a spawned goroutine cannot panic across the goroutine
	// boundary (Go runtime rule), so the spawn-recover stashes the code
	// here and the RunBundle main path consults it after spawnWg.Wait()
	// completes. First-spawned-to-exit wins via the sync.Once gate —
	// matches cgen's libc-exit semantics where the first thread to call
	// exit() takes the whole process down. Pointers so newSiblingInterp's
	// shallow-copy preserves the shared coordinator.
	spawnExitOnce *sync.Once
	spawnExitCode *atomic.Int64
	spawnExited   *atomic.Bool

	// v0.9 Unit 3: argv from the host. Index 0 is the executable name
	// (.zg path for `zerg run`, "<repl>" at the REPL, an arbitrary
	// sentinel in tests). os_argv_len / os_argv_at index into this slice.
	argv []string

	// v0.14 T2: lazy snapshot of os.Environ() shared by envp_len and
	// envp_at within a single interpreter run. Captured on first read so
	// the envp index space stays stable for a pure-Zerg env() loop; per-
	// interpreter (not process-global) so test ordering doesn't leak env
	// state from one run into the next.
	envpCache []string
}

// moduleData is the per-module decl table. Indexed maps mirror typeck's
// per-module checker tables; we duplicate the structure here so the
// runtime can resolve unqualified identifiers (fns, enums) and the import
// binding map without re-walking the AST.
type moduleData struct {
	view    syntax.ModuleView
	prog    *syntax.Program
	name    string
	fns     map[string]*syntax.FnDecl
	enums   map[string]*syntax.Type
	structs map[string]*syntax.Type
	// imports binds a local name (alias or bare path) to the target
	// module's data. Used to recognise `mod.foo()` shapes at run-time.
	imports map[string]*moduleData
	// moduleVars holds the module's top-level let / mut / const bindings.
	// Populated at init time (for imported modules) or by the entry
	// module's top-level execution (for the entry). lookup() falls
	// through to in.cur.moduleVars after walking the call-frame stack
	// so fn bodies see their owning module's top-level state.
	moduleVars *frame
}

// specPairKey is the (canonical *Type, spec name) pair used to index spec
// impls bundle-wide. We index by *Type pointer (canonical per defining
// module) rather than name because two modules can both declare a struct
// "Counter" — their canonical *Type pointers differ.
type specPairKey struct {
	recv     *syntax.Type
	specName string
}

// frame is one rung of the variable scope stack. Names live here only as
// long as the enclosing block or call is active.
type frame struct {
	vars map[string]*Value
}

func newFrame() *frame { return &frame{vars: map[string]*Value{}} }

// newBundleInterp constructs the interpreter, walks every module's decls
// into per-module tables, and builds the bundle-wide impl index keyed by
// canonical *Type pointer.
//
// Resolution of impl receiver types is by name lookup in the impl's
// declaring module's struct/enum tables (or, when ImplDecl.TypeModule is
// non-empty, in the named imported module's tables). Same for the spec
// slot. typeck has already validated existence, pubness, and the orphan
// rule, so a missing-entry fall-through is treated as an internal error.
func newBundleInterp(bundle syntax.BundleView, w io.Writer) *interp {
	in := &interp{
		w:                  w,
		spawnWg:            &sync.WaitGroup{},
		writeMu:            &sync.Mutex{},
		spawnExitOnce:      &sync.Once{},
		spawnExitCode:      &atomic.Int64{},
		spawnExited:        &atomic.Bool{},
		modules:            map[*syntax.Program]*moduleData{},
		fnOwner:            map[*syntax.FnDecl]*moduleData{},
		specMethodOwner:    map[*syntax.SpecMethod]*moduleData{},
		inherentByType:     map[*syntax.Type]map[string]*syntax.FnDecl{},
		specByType:         map[*syntax.Type]map[string]map[string]*syntax.FnDecl{},
		specByPair:         map[specPairKey]bool{},
		specDeclsByName:    map[string]*syntax.SpecDecl{},
		inherentByBaseName: map[string]map[string]*syntax.FnDecl{},
		specByBaseName:     map[string]map[string]map[string]*syntax.FnDecl{},
	}
	mods := bundle.BundleModules()
	// Phase 1: build per-module decl tables (fns, enums, structs, specs).
	// Done first so impl resolution can look up types in any module.
	for _, m := range mods {
		md := &moduleData{
			view:       m,
			prog:       m.ModuleProgram(),
			name:       m.ModuleName(),
			fns:        map[string]*syntax.FnDecl{},
			enums:      map[string]*syntax.Type{},
			structs:    map[string]*syntax.Type{},
			imports:    map[string]*moduleData{},
			moduleVars: newFrame(),
		}
		in.modules[md.prog] = md
		for _, stmt := range md.prog.Statements {
			switch s := stmt.(type) {
			case *syntax.FnDecl:
				md.fns[s.Name] = s
				in.fnOwner[s] = md
			case *syntax.EnumDecl:
				variants := make([]string, len(s.Variants))
				for i, v := range s.Variants {
					variants[i] = v.Name
				}
				md.enums[s.Name] = syntax.NewEnumType(s.Name, variants)
			case *syntax.StructDecl:
				// We don't need to construct a full *Type here; typeck has
				// stamped canonical *Type pointers on every StructLit.
				// Track the name set so impl resolution can route by name
				// when ImplDecl.TypeModule is empty.
				md.structs[s.Name] = nil
			case *syntax.SpecDecl:
				in.specDeclsByName[s.Name] = s
				for _, sm := range s.Methods {
					if sm.Body != nil {
						in.specMethodOwner[sm] = md
					}
				}
			}
		}
		// v0.6: monomorphised generic-fn clones (Program.MonoFns) carry the
		// callee's body for each (decl, type-args) instance. Stamp each
		// clone's owning module so cross-module dispatch routes lexical
		// scope correctly when CallExpr.Specialised points at the clone.
		for _, fn := range md.prog.MonoFns {
			in.fnOwner[fn] = md
		}
	}
	// Phase 2: bind imports (LocalName → *moduleData).
	for _, m := range mods {
		md := in.modules[m.ModuleProgram()]
		for _, imp := range m.ModuleImports() {
			if imp == nil {
				continue
			}
			target := imp.ImportTarget()
			if target == nil {
				continue
			}
			md.imports[imp.ImportLocalName()] = in.modules[target.ModuleProgram()]
		}
	}
	// Phase 3: walk impls and union into the bundle-wide index keyed by
	// canonical *Type pointer. typeck has stamped TypeModule / SpecModule
	// when the receiver / spec lives in an imported module, so we resolve
	// the *Type pointer through the importing module's tables for owners.
	for _, m := range mods {
		md := in.modules[m.ModuleProgram()]
		for _, stmt := range md.prog.Statements {
			id, ok := stmt.(*syntax.ImplDecl)
			if !ok {
				continue
			}
			// Stamp every method body's owning module so cross-module
			// dispatch can switch lexical scope on call. Done unconditionally
			// — generic-impl methods need this just like concrete ones.
			for _, fn := range id.Methods {
				in.fnOwner[fn] = md
			}
			// v0.6 generic impls (`impl[T] Box[T] ...`) carry no concrete
			// receiver *Type at typeck time. Register their methods by base
			// receiver name so dispatch falls back to the name-keyed table
			// when *Type lookup misses on a monomorphisation.
			if len(id.TypeParams) > 0 {
				if id.Spec == "" {
					mm, ok := in.inherentByBaseName[id.Type]
					if !ok {
						mm = map[string]*syntax.FnDecl{}
						in.inherentByBaseName[id.Type] = mm
					}
					for _, fn := range id.Methods {
						mm[fn.Name] = fn
					}
				} else {
					specMap, ok := in.specByBaseName[id.Type]
					if !ok {
						specMap = map[string]map[string]*syntax.FnDecl{}
						in.specByBaseName[id.Type] = specMap
					}
					mm, ok := specMap[id.Spec]
					if !ok {
						mm = map[string]*syntax.FnDecl{}
						specMap[id.Spec] = mm
					}
					for _, fn := range id.Methods {
						mm[fn.Name] = fn
					}
				}
				continue
			}
			recv := in.resolveImplReceiver(md, id)
			if recv == nil {
				// typeck would have rejected; defensive — skip the impl
				// rather than panic so a fixture quirk doesn't break the
				// whole run.
				continue
			}
			if id.Spec == "" {
				m, ok := in.inherentByType[recv]
				if !ok {
					m = map[string]*syntax.FnDecl{}
					in.inherentByType[recv] = m
				}
				for _, fn := range id.Methods {
					m[fn.Name] = fn
				}
				continue
			}
			specMap, ok := in.specByType[recv]
			if !ok {
				specMap = map[string]map[string]*syntax.FnDecl{}
				in.specByType[recv] = specMap
			}
			methodMap, ok := specMap[id.Spec]
			if !ok {
				methodMap = map[string]*syntax.FnDecl{}
				specMap[id.Spec] = methodMap
			}
			for _, fn := range id.Methods {
				methodMap[fn.Name] = fn
			}
			in.specByPair[specPairKey{recv: recv, specName: id.Spec}] = true
		}
	}
	// Set the active module to the entry; the entry's moduleVars frame
	// serves as the top-level execution scope. RunBundle walks the
	// entry's prog.Statements and execStmt declares each let/mut/const
	// into this frame, so subsequent top-level execution AND fn-body
	// lookups (via the lookup() fall-through to in.cur.moduleVars) see
	// the same storage. v0.14 P1: imported modules' moduleVars are
	// populated by initImportedModuleVars before the entry runs.
	if entry := bundle.BundleEntry(); entry != nil {
		in.cur = in.modules[entry.ModuleProgram()]
		in.stack = []*frame{in.cur.moduleVars}
	} else {
		in.pushFrame()
	}
	return in
}

// initImportedModuleVars walks every non-entry module's top-level
// let / mut / const stmts and executes them into the module's moduleVars
// frame. Iteration runs in reverse bundle order so a module's
// imports init before the importer — bundle.BundleModules() returns
// the entry first followed by transitive imports in pre-order
// discovery, so walking back-to-front gives the post-order "deepest
// first" we want.
//
// Only declaration-shape stmts (LetStmt / MutStmt / ConstStmt) are
// executed. Top-level Print / ExprStmt / Assign in an imported module
// is not executed at module-init time — those shapes are reserved for
// the entry module's main execution. Future work can lift this
// restriction if there's a clear use case.
//
// Errors from a module-init binding bubble up to RunBundle; a failing
// init aborts the whole program before the entry runs.
func (in *interp) initImportedModuleVars(bundle syntax.BundleView) error {
	entry := bundle.BundleEntry()
	var entryMd *moduleData
	if entry != nil {
		entryMd = in.modules[entry.ModuleProgram()]
	}
	mods := bundle.BundleModules()
	for i := len(mods) - 1; i >= 0; i-- {
		md := in.modules[mods[i].ModuleProgram()]
		if md == nil || md == entryMd {
			continue
		}
		savedCur := in.cur
		savedStack := in.stack
		in.cur = md
		in.stack = []*frame{md.moduleVars}
		for _, stmt := range md.prog.Statements {
			switch stmt.(type) {
			case *syntax.LetStmt, *syntax.MutStmt, *syntax.ConstStmt:
				if err := in.execStmt(stmt); err != nil {
					in.cur = savedCur
					in.stack = savedStack
					return err
				}
			}
		}
		in.cur = savedCur
		in.stack = savedStack
	}
	return nil
}

// resolveImplReceiver returns the canonical receiver *Type pointer that
// the impl's methods should be keyed by in the bundle-wide impl index.
//
// Primary source is id.Receiver, stamped by typeck during
// resolveImplsCross — the same canonical *Type pointer that
// StructLit.Type() and EnumLit.Type() carry. Pointer equality is the
// dispatch key, and typeck's stamp guarantees two modules each declaring
// `struct Counter` get distinct pointers (each module's own checker
// owns the *Type it built for its own decl).
//
// Fallbacks (rare, defensive) cover bundles checked by an older typeck
// pass that didn't populate id.Receiver: scan the owning module's prog
// for a stamp first (so we don't conflate cross-module same-named
// types), then synthesise a per-Decl stand-in *Type as a last resort.
func (in *interp) resolveImplReceiver(self *moduleData, id *syntax.ImplDecl) *syntax.Type {
	owner := self
	if id.TypeModule != "" {
		if t, ok := self.imports[id.TypeModule]; ok {
			owner = t
		}
	}
	// Primary: typeck-stamped canonical pointer.
	if id.Receiver != nil {
		if id.Receiver.Kind == syntax.TypeEnum {
			owner.enums[id.Type] = id.Receiver
		}
		return id.Receiver
	}
	// Fallback 1: scan the owning module's own prog for a TypeRef stamp.
	// Restricted to owner.prog so two modules' same-named types stay
	// pointer-distinct.
	if t := findCanonicalType(owner.prog, id.Type); t != nil {
		if t.Kind == syntax.TypeEnum {
			owner.enums[id.Type] = t
		}
		return t
	}
	// Fallback 2: per-Decl stand-in. Keyed by *StructDecl pointer so each
	// module's same-named struct still gets a distinct *Type.
	if _, ok := owner.structs[id.Type]; ok {
		for _, stmt := range owner.prog.Statements {
			if sd, ok := stmt.(*syntax.StructDecl); ok && sd.Name == id.Type {
				return getOrCreateStructType(sd)
			}
		}
	}
	return nil
}

// findCanonicalType walks prog for any TypeRef.Resolved or Expr.Type()
// whose Name matches and Kind is struct or enum. Returns the canonical
// *Type pointer the first time it's hit so the runtime impl index uses
// the same pointer typeck stamped onto values and TypeRefs.
func findCanonicalType(prog *syntax.Program, name string) *syntax.Type {
	for _, stmt := range prog.Statements {
		if t := scanStmtForType(stmt, name); t != nil {
			return t
		}
	}
	return nil
}

// structTypeCache holds stand-in *Type pointers for structs whose
// canonical *Type couldn't be recovered from a TypeRef. Keyed by
// *StructDecl so each module's struct gets a distinct *Type even when
// the names collide.
var structTypeCache = map[*syntax.StructDecl]*syntax.Type{}

func getOrCreateStructType(sd *syntax.StructDecl) *syntax.Type {
	if t, ok := structTypeCache[sd]; ok {
		return t
	}
	// Minimal stand-in: name + Kind. Field set is unknown without typeck;
	// dispatch only reads Name / Kind off the type at runtime, so this is
	// adequate for the impl-index key role.
	t := &syntax.Type{Kind: syntax.TypeStruct, Name: sd.Name}
	structTypeCache[sd] = t
	return t
}

// scanStmtForType recursively walks stmt looking for a TypeRef.Resolved or
// Expr.Type() whose Name matches and Kind is TypeStruct or TypeEnum.
// Returns the canonical pointer the first time it's hit.
func scanStmtForType(stmt syntax.Stmt, name string) *syntax.Type {
	switch s := stmt.(type) {
	case *syntax.StructDecl:
		for _, f := range s.Fields {
			if t := scanTypeRefForType(f.Type, name); t != nil {
				return t
			}
		}
	case *syntax.FnDecl:
		for _, p := range s.Params {
			if t := scanTypeRefForType(p.Type, name); t != nil {
				return t
			}
		}
		if t := scanTypeRefForType(s.Return, name); t != nil {
			return t
		}
		if s.Body != nil {
			for _, sub := range s.Body.Statements {
				if t := scanStmtForType(sub, name); t != nil {
					return t
				}
			}
		}
	case *syntax.LetStmt:
		if t := scanTypeRefForType(s.Type, name); t != nil {
			return t
		}
		if t := scanExprForType(s.Value, name); t != nil {
			return t
		}
	case *syntax.MutStmt:
		if t := scanTypeRefForType(s.Type, name); t != nil {
			return t
		}
		if t := scanExprForType(s.Value, name); t != nil {
			return t
		}
	case *syntax.ConstStmt:
		if t := scanTypeRefForType(s.Type, name); t != nil {
			return t
		}
		if t := scanExprForType(s.Value, name); t != nil {
			return t
		}
	case *syntax.AssignStmt:
		if t := scanExprForType(s.Target, name); t != nil {
			return t
		}
		if t := scanExprForType(s.Value, name); t != nil {
			return t
		}
	case *syntax.MultiAssignStmt:
		for _, target := range s.Targets {
			if t := scanExprForType(target, name); t != nil {
				return t
			}
		}
		if t := scanExprForType(s.Value, name); t != nil {
			return t
		}
	case *syntax.ExprStmt:
		if t := scanExprForType(s.Expr, name); t != nil {
			return t
		}
	case *syntax.PrintStmt:
		if t := scanExprForType(s.Expr, name); t != nil {
			return t
		}
	case *syntax.ReturnStmt:
		if t := scanExprForType(s.Value, name); t != nil {
			return t
		}
		if t := scanExprForType(s.Guard, name); t != nil {
			return t
		}
	case *syntax.IfStmt:
		if t := scanExprForType(s.Cond, name); t != nil {
			return t
		}
		if s.Then != nil {
			for _, sub := range s.Then.Statements {
				if t := scanStmtForType(sub, name); t != nil {
					return t
				}
			}
		}
		for _, ec := range s.Elifs {
			if t := scanExprForType(ec.Cond, name); t != nil {
				return t
			}
			if ec.Body != nil {
				for _, sub := range ec.Body.Statements {
					if t := scanStmtForType(sub, name); t != nil {
						return t
					}
				}
			}
		}
		if s.Else != nil {
			for _, sub := range s.Else.Statements {
				if t := scanStmtForType(sub, name); t != nil {
					return t
				}
			}
		}
	case *syntax.ForStmt:
		if t := scanExprForType(s.Cond, name); t != nil {
			return t
		}
		if t := scanExprForType(s.Iter, name); t != nil {
			return t
		}
		if s.Range != nil {
			if t := scanExprForType(s.Range.Start, name); t != nil {
				return t
			}
			if t := scanExprForType(s.Range.End, name); t != nil {
				return t
			}
		}
		if s.Body != nil {
			for _, sub := range s.Body.Statements {
				if t := scanStmtForType(sub, name); t != nil {
					return t
				}
			}
		}
	case *syntax.MatchStmt:
		if t := scanExprForType(s.Subject, name); t != nil {
			return t
		}
		for _, arm := range s.Arms {
			if t := scanExprForType(arm.Guard, name); t != nil {
				return t
			}
			if arm.Body != nil {
				for _, sub := range arm.Body.Statements {
					if t := scanStmtForType(sub, name); t != nil {
						return t
					}
				}
			}
		}
	case *syntax.ImplDecl:
		for _, fn := range s.Methods {
			if t := scanStmtForType(fn, name); t != nil {
				return t
			}
		}
	case *syntax.SpecDecl:
		for _, m := range s.Methods {
			for _, p := range m.Params {
				if t := scanTypeRefForType(p.Type, name); t != nil {
					return t
				}
			}
			if t := scanTypeRefForType(m.Return, name); t != nil {
				return t
			}
		}
	}
	return nil
}

func scanTypeRefForType(ref *syntax.TypeRef, name string) *syntax.Type {
	if ref == nil || ref.Resolved == nil {
		return nil
	}
	if ref.Resolved.Name == name &&
		(ref.Resolved.Kind == syntax.TypeStruct || ref.Resolved.Kind == syntax.TypeEnum) {
		return ref.Resolved
	}
	if ref.Element != nil {
		if t := scanTypeRefForType(ref.Element, name); t != nil {
			return t
		}
	}
	for _, e := range ref.Elements {
		if t := scanTypeRefForType(e, name); t != nil {
			return t
		}
	}
	return nil
}

// scanExprForType recursively walks an expression looking for a node
// whose Type() Name matches and Kind is TypeStruct or TypeEnum. typeck
// stamps Type() on every typed expression including ThisExpr inside an
// impl method body — so even a module that only declares a type and an
// impl block (no struct-lit or let-binding using it) yields a hit
// through the impl method's ThisExpr.
func scanExprForType(e syntax.Expr, name string) *syntax.Type {
	if e == nil {
		return nil
	}
	if t := exprMatchType(e, name); t != nil {
		return t
	}
	switch ex := e.(type) {
	case *syntax.ParenExpr:
		return scanExprForType(ex.Inner, name)
	case *syntax.UnaryExpr:
		return scanExprForType(ex.Operand, name)
	case *syntax.BinaryExpr:
		if t := scanExprForType(ex.Left, name); t != nil {
			return t
		}
		return scanExprForType(ex.Right, name)
	case *syntax.CallExpr:
		if t := scanExprForType(ex.Callee, name); t != nil {
			return t
		}
		for _, a := range ex.Args {
			if t := scanExprForType(a, name); t != nil {
				return t
			}
		}
	case *syntax.MethodCallExpr:
		if t := scanExprForType(ex.Receiver, name); t != nil {
			return t
		}
		for _, a := range ex.Args {
			if t := scanExprForType(a, name); t != nil {
				return t
			}
		}
		if ex.Lowered != nil {
			if t := exprMatchType(ex.Lowered, name); t != nil {
				return t
			}
			for _, a := range ex.Lowered.Payload {
				if t := scanExprForType(a, name); t != nil {
					return t
				}
			}
		}
	case *syntax.IndexExpr:
		if t := scanExprForType(ex.Receiver, name); t != nil {
			return t
		}
		return scanExprForType(ex.Index, name)
	case *syntax.SliceExpr:
		if t := scanExprForType(ex.Receiver, name); t != nil {
			return t
		}
		if t := scanExprForType(ex.Low, name); t != nil {
			return t
		}
		return scanExprForType(ex.High, name)
	case *syntax.FieldAccessExpr:
		if ex.Lowered != nil {
			if t := exprMatchType(ex.Lowered, name); t != nil {
				return t
			}
		}
		return scanExprForType(ex.Receiver, name)
	case *syntax.ListLit:
		for _, el := range ex.Elements {
			if t := scanExprForType(el, name); t != nil {
				return t
			}
		}
	case *syntax.TupleLit:
		for _, el := range ex.Elements {
			if t := scanExprForType(el, name); t != nil {
				return t
			}
		}
	case *syntax.StructLit:
		for _, f := range ex.Fields {
			if t := scanExprForType(f.Value, name); t != nil {
				return t
			}
		}
	case *syntax.EnumLit:
		for _, p := range ex.Payload {
			if t := scanExprForType(p, name); t != nil {
				return t
			}
		}
	}
	return nil
}

// exprMatchType returns e.Type() iff Name matches and Kind is struct
// or enum. Helper to keep scanExprForType readable.
func exprMatchType(e syntax.Expr, name string) *syntax.Type {
	t := e.Type()
	if t == nil {
		return nil
	}
	if t.Name == name && (t.Kind == syntax.TypeStruct || t.Kind == syntax.TypeEnum) {
		return t
	}
	return nil
}

func (in *interp) pushFrame() { in.stack = append(in.stack, newFrame()) }
func (in *interp) popFrame()  { in.stack = in.stack[:len(in.stack)-1] }

// declare binds name in the current (innermost) frame. typeck has already
// rejected same-block redeclarations, so we do not re-validate here — but we
// guard against the impossible case to fail loudly rather than silently.
func (in *interp) declare(name string, v Value) error {
	top := in.stack[len(in.stack)-1]
	if _, dup := top.vars[name]; dup {
		return fmt.Errorf("internal: %q already bound in current frame", name)
	}
	val := v
	top.vars[name] = &val
	return nil
}

// lookup walks frames from innermost to outermost. Returns the storage slot
// (so assignment can mutate it) plus a found bool. v0.14 P1: after the
// call-frame walk misses, fall through to the active module's top-level
// frame (in.cur.moduleVars). This is what lets a fn body reference its
// owning module's top-level let / mut / const bindings even though
// callFn replaces the call stack with a fresh single-frame slice — the
// module-scope frame is reached by lexical-owner lookup, not by stack
// walking.
func (in *interp) lookup(name string) (*Value, bool) {
	for i := len(in.stack) - 1; i >= 0; i-- {
		if slot, ok := in.stack[i].vars[name]; ok {
			return slot, true
		}
	}
	if in.cur != nil && in.cur.moduleVars != nil {
		if slot, ok := in.cur.moduleVars.vars[name]; ok {
			return slot, true
		}
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Control-flow sentinels.
//
// We use sentinel errors to unwind the stack on return / break / continue.
// Carrying the value (for return) on a struct field of the unwinding error
// keeps the call-site signature uniform: every execStmt returns error, and
// the enclosing fn / loop catches the right sentinel kind.
// ---------------------------------------------------------------------------

// errReturn carries a returning value out of a function body. The Value field
// is the zero Value when the function declares no return type and uses bare
// `return`. callFn() recognises this and unwinds.
type errReturn struct{ value Value }

func (e *errReturn) Error() string { return "return" }

// errBreak unwinds out of the innermost loop. The enclosing for-loop catches
// it and exits cleanly.
var errBreak = errors.New("break")

// errContinue unwinds to the top of the innermost loop. The enclosing for-loop
// catches it and proceeds to the next iteration.
var errContinue = errors.New("continue")

// ---------------------------------------------------------------------------
// Statement execution.
// ---------------------------------------------------------------------------

func (in *interp) execStmt(stmt syntax.Stmt) error {
	// v0.9 Phase 4 Fix 2: a spawned goroutine that hit os.exit stashed
	// the code on the bundle-shared coordinator. Mimic cgen's libc-exit
	// semantics — the first thread to exit kills the whole process —
	// by raising an exitErr at the next statement boundary on the main
	// path so further user code does not run after a spawn-exit fires.
	if in.spawnExited != nil && in.spawnExited.Load() {
		panic(exitErr{Code: int(in.spawnExitCode.Load())})
	}
	switch s := stmt.(type) {
	case *syntax.NopStmt:
		return nil
	case *syntax.PrintStmt:
		return in.execPrint(s)
	case *syntax.LetStmt:
		if s.Tuple != nil {
			return in.execTupleDestructure(s.Tuple, s.Value)
		}
		return in.execDecl(s.Name, s.Type, s.Value)
	case *syntax.MutStmt:
		if s.Tuple != nil {
			return in.execTupleDestructure(s.Tuple, s.Value)
		}
		return in.execDecl(s.Name, s.Type, s.Value)
	case *syntax.ConstStmt:
		// At v0.1 a const is just an immutable binding. The type checker has
		// already enforced that the rhs is a constant expression; runtime
		// evaluation is the same as let. The destructure form is rejected by
		// typeck so s.Tuple is always nil here.
		return in.execDecl(s.Name, s.Type, s.Value)
	case *syntax.AssignStmt:
		return in.execAssign(s)
	case *syntax.MultiAssignStmt:
		return in.execMultiAssign(s)
	case *syntax.ExprStmt:
		_, err := in.evalExpr(s.Expr)
		return err
	case *syntax.IfStmt:
		return in.execIf(s)
	case *syntax.ForStmt:
		return in.execFor(s)
	case *syntax.ReturnStmt:
		return in.execReturn(s)
	case *syntax.BreakStmt:
		ok, err := in.guardTrue(s.Guard)
		if err != nil {
			return err
		}
		if ok {
			return errBreak
		}
		return nil
	case *syntax.ContinueStmt:
		ok, err := in.guardTrue(s.Guard)
		if err != nil {
			return err
		}
		if ok {
			return errContinue
		}
		return nil
	case *syntax.FnDecl:
		// Nested fn decls are rejected by typeck; reaching this from a top-
		// level walk is handled in Run() by the FnDecl skip. A FnDecl seen
		// elsewhere is an internal error.
		return fmt.Errorf("internal: unexpected FnDecl at %s", s.Pos)
	case *syntax.StructDecl:
		// Top-level type declarations are registered in newInterp; nothing
		// to execute at statement-walk time. typeck rejects nested decls.
		return nil
	case *syntax.EnumDecl:
		// Same as StructDecl — registration happens once at interp init.
		return nil
	case *syntax.MatchStmt:
		return in.execMatch(s)
	case *syntax.SpecDecl, *syntax.ImplDecl:
		// v0.4: spec / impl declarations are processed at interp init
		// (newInterp aggregates inherentImpls / specImpls). At statement-walk
		// time they are no-ops — like StructDecl / EnumDecl.
		_ = s
		return nil
	case *syntax.ImportDecl:
		// v0.5 Unit 1b: imports are resolved by the loader before Run sees
		// the merged program. A stray ImportDecl at this layer is a no-op.
		_ = s
		return nil
	case *syntax.SendStmt:
		return in.execSend(s)
	case *syntax.SpawnStmt:
		return in.execSpawn(s)
	case *syntax.DeferStmt:
		return in.execDefer(s)
	case *syntax.SelectStmt:
		return in.execSelect(s)
	case *syntax.AsmBlock:
		// v0.13 inline asm runs only under `zerg build` — the interpreter
		// has no path to execute raw machine code. The diagnostic shape
		// is fixed by PLAN pin 6 and the position anchors on the `asm`
		// keyword so the user lands on the exact construct that prevents
		// `zerg run` from making progress.
		return fmt.Errorf("%s: inline asm requires 'zerg build' (interpreter cannot execute machine code)", s.Pos)
	}
	return fmt.Errorf("internal: unhandled statement %T at %s", stmt, stmt.StmtPos())
}

// execPrint formats per the v0.1 print table: trailing '\n' always, no quotes
// around str, decimal int, %g float, "true"/"false" for bool.
func (in *interp) execPrint(s *syntax.PrintStmt) error {
	v, err := in.evalExpr(s.Expr)
	if err != nil {
		return err
	}
	out := formatValue(v)
	// Append '\n' once — every output line gets it. fmt.Fprintln would also
	// work but introduces a Fprintln-specific space-between-args behaviour
	// that does not matter here yet may surprise a reader; explicit is safer.
	in.writeMu.Lock()
	defer in.writeMu.Unlock()
	if _, err := io.WriteString(in.w, out); err != nil {
		return err
	}
	_, err = io.WriteString(in.w, "\n")
	return err
}

// formatValue is the print-format spec. C codegen MUST emit the same bytes;
// see PLAN.md "print format spec (pinned)".
//
// v0.2 extensions (PLAN lines 153-160):
//   - byte: decimal of the unsigned 0..255 value.
//   - rune: decimal of the Unicode codepoint.
//   - list[T]: "[ e1, e2, e3 ]" — comma+space between elements; empty list
//     prints "[]" with no inner spaces.
//   - tuple: "( e1, e2 )" — same comma+space rule; tuples have ≥ 2 elements
//     so the empty-pair guard does not apply.
//   - struct: "Name { field1: e1, field2: e2 }" — declaration field order.
//   - enum: "Name.VariantName".
//
// Inner element formatting recurses through formatValue, so a list of
// structs prints with the struct format inline.
func formatValue(v Value) string {
	if v.Type == nil {
		return fmt.Sprintf("<unprintable %s>", v.Type)
	}
	switch v.Type {
	case syntax.TInt():
		return strconv.FormatInt(v.Int, 10)
	case syntax.TFloat():
		return strconv.FormatFloat(v.Float, 'g', 17, 64)
	case syntax.TBool():
		if v.Bool {
			return "true"
		}
		return "false"
	case syntax.TStr():
		return v.Str
	case syntax.TByte():
		// PLAN: decimal of the unsigned value. Token/typeck guarantee
		// 0 <= v.Int < 128 for byte (ASCII range), but we mask defensively.
		return strconv.FormatUint(uint64(uint8(v.Int)), 10)
	case syntax.TRune():
		return strconv.FormatInt(v.Int, 10)
	}
	switch v.Type.Kind {
	case syntax.TypeList:
		if len(v.List) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[ ")
		for i, e := range v.List {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(formatValue(e))
		}
		b.WriteString(" ]")
		return b.String()
	case syntax.TypeTuple:
		var b strings.Builder
		b.WriteString("( ")
		for i, e := range v.Tuple {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(formatValue(e))
		}
		b.WriteString(" )")
		return b.String()
	case syntax.TypeStruct:
		var b strings.Builder
		// v0.6: monomorphized generic structs carry a `Name[args]` instance
		// name; the print path strips the bracketed suffix so `Box[int] {
		// value: 7 }` prints as `Box { value: 7 }`. Diagnostics elsewhere
		// keep the full name for disambiguation.
		b.WriteString(displayEnumName(v.Type.Name))
		b.WriteString(" { ")
		for i, f := range v.Type.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Name)
			b.WriteString(": ")
			b.WriteString(formatValue(v.Fields[i]))
		}
		b.WriteString(" }")
		return b.String()
	case syntax.TypeEnum:
		// Nullable values (the synthetic Option-backed enum) print the
		// inner value for the present case and `nil` for the absent case
		// — `Option` is not a user-visible name and so does not appear
		// in stdout. Result and user-defined enums keep the
		// "Name.Variant(args)" shape.
		if syntax.IsNullable(v.Type) {
			if v.VariantName == "None" {
				return "nil"
			}
			if len(v.Payload) == 1 {
				return formatValue(v.Payload[0])
			}
		}
		// v0.6 print parity: `Result[int,str].Ok(7)` renders as
		// `Result.Ok(7)`; the `[type-args]` instance suffix is suppressed
		// for stdout so golden files stay stable across re-monomorphization.
		// Diagnostics keep the bracketed name (Type.String() has its own
		// path).
		name := displayEnumName(v.Type.Name)
		if len(v.Payload) == 0 {
			return name + "." + v.VariantName
		}
		// PLAN: payload variants print as "Name.Variant(arg1, arg2)" with
		// recursive formatValue per position. No leading/trailing spaces
		// inside the parens — matches the literal source-form construction.
		var b strings.Builder
		b.WriteString(name)
		b.WriteString(".")
		b.WriteString(v.VariantName)
		b.WriteString("(")
		for i, p := range v.Payload {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(formatValue(p))
		}
		b.WriteString(")")
		return b.String()
	}
	// typeck rejects anything else for `print`; reaching here is an internal
	// error rather than user-visible.
	return fmt.Sprintf("<unprintable %s>", v.Type)
}

// execDecl evaluates the rhs and binds the name in the current frame. The
// value is deep-copied so any composite payload is independent of the source
// — this is the v0.2 value-semantics rule for lists / tuples / structs.
// Primitives copy trivially through the same helper; the cost is negligible
// for small composite shapes the corpus exercises.
func (in *interp) execDecl(name string, ref *syntax.TypeRef, value syntax.Expr) error {
	v, err := in.evalExpr(value)
	if err != nil {
		return err
	}
	// v0.4: when the binding is annotated with a spec type (or a composite
	// containing a spec position), wrap the rhs at the bind site so method
	// dispatch can reach the right (concrete, spec) impl.
	if ref != nil && ref.Resolved != nil {
		v = in.coerceToType(v, ref.Resolved)
	}
	// v0.3: no implicit deep-copy on bind. The borrow checker has
	// invalidated the source binding at the move site, so sharing the
	// underlying Go slice/struct is safe. clone(xs) is the explicit
	// opt-in for the v0.2-style deep copy.
	return in.declare(name, v)
}

// execTupleDestructure evaluates tuple-destructure binding `(a, b, ...) := expr` (and the mut
// form). The RHS must yield a tuple value of matching arity — typeck has
// already enforced this, so a mismatch here is an internal error rather
// than user-facing. Each name is bound to a deep copy of the matching
// element so the new bindings are independent of the source tuple.
func (in *interp) execTupleDestructure(tb *syntax.TupleBinding, value syntax.Expr) error {
	v, err := in.evalExpr(value)
	if err != nil {
		return err
	}
	if v.Type == nil || v.Type.Kind != syntax.TypeTuple {
		return fmt.Errorf("internal: destructure rhs is not a tuple at %s", tb.Pos)
	}
	if len(v.Tuple) != len(tb.Names) {
		return fmt.Errorf("internal: destructure arity mismatch at %s: %d names vs %d elements", tb.Pos, len(tb.Names), len(v.Tuple))
	}
	for i, name := range tb.Names {
		// v0.3: no implicit deep-copy on tuple destructure bind.
		if err := in.declare(name, v.Tuple[i]); err != nil {
			return err
		}
	}
	return nil
}

// execAssign mutates an existing binding. typeck has already checked the
// target is mut and the rhs type matches; here we just do the operation.
//
// Two LHS shapes are admitted: a bare IdentExpr (`x = v`) which writes the
// named slot in scope, and an IndexExpr (`xs[i] = v`) which writes through a
// list slot's slice header at the given position. typeck and the borrow
// checker have already verified mutability and aliasing; the interpreter
// only does the work.
func (in *interp) execAssign(s *syntax.AssignStmt) error {
	if idx, ok := s.Target.(*syntax.IndexExpr); ok {
		return in.execIndexAssign(s, idx)
	}
	target, ok := s.Target.(*syntax.IdentExpr)
	if !ok {
		return fmt.Errorf("internal: unsupported assignment target %T at %s", s.Target, s.Pos)
	}
	slot, ok := in.lookup(target.Name)
	if !ok {
		return fmt.Errorf("internal: undefined name %q at %s", target.Name, s.Pos)
	}
	rhs, err := in.evalExpr(s.Value)
	if err != nil {
		return err
	}
	switch s.Op {
	case syntax.AssignSet:
		// v0.3: plain `x = v` is only meaningful for primitive targets
		// (composite mut bindings rebind via `:=` or write through
		// `xs[i] = v`); no implicit deep-copy.
		*slot = rhs
	case syntax.AssignAdd:
		*slot, err = applyBin(syntax.BinAdd, *slot, rhs)
	case syntax.AssignSub:
		*slot, err = applyBin(syntax.BinSub, *slot, rhs)
	case syntax.AssignMul:
		*slot, err = applyBin(syntax.BinMul, *slot, rhs)
	case syntax.AssignDiv:
		*slot, err = applyBin(syntax.BinDiv, *slot, rhs)
	case syntax.AssignMod:
		*slot, err = applyBin(syntax.BinMod, *slot, rhs)
	case syntax.AssignAnd:
		*slot, err = applyBin(syntax.BinBitAnd, *slot, rhs)
	case syntax.AssignOr:
		*slot, err = applyBin(syntax.BinBitOr, *slot, rhs)
	case syntax.AssignXor:
		*slot, err = applyBin(syntax.BinBitXor, *slot, rhs)
	case syntax.AssignShl:
		*slot, err = applyBin(syntax.BinShl, *slot, rhs)
	case syntax.AssignShr:
		*slot, err = applyBin(syntax.BinShr, *slot, rhs)
	default:
		return fmt.Errorf("internal: unknown assign op %s at %s", s.Op, s.Pos)
	}
	return err
}

// execMultiAssign handles the v0.15 `a, b, ... = e1, e2, ...` form. The
// RHS is evaluated to a single tuple Value before any LHS slot is written —
// that ordering is what makes `a, b = b, a + b` observe the OLD values of
// a and b on the right. Once we have the tuple in hand, the LHS writes are
// straight slot pokes, mirroring execAssign's plain-ident path.
func (in *interp) execMultiAssign(s *syntax.MultiAssignStmt) error {
	v, err := in.evalExpr(s.Value)
	if err != nil {
		return err
	}
	if v.Type == nil || v.Type.Kind != syntax.TypeTuple {
		return fmt.Errorf("internal: multi-assign rhs is not a tuple at %s", s.Pos)
	}
	if len(v.Tuple) != len(s.Targets) {
		return fmt.Errorf("internal: multi-assign arity mismatch at %s: %d targets vs %d tuple elements", s.Pos, len(s.Targets), len(v.Tuple))
	}
	for i, target := range s.Targets {
		id, ok := target.(*syntax.IdentExpr)
		if !ok {
			return fmt.Errorf("internal: unsupported multi-assign target %T at %s", target, s.Pos)
		}
		slot, ok := in.lookup(id.Name)
		if !ok {
			return fmt.Errorf("internal: undefined name %q at %s", id.Name, s.Pos)
		}
		*slot = v.Tuple[i]
	}
	return nil
}

// execIndexAssign handles `xs[i] = v`. The receiver must be a bare named
// list (typeck and the borrow checker have already enforced this); we look
// up the slot, evaluate the index, range-check it, and write a deep copy of
// the rhs through the slice header at the indexed position.
//
// Only AssignSet (`=`) is admitted on a list element — compound assigns
// (`xs[i] += 1`) are out of scope at v0.3 because typeck doesn't yet plumb
// them through the IndexExpr LHS path. Any compound op that reaches here
// is a typeck bug; we report it rather than guess.
func (in *interp) execIndexAssign(s *syntax.AssignStmt, idx *syntax.IndexExpr) error {
	id, ok := idx.Receiver.(*syntax.IdentExpr)
	if !ok {
		return fmt.Errorf("internal: list-element assignment requires a named list at %s", s.Pos)
	}
	slot, ok := in.lookup(id.Name)
	if !ok {
		return fmt.Errorf("internal: undefined name %q at %s", id.Name, s.Pos)
	}
	iv, err := in.evalExpr(idx.Index)
	if err != nil {
		return err
	}
	rhs, err := in.evalExpr(s.Value)
	if err != nil {
		return err
	}
	if s.Op != syntax.AssignSet {
		return fmt.Errorf("internal: list-element compound assign %s not supported at %s", s.Op, s.Pos)
	}
	n := int64(len(slot.List))
	i := iv.Int
	if i < 0 || i >= n {
		return fmt.Errorf("runtime error at %s: list index %d out of range [0..%d)", s.Pos, i, n)
	}
	// v0.3: no implicit deep-copy on element write — the borrow checker
	// has invalidated the rhs's source binding at the move site.
	slot.List[i] = rhs
	return nil
}

// execIf walks the if-elif-else chain. A matched branch executes its block
// in a fresh frame, then the chain ends.
func (in *interp) execIf(s *syntax.IfStmt) error {
	cond, err := in.evalExpr(s.Cond)
	if err != nil {
		return err
	}
	if cond.Bool {
		return in.execBlock(s.Then)
	}
	for i := range s.Elifs {
		ec := &s.Elifs[i]
		c, err := in.evalExpr(ec.Cond)
		if err != nil {
			return err
		}
		if c.Bool {
			return in.execBlock(ec.Body)
		}
	}
	if s.Else != nil {
		return in.execBlock(s.Else)
	}
	return nil
}

// execBlock pushes a frame, walks statements, pops on the way out.
func (in *interp) execBlock(b *syntax.Block) error {
	in.pushFrame()
	defer in.popFrame()
	for _, st := range b.Statements {
		if err := in.execStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// execFor handles all three for-loop shapes. break/continue are caught here.
func (in *interp) execFor(s *syntax.ForStmt) error {
	switch s.Kind {
	case syntax.ForInfinite:
		for {
			err := in.execBlock(s.Body)
			if errors.Is(err, errBreak) {
				return nil
			}
			if errors.Is(err, errContinue) {
				continue
			}
			if err != nil {
				return err
			}
		}
	case syntax.ForCond:
		for {
			c, err := in.evalExpr(s.Cond)
			if err != nil {
				return err
			}
			if !c.Bool {
				return nil
			}
			err = in.execBlock(s.Body)
			if errors.Is(err, errBreak) {
				return nil
			}
			if errors.Is(err, errContinue) {
				continue
			}
			if err != nil {
				return err
			}
		}
	case syntax.ForRange:
		startV, err := in.evalExpr(s.Range.Start)
		if err != nil {
			return err
		}
		endV, err := in.evalExpr(s.Range.End)
		if err != nil {
			return err
		}
		start, end := startV.Int, endV.Int
		if s.Range.Inclusive {
			// For closed ranges we walk start..end inclusive. If end < start
			// the loop body never runs — same as half-open with reversed
			// bounds. We don't iterate downward at v0.1; PLAN.md doesn't
			// pin reverse iteration semantics so we keep it forward-only.
			for i := start; i <= end; i++ {
				if cont, err := in.runRangeIter(s, i); err != nil {
					return err
				} else if !cont {
					return nil
				}
			}
		} else {
			for i := start; i < end; i++ {
				if cont, err := in.runRangeIter(s, i); err != nil {
					return err
				} else if !cont {
					return nil
				}
			}
		}
		return nil
	case syntax.ForChan:
		return in.execForChan(s)
	case syntax.ForIter:
		// `for x in xs { ... }` — list iteration. Evaluate the iterable
		// once; deep-copy each element on bind so the loop body sees a
		// snapshot independent of any later mutation of xs (no list
		// mutation at v0.2 keeps this academic, but the contract holds).
		iterV, err := in.evalExpr(s.Iter)
		if err != nil {
			return err
		}
		if iterV.Type == nil || iterV.Type.Kind != syntax.TypeList {
			return fmt.Errorf("internal: for-in iterable is not a list at %s", s.Pos)
		}
		for _, elem := range iterV.List {
			cont, err := in.runListIter(s, elem)
			if err != nil {
				return err
			}
			if !cont {
				return nil
			}
		}
		return nil
	}
	return fmt.Errorf("internal: unknown for kind at %s", s.Pos)
}

// runListIter executes one iteration of a `for x in xs` body with the loop
// variable bound to a deep copy of elem. Mirrors runRangeIter's contract:
// returns (continueLoop, err) where false means break, true means proceed.
func (in *interp) runListIter(s *syntax.ForStmt, elem Value) (bool, error) {
	in.pushFrame()
	defer in.popFrame()
	// v0.3: no implicit deep-copy on for-iter element bind. The borrow
	// checker has BorrowedShared the iterable for the body's duration
	// and rejects mutation of it inside, so shared backing is safe.
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

// runRangeIter executes one iteration of a for-in body with the loop var
// bound to i. Returns (continueLoop, err): false means break, true means
// proceed (whether or not continue fired). Errors not-equal-to break/continue
// propagate.
func (in *interp) runRangeIter(s *syntax.ForStmt, i int64) (bool, error) {
	in.pushFrame()
	defer in.popFrame()
	if err := in.declare(s.Var, intVal(i)); err != nil {
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

// execReturn unwinds to the enclosing call. typeck has validated the value
// type; the guard form returns only when the guard is true.
func (in *interp) execReturn(s *syntax.ReturnStmt) error {
	ok, err := in.guardTrue(s.Guard)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if s.Value == nil {
		return &errReturn{value: Value{}}
	}
	v, err := in.evalExpr(s.Value)
	if err != nil {
		return err
	}
	return &errReturn{value: v}
}

// guardTrue evaluates a break/continue/return guard. A nil guard means
// "unconditional", so the result is true.
func (in *interp) guardTrue(g syntax.Expr) (bool, error) {
	if g == nil {
		return true, nil
	}
	v, err := in.evalExpr(g)
	if err != nil {
		return false, err
	}
	return v.Bool, nil
}

// ---------------------------------------------------------------------------
// Expression evaluation.
// ---------------------------------------------------------------------------

func (in *interp) evalExpr(expr syntax.Expr) (Value, error) {
	switch e := expr.(type) {
	case *syntax.IntLit:
		return intVal(e.Int), nil
	case *syntax.FloatLit:
		return floatVal(e.Float), nil
	case *syntax.StringLit:
		return strVal(e.Value), nil
	case *syntax.InterpolatedStringLit:
		return in.evalInterpolatedStringLit(e)
	case *syntax.BoolLit:
		return boolVal(e.Value), nil
	case *syntax.IdentExpr:
		slot, ok := in.lookup(e.Name)
		if !ok {
			return Value{}, fmt.Errorf("internal: undefined name %q at %s", e.Name, e.Pos)
		}
		return *slot, nil
	case *syntax.ParenExpr:
		return in.evalExpr(e.Inner)
	case *syntax.UnaryExpr:
		return in.evalUnary(e)
	case *syntax.BinaryExpr:
		return in.evalBinary(e)
	case *syntax.CallExpr:
		return in.evalCall(e)
	case *syntax.RuneLit:
		// typeck has classified the literal as TByte or TRune via Type();
		// reuse that decision so the print path picks the right format.
		if e.Type() == syntax.TByte() {
			return byteVal(e.Value), nil
		}
		return runeVal(e.Value), nil
	case *syntax.ListLit:
		return in.evalListLit(e)
	case *syntax.TupleLit:
		return in.evalTupleLit(e)
	case *syntax.StructLit:
		return in.evalStructLit(e)
	case *syntax.IndexExpr:
		return in.evalIndex(e)
	case *syntax.SliceExpr:
		return in.evalSlice(e)
	case *syntax.FieldAccessExpr:
		return in.evalFieldAccess(e)
	case *syntax.MethodCallExpr:
		return in.evalMethodCall(e)
	case *syntax.ThisExpr:
		slot, ok := in.lookup("this")
		if !ok {
			return Value{}, fmt.Errorf("internal: 'this' is only valid inside an impl method body at %s", e.Pos)
		}
		return *slot, nil
	case *syntax.EnumLit:
		return in.evalEnumLit(e)
	case *syntax.NilLit:
		return in.evalNilLit(e)
	case *syntax.PropagateExpr:
		return in.evalPropagate(e)
	case *syntax.CoalesceExpr:
		return in.evalCoalesce(e)
	case *syntax.ChanConstructorExpr:
		return in.evalChanConstructor(e)
	case *syntax.RecvExpr:
		return in.evalRecv(e)
	case *syntax.AnonFnExpr:
		return in.evalAnonFn(e)
	}
	return Value{}, fmt.Errorf("internal: unhandled expression %T at %s", expr, expr.ExprPos())
}

// evalInterpolatedStringLit reuses formatValue (the same canonical text form
// the print path uses), so cgen's per-type helpers and the interpreter agree
// byte-for-byte on every primitive.
func (in *interp) evalInterpolatedStringLit(e *syntax.InterpolatedStringLit) (Value, error) {
	var b strings.Builder
	for _, piece := range e.Pieces {
		switch p := piece.(type) {
		case *syntax.StringLitPiece:
			b.WriteString(p.Text)
		case *syntax.StringVarPiece:
			v, err := in.evalExpr(p.Ident)
			if err != nil {
				return Value{}, err
			}
			b.WriteString(formatValue(v))
		default:
			return Value{}, fmt.Errorf("internal: unknown string piece %T at %s", piece, e.Pos)
		}
	}
	return strVal(b.String()), nil
}

func (in *interp) evalUnary(e *syntax.UnaryExpr) (Value, error) {
	v, err := in.evalExpr(e.Operand)
	if err != nil {
		return Value{}, err
	}
	switch e.Op {
	case syntax.UnaryNeg:
		if v.Type == syntax.TInt() {
			return intVal(-v.Int), nil
		}
		// typeck restricts unary - to numeric, so the only other case is float.
		return floatVal(-v.Float), nil
	case syntax.UnaryBitNot:
		return intVal(^v.Int), nil
	case syntax.UnaryNot:
		return boolVal(!v.Bool), nil
	}
	return Value{}, fmt.Errorf("internal: unknown unary op %s at %s", e.Op, e.Pos)
}

// evalBinary handles short-circuit `and`/`or`; everything else delegates to
// applyBin so the assignment path can share the implementation.
func (in *interp) evalBinary(e *syntax.BinaryExpr) (Value, error) {
	switch e.Op {
	case syntax.BinAnd:
		// Short-circuit: skip the rhs when lhs is false.
		l, err := in.evalExpr(e.Left)
		if err != nil {
			return Value{}, err
		}
		if !l.Bool {
			return boolVal(false), nil
		}
		r, err := in.evalExpr(e.Right)
		if err != nil {
			return Value{}, err
		}
		return boolVal(r.Bool), nil
	case syntax.BinOr:
		// Short-circuit: skip the rhs when lhs is true.
		l, err := in.evalExpr(e.Left)
		if err != nil {
			return Value{}, err
		}
		if l.Bool {
			return boolVal(true), nil
		}
		r, err := in.evalExpr(e.Right)
		if err != nil {
			return Value{}, err
		}
		return boolVal(r.Bool), nil
	}
	// All non-short-circuit ops evaluate both sides eagerly.
	lv, err := in.evalExpr(e.Left)
	if err != nil {
		return Value{}, err
	}
	rv, err := in.evalExpr(e.Right)
	if err != nil {
		return Value{}, err
	}
	return applyBin(e.Op, lv, rv)
}

// applyBin performs op on already-evaluated lv, rv. Shared by direct binary
// expressions and compound assignments. typeck has guaranteed the operand
// types match the operator's expectations, so the dispatch is type-safe.
//
// Numeric semantics (pinned in PLAN.md):
//   - int arithmetic wraps via Go's int64 (matches C `-fwrapv`).
//   - int / and // both truncate toward zero (Go and C99+ agree).
//   - float / produces IEEE 754 quotient.
//   - float // produces math.Floor(quotient) as a float — PLAN.md does not
//     pin float floor-division, but the codegen will emit the same lowering
//     so v0.1 parity holds. Document here so Unit 4 follows suit.
//   - int % follows the dividend's sign (Go and C99+ agree).
//   - String + concatenates.
//   - byte arithmetic uses int64 internally then narrows back to byte with
//     a 0xFF mask, matching the codegen's uint8_t wrap semantics. The pre-
//     v0.14 interpreter erroneously dropped through to the float branch
//     for byte operands (storing 0.0) — pure-Zerg strings.zg's case-
//     conversion paths need byte arithmetic to round-trip, so this gap is
//     closed here.
func applyBin(op syntax.BinaryOp, lv, rv Value) (Value, error) {
	// wrapByte narrows an int64 result to a byteVal with uint8 wrap parity
	// (matches the cgen's `& 0xFF` lowering). Used by every numeric op so
	// byte ± byte / shift / bitop round-trip in the interpreter the same
	// way they do under the C codegen.
	wrapByte := func(v int64) Value { return byteVal(v & 0xFF) }
	isByte := lv.Type == syntax.TByte()
	switch op {
	case syntax.BinAdd:
		if lv.Type == syntax.TStr() {
			return strVal(lv.Str + rv.Str), nil
		}
		if lv.Type == syntax.TInt() {
			return intVal(lv.Int + rv.Int), nil
		}
		if isByte {
			return wrapByte(lv.Int + rv.Int), nil
		}
		return floatVal(lv.Float + rv.Float), nil
	case syntax.BinSub:
		if lv.Type == syntax.TInt() {
			return intVal(lv.Int - rv.Int), nil
		}
		if isByte {
			return wrapByte(lv.Int - rv.Int), nil
		}
		return floatVal(lv.Float - rv.Float), nil
	case syntax.BinMul:
		if lv.Type == syntax.TInt() {
			return intVal(lv.Int * rv.Int), nil
		}
		if isByte {
			return wrapByte(lv.Int * rv.Int), nil
		}
		return floatVal(lv.Float * rv.Float), nil
	case syntax.BinDiv:
		if lv.Type == syntax.TInt() {
			// PLAN.md: "Division by zero on int: runtime-undefined; not
			// exercised." We don't synthesise a dedicated error; Go panics on
			// integer division by zero and that is acceptable parity with C
			// undefined behaviour for the v0.1 corpus.
			return intVal(lv.Int / rv.Int), nil
		}
		if isByte {
			return wrapByte(lv.Int / rv.Int), nil
		}
		return floatVal(lv.Float / rv.Float), nil
	case syntax.BinFloorDiv:
		// On int: identical to BinDiv (truncating toward zero). PLAN.md does
		// not split `//` from `/` for int at v0.1; we choose to make them
		// identical because (a) the parity codegen will lower both to the
		// same C expression for ints, (b) any user who reaches for `//` on
		// ints gets the answer they expect for non-negative operands.
		// On float: math.Floor of the quotient — see Note above applyBin.
		if lv.Type == syntax.TInt() {
			return intVal(lv.Int / rv.Int), nil
		}
		if isByte {
			return wrapByte(lv.Int / rv.Int), nil
		}
		// We avoid pulling in math just for Floor here; the float64 trick
		// `q := a/b; if (q != int64(q)) && (signMismatch) { q-- }` is more
		// fragile than just using math.Floor. Use math.Floor.
		return floatVal(floorFloat(lv.Float / rv.Float)), nil
	case syntax.BinMod:
		if lv.Type == syntax.TInt() {
			return intVal(lv.Int % rv.Int), nil
		}
		if isByte {
			return wrapByte(lv.Int % rv.Int), nil
		}
		// Go has no float64 % at the language level; we are not required to
		// support it (typeck rejects float % at parse-or-check time? Actually
		// it does not — see typeck.go BinSub/...,BinMod accepts numeric.
		// PLAN.md does not exercise it, but the codegen should match. Use
		// math.Mod equivalent via the standard "a - b*trunc(a/b)" identity.
		return floatVal(floatMod(lv.Float, rv.Float)), nil
	case syntax.BinBitAnd:
		if isByte {
			return wrapByte(lv.Int & rv.Int), nil
		}
		return intVal(lv.Int & rv.Int), nil
	case syntax.BinBitOr:
		if isByte {
			return wrapByte(lv.Int | rv.Int), nil
		}
		return intVal(lv.Int | rv.Int), nil
	case syntax.BinBitXor:
		if isByte {
			return wrapByte(lv.Int ^ rv.Int), nil
		}
		return intVal(lv.Int ^ rv.Int), nil
	case syntax.BinShl:
		// Shift by negative amounts is undefined in C; typeck does not catch
		// it. Go panics on negative shift count in some Go versions; we let
		// the runtime decide rather than synthesising a specific error.
		if isByte {
			return wrapByte(lv.Int << uint64(rv.Int)), nil
		}
		return intVal(lv.Int << uint64(rv.Int)), nil
	case syntax.BinShr:
		if isByte {
			return wrapByte(lv.Int >> uint64(rv.Int)), nil
		}
		return intVal(lv.Int >> uint64(rv.Int)), nil
	case syntax.BinEq:
		eq, err := eqValues(lv, rv)
		if err != nil {
			return Value{}, err
		}
		return boolVal(eq), nil
	case syntax.BinNE:
		eq, err := eqValues(lv, rv)
		if err != nil {
			return Value{}, err
		}
		return boolVal(!eq), nil
	case syntax.BinLT:
		return boolVal(valueLT(lv, rv)), nil
	case syntax.BinGT:
		return boolVal(valueLT(rv, lv)), nil
	case syntax.BinLE:
		return boolVal(!valueLT(rv, lv)), nil
	case syntax.BinGE:
		return boolVal(!valueLT(lv, rv)), nil
	case syntax.BinXor:
		// Logical xor — non-short-circuit per PLAN.md.
		return boolVal(lv.Bool != rv.Bool), nil
	case syntax.BinAnd, syntax.BinOr:
		// Short-circuit forms are handled in evalBinary; if we land here it's
		// because applyBin was called from a compound-assign path (which
		// never targets bool ops) — that's an internal error.
		return Value{}, fmt.Errorf("internal: %s reached applyBin", op)
	}
	return Value{}, fmt.Errorf("internal: unhandled binary op %s", op)
}

// valueEq is == over typed values. typeck guarantees lv.Type == rv.Type.
// Byte and rune compare on their integer codepoint — same as the codegen
// side which lowers to a plain `==` on uint8_t / int32_t.
func valueEq(lv, rv Value) bool {
	switch lv.Type {
	case syntax.TInt():
		return lv.Int == rv.Int
	case syntax.TFloat():
		return lv.Float == rv.Float
	case syntax.TBool():
		return lv.Bool == rv.Bool
	case syntax.TStr():
		return lv.Str == rv.Str
	case syntax.TByte(), syntax.TRune():
		return lv.Int == rv.Int
	}
	return false
}

// valueLT is < over typed values. typeck guarantees same-typed numeric/str
// operands; bool ordering is rejected at check time. Byte and rune order on
// codepoint, mirroring the codegen's int compare.
func valueLT(lv, rv Value) bool {
	switch lv.Type {
	case syntax.TInt():
		return lv.Int < rv.Int
	case syntax.TFloat():
		return lv.Float < rv.Float
	case syntax.TStr():
		return lv.Str < rv.Str
	case syntax.TByte(), syntax.TRune():
		return lv.Int < rv.Int
	}
	return false
}

// floorFloat returns math.Floor(x). Wrapped in a helper so the few call
// sites read "this is float floor-division semantics" rather than reaching
// for the math package directly.
func floorFloat(x float64) float64 { return math.Floor(x) }

// floatMod implements a - b*trunc(a/b) for float64 operands. typeck currently
// admits float % even though the corpus does not exercise it. The codegen
// will emit fmod(a,b) for parity; we use math.Mod here to match.
func floatMod(a, b float64) float64 { return math.Mod(a, b) }

// evalCall executes a function call. typeck has verified the callee is a
// declared fn and the argument types match. We push a fresh frame, bind
// parameters, walk the body, and catch errReturn to extract the value.
//
// The built-in `len` is dispatched here before the user-fn lookup. typeck
// has already enforced that `len` accepts exactly one list argument and
// returns int — at v0.2 it's the only generic intrinsic, so a single-name
// switch is the right shape; future built-ins will append.
func (in *interp) evalCall(e *syntax.CallExpr) (Value, error) {
	// Bare `Ok(v)` / `Err(e)` sugar — typeck lowered to a Result enum-lit.
	// Route through the EnumLit evaluator so construction follows the same
	// path as `Result.Ok(v)`.
	if e.Lowered != nil {
		return in.evalEnumLit(e.Lowered)
	}
	// v0.7: anon-fn IIFE — `fn() { ... }()`. The callee is the AnonFnExpr
	// itself; evaluate it to an fnValue and dispatch through callFnValue.
	if anon, ok := e.Callee.(*syntax.AnonFnExpr); ok {
		fnv, err := in.evalAnonFn(anon)
		if err != nil {
			return Value{}, err
		}
		return in.callFnValue(fnv.Fn, e.Args, e.Type(), e.Pos)
	}
	ident, ok := e.Callee.(*syntax.IdentExpr)
	if !ok {
		return Value{}, fmt.Errorf("internal: non-ident callee at %s", e.Pos)
	}
	if ident.Name == "len" {
		return in.evalLen(e)
	}
	if ident.Name == "wait_group" {
		return in.evalWaitGroupCtor(e)
	}
	if ident.Name == "close" {
		return in.evalCloseCall(e)
	}
	// v0.3 builtins. `clone(xs)` returns a deep copy of its composite argument
	// — the borrow checker already enforces that primitives are rejected. The
	// interpreter implementation reuses copyValue, which is the same logic v0.2
	// applied implicitly on every bind. `push(xs, v)` mutates the named mut
	// list in place; the borrow checker has already validated mut and state.
	if ident.Name == "clone" {
		return in.evalClone(e)
	}
	if ident.Name == "push" {
		return in.evalPush(e)
	}
	// v0.14 str ↔ list[byte] bridge builtins.
	if ident.Name == "bytes" {
		return in.evalStrBytes(e)
	}
	if ident.Name == "to_str" {
		return in.evalListByteToStr(e)
	}
	// v0.14 panic(msg) — writes "zerg: runtime: <msg>\n" to stderr
	// and exits with code 1. Surfaces as a non-recoverable runtime
	// failure — never returns to the caller.
	if ident.Name == "panic" {
		return in.evalPanic(e)
	}
	// v0.6: typeck stamps e.Specialised on calls to generic fns with the
	// monomorphised FnDecl clone. Body type-refs in the clone resolve to
	// concrete *Type pointers, which the body-walking interpreter doesn't
	// strictly need but matches the C codegen route — and the body itself
	// is shared with the generic decl so behaviour is identical.
	if e.Specialised != nil {
		return in.callFn(e.Specialised, e.Args, e.Type(), e.Pos)
	}
	// v0.7: a local binding may carry a TypeFn (an anon-fn captured in a
	// let). Calling such a binding routes through the fn-value path. The
	// scope lookup runs first so a local fn-typed binding shadows any
	// same-name top-level fn (matches typeck's resolution order).
	if slot, ok := in.lookup(ident.Name); ok && slot.Type != nil && slot.Type.Kind == syntax.TypeFn && slot.Fn != nil {
		return in.callFnValue(slot.Fn, e.Args, e.Type(), e.Pos)
	}
	fn, ok := in.cur.fns[ident.Name]
	if !ok {
		return Value{}, fmt.Errorf("internal: undefined function %q at %s", ident.Name, e.Pos)
	}
	return in.callFn(fn, e.Args, e.Type(), e.Pos)
}

// callFn binds args, switches to the callee fn's owning module for the
// body's lexical context, walks the body, and catches errReturn. Shared
// by direct fn calls and the cross-module method-call shape recognised
// in evalMethodCall.
func (in *interp) callFn(fn *syntax.FnDecl, argExprs []syntax.Expr, resultType *syntax.Type, callPos syntax.Position) (Value, error) {
	// Evaluate args in left-to-right order BEFORE pushing the call frame,
	// so the args are evaluated in the caller's scope (matters for nested
	// calls or self-recursion).
	args := make([]Value, len(argExprs))
	for i, a := range argExprs {
		v, err := in.evalExpr(a)
		if err != nil {
			return Value{}, err
		}
		// v0.4: spec widening at the call boundary. typeck's typeEq path
		// rejects concrete-into-spec at the surface, but the lowered list-
		// builtin path lets typeck-valid spec arguments through; safe to call
		// unconditionally because coerceToType is identity when the param
		// type doesn't reach for a spec.
		if i < len(fn.Params) && fn.Params[i].Type != nil && fn.Params[i].Type.Resolved != nil {
			v = in.coerceToType(v, fn.Params[i].Type.Resolved)
		}
		args[i] = v
	}

	// v0.8: __builtin fn-decls have no body. Route to the host primitive
	// dispatch keyed by fn.BuiltinName. Args have already been coerced; the
	// result type carries the canonical Result/Option *Type stamped by
	// typeck on the call expression.
	if fn.BuiltinName != "" {
		return in.callBuiltin(fn, args, resultType, callPos)
	}

	// v0.14: sys/syscall wrapper fns have a non-empty body, but the body
	// is a single `asm { svc #0x80 ... }` block the interpreter cannot
	// execute. The intrinsic dispatch returns handled=true when fn is
	// one of those wrappers; we bypass the body walk and return its
	// signed-errno result directly. See run_v14_syscall.go.
	if v, handled, err := in.invokeSysSyscallIntrinsic(fn, args); handled {
		return v, err
	}

	// Calls do NOT inherit the caller's scope: a fresh frame stack rooted at
	// just the new frame. v0.5: also switch the active module to the
	// callee's owning module so the body resolves unqualified identifiers
	// (its own fns / enums / structs / imports) against the right tables.
	savedStack := in.stack
	savedCur := in.cur
	in.stack = []*frame{newFrame()}
	if owner := in.fnOwner[fn]; owner != nil {
		in.cur = owner
	}
	in.pushDeferFrame(fn.HasDefers)
	defer func() {
		in.stack = savedStack
		in.cur = savedCur
	}()

	for i, p := range fn.Params {
		// v0.3: fn-call composite args are implicit shared borrows. No
		// deep copy at the call boundary — the borrow checker enforces
		// that fn parameters of composite type are BorrowedShared and
		// cannot be moved/mutated inside the body, so sharing the
		// underlying value with the caller's binding is safe.
		if err := in.declare(p.Name, args[i]); err != nil {
			in.popDeferFrame(fn.HasDefers)
			return Value{}, err
		}
	}

	for _, st := range fn.Body.Statements {
		err := in.execStmt(st)
		if err == nil {
			continue
		}
		var ret *errReturn
		if errors.As(err, &ret) {
			in.drainDefers(fn.HasDefers)
			retVal := ret.value
			if fn.Return != nil && fn.Return.Resolved != nil {
				retVal = in.coerceToType(retVal, fn.Return.Resolved)
			}
			return retVal, nil
		}
		// `?` propagation: convert to a return of the outer fn's declared
		// return type. Typeck has guaranteed shape compatibility (Option
		// in Option fn, or Result in Result fn with matching E).
		if pret, perr := in.catchPropagateForFn(err, fn); perr != nil {
			in.drainDefers(fn.HasDefers)
			return Value{}, perr
		} else if pret != nil {
			in.drainDefers(fn.HasDefers)
			retVal := pret.value
			if fn.Return != nil && fn.Return.Resolved != nil {
				retVal = in.coerceToType(retVal, fn.Return.Resolved)
			}
			return retVal, nil
		}
		// break/continue must NOT escape a function: typeck rejects them
		// outside loops, and a function body without an enclosing loop in
		// scope means any `break` is in a loop strictly inside the body and
		// is caught by execFor before reaching us. Defensive check.
		if errors.Is(err, errBreak) || errors.Is(err, errContinue) {
			in.drainDefers(fn.HasDefers)
			return Value{}, fmt.Errorf("internal: %v escaped fn %s", err, fn.Name)
		}
		in.drainDefers(fn.HasDefers)
		return Value{}, err
	}
	in.drainDefers(fn.HasDefers)
	// Fall-through end of body. typeck rejects falling off a non-void fn,
	// so reaching here for a void fn is fine; for a non-void fn it is an
	// internal error.
	if resultType != nil && resultType != syntax.TVoid() {
		return Value{}, fmt.Errorf("function %q ended without return at %s", fn.Name, callPos)
	}
	return Value{}, nil
}

// catchPropagateForFn converts an errPropagate raised inside fn's body to an
// errReturn whose value's Type matches fn's declared return type. Returns
// (nil, nil) when err is not a propagate sentinel so the caller falls through.
func (in *interp) catchPropagateForFn(err error, fn *syntax.FnDecl) (*errReturn, error) {
	var ret *syntax.Type
	if fn.Return != nil {
		ret = fn.Return.Resolved
	}
	return catchPropagate(err, ret)
}

// evalLen implements the `len` built-in. typeck has validated argument count
// and type (one list[T]). For str the codepoint-count rule is also pinned in
// PLAN line 233; we accept str defensively even though typeck currently
// rejects str arguments to len at v0.2 — the dispatch is harmless and lines
// run.go up for a future PLAN tweak without code churn.
func (in *interp) evalLen(e *syntax.CallExpr) (Value, error) {
	if len(e.Args) != 1 {
		return Value{}, fmt.Errorf("internal: len expects 1 arg, got %d at %s", len(e.Args), e.Pos)
	}
	v, err := in.evalExpr(e.Args[0])
	if err != nil {
		return Value{}, err
	}
	if v.Type == nil {
		return Value{}, fmt.Errorf("internal: len argument has nil type at %s", e.Pos)
	}
	switch v.Type.Kind {
	case syntax.TypeList:
		return intVal(int64(len(v.List))), nil
	case syntax.TypeStr:
		// v0.14: byte count (the v0.2 rune-count reading was dead code
		// — typeck rejected str — and the live reading matches
		// list[byte].len() semantics so stdlib byte-oriented ops can
		// be implemented in pure Zerg). The Go string's len() returns
		// byte count directly.
		return intVal(int64(len(v.Str))), nil
	}
	return Value{}, fmt.Errorf("internal: len cannot accept %s at %s", v.Type, e.Pos)
}

// evalStrBytes implements `bytes(s)` — the v0.14 str → list[byte] bridge.
// Allocates a fresh list[byte] from s.Str's bytes. UTF-8 boundaries are
// not respected: every byte becomes one element regardless of whether
// it's a lead, continuation, or BOM — matches the byte-buffer semantics
// the stdlib byte-oriented ops want.
func (in *interp) evalStrBytes(e *syntax.CallExpr) (Value, error) {
	if len(e.Args) != 1 {
		return Value{}, fmt.Errorf("internal: bytes expects 1 arg, got %d at %s", len(e.Args), e.Pos)
	}
	v, err := in.evalExpr(e.Args[0])
	if err != nil {
		return Value{}, err
	}
	if v.Type == nil || v.Type.Kind != syntax.TypeStr {
		return Value{}, fmt.Errorf("internal: bytes arg must be str, got %s at %s", v.Type, e.Pos)
	}
	out := make([]Value, 0, len(v.Str))
	for i := 0; i < len(v.Str); i++ {
		out = append(out, byteVal(int64(v.Str[i])))
	}
	return listVal(syntax.TByte(), out), nil
}

// evalPanic implements `panic(msg)` — surfaces the message as a Go
// error that the eval chain bubbles up to the CLI / test harness. The
// build half writes "zerg: runtime: <msg>\n" to stderr via the
// always-emitted zerg_panic helper and exits with code 1; the interp
// half lets the host print the returned error before exiting (the CLI
// driver wraps eval errors with the "runtime error at <pos>:" prefix
// for source-locatable diagnostics). Both halves' stderr output ends
// up containing the original `msg` text, which is what the v0.10-
// pinned stability tests check for.
func (in *interp) evalPanic(e *syntax.CallExpr) (Value, error) {
	if len(e.Args) != 1 {
		return Value{}, fmt.Errorf("internal: panic expects 1 arg, got %d at %s", len(e.Args), e.Pos)
	}
	v, err := in.evalExpr(e.Args[0])
	if err != nil {
		return Value{}, err
	}
	if v.Type == nil || v.Type.Kind != syntax.TypeStr {
		return Value{}, fmt.Errorf("internal: panic arg must be str, got %s at %s", v.Type, e.Pos)
	}
	return Value{}, fmt.Errorf("runtime error at %s: %s", e.Pos, v.Str)
}

// evalListByteToStr implements `to_str(buf)` — the v0.14 list[byte] → str
// bridge. Concatenates the byte values into a Go string; UTF-8 validity
// is the caller's responsibility (mirrors the C-side helper's contract).
func (in *interp) evalListByteToStr(e *syntax.CallExpr) (Value, error) {
	if len(e.Args) != 1 {
		return Value{}, fmt.Errorf("internal: to_str expects 1 arg, got %d at %s", len(e.Args), e.Pos)
	}
	v, err := in.evalExpr(e.Args[0])
	if err != nil {
		return Value{}, err
	}
	if v.Type == nil || v.Type.Kind != syntax.TypeList ||
		v.Type.Element == nil || v.Type.Element.Kind != syntax.TypeByte {
		return Value{}, fmt.Errorf("internal: to_str arg must be list[byte], got %s at %s", v.Type, e.Pos)
	}
	buf := make([]byte, len(v.List))
	for i, el := range v.List {
		buf[i] = byte(el.Int & 0xFF)
	}
	return strVal(string(buf)), nil
}

// evalClone implements `clone(xs)`. The argument has already been validated by
// typeck (composite, exactly one) and by the borrow checker (the receiver is a
// shared borrow — caller retains ownership). The runtime is purely a fresh
// deep copy via copyValue.
func (in *interp) evalClone(e *syntax.CallExpr) (Value, error) {
	if len(e.Args) != 1 {
		return Value{}, fmt.Errorf("internal: clone expects 1 arg, got %d at %s", len(e.Args), e.Pos)
	}
	v, err := in.evalExpr(e.Args[0])
	if err != nil {
		return Value{}, err
	}
	return copyValue(v), nil
}

// evalPush implements `push(xs, v)`. typeck has already required xs to be a
// mut-bound list ident and v's type to match the list's element type; the
// borrow checker has further verified xs is in Owned state. The runtime
// appends to the named binding's slice in place — we look up the slot, take
// the current list value, append a deep copy of v, and store the new header
// back so the slice grows independently of any other view.
func (in *interp) evalPush(e *syntax.CallExpr) (Value, error) {
	if len(e.Args) != 2 {
		return Value{}, fmt.Errorf("internal: push expects 2 args, got %d at %s", len(e.Args), e.Pos)
	}
	id, ok := e.Args[0].(*syntax.IdentExpr)
	if !ok {
		return Value{}, fmt.Errorf("internal: push first arg must be ident at %s", e.Pos)
	}
	slot, ok := in.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("internal: push undefined name %q at %s", id.Name, e.Pos)
	}
	v, err := in.evalExpr(e.Args[1])
	if err != nil {
		return Value{}, err
	}
	// v0.3: no implicit deep-copy on push — the borrow checker has
	// invalidated v's source binding at the move site if v is a name.
	slot.List = append(slot.List, v)
	return Value{}, nil
}

// ---------------------------------------------------------------------------
// v0.2 composite-data evaluators.
// ---------------------------------------------------------------------------

// evalListLit evaluates `[e1, e2, ...]` to a list Value. Each element is
// deep-copied as it goes into the list so the source bindings stay
// independent of the constructed list (a later mutation of an element source
// — none today, but the contract holds — cannot leak).
func (in *interp) evalListLit(e *syntax.ListLit) (Value, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeList {
		return Value{}, fmt.Errorf("internal: list literal has non-list type %s at %s", t, e.Pos)
	}
	elems := make([]Value, len(e.Elements))
	for i, sub := range e.Elements {
		ev, err := in.evalExpr(sub)
		if err != nil {
			return Value{}, err
		}
		// v0.4: when the literal's element type is a spec, each element is
		// wrapped at this construction point so list[Printable] holds fat
		// pointers regardless of which concrete types the user wrote.
		if t.Element != nil {
			ev = in.coerceToType(ev, t.Element)
		}
		elems[i] = ev
	}
	return Value{Type: t, List: elems}, nil
}

// evalTupleLit evaluates `(e1, e2, ...)`. The tuple length is fixed at parse
// time; element values are deep-copied as they enter the tuple so any
// composite element is independent of its source binding.
func (in *interp) evalTupleLit(e *syntax.TupleLit) (Value, error) {
	elems := make([]Value, len(e.Elements))
	for i, sub := range e.Elements {
		ev, err := in.evalExpr(sub)
		if err != nil {
			return Value{}, err
		}
		// v0.3: no implicit deep-copy at tuple-literal construction.
		elems[i] = ev
	}
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeTuple {
		return Value{}, fmt.Errorf("internal: tuple literal has non-tuple type %s at %s", t, e.Pos)
	}
	return Value{Type: t, Tuple: elems}, nil
}

// evalStructLit evaluates `Name { f1: v1, f2: v2 }`. Field order in the
// runtime Value follows declaration order (PLAN-pinned for print
// determinism), regardless of the order the user wrote field initialisers.
// typeck has already validated completeness and uniqueness.
func (in *interp) evalStructLit(e *syntax.StructLit) (Value, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeStruct {
		return Value{}, fmt.Errorf("internal: struct literal has non-struct type %s at %s", t, e.Pos)
	}
	// Walk the user's FieldInits, but write into a slice indexed by the
	// declared field order so print order stays deterministic. typeck
	// guarantees every declared field appears exactly once.
	values := make([]Value, len(t.Fields))
	provided := make([]bool, len(t.Fields))
	for _, init := range e.Fields {
		idx := -1
		for i, f := range t.Fields {
			if f.Name == init.Name {
				idx = i
				break
			}
		}
		if idx == -1 {
			return Value{}, fmt.Errorf("internal: struct %q has no field %q at %s", t.Name, init.Name, init.Pos)
		}
		v, err := in.evalExpr(init.Value)
		if err != nil {
			return Value{}, err
		}
		// v0.4: coerce when the field's declared type is a spec (or
		// composite containing a spec). For other field types this is a
		// no-op — coerceToType returns v unchanged.
		v = in.coerceToType(v, t.Fields[idx].Type)
		values[idx] = v
		provided[idx] = true
	}
	for i, ok := range provided {
		if !ok {
			return Value{}, fmt.Errorf("internal: struct %q literal missing field %q at %s", t.Name, t.Fields[i].Name, e.Pos)
		}
	}
	return structVal(t, values), nil
}

// evalIndex evaluates `xs[i]`. List indexing returns a deep copy of the
// element so a later mutation of the index target cannot leak into the
// source list. String indexing returns a rune Value (Unicode codepoint at
// position i over the rune-decoded string). Out-of-range indices are
// runtime errors — typeck cannot prove bounds at v0.2.
func (in *interp) evalIndex(e *syntax.IndexExpr) (Value, error) {
	rv, err := in.evalExpr(e.Receiver)
	if err != nil {
		return Value{}, err
	}
	iv, err := in.evalExpr(e.Index)
	if err != nil {
		return Value{}, err
	}
	idx := iv.Int
	if rv.Type == nil {
		return Value{}, fmt.Errorf("internal: index receiver has nil type at %s", e.Pos)
	}
	switch rv.Type.Kind {
	case syntax.TypeList:
		n := int64(len(rv.List))
		if idx < 0 || idx >= n {
			return Value{}, fmt.Errorf("runtime error at %s: list index %d out of range [0..%d)", e.Pos, idx, n)
		}
		// v0.3: index read aliases the element rather than deep-copying.
		return rv.List[idx], nil
	case syntax.TypeStr:
		runes := []rune(rv.Str)
		n := int64(len(runes))
		if idx < 0 || idx >= n {
			return Value{}, fmt.Errorf("runtime error at %s: string index %d out of range [0..%d)", e.Pos, idx, n)
		}
		return runeVal(int64(runes[idx])), nil
	}
	return Value{}, fmt.Errorf("internal: cannot index %s at %s", rv.Type, e.Pos)
}

// evalSlice evaluates list-slicing forms: `xs[lo..hi]`, `xs[..hi]`,
// `xs[lo..]`, `xs[..]`, `xs[lo..=hi]`. The result is a NEW list that
// deep-copies the selected range so the source list is unaffected by later
// mutations of the slice (and vice-versa). String slicing is rejected by
// typeck so this path only ever sees lists.
func (in *interp) evalSlice(e *syntax.SliceExpr) (Value, error) {
	rv, err := in.evalExpr(e.Receiver)
	if err != nil {
		return Value{}, err
	}
	if rv.Type == nil || rv.Type.Kind != syntax.TypeList {
		return Value{}, fmt.Errorf("internal: cannot slice %s at %s", rv.Type, e.Pos)
	}
	n := int64(len(rv.List))
	lo := int64(0)
	hi := n
	if e.Low != nil {
		v, err := in.evalExpr(e.Low)
		if err != nil {
			return Value{}, err
		}
		lo = v.Int
	}
	if e.High != nil {
		v, err := in.evalExpr(e.High)
		if err != nil {
			return Value{}, err
		}
		hi = v.Int
		if e.Inclusive {
			hi++
		}
	} else if e.Inclusive {
		// `xs[lo..=]` is a parse error (the parser requires `=`'s rhs);
		// reaching here would be an internal bug.
		return Value{}, fmt.Errorf("internal: inclusive slice without high bound at %s", e.Pos)
	}
	if lo < 0 || hi > n || lo > hi {
		return Value{}, fmt.Errorf("runtime error at %s: slice [%d..%d] out of range [0..%d]", e.Pos, lo, hi, n)
	}
	out := make([]Value, hi-lo)
	for i := lo; i < hi; i++ {
		// v0.3: slice copies the OUTER list header but aliases each
		// element. Primitives are value-copied by Go assignment anyway.
		out[i-lo] = rv.List[i]
	}
	// Reuse the receiver's list type so the constructed Value's Type pointer
	// matches the receiver's (consistent with the rest of the interpreter's
	// "return the same list[T] *Type" contract).
	return Value{Type: rv.Type, List: out}, nil
}

// evalFieldAccess evaluates `receiver.field`. Three paths:
//
//  1. typeck has stashed a lowered EnumLit (bare-variant enum construction
//     such as `Token.Eof`) — evaluate that.
//  2. Receiver is a bare IdentExpr naming a known enum type — produce the
//     variant Value via the enum table. typeck has validated the variant.
//  3. Otherwise the receiver is a struct value; look up the field by name
//     in the struct's declared field order and return the field value.
func (in *interp) evalFieldAccess(e *syntax.FieldAccessExpr) (Value, error) {
	if e.Lowered != nil {
		return in.evalEnumLit(e.Lowered)
	}
	if e.Safe {
		return in.evalSafeFieldAccess(e)
	}
	if id, ok := e.Receiver.(*syntax.IdentExpr); ok {
		if en, isEnum := in.cur.enums[id.Name]; isEnum {
			for i, v := range en.Variants {
				if v == e.FieldName {
					return enumVal(en, i, v, nil), nil
				}
			}
			return Value{}, fmt.Errorf("internal: enum %q has no variant %q at %s", id.Name, e.FieldName, e.NamePos)
		}
	}
	rv, err := in.evalExpr(e.Receiver)
	if err != nil {
		return Value{}, err
	}
	if rv.Type == nil || rv.Type.Kind != syntax.TypeStruct {
		return Value{}, fmt.Errorf("internal: field access on non-struct %s at %s", rv.Type, e.Pos)
	}
	for i, f := range rv.Type.Fields {
		if f.Name == e.FieldName {
			// v0.3: field read aliases the field rather than deep-copying.
			return rv.Fields[i], nil
		}
	}
	return Value{}, fmt.Errorf("internal: struct %q has no field %q at %s", rv.Type.Name, e.FieldName, e.NamePos)
}

// ---------------------------------------------------------------------------
// match.
// ---------------------------------------------------------------------------

// execMatch evaluates a match statement. PLAN-pinned semantics:
//   - arms tested top-to-bottom, first match wins
//   - guards evaluate against pattern bindings; on false, fall through
//   - if no arm matches, the statement is a runtime error (no silent
//     fall-through, per the tenth-man revision in PLAN.md)
//
// Each arm runs in a fresh frame populated with the pattern's bindings; the
// body itself is a Block whose execBlock pushes another frame, so an arm
// body is free to redeclare a name without clobbering the pattern binding.
func (in *interp) execMatch(s *syntax.MatchStmt) error {
	subj, err := in.evalExpr(s.Subject)
	if err != nil {
		return err
	}
	for i := range s.Arms {
		arm := &s.Arms[i]
		in.pushFrame()
		bound, perr := in.bindPattern(arm.Pattern, subj)
		if perr != nil {
			in.popFrame()
			return perr
		}
		if !bound {
			in.popFrame()
			continue
		}
		if arm.Guard != nil {
			gv, err := in.evalExpr(arm.Guard)
			if err != nil {
				in.popFrame()
				return err
			}
			if !gv.Bool {
				in.popFrame()
				continue
			}
		}
		err := in.execBlock(arm.Body)
		in.popFrame()
		return err
	}
	return fmt.Errorf("match: no arm matched at %s", s.Pos)
}

// bindPattern attempts to match pat against v, recording any bindings in the
// current frame. Returns (matched, err). A pattern that fails to match
// without a runtime error returns (false, nil); typeck rules out shape
// mismatches (e.g. tuple-pat against non-tuple), so this path only fires on
// value-disagreement (literal mismatch, enum variant mismatch, ...).
func (in *interp) bindPattern(pat syntax.Pattern, v Value) (bool, error) {
	// Auto-unwrap on nullable instance scrutinees. typeck has validated
	// that a non-nil pattern targets the inner element type; here we
	// mirror by matching the variant tag first and recursing on the Some
	// payload. `nil` LitPats keep the full nullable instance view so
	// litEq's tag comparison fires.
	if syntax.IsNullable(v.Type) {
		if syntax.IsNilLitPattern(pat) {
			return v.VariantName == "None", nil
		}
		if _, isEnumPat := pat.(*syntax.EnumPat); !isEnumPat {
			if v.VariantName == "None" {
				return false, nil
			}
			if len(v.Payload) == 1 {
				return in.bindPattern(pat, v.Payload[0])
			}
		}
	}
	switch p := pat.(type) {
	case *syntax.WildcardPat:
		return true, nil
	case *syntax.BindPat:
		// v0.3: bind shares the value. The borrow checker has flagged
		// the scrutinee as Moved at the BindPat site, so the user
		// can't observe the alias.
		if err := in.declare(p.Name, v); err != nil {
			return false, err
		}
		return true, nil
	case *syntax.LitPat:
		// Evaluate the literal expression in the current scope; typeck has
		// constrained it to a primitive literal (optionally negated).
		lv, err := in.evalExpr(p.Lit)
		if err != nil {
			return false, err
		}
		return litEq(lv, v), nil
	case *syntax.TuplePat:
		if v.Type == nil || v.Type.Kind != syntax.TypeTuple {
			return false, fmt.Errorf("internal: tuple pattern against non-tuple at %s", p.Pos)
		}
		if len(p.Elements) != len(v.Tuple) {
			return false, nil
		}
		for i, sub := range p.Elements {
			ok, err := in.bindPattern(sub, v.Tuple[i])
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case *syntax.StructPat:
		if v.Type == nil || v.Type.Kind != syntax.TypeStruct {
			return false, fmt.Errorf("internal: struct pattern against non-struct at %s", p.Pos)
		}
		// typeck has validated that each named field exists on the struct
		// and that all declared fields are covered when `..` is absent.
		// Field order in the pattern doesn't have to match decl order — we
		// look each field up by name. The struct value's Fields slice is
		// ordered by declaration so we use the type's Fields[i].Name to
		// find the right slot.
		for _, f := range p.Fields {
			idx := -1
			for i, df := range v.Type.Fields {
				if df.Name == f.Name {
					idx = i
					break
				}
			}
			if idx == -1 {
				return false, fmt.Errorf("internal: struct %q has no field %q at %s", v.Type.Name, f.Name, f.Pos)
			}
			ok, err := in.bindPattern(f.Pattern, v.Fields[idx])
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case *syntax.EnumPat:
		if v.Type == nil || v.Type.Kind != syntax.TypeEnum {
			return false, fmt.Errorf("internal: enum pattern against non-enum at %s", p.Pos)
		}
		// typeck rejects mismatched type names; here we compare variants.
		if v.VariantName != p.VariantName {
			return false, nil
		}
		// v0.4: bare patterns short-circuit; payload patterns recurse over the
		// runtime payload slice. typeck has already validated arity, so a
		// mismatch here would be an internal error.
		if len(p.Payload) == 0 {
			return true, nil
		}
		if len(v.Payload) != len(p.Payload) {
			return false, fmt.Errorf("internal: enum pattern arity mismatch (%d vs %d) at %s", len(p.Payload), len(v.Payload), p.Pos)
		}
		for i, sub := range p.Payload {
			ok, err := in.bindPattern(sub, v.Payload[i])
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("internal: unhandled pattern %T at %s", pat, pat.PatPos())
}

// litEq compares a literal-pattern value against the scrutinee using v0.1
// primitive equality semantics, plus byte/rune compared by codepoint. typeck
// ensures the types match, so we just dispatch on Type.
func litEq(lit, v Value) bool {
	if lit.Type == nil || v.Type == nil {
		return false
	}
	switch lit.Type {
	case syntax.TInt():
		return lit.Int == v.Int
	case syntax.TFloat():
		return lit.Float == v.Float
	case syntax.TBool():
		return lit.Bool == v.Bool
	case syntax.TStr():
		return lit.Str == v.Str
	case syntax.TByte(), syntax.TRune():
		return lit.Int == v.Int
	}
	return false
}

// ---------------------------------------------------------------------------
// v0.4 — enum payloads, method calls, vtable dispatch, composite ==.
// ---------------------------------------------------------------------------

// evalEnumLit evaluates a typeck-lowered EnumLit. The lowering walks any
// payload arguments in order and produces a runtime enum value with the
// variant tag and the per-position payload slice. typeck has validated arity
// and per-position type so this path only needs to evaluate sub-expressions.
func (in *interp) evalEnumLit(e *syntax.EnumLit) (Value, error) {
	en := e.Type()
	if en == nil || en.Kind != syntax.TypeEnum {
		return Value{}, fmt.Errorf("internal: enum literal has non-enum type %s at %s", en, e.Pos)
	}
	idx := -1
	for i, v := range en.Variants {
		if v == e.Variant {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Value{}, fmt.Errorf("internal: enum %q has no variant %q at %s", en.Name, e.Variant, e.VariantPos)
	}
	if len(e.Payload) == 0 {
		return enumVal(en, idx, e.Variant, nil), nil
	}
	payload := make([]Value, len(e.Payload))
	// The variant's declared payload types drive any spec coercion at element
	// position so a `Token.Wrap(c)` where `Wrap(Printable)` was declared
	// widens the concrete arg to a fat pointer.
	declared := en.VariantPayloads[idx]
	for i, arg := range e.Payload {
		v, err := in.evalExpr(arg)
		if err != nil {
			return Value{}, err
		}
		if i < len(declared) {
			v = in.coerceToType(v, declared[i])
		}
		payload[i] = v
	}
	return enumVal(en, idx, e.Variant, payload), nil
}

// evalMethodCall is the runtime dispatcher for `receiver.method(args)`.
// Resolution mirrors typeck's precedence:
//   1. typeck has lowered the call to an EnumLit (`Token.Ident("foo")`) →
//      delegate to evalEnumLit.
//   2. typeck has lowered the call to a list builtin (`xs.push(v)`) →
//      delegate to evalCall on the synthetic CallExpr.
//   3. Receiver is a spec-typed fat pointer → vtable dispatch via
//      ConcreteType + spec name.
//   4. Receiver is a struct/enum value → dispatch by inherent first, then
//      unique spec impl by method name.
func (in *interp) evalMethodCall(e *syntax.MethodCallExpr) (Value, error) {
	if e.Lowered != nil {
		return in.evalEnumLit(e.Lowered)
	}
	if e.LoweredCall != nil {
		return in.evalCall(e.LoweredCall)
	}
	// v0.5: cross-module fn call shape — receiver is an IdentExpr that
	// resolves to a module binding in the active module's import table,
	// and Method is a pub fn declared in that module. typeck has
	// validated pubness and arity; the runtime detects the shape and
	// routes the call into the foreign module's fn body. The body
	// executes with its own module's lexical scope (callFn switches cur
	// to the callee's owning module).
	if id, ok := e.Receiver.(*syntax.IdentExpr); ok {
		if foreign, isMod := in.cur.imports[id.Name]; isMod {
			if fn, ok := foreign.fns[e.Method]; ok {
				return in.callFn(fn, e.Args, e.Type(), e.Pos)
			}
			return Value{}, fmt.Errorf("internal: module %q has no fn %q at %s", id.Name, e.Method, e.MethodPos)
		}
	}
	rv, err := in.evalExpr(e.Receiver)
	if err != nil {
		return Value{}, err
	}
	if rv.Type == nil {
		return Value{}, fmt.Errorf("internal: method call receiver has nil type at %s", e.Pos)
	}
	// Spec-typed receiver — vtable dispatch.
	if rv.Type.Kind == syntax.TypeSpec {
		return in.dispatchSpec(e, rv)
	}
	// Concrete receiver — typeck has narrowed to struct or enum.
	return in.dispatchConcrete(e, rv)
}

// dispatchSpec routes a method call through a spec-typed fat pointer's
// (ConcreteType, Spec) pair. Resolution: concrete impl override > spec
// default > NotImplemented panic.
func (in *interp) dispatchSpec(e *syntax.MethodCallExpr, rv Value) (Value, error) {
	specName := rv.Type.Name
	if rv.Data == nil {
		return Value{}, fmt.Errorf("internal: spec value has nil data at %s", e.Pos)
	}
	this := *rv.Data
	// Bundle-wide spec method dispatch keyed by canonical *Type pointer
	// (rv.Data.Type) and spec name. The wrapped value's *Type is the
	// canonical pointer the impl table was indexed under.
	recvType := rv.Data.Type
	fn, sm := in.resolveSpecMethod(recvType, specName, e.Method)
	if fn != nil {
		return in.callMethodFn(e, fn, this)
	}
	if sm != nil {
		return in.callSpecDefault(e, sm, this)
	}
	return Value{}, fmt.Errorf("not implemented: %s.%s (declared in spec %s at %s)",
		rv.ConcreteType, e.Method, specName, e.MethodPos)
}

// resolveSpecMethod looks up the (canonical *Type, Spec) override; returns
// the impl's FnDecl if one is supplied, the spec's default method AST if
// not. Both nil means the method is signature-only with no override —
// NotImplemented.
//
// v0.5: keyed by *Type pointer so two modules' same-name structs don't
// collide. Spec name remains a string because typeck has already
// validated cross-module spec resolution and the bundle-wide
// specDeclsByName / specByType union routes correctly.
func (in *interp) resolveSpecMethod(recv *syntax.Type, specName, methodName string) (*syntax.FnDecl, *syntax.SpecMethod) {
	if specMap, ok := in.specByType[recv]; ok {
		if methods, ok := specMap[specName]; ok {
			if fn, ok := methods[methodName]; ok {
				return fn, nil
			}
		}
	}
	// v0.6 generic-impl fallback: when recv is a monomorphized type and the
	// impl was generic (`impl[T] Box[T] for Spec`), the methods are keyed
	// by the receiver's base name (`"Box"`).
	if recv != nil {
		baseName := displayEnumName(recv.Name)
		if specMap, ok := in.specByBaseName[baseName]; ok {
			if methods, ok := specMap[specName]; ok {
				if fn, ok := methods[methodName]; ok {
					return fn, nil
				}
			}
		}
	}
	// Fall through to spec default body if present anywhere in the bundle.
	if sd, ok := in.specDeclsByName[specName]; ok {
		for _, m := range sd.Methods {
			if m.Name == methodName {
				if m.Body != nil {
					return nil, m
				}
				return nil, nil
			}
		}
	}
	return nil, nil
}

// dispatchConcrete routes a method call against a struct- or enum-typed
// receiver. Inherent methods take precedence over spec impls; if no
// inherent exists, the unique spec impl exposing the method wins. typeck
// has already rejected ambiguity, so the first matching spec impl is
// sufficient.
//
// v0.5: dispatch is keyed by canonical *Type pointer (rv.Type), so two
// modules each declaring a struct named "Counter" with their own impls
// don't collide. The bundle-wide impl index unions every module's impls
// against the receiver's owning *Type.
func (in *interp) dispatchConcrete(e *syntax.MethodCallExpr, rv Value) (Value, error) {
	recv := rv.Type
	// v0.7: synthetic WaitGroup methods route to host sync.WaitGroup ops.
	if recv != nil && recv.Kind == syntax.TypeStruct && recv.Name == "WaitGroup" {
		return in.dispatchWaitGroup(e, rv)
	}
	// 1. Inherent.
	if methods, ok := in.inherentByType[recv]; ok {
		if fn, ok := methods[e.Method]; ok {
			return in.callMethodFn(e, fn, rv)
		}
	}
	// 2. Spec impls. Walk every spec name in the spec-impl map for this
	// receiver type to find one that exposes the method (override or via
	// default). typeck has rejected ambiguity so the first match is
	// definitive.
	specMap := in.specByType[recv]
	for specName := range specMap {
		fn, sm := in.resolveSpecMethod(recv, specName, e.Method)
		if fn != nil {
			return in.callMethodFn(e, fn, rv)
		}
		if sm != nil {
			return in.callSpecDefault(e, sm, rv)
		}
		// (Type, Spec) impl exists but the method has no override or default.
		// Only return NotImplemented if the spec actually declares this
		// method — otherwise keep searching (other specs might have it).
		if sd, ok := in.specDeclsByName[specName]; ok {
			for _, m := range sd.Methods {
				if m.Name == e.Method {
					return Value{}, fmt.Errorf("not implemented: %s.%s (declared in spec %s at %s)",
						recv.Name, e.Method, specName, e.MethodPos)
				}
			}
		}
	}
	// 3. v0.6 fallback: generic-impl methods registered by base receiver
	// name (`impl[T] Box[T] {...}` ⇒ recv name "Box[int]" base "Box").
	baseName := displayEnumName(recv.Name)
	if methods, ok := in.inherentByBaseName[baseName]; ok {
		if fn, ok := methods[e.Method]; ok {
			return in.callMethodFn(e, fn, rv)
		}
	}
	if specMap, ok := in.specByBaseName[baseName]; ok {
		for specName, methods := range specMap {
			if fn, ok := methods[e.Method]; ok {
				return in.callMethodFn(e, fn, rv)
			}
			// Spec default fallback for the (base, spec) pair.
			if sd, ok := in.specDeclsByName[specName]; ok {
				for _, m := range sd.Methods {
					if m.Name == e.Method && m.Body != nil {
						return in.callSpecDefault(e, m, rv)
					}
				}
			}
		}
	}
	return Value{}, fmt.Errorf("internal: method %q not resolvable on %s at %s", e.Method, recv.Name, e.MethodPos)
}

// callMethodFn binds `this` plus declared params, walks the impl method's
// body, and catches the return sentinel.
func (in *interp) callMethodFn(e *syntax.MethodCallExpr, fn *syntax.FnDecl, this Value) (Value, error) {
	// Evaluate args in caller scope first.
	args := make([]Value, len(e.Args))
	for i, a := range e.Args {
		v, err := in.evalExpr(a)
		if err != nil {
			return Value{}, err
		}
		// Coerce to declared param type so spec-typed params widen.
		if i < len(fn.Params) && fn.Params[i].Type != nil && fn.Params[i].Type.Resolved != nil {
			v = in.coerceToType(v, fn.Params[i].Type.Resolved)
		}
		args[i] = v
	}
	// Method bodies are like fn calls — a fresh frame stack rooted at one
	// frame, plus a switch to the method's owning module so identifier
	// lookups inside the body see the right per-module tables. Save and
	// restore both.
	savedStack := in.stack
	savedCur := in.cur
	in.stack = []*frame{newFrame()}
	if owner := in.fnOwner[fn]; owner != nil {
		in.cur = owner
	}
	in.pushDeferFrame(fn.HasDefers)
	defer func() {
		in.stack = savedStack
		in.cur = savedCur
	}()
	if err := in.declare("this", this); err != nil {
		in.popDeferFrame(fn.HasDefers)
		return Value{}, err
	}
	for i, p := range fn.Params {
		if err := in.declare(p.Name, args[i]); err != nil {
			in.popDeferFrame(fn.HasDefers)
			return Value{}, err
		}
	}
	for _, st := range fn.Body.Statements {
		err := in.execStmt(st)
		if err == nil {
			continue
		}
		var ret *errReturn
		if errors.As(err, &ret) {
			in.drainDefers(fn.HasDefers)
			retVal := ret.value
			if fn.Return != nil && fn.Return.Resolved != nil {
				retVal = in.coerceToType(retVal, fn.Return.Resolved)
			}
			return retVal, nil
		}
		if pret, perr := in.catchPropagateForFn(err, fn); perr != nil {
			in.drainDefers(fn.HasDefers)
			return Value{}, perr
		} else if pret != nil {
			in.drainDefers(fn.HasDefers)
			retVal := pret.value
			if fn.Return != nil && fn.Return.Resolved != nil {
				retVal = in.coerceToType(retVal, fn.Return.Resolved)
			}
			return retVal, nil
		}
		if errors.Is(err, errBreak) || errors.Is(err, errContinue) {
			in.drainDefers(fn.HasDefers)
			return Value{}, fmt.Errorf("internal: %v escaped method %s", err, fn.Name)
		}
		in.drainDefers(fn.HasDefers)
		return Value{}, err
	}
	in.drainDefers(fn.HasDefers)
	if e.Type() != nil && e.Type() != syntax.TVoid() {
		return Value{}, fmt.Errorf("method %q ended without return at %s", fn.Name, fn.Pos)
	}
	return Value{}, nil
}

// callSpecDefault is the SpecMethod analogue of callMethodFn — spec defaults
// store their body on a SpecMethod, not a FnDecl, but the receiver-binding
// machinery is otherwise identical.
func (in *interp) callSpecDefault(e *syntax.MethodCallExpr, sm *syntax.SpecMethod, this Value) (Value, error) {
	args := make([]Value, len(e.Args))
	for i, a := range e.Args {
		v, err := in.evalExpr(a)
		if err != nil {
			return Value{}, err
		}
		if i < len(sm.Params) && sm.Params[i].Type != nil && sm.Params[i].Type.Resolved != nil {
			v = in.coerceToType(v, sm.Params[i].Type.Resolved)
		}
		args[i] = v
	}
	savedStack := in.stack
	savedCur := in.cur
	in.stack = []*frame{newFrame()}
	if owner := in.specMethodOwner[sm]; owner != nil {
		in.cur = owner
	}
	in.pushDeferFrame(sm.HasDefers)
	defer func() {
		in.stack = savedStack
		in.cur = savedCur
	}()
	if err := in.declare("this", this); err != nil {
		in.popDeferFrame(sm.HasDefers)
		return Value{}, err
	}
	for i, p := range sm.Params {
		if err := in.declare(p.Name, args[i]); err != nil {
			in.popDeferFrame(sm.HasDefers)
			return Value{}, err
		}
	}
	for _, st := range sm.Body.Statements {
		err := in.execStmt(st)
		if err == nil {
			continue
		}
		var ret *errReturn
		if errors.As(err, &ret) {
			in.drainDefers(sm.HasDefers)
			retVal := ret.value
			if sm.Return != nil && sm.Return.Resolved != nil {
				retVal = in.coerceToType(retVal, sm.Return.Resolved)
			}
			return retVal, nil
		}
		var smRet *syntax.Type
		if sm.Return != nil {
			smRet = sm.Return.Resolved
		}
		if pret, perr := catchPropagate(err, smRet); perr != nil {
			in.drainDefers(sm.HasDefers)
			return Value{}, perr
		} else if pret != nil {
			in.drainDefers(sm.HasDefers)
			retVal := pret.value
			if smRet != nil {
				retVal = in.coerceToType(retVal, smRet)
			}
			return retVal, nil
		}
		if errors.Is(err, errBreak) || errors.Is(err, errContinue) {
			in.drainDefers(sm.HasDefers)
			return Value{}, fmt.Errorf("internal: %v escaped spec default %s", err, sm.Name)
		}
		in.drainDefers(sm.HasDefers)
		return Value{}, err
	}
	in.drainDefers(sm.HasDefers)
	if e.Type() != nil && e.Type() != syntax.TVoid() {
		return Value{}, fmt.Errorf("spec default %q ended without return at %s", sm.Name, sm.Pos)
	}
	return Value{}, nil
}

// coerceToType narrows the runtime widening rule that typeck encodes via
// assignableTo: a concrete struct / enum value flowing into a spec-typed
// slot is wrapped in a fat pointer; list[Spec] / tuple[..., Spec] / struct
// fields of spec type recurse element-wise. Same shape on both sides → no-op.
//
// The wrap point matters because spec method dispatch reads ConcreteType off
// the wrapper; without coercion at binding / arg / return / list-elem / struct-
// field sites, vtable lookup would fail.
func (in *interp) coerceToType(v Value, target *syntax.Type) Value {
	if target == nil || v.Type == nil {
		return v
	}
	switch target.Kind {
	case syntax.TypeSpec:
		if v.Type.Kind == syntax.TypeSpec {
			// Already wrapped — typically the same spec; assume so since
			// typeck rejects spec-to-different-spec.
			return v
		}
		// Wrap concrete value.
		return specVal(target, v)
	case syntax.TypeList:
		if v.Type.Kind != syntax.TypeList || target.Element == nil {
			return v
		}
		// Only descend when the target element type differs (e.g. Spec) —
		// recursion into pure-primitive lists would be an unnecessary copy.
		if target.Element.Equals(v.Type.Element) && target.Element.Kind != syntax.TypeSpec {
			return v
		}
		out := make([]Value, len(v.List))
		for i, e := range v.List {
			out[i] = in.coerceToType(e, target.Element)
		}
		return Value{Type: target, List: out}
	case syntax.TypeTuple:
		if v.Type.Kind != syntax.TypeTuple || len(target.Tuple) != len(v.Tuple) {
			return v
		}
		needs := false
		for _, t := range target.Tuple {
			if t != nil && t.Kind == syntax.TypeSpec {
				needs = true
				break
			}
		}
		if !needs {
			return v
		}
		out := make([]Value, len(v.Tuple))
		for i, e := range v.Tuple {
			out[i] = in.coerceToType(e, target.Tuple[i])
		}
		return Value{Type: target, Tuple: out}
	}
	return v
}

// eqValues recursively compares two runtime values structurally. typeck has
// validated that comparable shapes match; this walker handles primitives via
// the existing valueEq plus composite kinds (list, tuple, struct, enum with
// payload). Spec-typed bindings on either side are typeck-rejected; defensive
// here so a slip-through fails loudly.
func eqValues(a, b Value) (bool, error) {
	if a.Type == nil || b.Type == nil {
		return false, fmt.Errorf("internal: eqValues on untyped value")
	}
	if a.Type.Kind == syntax.TypeSpec || b.Type.Kind == syntax.TypeSpec {
		return false, fmt.Errorf("internal: spec == reached runtime")
	}
	switch a.Type.Kind {
	case syntax.TypeInt, syntax.TypeFloat, syntax.TypeBool, syntax.TypeStr,
		syntax.TypeByte, syntax.TypeRune:
		return valueEq(a, b), nil
	case syntax.TypeList:
		if len(a.List) != len(b.List) {
			return false, nil
		}
		for i := range a.List {
			eq, err := eqValues(a.List[i], b.List[i])
			if err != nil {
				return false, err
			}
			if !eq {
				return false, nil
			}
		}
		return true, nil
	case syntax.TypeTuple:
		if len(a.Tuple) != len(b.Tuple) {
			return false, nil
		}
		for i := range a.Tuple {
			eq, err := eqValues(a.Tuple[i], b.Tuple[i])
			if err != nil {
				return false, err
			}
			if !eq {
				return false, nil
			}
		}
		return true, nil
	case syntax.TypeStruct:
		// typeck guarantees same struct type → same field count.
		if len(a.Fields) != len(b.Fields) {
			return false, nil
		}
		for i := range a.Fields {
			eq, err := eqValues(a.Fields[i], b.Fields[i])
			if err != nil {
				return false, err
			}
			if !eq {
				return false, nil
			}
		}
		return true, nil
	case syntax.TypeEnum:
		if a.VariantIndex != b.VariantIndex {
			return false, nil
		}
		if len(a.Payload) != len(b.Payload) {
			return false, nil
		}
		for i := range a.Payload {
			eq, err := eqValues(a.Payload[i], b.Payload[i])
			if err != nil {
				return false, err
			}
			if !eq {
				return false, nil
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("internal: eqValues unhandled kind %d", int(a.Type.Kind))
}
