// Package build emits a C source file from a parsed Zerg program and shells
// out to the system C compiler to produce a native binary.
//
// At v0.2 the codegen lowers the full primitive-typed surface PLUS composite
// data: tuples, lists, structs, enums, match. The runtime helpers live inline
// in runtime.go and are emitted once at the top of the generated .c file.
// Stdout produced by the compiled binary must equal stdout produced by the
// interpreter for every program in the parity corpus — mirror run.go's
// semantics, do not freelance.
package build

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// Emit writes the C source for prog to w. Single-program adapter for the
// v0.5 EmitBundle entry — wraps prog in a one-module bundle whose entry
// module is named "main" and forwards. Backward compatible with v0.0–v0.4
// callers; the only output difference is the module-mangle prefix on
// composite-type symbol names per PLAN.md §Mangling for codegen.
func Emit(prog *syntax.Program, w io.Writer) error {
	return EmitBundle(&singleEmitBundle{prog: prog}, w)
}

// emitBundleView is the minimal interface EmitBundle needs over a
// loader.Bundle. We declare it locally to avoid an import cycle on the
// loader package; loader.Bundle satisfies it via syntax.BundleView's
// methods (BundleEntry, BundleModules) plus the additional ModulePath
// method on each ModuleView, which we layer here. For programs that go
// through syntax.BundleView only (no canonical path), the entry uses
// "main" and siblings fall back to ModuleName().
type emitBundleView interface {
	BundleEntry() syntax.ModuleView
	BundleModules() []syntax.ModuleView
}

// EmitBundle is the v0.5 codegen entry: emits one merged C TU containing
// every module's symbols, with module-mangled names per PLAN.md §Mangling
// for codegen. Single-file programs reach here through Emit's one-module
// adapter and produce identical output to v0.4 plus the module prefix.
func EmitBundle(bundle emitBundleView, w io.Writer) error {
	if bundle == nil {
		return nil
	}
	mods := bundle.BundleModules()
	if len(mods) == 0 {
		return nil
	}
	entry := bundle.BundleEntry()

	g := &cgen{
		indent:        1,
		fnTable:       map[string]*syntax.FnDecl{},
		specs:         map[string]*syntax.SpecDecl{},
		inherent:      map[string][]*syntax.FnDecl{},
		specImpls:     map[implKey]*syntax.ImplDecl{},
		receiverTypes: map[string]*syntax.Type{},
		specsUsed:     map[string]bool{},
		modules:       []moduleEmit{},
		typeOwner:     map[*syntax.Type]string{},
		specOwner:     map[string]string{},
		fnOwner:       map[*syntax.FnDecl]string{},
		moduleByName:  map[string]*moduleEmit{},
		entryProg:     nil,
		anonByNode:    map[interface{}]*anonFnEmit{},
	}
	g.shapes = newShapeRegistry()

	// Phase 1: register every module with its mangle prefix and prime the
	// owner tables. The entry module's canonical name is the literal "main"
	// per PLAN.md §Resolution rules; siblings use ModuleName() (the
	// loader sets this to the absolute path of the resolved sibling).
	for _, m := range mods {
		canonical := m.ModuleName()
		mangle := mangleModule(canonical)
		me := moduleEmit{
			view:         m,
			prog:         m.ModuleProgram(),
			mangle:       mangle,
			topLevelVars: map[string]bool{},
		}
		g.modules = append(g.modules, me)
	}
	for i := range g.modules {
		me := &g.modules[i]
		// Bind imports' local name -> target module mangle so cross-module
		// fn calls can route via IdentExpr receiver.
		me.imports = map[string]string{}
		for _, imp := range me.view.ModuleImports() {
			if imp == nil {
				continue
			}
			target := imp.ImportTarget()
			if target == nil {
				continue
			}
			for j := range g.modules {
				if g.modules[j].view == target {
					me.imports[imp.ImportLocalName()] = g.modules[j].mangle
				}
			}
		}
	}
	for i := range g.modules {
		me := &g.modules[i]
		g.moduleByName[me.mangle] = me
	}
	if entry != nil {
		g.entryProg = entry.ModuleProgram()
		for i := range g.modules {
			if g.modules[i].view == entry {
				g.entryMangle = g.modules[i].mangle
				break
			}
		}
	}
	// v0.14 P1: collect top-level let / mut / const binding names for
	// non-entry modules so fn-body identifiers resolving to those names
	// get module-mangled in identName. The entry module's bindings stay
	// in main()'s local scope per the historical emit, so they're not
	// registered here.
	for i := range g.modules {
		me := &g.modules[i]
		if me.mangle == g.entryMangle {
			continue
		}
		for _, stmt := range me.prog.Statements {
			switch s := stmt.(type) {
			case *syntax.LetStmt:
				if s.Name != "" {
					me.topLevelVars[s.Name] = true
				}
			case *syntax.MutStmt:
				if s.Name != "" {
					me.topLevelVars[s.Name] = true
				}
			case *syntax.ConstStmt:
				if s.Name != "" {
					me.topLevelVars[s.Name] = true
				}
			}
		}
	}

	// Phase 2: stamp every canonical *Type and spec name with its owning
	// module's mangle. typeck has stamped TypeRef.Resolved with canonical
	// pointers; we walk every module's struct/enum/spec decls to build the
	// table. Cross-module references reach the same canonical pointer.
	for i := range g.modules {
		me := &g.modules[i]
		for _, stmt := range me.prog.Statements {
			switch s := stmt.(type) {
			case *syntax.StructDecl:
				if t := findCanonicalTypeRef(me.prog, s.Name, syntax.TypeStruct); t != nil {
					g.typeOwner[t] = me.mangle
				}
			case *syntax.EnumDecl:
				if t := findCanonicalTypeRef(me.prog, s.Name, syntax.TypeEnum); t != nil {
					g.typeOwner[t] = me.mangle
				}
			case *syntax.SpecDecl:
				g.specOwner[s.Name] = me.mangle
			case *syntax.FnDecl:
				g.fnOwner[s] = me.mangle
				if me.view == entry {
					g.fnTable[s.Name] = s
				}
				// v0.6: generic FnDecls stay registered for the call-site
				// resolver (so checkExpr can find the original by name) but
				// the codegen iterator skips them — see fn-emit loop above.
			case *syntax.ImplDecl:
				// typeck (Unit 6.5) stamps the canonical *Type pointer of
				// the resolved receiver onto s.Receiver. Use it to claim
				// ownership when the receiver type is declared in this
				// module — covers the case where the module declares
				// `enum X { ... } impl X { ... }` but never references X
				// from an expression site (so neither findCanonicalTypeRef
				// nor stampCrossModuleOwners can recover the canonical
				// pointer for this module). Same root cause that Unit 6.5
				// fixed for the interp-side dispatch table.
				if s.TypeModule == "" && s.Receiver != nil {
					if _, set := g.typeOwner[s.Receiver]; !set {
						g.typeOwner[s.Receiver] = me.mangle
					}
				}
			}
		}
	}
	// Phase 2.5: stamp ownership for monomorphised FnDecl clones. The
	// clones live on each module's prog.MonoFns (set by typeck during
	// generic-fn specialisation in the defining module). The owning
	// module's mangle drives fnCName so the emitted symbol differs from
	// the generic decl's name.
	for i := range g.modules {
		me := &g.modules[i]
		for _, fn := range me.prog.MonoFns {
			if fn == nil {
				continue
			}
			g.fnOwner[fn] = me.mangle
		}
	}
	// Phase 2b: cross-module-aware stamping. Walks every module's
	// expressions for cross-module references (`mod.Type {...}`,
	// `mod.Variant`, `mod.fn(...)`) and stamps the resolved canonical *Type
	// with the *owning* module's mangle (not the host's). This catches
	// types whose owning module never references them locally — e.g. an
	// `impl Counter { fn show() -> str { return "a" } }` block with a body
	// that returns a primitive and so never surfaces Counter via
	// findCanonicalTypeRef on a.zg. The canonical pointer is reachable
	// only from the importing module's expressions; we use the
	// `Module` qualifier on the expr to know who owns the type.
	for i := range g.modules {
		host := &g.modules[i]
		for _, stmt := range host.prog.Statements {
			stampCrossModuleOwners(stmt, host, g.moduleByName, g.typeOwner)
		}
	}
	// Fallback: scan every module's expressions for any Type the decl-walk
	// missed (e.g. types declared in module A but only referenced via
	// expressions in module B). The bundle-wide canonical pointer set is
	// the union; first occurrence wins.
	for i := range g.modules {
		me := &g.modules[i]
		stampTypeOwners(me.prog, me.mangle, g.typeOwner)
	}

	// Phase 3: collect specs / impls bundle-wide. Each impl is registered
	// against its receiver's canonical *Type pointer (so two modules'
	// same-name structs index distinctly).
	for i := range g.modules {
		me := &g.modules[i]
		g.collectSpecsImpls(me.prog)
	}

	// Phase 4: collect all composite shapes across every module.
	for i := range g.modules {
		me := &g.modules[i]
		if err := g.collectShapes(me.prog); err != nil {
			return err
		}
	}

	// v0.7 runtime is emitted only when the program uses any concurrency
	// primitive — keeps the cgen size guard for v0.0–v0.6 programs intact.
	needsV07 := g.programUsesV07()
	// 0. Feature-test macros. ucontext on macOS arm64 needs _XOPEN_SOURCE
	// 600 set BEFORE any system header is parsed; otherwise the first
	// transitive include (commonly <stdio.h>) parses sys/ucontext.h
	// without _XOPEN_SOURCE active and ucontext_t never gets defined.
	// _DARWIN_C_SOURCE re-exposes MAP_ANON which strict _XOPEN_SOURCE
	// would hide. Emitting these unconditionally is harmless for non-
	// concurrency programs.
	if needsV07 {
		g.b.WriteString("#define _XOPEN_SOURCE 600\n")
		g.b.WriteString("#define _DARWIN_C_SOURCE\n")
		g.b.WriteString("#define _DEFAULT_SOURCE\n")
	}
	// 1. Runtime header.
	g.b.WriteString(runtimeC)
	g.b.WriteString("\n")
	g.b.WriteString(runtimeV04C)
	g.b.WriteString("\n")
	if needsV07 {
		g.b.WriteString(buildV12RuntimePreamble())
		g.b.WriteString("\n")
	}
	// v0.8 stdlib runtime — gated on any reachable __builtin call so v0.0-
	// v0.7 programs preserve their byte-identical emit. Actual emission is
	// deferred to after the shape registry's typedef pass because the
	// runtime references zerg_list_zerg_str (strings_split's return) and
	// the corresponding typedef must precede it. The walker still runs
	// here so the gate is computed once.
	needsV08 := g.programUsesV08()
	needsArgv := g.programUsesArgv()
	if needsV08 || needsArgv {
		// Force-monomorphise the list[str] shape so its typedef + helpers
		// land in the shape registry before the runtime references them.
		// Without this, a program that calls only strings_join (which takes
		// list[str] but doesn't construct one) would never reach the shape
		// through the user-AST walks. v0.9 os.argv has the same need.
		g.shapes.addType(g, listOfStrType())
	}
	// v0.14 str ↔ list[byte] bridge: force-mono list[byte] so the
	// zerg_str_bytes / zerg_list_uint8_t_to_str helpers compile when a
	// program calls s.bytes() / buf.to_str() without otherwise touching
	// list[byte] (matches the v0.8 list[str] force-mono pattern).
	needsV14StrPrims := g.programUsesV14StrPrims()
	if needsV14StrPrims {
		g.shapes.addType(g, listOfByteType())
	}

	// v0.7: wire the Option[T] lookup so chan recv helpers can name the
	// canonical Option[T] enum. The typed AST stamps every RecvExpr.Type()
	// with the canonical Option[T]; we walk every RecvExpr and chan-typed
	// `for v in ch` site to harvest the pointer keyed by element-type
	// string. addChanShape later consults this index when registering the
	// chan helper.
	g.chanOptionByElemKey = map[string]*syntax.Type{}
	for i := range g.modules {
		g.harvestChanOptionTypes(g.modules[i].prog)
	}
	g.chanOptionLookup = func(elem *syntax.Type) *syntax.Type {
		if elem == nil {
			return nil
		}
		return g.chanOptionByElemKey[elem.String()]
	}

	// v0.7: pre-collect chan shapes from typed AST so emitChanTypedefs runs
	// before the shape registry's typedef pass (chan struct lives next to
	// list/tuple struct definitions but is its own table). The same walk
	// pre-registers anon-fn / defer / spawn-named-call records so the env
	// structs and trampoline fns can be forward-declared before user fn
	// bodies reference them.
	for i := range g.modules {
		me := &g.modules[i]
		g.collectChanShapes(me.prog)
		g.preregisterAnonFns(me.prog)
	}

	// 2. Composite-shape typedefs and helpers (forward decls then bodies).
	g.shapes.emitForwardDecls(g, &g.b)
	g.emitSpecForwardDecls()
	g.emitChanForwardDecls()
	g.shapes.emitTypedefs(g, &g.b)
	g.emitChanTypedefs()
	g.shapes.emitHelpers(g, &g.b)
	g.emitChanHelpers()
	// v0.8 stdlib runtime lands after the shape helpers because
	// zerg_strings_split returns zerg_list_zerg_str — the per-shape push /
	// copy helpers must be defined before the runtime calls them.
	if needsV08 {
		g.b.WriteString(runtimeV08C)
		g.b.WriteString("\n")
	}
	// v0.14 str-prim helpers — emitted after the shape helpers and the
	// v0.8 runtime so the zerg_list_uint8_t typedef is in scope when
	// the bytes / to_str helpers reference it. Gated independently of
	// v0.8 because a program may use s.bytes() without pulling in any
	// __builtin module (the v0.14 bridge is implementable in pure Zerg
	// over inline asm, so a stdlib-free program can still need it).
	if needsV14StrPrims {
		g.b.WriteString(runtimeV14StrPrimsC)
		g.b.WriteString("\n")
	}
	// v0.9 stdlib runtime — gated on a reachable time builtin so v0.0–v0.8
	// programs (and v0.9 programs that use only os.argv / os.exit) preserve
	// their byte-identical emit. <time.h> is conditionally included by the
	// runtime block itself.
	if g.programUsesV09Time() {
		g.b.WriteString(runtimeV09TimeC)
		g.b.WriteString("\n")
	}
	// v0.9 Unit 3 — std/os primitive runtime. Lands after the shape
	// helpers so any list[zerg_str] shape force-mono lands first
	// (historically required when zerg_os_argv emitted a list-builder;
	// kept as-is for consistency with the gate). Emit ONLY when a
	// program reaches an argv or envp primitive so a v0.8 program that
	// imports std/os for nothing keeps its pre-v0.9 byte-identical emit.
	if needsArgv || g.programUsesEnvp() {
		g.b.WriteString(runtimeV09ArgvExitC)
		g.b.WriteString("\n")
	}
	g.emitEqHelpers()
	g.emitSpecVtablesAndMethods()
	if err := g.emitAnonFnHeaders(); err != nil {
		return err
	}

	// v0.14 P1: imported-module top-level globals. Each non-entry module's
	// let / mut / const stmts emit as `static <T> z_<mangle>__<name> = init;`
	// at file scope before the fn forward decls. identName resolves
	// fn-body references to these symbols. Initializer must be a C
	// constant expression today; non-constant inits would need a runtime
	// init function, deferred until a use case appears.
	if err := g.emitImportedModuleGlobals(); err != nil {
		return err
	}

	// 3. Top-level fn forward decls then bodies, ACROSS every module.
	// Forward decls first so any fn can call any other regardless of
	// textual or module order.
	//
	// v0.6: generic FnDecls (those with TypeParams) have unresolved body
	// type-refs; they are NEVER emitted. Each call site routes to its
	// monomorphised clone which lives in prog.MonoFns and produces a
	// concrete C symbol per (decl, type-args).
	hasAnyFn := false
	for i := range g.modules {
		me := &g.modules[i]
		for _, stmt := range me.prog.Statements {
			fn, ok := stmt.(*syntax.FnDecl)
			if !ok {
				continue
			}
			if len(fn.TypeParams) > 0 {
				continue
			}
			if g.skipBuiltinFn(fn, needsArgv) {
				continue
			}
			hasAnyFn = true
			g.writeFnSig(fn)
			g.b.WriteString(";\n")
		}
		for _, fn := range me.prog.MonoFns {
			if fn == nil {
				continue
			}
			hasAnyFn = true
			g.writeFnSig(fn)
			g.b.WriteString(";\n")
		}
	}
	if hasAnyFn {
		g.b.WriteString("\n")
	}

	for i := range g.modules {
		me := &g.modules[i]
		for _, stmt := range me.prog.Statements {
			fn, ok := stmt.(*syntax.FnDecl)
			if !ok {
				continue
			}
			if len(fn.TypeParams) > 0 {
				continue
			}
			if g.skipBuiltinFn(fn, needsArgv) {
				continue
			}
			if err := g.emitFn(fn); err != nil {
				return err
			}
			g.b.WriteString("\n")
		}
		for _, fn := range me.prog.MonoFns {
			if fn == nil {
				continue
			}
			if err := g.emitFn(fn); err != nil {
				return err
			}
			g.b.WriteString("\n")
		}
	}

	// 3.5 v0.7: emit anon-fn / defer-body trampolines AFTER user fns so the
	// trampolines can reference user fns by their mangled symbols. The
	// env-struct typedefs and forward declarations were emitted earlier
	// (right after the runtime header) so user fn bodies could call into
	// them too.
	if err := g.emitAnonFnBodies(); err != nil {
		return err
	}

	// 4. main(). Top-level type decls and import decls are NOT executable;
	// only the entry module's executable statements run. The active module
	// is the entry's mangle so cross-module fn calls resolve through its
	// import table.
	prevMod := g.currentMod
	g.currentMod = g.entryMangle
	defer func() { g.currentMod = prevMod }()

	if needsV07 {
		// v0.12 wraps the user's top-level body in a coroutine so its
		// defers run via the per-coroutine LIFO walk in zerg_coro_entry,
		// AND so any user fn it calls executes inside a coro context
		// (zerg_coro_defer would abort otherwise). C main becomes a
		// thin shell: scheduler init → spawn the wrapper → drain.
		g.b.WriteString("static void __zerg_top_main(void *__arg) {\n")
		g.b.WriteString("    (void)__arg;\n")
		if g.entryProg != nil {
			for _, stmt := range g.entryProg.Statements {
				switch stmt.(type) {
				case *syntax.FnDecl, *syntax.StructDecl, *syntax.EnumDecl:
					continue
				case *syntax.SpecDecl, *syntax.ImplDecl:
					continue
				case *syntax.ImportDecl:
					continue
				}
				if err := g.emitStmt(stmt); err != nil {
					return err
				}
			}
		}
		g.b.WriteString("}\n")
		if needsArgv {
			g.b.WriteString("int main(int argc, char **argv) {\n")
			g.b.WriteString("    setvbuf(stdout, 0, _IONBF, 0);\n")
			g.b.WriteString("    __zerg_argc = argc;\n")
			g.b.WriteString("    __zerg_argv = argv;\n")
		} else {
			g.b.WriteString("int main(void) {\n")
			g.b.WriteString("    setvbuf(stdout, 0, _IONBF, 0);\n")
		}
		g.b.WriteString("    zerg_sched_init(0);\n")
		g.b.WriteString("    zerg_coro_spawn(__zerg_top_main, 0);\n")
		g.b.WriteString("    zerg_sched_drain();\n")
		g.b.WriteString("    return 0;\n")
		g.b.WriteString("}\n")
	} else {
		// No v0.7 features → no concurrency runtime → plain main with
		// the top-level statements inlined as before.
		if needsArgv {
			g.b.WriteString("int main(int argc, char **argv) {\n")
			g.b.WriteString("    setvbuf(stdout, 0, _IONBF, 0);\n")
			g.b.WriteString("    __zerg_argc = argc;\n")
			g.b.WriteString("    __zerg_argv = argv;\n")
		} else {
			g.b.WriteString("int main(void) {\n")
			g.b.WriteString("    setvbuf(stdout, 0, _IONBF, 0);\n")
		}
		if g.entryProg != nil {
			for _, stmt := range g.entryProg.Statements {
				switch stmt.(type) {
				case *syntax.FnDecl, *syntax.StructDecl, *syntax.EnumDecl:
					continue
				case *syntax.SpecDecl, *syntax.ImplDecl:
					continue
				case *syntax.ImportDecl:
					continue
				}
				if err := g.emitStmt(stmt); err != nil {
					return err
				}
			}
		}
		g.b.WriteString("    return 0;\n")
		g.b.WriteString("}\n")
	}

	_, err := io.WriteString(w, g.b.String())
	return err
}

// singleEmitBundle wraps a single Program in the BundleView interface so
// Emit (single-program callers) routes through EmitBundle exactly like the
// loader's multi-module Bundle does. The entry module pointer is shared
// across BundleEntry / BundleModules so EmitBundle's `entry == module.view`
// pointer-equality check fires correctly.
type singleEmitBundle struct {
	prog *syntax.Program
	mod  *singleEmitModule
}

func (b *singleEmitBundle) module() *singleEmitModule {
	if b.mod == nil {
		b.mod = &singleEmitModule{prog: b.prog}
	}
	return b.mod
}

func (b *singleEmitBundle) BundleEntry() syntax.ModuleView {
	return b.module()
}

func (b *singleEmitBundle) BundleModules() []syntax.ModuleView {
	return []syntax.ModuleView{b.module()}
}

type singleEmitModule struct {
	prog *syntax.Program
}

func (m *singleEmitModule) ModuleName() string             { return "main" }
func (m *singleEmitModule) ModuleProgram() *syntax.Program { return m.prog }
func (m *singleEmitModule) ModuleImports() []syntax.ImportView {
	return nil
}

// moduleEmit is a per-module record consumed by EmitBundle: the canonical
// module-mangle prefix, the program AST, and the import-binding table
// (local name → target module's mangle prefix). Cross-module fn calls
// route through imports.
type moduleEmit struct {
	view    syntax.ModuleView
	prog    *syntax.Program
	mangle  string
	imports map[string]string
	// topLevelVars is the set of top-level let / mut / const binding
	// names declared in this module. Populated for non-entry modules
	// only — those bindings get promoted to file-scope C statics
	// (`z_<mangle>__<name>`) so fn bodies in the same module can read
	// and write them via the identName lookup. The entry module keeps
	// its top-level lets / muts inline inside main() per the historical
	// emit; only imported modules' bindings need promotion since their
	// fn bodies execute in a foreign translation unit.
	topLevelVars map[string]bool
}

// stampCrossModuleOwners walks stmt's expressions inside a host module
// and, for any cross-module reference (`mod.Type {...}`,
// `mod.Variant(...)`), stamps the resolved canonical *Type with the
// owning (foreign) module's mangle. Already-stamped types are not
// overwritten (first owner wins). This pass exists because the owning
// module may never reference its own type from an expression — leaving
// `findCanonicalTypeRef` on that module empty-handed for the type's
// canonical pointer.
func stampCrossModuleOwners(stmt syntax.Stmt, host *moduleEmit, moduleByName map[string]*moduleEmit, owner map[*syntax.Type]string) {
	visit := func(e syntax.Expr) {
		walkExprTypes(e, func(*syntax.Type) {})
	}
	_ = visit
	var walkE func(syntax.Expr)
	var walkS func(syntax.Stmt)
	walkE = func(e syntax.Expr) {
		if e == nil {
			return
		}
		switch x := e.(type) {
		case *syntax.StructLit:
			if x.Module != "" {
				if foreignMangle, ok := host.imports[x.Module]; ok {
					if t := x.Type(); t != nil {
						if _, set := owner[t]; !set {
							owner[t] = foreignMangle
						}
					}
				}
			}
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *syntax.EnumLit:
			// Cross-module enum lits (`mod.Status.Ok`,
			// `mod.Token.Ident("x")`) carry the importing-module's local
			// binding name in x.Module; resolve it to the foreign mangle
			// and stamp the canonical *Type so the receiver's defining
			// module wins ownership even when the foreign module never
			// references its own enum from an expression. Mirrors the
			// StructLit branch above.
			if x.Module != "" {
				if foreignMangle, ok := host.imports[x.Module]; ok {
					if t := x.Type(); t != nil {
						if _, set := owner[t]; !set {
							owner[t] = foreignMangle
						}
					}
				}
			}
			for _, p := range x.Payload {
				walkE(p)
			}
		case *syntax.MethodCallExpr:
			// `mod.fn(args)` cross-module call: receiver is an IdentExpr
			// whose Name is a module local-binding. The result type is
			// foreign — but it's a *fn return*, not necessarily a struct
			// owned by the foreign module (could be a primitive or a
			// composite-of-foreign). The Lowered enum-lit / Lowered
			// struct-lit hop handles the foreign-type stamping for those.
			if id, ok := x.Receiver.(*syntax.IdentExpr); ok {
				if foreignMangle, fok := host.imports[id.Name]; fok {
					// Foreign call. If the call's result type is a struct
					// or enum and the call's foreign module owns it,
					// stamp it.
					if t := x.Type(); t != nil && (t.Kind == syntax.TypeStruct || t.Kind == syntax.TypeEnum) {
						// We need to confirm the type is owned by the
						// foreign module — defer to the local stamp
						// pass. For safety, stamp only if no owner is
						// recorded yet AND the foreign module declares
						// a type with this name.
						if _, set := owner[t]; !set {
							if me, mok := moduleByName[foreignMangle]; mok {
								for _, ms := range me.prog.Statements {
									switch ds := ms.(type) {
									case *syntax.StructDecl:
										if ds.Name == t.Name {
											owner[t] = foreignMangle
										}
									case *syntax.EnumDecl:
										if ds.Name == t.Name {
											owner[t] = foreignMangle
										}
									}
									if _, set2 := owner[t]; set2 {
										break
									}
								}
							}
						}
					}
				}
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
		case *syntax.FieldAccessExpr:
			// `mod.Type.Variant` for bare cross-module enum lits arrives
			// here with a chain of FieldAccessExpr, but typeck typically
			// lowers these to EnumLit/MethodCallExpr forms. Recurse for
			// completeness.
			walkE(x.Receiver)
			if x.Lowered != nil {
				walkE(x.Lowered)
			}
		case *syntax.UnaryExpr:
			walkE(x.Operand)
		case *syntax.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.ParenExpr:
			walkE(x.Inner)
		case *syntax.CallExpr:
			walkE(x.Callee)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.ListLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.TupleLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.IndexExpr:
			walkE(x.Receiver)
			walkE(x.Index)
		case *syntax.SliceExpr:
			walkE(x.Receiver)
			walkE(x.Low)
			walkE(x.High)
		}
	}
	walkS = func(s syntax.Stmt) {
		if s == nil {
			return
		}
		switch n := s.(type) {
		case *syntax.LetStmt:
			walkE(n.Value)
		case *syntax.MutStmt:
			walkE(n.Value)
		case *syntax.ConstStmt:
			walkE(n.Value)
		case *syntax.AssignStmt:
			walkE(n.Target)
			walkE(n.Value)
		case *syntax.ExprStmt:
			walkE(n.Expr)
		case *syntax.PrintStmt:
			walkE(n.Expr)
		case *syntax.ReturnStmt:
			walkE(n.Value)
			walkE(n.Guard)
		case *syntax.BreakStmt:
			walkE(n.Guard)
		case *syntax.ContinueStmt:
			walkE(n.Guard)
		case *syntax.IfStmt:
			walkE(n.Cond)
			if n.Then != nil {
				for _, st := range n.Then.Statements {
					walkS(st)
				}
			}
			for _, ec := range n.Elifs {
				walkE(ec.Cond)
				if ec.Body != nil {
					for _, st := range ec.Body.Statements {
						walkS(st)
					}
				}
			}
			if n.Else != nil {
				for _, st := range n.Else.Statements {
					walkS(st)
				}
			}
		case *syntax.ForStmt:
			walkE(n.Cond)
			walkE(n.Iter)
			if n.Range != nil {
				walkE(n.Range.Start)
				walkE(n.Range.End)
			}
			if n.Body != nil {
				for _, st := range n.Body.Statements {
					walkS(st)
				}
			}
		case *syntax.MatchStmt:
			walkE(n.Subject)
			for _, arm := range n.Arms {
				walkE(arm.Guard)
				if arm.Body != nil {
					for _, st := range arm.Body.Statements {
						walkS(st)
					}
				}
			}
		case *syntax.FnDecl:
			if n.Body != nil {
				for _, st := range n.Body.Statements {
					walkS(st)
				}
			}
		case *syntax.ImplDecl:
			for _, fn := range n.Methods {
				if fn.Body != nil {
					for _, st := range fn.Body.Statements {
						walkS(st)
					}
				}
			}
		}
	}
	walkS(stmt)
}

// findCanonicalTypeRef walks prog for a TypeRef.Resolved or Expr.Type()
// whose Name matches and Kind is the requested kind. Returns the first
// canonical *Type pointer hit. Used to recover the canonical pointer
// typeck stamped on the program for a struct or enum decl.
func findCanonicalTypeRef(prog *syntax.Program, name string, kind syntax.TypeKind) *syntax.Type {
	var found *syntax.Type
	walk := func(t *syntax.Type) {
		if found != nil || t == nil {
			return
		}
		if t.Kind == kind && t.Name == name {
			found = t
		}
	}
	walkProgramTypes(prog, walk)
	return found
}

// stampTypeOwners walks prog's typed expressions and TypeRefs for any
// struct/enum *Type whose owner has not yet been recorded. The first
// occurrence wins. Used to backfill types whose decl walk did not surface
// a canonical pointer (rare — defensive).
func stampTypeOwners(prog *syntax.Program, mangle string, owner map[*syntax.Type]string) {
	walkProgramTypes(prog, func(t *syntax.Type) {
		if t == nil {
			return
		}
		if t.Kind != syntax.TypeStruct && t.Kind != syntax.TypeEnum {
			return
		}
		if _, ok := owner[t]; !ok {
			owner[t] = mangle
		}
	})
}

// walkProgramTypes invokes visit on every *Type pointer reachable from
// prog's TypeRefs and Expr.Type() stamps. Composite types recurse into
// their components (list element, tuple positions, struct fields,
// enum payloads). Used by findCanonicalTypeRef and stampTypeOwners.
func walkProgramTypes(prog *syntax.Program, visit func(*syntax.Type)) {
	if prog == nil {
		return
	}
	for _, stmt := range prog.Statements {
		walkStmtTypes(stmt, visit)
	}
}

func walkStmtTypes(stmt syntax.Stmt, visit func(*syntax.Type)) {
	switch s := stmt.(type) {
	case *syntax.StructDecl:
		for _, f := range s.Fields {
			walkTypeRefTypes(f.Type, visit)
		}
	case *syntax.EnumDecl:
		for _, v := range s.Variants {
			for _, p := range v.Payload {
				walkTypeRefTypes(p, visit)
			}
		}
	case *syntax.FnDecl:
		for _, p := range s.Params {
			walkTypeRefTypes(p.Type, visit)
		}
		walkTypeRefTypes(s.Return, visit)
		walkBlockTypes(s.Body, visit)
	case *syntax.LetStmt:
		walkTypeRefTypes(s.Type, visit)
		walkExprTypes(s.Value, visit)
	case *syntax.MutStmt:
		walkTypeRefTypes(s.Type, visit)
		walkExprTypes(s.Value, visit)
	case *syntax.ConstStmt:
		walkTypeRefTypes(s.Type, visit)
		walkExprTypes(s.Value, visit)
	case *syntax.AssignStmt:
		walkExprTypes(s.Target, visit)
		walkExprTypes(s.Value, visit)
	case *syntax.ExprStmt:
		walkExprTypes(s.Expr, visit)
	case *syntax.PrintStmt:
		walkExprTypes(s.Expr, visit)
	case *syntax.ReturnStmt:
		walkExprTypes(s.Value, visit)
		walkExprTypes(s.Guard, visit)
	case *syntax.BreakStmt:
		walkExprTypes(s.Guard, visit)
	case *syntax.ContinueStmt:
		walkExprTypes(s.Guard, visit)
	case *syntax.IfStmt:
		walkExprTypes(s.Cond, visit)
		walkBlockTypes(s.Then, visit)
		for _, ec := range s.Elifs {
			walkExprTypes(ec.Cond, visit)
			walkBlockTypes(ec.Body, visit)
		}
		walkBlockTypes(s.Else, visit)
	case *syntax.ForStmt:
		walkExprTypes(s.Cond, visit)
		walkExprTypes(s.Iter, visit)
		if s.Range != nil {
			walkExprTypes(s.Range.Start, visit)
			walkExprTypes(s.Range.End, visit)
		}
		walkBlockTypes(s.Body, visit)
	case *syntax.MatchStmt:
		walkExprTypes(s.Subject, visit)
		for _, arm := range s.Arms {
			walkExprTypes(arm.Guard, visit)
			walkBlockTypes(arm.Body, visit)
		}
	case *syntax.SpecDecl:
		for _, m := range s.Methods {
			for _, p := range m.Params {
				walkTypeRefTypes(p.Type, visit)
			}
			walkTypeRefTypes(m.Return, visit)
			walkBlockTypes(m.Body, visit)
		}
	case *syntax.ImplDecl:
		for _, fn := range s.Methods {
			for _, p := range fn.Params {
				walkTypeRefTypes(p.Type, visit)
			}
			walkTypeRefTypes(fn.Return, visit)
			walkBlockTypes(fn.Body, visit)
		}
	}
}

func walkBlockTypes(b *syntax.Block, visit func(*syntax.Type)) {
	if b == nil {
		return
	}
	for _, s := range b.Statements {
		walkStmtTypes(s, visit)
	}
}

func walkTypeRefTypes(ref *syntax.TypeRef, visit func(*syntax.Type)) {
	if ref == nil {
		return
	}
	visit(ref.Resolved)
	walkTypeRefTypes(ref.Element, visit)
	for _, e := range ref.Elements {
		walkTypeRefTypes(e, visit)
	}
}

func walkExprTypes(e syntax.Expr, visit func(*syntax.Type)) {
	if e == nil {
		return
	}
	visit(e.Type())
	switch x := e.(type) {
	case *syntax.UnaryExpr:
		walkExprTypes(x.Operand, visit)
	case *syntax.BinaryExpr:
		walkExprTypes(x.Left, visit)
		walkExprTypes(x.Right, visit)
	case *syntax.ParenExpr:
		walkExprTypes(x.Inner, visit)
	case *syntax.CallExpr:
		walkExprTypes(x.Callee, visit)
		for _, a := range x.Args {
			walkExprTypes(a, visit)
		}
	case *syntax.ListLit:
		for _, sub := range x.Elements {
			walkExprTypes(sub, visit)
		}
	case *syntax.TupleLit:
		for _, sub := range x.Elements {
			walkExprTypes(sub, visit)
		}
	case *syntax.StructLit:
		for _, f := range x.Fields {
			walkExprTypes(f.Value, visit)
		}
	case *syntax.IndexExpr:
		walkExprTypes(x.Receiver, visit)
		walkExprTypes(x.Index, visit)
	case *syntax.SliceExpr:
		walkExprTypes(x.Receiver, visit)
		walkExprTypes(x.Low, visit)
		walkExprTypes(x.High, visit)
	case *syntax.FieldAccessExpr:
		walkExprTypes(x.Receiver, visit)
		if x.Lowered != nil {
			walkExprTypes(x.Lowered, visit)
		}
	case *syntax.MethodCallExpr:
		walkExprTypes(x.Receiver, visit)
		for _, a := range x.Args {
			walkExprTypes(a, visit)
		}
		if x.Lowered != nil {
			walkExprTypes(x.Lowered, visit)
		}
		if x.LoweredCall != nil {
			walkExprTypes(x.LoweredCall, visit)
		}
	case *syntax.EnumLit:
		for _, p := range x.Payload {
			walkExprTypes(p, visit)
		}
	case *syntax.NilLit:
		// no children
	case *syntax.PropagateExpr:
		walkExprTypes(x.Inner, visit)
	case *syntax.CoalesceExpr:
		walkExprTypes(x.Left, visit)
		walkExprTypes(x.Right, visit)
	}
}

// hasFn reports whether prog declares any top-level function.
func hasFn(prog *syntax.Program) bool {
	for _, s := range prog.Statements {
		if _, ok := s.(*syntax.FnDecl); ok {
			return true
		}
	}
	return false
}

// cgen is the per-Emit codegen state.
type cgen struct {
	b      strings.Builder
	indent int
	shapes *shapeRegistry

	// matchCounter generates unique labels per match statement. Each match
	// lowers to a labeled block; arms use `goto matchend_<n>` on success.
	matchCounter int

	// tmpCounter is for fresh local variable names inside generated blocks
	// (slice receivers, match scrutinees, etc.).
	tmpCounter int

	// fnTable indexes top-level FnDecl by name so callStr can coerce args
	// to declared param types (spec coercion at the call site). v0.5: this
	// holds the entry module's fns. Cross-module fn calls route through
	// MethodCallExpr's IdentExpr-receiver shape and use moduleByName /
	// imports for owner resolution at the call site.
	fnTable map[string]*syntax.FnDecl

	// currentFnRet is the resolved return type of the FnDecl whose body is
	// being emitted, used to coerce return-value expressions when the
	// declared return is spec-typed. nil at top level / inside a method body.
	currentFnRet *syntax.Type

	// currentMod is the mangle prefix of the module whose body is being
	// emitted. Cross-module fn calls inside a body resolve their import
	// table via this module's record.
	currentMod string

	// v0.4 spec / impl bookkeeping. Populated in collectShapes from top-level
	// SpecDecl / ImplDecl statements; used by the vtable / method emitters.
	specs             map[string]*syntax.SpecDecl  // spec name → AST
	specOrder         []string                     // declaration order
	inherent          map[string][]*syntax.FnDecl  // type name → inherent methods
	inherentTypeOrder []string                     // declaration order
	specImpls         map[implKey]*syntax.ImplDecl // (type, spec) → AST
	specImplKeys      []implKey                    // declaration order
	receiverTypes     map[string]*syntax.Type      // type name → resolved type (for impl receivers)

	// Spec types referenced anywhere in the program (binding : Spec, list[Spec],
	// fn arg/return of Spec, etc.). Order is declaration order; emitForwardDecls
	// uses it to emit the fat-pointer typedef + vtable struct definitions.
	specsUsed map[string]bool

	// v0.5 Unit 6 — bundle / module tracking.
	// modules holds every module in the bundle, in discovery order. Iteration
	// is stable because we walk this slice (not a map).
	modules []moduleEmit
	// entryProg / entryMangle identify the entry module — its top-level
	// statements run in `int main()`, others contribute decls only.
	entryProg   *syntax.Program
	entryMangle string
	// typeOwner maps every canonical struct/enum *Type pointer to the mangle
	// of its owning module. mangleType uses this to prefix composite-type
	// names. Spec types route through specOwner instead.
	typeOwner map[*syntax.Type]string
	// specOwner maps a spec name to the mangle of its owning module. Used by
	// methodMangle, vtable struct emission, and fat-pointer typedef
	// emission to route a spec's symbols through its owning module.
	specOwner map[string]string
	// fnOwner maps every FnDecl to its owning module's mangle. The entry
	// module's fns retain their bare-name C identifier (z_<name>) so v0.4-
	// style top-level `print foo(...)` continues to compile; sibling
	// modules' fns gain a module-mangle prefix to defeat name collisions.
	fnOwner map[*syntax.FnDecl]string
	// moduleByName indexes modules by their mangle prefix for O(1) lookup
	// during cross-module fn dispatch.
	moduleByName map[string]*moduleEmit

	// v0.7 Unit 7 — concurrency codegen state.
	// chanShapes records every chan element type the program references.
	// Two `chan[int]` uses dedupe to one entry. emitChanTypedefs walks the
	// map to emit per-element struct + helpers ahead of user fns.
	chanShapes map[string]*chanShape
	chanOrder  []string
	// chanOptionLookup is a closure that returns the canonical Option[T]
	// *Type for a given element type T. Wired during EmitBundle from the
	// per-program built-in Option monomorphisation cache so the chan
	// recv helper emits the right Option[T] enum.
	chanOptionLookup func(elem *syntax.Type) *syntax.Type
	// chanOptionByElemKey is the harvested Option[T] map populated at
	// EmitBundle entry by harvestChanOptionTypes. Key is element-type
	// String(); value is the canonical Option[T] *Type stamped on a
	// RecvExpr or for-chan iter site.
	chanOptionByElemKey map[string]*syntax.Type
	// anonFns holds every spawn / defer body queued for top-level emission,
	// in declaration order. Pre-registered by preregisterAnonFns so the
	// emitted .c file can forward-declare each trampoline ahead of any user
	// fn body that calls it.
	anonFns       []*anonFnEmit
	anonFnCounter int
	// anonByNode maps an AST node (AnonFnExpr / DeferStmt / SpawnStmt) to
	// its pre-registered anonFnEmit. emitSpawn / emitDefer look up the
	// record rather than allocating a new id, so the AST's order matches
	// the forward declarations.
	anonByNode map[interface{}]*anonFnEmit
	// currentHasDefers / currentFnEndLabel drive the defer-drain epilogue
	// and the `?` early-return goto label inside the emitting fn body.
	currentHasDefers  bool
	currentFnEndLabel string
	// inDeferDrain reserved for nested-defer scoping support; today it
	// stays false and is restored across nested anon-fn emission.
	inDeferDrain bool
	// fnEndCounter generates unique fn-end label suffixes for each fn whose
	// HasDefers is set. The label is the jump target for `?` propagation
	// inside that fn so the defer-drain epilogue fires.
	fnEndCounter int
}

// implKey deduplicates impls by (type, spec). Mirrors run.go's implKey.
type implKey struct {
	typeName string
	specName string
}

// freshTmp returns a unique C identifier safe to introduce as a local.
func (g *cgen) freshTmp(prefix string) string {
	g.tmpCounter++
	return fmt.Sprintf("__zg_%s_%d", prefix, g.tmpCounter)
}

// writeIndent writes the current indent prefix (4 spaces per level).
func (g *cgen) writeIndent() {
	for i := 0; i < g.indent; i++ {
		g.b.WriteString("    ")
	}
}

// ---------------------------------------------------------------------------
// Shape registry — collects every composite type that needs a C typedef and
// per-shape helpers (copy / print / slice / index-check).
// ---------------------------------------------------------------------------

type shapeRegistry struct {
	// listShapes is the set of every concrete list element type seen, keyed
	// by the canonical mangled name of the LIST type itself.
	listShapes   map[string]*syntax.Type
	tupleShapes  map[string]*syntax.Type
	structShapes map[string]*syntax.Type
	enumShapes   map[string]*syntax.Type

	// listOrder, tupleOrder, etc. preserve insertion order so the emitted
	// .c file is deterministic. (A map walk is not deterministic in Go.)
	listOrder   []string
	tupleOrder  []string
	structOrder []string
	enumOrder   []string
}

func newShapeRegistry() *shapeRegistry {
	return &shapeRegistry{
		listShapes:   map[string]*syntax.Type{},
		tupleShapes:  map[string]*syntax.Type{},
		structShapes: map[string]*syntax.Type{},
		enumShapes:   map[string]*syntax.Type{},
	}
}

// addType registers t and all its sub-shapes recursively. Primitives are
// skipped — they map to canonical C primitives and need no per-shape helpers.
//
// v0.5: takes the *cgen so the per-type module-mangle resolves correctly.
// Two modules' identically-named structs must produce distinct shape keys
// so each gets its own typedef and helper functions.
func (r *shapeRegistry) addType(g *cgen, t *syntax.Type) {
	if t == nil {
		return
	}
	switch t.Kind {
	case syntax.TypeList:
		r.addType(g, t.Element)
		key := g.mangleType(t)
		if _, ok := r.listShapes[key]; !ok {
			r.listShapes[key] = t
			r.listOrder = append(r.listOrder, key)
		}
	case syntax.TypeTuple:
		for _, e := range t.Tuple {
			r.addType(g, e)
		}
		key := g.mangleType(t)
		if _, ok := r.tupleShapes[key]; !ok {
			r.tupleShapes[key] = t
			r.tupleOrder = append(r.tupleOrder, key)
		}
	case syntax.TypeStruct:
		// v0.7: the WaitGroup synthetic struct is a runtime-owned handle —
		// no per-shape typedef / helpers. Skip the registry path; the cTypeName
		// mapping above renders it as `zerg_wait_group_t *`.
		if t.Name == "WaitGroup" {
			return
		}
		// Add the struct itself; recurse into field types so nested composites
		// are picked up even when they are only used inside a struct.
		key := g.mangleType(t)
		if _, ok := r.structShapes[key]; !ok {
			r.structShapes[key] = t
			r.structOrder = append(r.structOrder, key)
			for _, f := range t.Fields {
				r.addType(g, f.Type)
			}
		}
	case syntax.TypeEnum:
		key := g.mangleType(t)
		if _, ok := r.enumShapes[key]; !ok {
			r.enumShapes[key] = t
			r.enumOrder = append(r.enumOrder, key)
			for _, payload := range t.VariantPayloads {
				for _, pt := range payload {
					r.addType(g, pt)
				}
			}
		}
	case syntax.TypeSpec:
		// TypeSpec is registered separately on the cgen so the spec
		// fat-pointer typedef + vtable struct emit at the v0.4 stage.
		// Recording it here is a no-op — the shape registry handles only
		// the existing shape kinds.
	}
}

// emitForwardDecls writes a `typedef struct ...;` for every composite shape
// so helpers can refer to other shapes without ordering constraints. (List
// of struct, struct containing list[Foo], etc.)
func (r *shapeRegistry) emitForwardDecls(g *cgen, b *strings.Builder) {
	_ = g
	// Sort struct/enum order by name for stability — declaration order from
	// the source already gives a stable order, but a canonical sort makes
	// the output independent of source-side reorderings.
	sort.Strings(r.structOrder)
	sort.Strings(r.enumOrder)
	sort.Strings(r.listOrder)
	sort.Strings(r.tupleOrder)

	if len(r.structOrder) > 0 || len(r.enumOrder) > 0 ||
		len(r.listOrder) > 0 || len(r.tupleOrder) > 0 {
		b.WriteString("/* Composite shape forward declarations. */\n")
	}
	for _, k := range r.structOrder {
		fmt.Fprintf(b, "typedef struct %s %s;\n", k, k)
	}
	for _, k := range r.tupleOrder {
		fmt.Fprintf(b, "typedef struct %s %s;\n", k, k)
	}
	for _, k := range r.listOrder {
		fmt.Fprintf(b, "typedef struct %s %s;\n", k, k)
	}
	for _, k := range r.enumOrder {
		// v0.4: enums are tag+union structs even when no variant carries a
		// payload — keeps the surface uniform between bare-only enums and
		// payload-carrying enums and means run-time `==` walks the same
		// shape regardless. Forward-declare the struct here; the typedef
		// body comes in emitTypedefs.
		fmt.Fprintf(b, "typedef struct %s %s;\n", k, k)
	}
	if len(r.structOrder) > 0 || len(r.enumOrder) > 0 ||
		len(r.listOrder) > 0 || len(r.tupleOrder) > 0 {
		b.WriteString("\n")
	}
}

// emitTypedefs writes the actual struct definitions for list / tuple / struct
// types, plus the variant-index macros for each enum.
//
// Order matters: a complete C struct definition needs the COMPLETE type of
// each composite field (a forward declaration is enough only behind a
// pointer). Strategy:
//
//   - List shape definitions first — every list field is a pointer-to-element,
//     so element types only need their forward decl (already emitted in
//     emitForwardDecls). Lists therefore have no shape-def dependency on
//     other shapes and can be emitted en bloc.
//   - Tuple, struct and enum shape definitions in a unified topological sort.
//     A tuple-of-struct needs the struct's full definition; a struct-of-
//     tuple needs the tuple's full definition; an enum variant payload
//     embeds its payload types by value, so a `Frame { Args(list[int]) }`
//     enum needs `zerg_list_int64_t`'s full definition (not just its forward
//     decl) before its own struct body can be emitted. Handling tuples,
//     structs and enums as one dependency graph respects whichever direction
//     the user wrote.
//
// typeck has rejected composite cycles so the fixed-point loop terminates.
func (r *shapeRegistry) emitTypedefs(g *cgen, b *strings.Builder) {
	if len(r.listOrder) > 0 {
		b.WriteString("/* List shape definitions. */\n")
	}
	for _, k := range r.listOrder {
		t := r.listShapes[k]
		elem := g.cTypeName(t.Element)
		// `cap` was dropped in v0.2 because lists were value-copied with
		// cap == len at every site. With `push` in play at v0.3, cap is
		// needed so the per-shape grow helper knows when to realloc.
		fmt.Fprintf(b, "struct %s { %s* data; size_t len; size_t cap; };\n", k, elem)
	}

	// Unified topo over tuple, struct and enum shapes. depsOf returns the
	// mangled names of OTHER composite shapes whose full definition is
	// needed before this one can be emitted. List deps resolve immediately
	// because lists are already fully defined above.
	depsOf := func(t *syntax.Type) []string {
		var out []string
		var fields []*syntax.Type
		switch t.Kind {
		case syntax.TypeTuple:
			fields = append(fields, t.Tuple...)
		case syntax.TypeStruct:
			for _, f := range t.Fields {
				fields = append(fields, f.Type)
			}
		case syntax.TypeEnum:
			for i := range t.Variants {
				fields = append(fields, variantPayload(t, i)...)
			}
		}
		for _, ft := range fields {
			if ft == nil {
				continue
			}
			switch ft.Kind {
			case syntax.TypeStruct, syntax.TypeTuple, syntax.TypeEnum, syntax.TypeList:
				out = append(out, g.mangleType(ft))
			}
		}
		return out
	}

	emittedTuple := map[string]bool{}
	emittedStruct := map[string]bool{}
	emittedEnum := map[string]bool{}
	depReady := func(deps []string) bool {
		for _, dep := range deps {
			if _, ok := r.tupleShapes[dep]; ok && !emittedTuple[dep] {
				return false
			}
			if _, ok := r.structShapes[dep]; ok && !emittedStruct[dep] {
				return false
			}
			if _, ok := r.enumShapes[dep]; ok && !emittedEnum[dep] {
				return false
			}
			// Lists are emitted en bloc above and are always ready.
		}
		return true
	}

	wroteTupleHeader := false
	wroteStructHeader := false
	wroteEnumHeader := false
	emitTuple := func(k string) {
		if !wroteTupleHeader {
			b.WriteString("\n/* Tuple shape definitions. */\n")
			wroteTupleHeader = true
		}
		t := r.tupleShapes[k]
		fmt.Fprintf(b, "struct %s {", k)
		for i, e := range t.Tuple {
			if i > 0 {
				b.WriteString(";")
			}
			fmt.Fprintf(b, " %s e%d", g.cTypeName(e), i)
		}
		b.WriteString("; };\n")
		emittedTuple[k] = true
	}
	emitStruct := func(k string) {
		if !wroteStructHeader {
			b.WriteString("\n/* Struct shape definitions. */\n")
			wroteStructHeader = true
		}
		t := r.structShapes[k]
		fmt.Fprintf(b, "struct %s {", k)
		for i, f := range t.Fields {
			if i > 0 {
				b.WriteString(";")
			}
			fmt.Fprintf(b, " %s %s", g.cTypeName(f.Type), mangleField(f.Name))
		}
		b.WriteString("; };\n")
		emittedStruct[k] = true
	}
	// v0.4 enum layout: `struct { int32_t tag; union { ... } payload; }`.
	// Each variant gets a payload sub-struct named pN where N is the variant
	// index; bare variants use a placeholder slot so the union is never empty.
	// Variant index macros are emitted alongside as `<Mangle>__<Variant>_TAG`
	// for use in match scrutinee tag tests.
	emitEnum := func(k string) {
		if !wroteEnumHeader {
			b.WriteString("\n/* Enum tag+union shape definitions. */\n")
			wroteEnumHeader = true
		}
		t := r.enumShapes[k]
		fmt.Fprintf(b, "struct %s {\n", k)
		fmt.Fprintf(b, "    int32_t tag;\n")
		fmt.Fprintf(b, "    union {\n")
		for i, v := range t.Variants {
			fmt.Fprintf(b, "        struct {")
			payload := variantPayload(t, i)
			if len(payload) == 0 {
				fmt.Fprintf(b, " char _empty;")
			} else {
				for j, pt := range payload {
					fmt.Fprintf(b, " %s a%d;", g.cTypeName(pt), j)
				}
			}
			fmt.Fprintf(b, " } p%d; /* %s */\n", i, v)
		}
		fmt.Fprintf(b, "    } payload;\n")
		fmt.Fprintf(b, "};\n")
		for i, v := range t.Variants {
			fmt.Fprintf(b, "#define %s__%s_TAG (%d)\n", k, v, i)
		}
		emittedEnum[k] = true
	}

	totalRemaining := len(r.tupleOrder) + len(r.structOrder) + len(r.enumOrder)
	for totalRemaining > 0 {
		progress := false
		for _, k := range r.tupleOrder {
			if emittedTuple[k] {
				continue
			}
			if !depReady(depsOf(r.tupleShapes[k])) {
				continue
			}
			emitTuple(k)
			progress = true
			totalRemaining--
		}
		for _, k := range r.structOrder {
			if emittedStruct[k] {
				continue
			}
			if !depReady(depsOf(r.structShapes[k])) {
				continue
			}
			emitStruct(k)
			progress = true
			totalRemaining--
		}
		for _, k := range r.enumOrder {
			if emittedEnum[k] {
				continue
			}
			if !depReady(depsOf(r.enumShapes[k])) {
				continue
			}
			emitEnum(k)
			progress = true
			totalRemaining--
		}
		if !progress {
			// Should not happen post-typeck cycle check; emit remaining
			// regardless rather than spin forever. The C compiler will
			// surface the underlying issue if any.
			for _, k := range r.tupleOrder {
				if !emittedTuple[k] {
					emitTuple(k)
				}
			}
			for _, k := range r.structOrder {
				if !emittedStruct[k] {
					emitStruct(k)
				}
			}
			for _, k := range r.enumOrder {
				if !emittedEnum[k] {
					emitEnum(k)
				}
			}
			break
		}
	}

	if len(r.listOrder)+len(r.tupleOrder)+len(r.structOrder)+len(r.enumOrder) > 0 {
		b.WriteString("\n")
	}
}

// emitHelpers writes per-shape copy / print / slice helpers. Every shape gets
// a copy helper even when the shape contains no lists (the helper is then a
// trivial pass-through that the C optimiser inlines), so call sites can be
// uniform.
func (r *shapeRegistry) emitHelpers(g *cgen, b *strings.Builder) {
	// Forward-declare all copy + print helpers first so they can reference
	// each other in any order (a list of struct copy calls the struct copy
	// which itself may call a list copy for an inner field).
	for _, k := range r.listOrder {
		t := r.listShapes[k]
		elem := g.cTypeName(t.Element)
		fmt.Fprintf(b, "static %s %s_copy(%s xs);\n", k, k, k)
		fmt.Fprintf(b, "static %s %s_slice(%s xs, int64_t lo, int64_t hi, const char* pos);\n", k, k, k)
		fmt.Fprintf(b, "static void %s_push(%s* xs, %s v);\n", k, k, elem)
		fmt.Fprintf(b, "static void zerg_print_%s(%s xs);\n", k, k)
	}
	for _, k := range r.tupleOrder {
		fmt.Fprintf(b, "static %s %s_copy(%s t);\n", k, k, k)
		fmt.Fprintf(b, "static void zerg_print_%s(%s t);\n", k, k)
	}
	for _, k := range r.structOrder {
		fmt.Fprintf(b, "static %s %s_copy(%s s);\n", k, k, k)
		fmt.Fprintf(b, "static void zerg_print_%s(%s s);\n", k, k)
	}
	for _, k := range r.enumOrder {
		fmt.Fprintf(b, "static %s %s_copy(%s e);\n", k, k, k)
		fmt.Fprintf(b, "static void zerg_print_%s(%s e);\n", k, k)
	}
	if len(r.listOrder)+len(r.tupleOrder)+len(r.structOrder)+len(r.enumOrder) > 0 {
		b.WriteString("\n")
	}

	// list helpers
	for _, k := range r.listOrder {
		t := r.listShapes[k]
		emitListHelpers(g, b, k, t)
		b.WriteString("\n")
	}
	// tuple helpers
	for _, k := range r.tupleOrder {
		t := r.tupleShapes[k]
		emitTupleHelpers(g, b, k, t)
		b.WriteString("\n")
	}
	// struct helpers
	for _, k := range r.structOrder {
		t := r.structShapes[k]
		emitStructHelpers(g, b, k, t)
		b.WriteString("\n")
	}
	// enum helpers (copy + print).
	for _, k := range r.enumOrder {
		t := r.enumShapes[k]
		emitEnumCopy(g, b, k, t)
		emitEnumPrint(g, b, k, t)
		b.WriteString("\n")
	}
}

// emitListHelpers writes copy, slice, push, and print for a list[T] shape.
func emitListHelpers(g *cgen, b *strings.Builder, mname string, t *syntax.Type) {
	elem := g.cTypeName(t.Element)
	// copy: malloc a fresh buffer (cap == len so subsequent pushes start
	// from a tight buffer), deep-copy each element via copyExpr.
	fmt.Fprintf(b, "static %s %s_copy(%s xs) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    %s out;\n", mname)
	fmt.Fprintf(b, "    out.len = xs.len;\n")
	fmt.Fprintf(b, "    out.cap = xs.len;\n")
	fmt.Fprintf(b, "    out.data = (%s*)malloc(out.len ? out.len * sizeof(%s) : 1);\n", elem, elem)
	fmt.Fprintf(b, "    for (size_t i = 0; i < out.len; i++) { out.data[i] = %s; }\n",
		g.copyExpr(t.Element, "xs.data[i]"))
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")

	// slice: bounds-check, malloc fresh buffer, deep-copy elements. The
	// resulting list owns its buffer with cap == len.
	fmt.Fprintf(b, "static %s %s_slice(%s xs, int64_t lo, int64_t hi, const char* pos) {\n",
		mname, mname, mname)
	fmt.Fprintf(b, "    if (lo < 0 || hi < lo || (size_t)hi > xs.len) {\n")
	fmt.Fprintf(b, "        fprintf(stderr, \"zerg: %%s: slice [%%lld..%%lld] out of range [0..%%zu]\\n\", pos, (long long)lo, (long long)hi, xs.len);\n")
	fmt.Fprintf(b, "        exit(1);\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    %s out;\n", mname)
	fmt.Fprintf(b, "    out.len = (size_t)(hi - lo);\n")
	fmt.Fprintf(b, "    out.cap = out.len;\n")
	fmt.Fprintf(b, "    out.data = (%s*)malloc(out.len ? out.len * sizeof(%s) : 1);\n", elem, elem)
	fmt.Fprintf(b, "    for (size_t i = 0; i < out.len; i++) { out.data[i] = %s; }\n",
		g.copyExpr(t.Element, "xs.data[lo + i]"))
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")

	// push: amortised-O(1) growth. Doubles cap when len catches up; first
	// growth from cap == 0 jumps to 4 to avoid the 0 → 1 → 2 → 4 ramp on
	// freshly-constructed empty lists. Takes a pointer so the caller's
	// (data, len, cap) header is updated in place.
	fmt.Fprintf(b, "static void %s_push(%s* xs, %s v) {\n", mname, mname, elem)
	fmt.Fprintf(b, "    if (xs->len == xs->cap) {\n")
	fmt.Fprintf(b, "        size_t newcap = xs->cap == 0 ? 4 : xs->cap * 2;\n")
	fmt.Fprintf(b, "        xs->data = (%s*)realloc(xs->data, newcap * sizeof(%s));\n", elem, elem)
	fmt.Fprintf(b, "        xs->cap = newcap;\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    xs->data[xs->len++] = v;\n")
	fmt.Fprintf(b, "}\n")

	// print: "[ e1, e2 ]" with space-comma-space; "[]" when empty.
	fmt.Fprintf(b, "static void zerg_print_%s(%s xs) {\n", mname, mname)
	fmt.Fprintf(b, "    if (xs.len == 0) { fputs(\"[]\", stdout); return; }\n")
	fmt.Fprintf(b, "    fputs(\"[ \", stdout);\n")
	fmt.Fprintf(b, "    for (size_t i = 0; i < xs.len; i++) {\n")
	fmt.Fprintf(b, "        if (i > 0) fputs(\", \", stdout);\n")
	fmt.Fprintf(b, "        %s;\n", g.printExpr(t.Element, "xs.data[i]"))
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    fputs(\" ]\", stdout);\n")
	fmt.Fprintf(b, "}\n")
}

// emitTupleHelpers writes copy and print for a tuple shape.
func emitTupleHelpers(g *cgen, b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static %s %s_copy(%s t) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    %s out;\n", mname)
	for i, e := range t.Tuple {
		fmt.Fprintf(b, "    out.e%d = %s;\n", i, g.copyExpr(e, fmt.Sprintf("t.e%d", i)))
	}
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")

	fmt.Fprintf(b, "static void zerg_print_%s(%s t) {\n", mname, mname)
	fmt.Fprintf(b, "    fputs(\"( \", stdout);\n")
	for i, e := range t.Tuple {
		if i > 0 {
			fmt.Fprintf(b, "    fputs(\", \", stdout);\n")
		}
		fmt.Fprintf(b, "    %s;\n", g.printExpr(e, fmt.Sprintf("t.e%d", i)))
	}
	fmt.Fprintf(b, "    fputs(\" )\", stdout);\n")
	fmt.Fprintf(b, "}\n")
}

// emitStructHelpers writes copy and print for a struct shape.
func emitStructHelpers(g *cgen, b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static %s %s_copy(%s s) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    %s out;\n", mname)
	for _, f := range t.Fields {
		fmt.Fprintf(b, "    out.%s = %s;\n",
			mangleField(f.Name),
			g.copyExpr(f.Type, "s."+mangleField(f.Name)))
	}
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")

	// v0.6 print parity: monomorphised generic struct Names carry the
	// bracketed type-arg suffix; the print path emits the bare base.
	displayName := printStructDisplayName(t)
	fmt.Fprintf(b, "static void zerg_print_%s(%s s) {\n", mname, mname)
	fmt.Fprintf(b, "    fputs(%q, stdout);\n", displayName+" { ")
	for i, f := range t.Fields {
		if i > 0 {
			fmt.Fprintf(b, "    fputs(\", \", stdout);\n")
		}
		fmt.Fprintf(b, "    fputs(%q, stdout);\n", f.Name+": ")
		fmt.Fprintf(b, "    %s;\n", g.printExpr(f.Type, "s."+mangleField(f.Name)))
	}
	fmt.Fprintf(b, "    fputs(\" }\", stdout);\n")
	fmt.Fprintf(b, "}\n")
}

// emitEnumPrint writes print-helper for an enum: switch on the tag and emit
// either "Name.VariantName" (bare) or "Name.VariantName(payload, ...)" (with
// per-position recursive print of each payload value).
//
// v0.6: monomorphised generic enums carry a Name like `Box[int]` for
// diagnostic prose; the print path uses the bare base name (`Box`) per
// PLAN.md §Print parity. printEnumDisplayName extracts that base.
func emitEnumPrint(g *cgen, b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static void zerg_print_%s(%s e) {\n", mname, mname)
	fmt.Fprintf(b, "    switch (e.tag) {\n")
	displayName := printEnumDisplayName(t)
	for i, v := range t.Variants {
		payload := variantPayload(t, i)
		if len(payload) == 0 {
			fmt.Fprintf(b, "    case %d: fputs(%q, stdout); break;\n", i, displayName+"."+v)
		} else {
			fmt.Fprintf(b, "    case %d: {\n", i)
			fmt.Fprintf(b, "        fputs(%q, stdout);\n", displayName+"."+v+"(")
			for j, pt := range payload {
				if j > 0 {
					fmt.Fprintf(b, "        fputs(\", \", stdout);\n")
				}
				fmt.Fprintf(b, "        %s;\n", g.printExpr(pt, fmt.Sprintf("e.payload.p%d.a%d", i, j)))
			}
			fmt.Fprintf(b, "        fputs(\")\", stdout);\n")
			fmt.Fprintf(b, "        break;\n")
			fmt.Fprintf(b, "    }\n")
		}
	}
	fmt.Fprintf(b, "    default: fputs(\"<bad enum>\", stdout); break;\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "}\n")
}

// emitEnumCopy writes a deep-copy helper for an enum value. Copies primitive
// payloads by value and recurses through composite payloads via the per-shape
// _copy helpers. Bare variants copy the (single-byte) placeholder.
func emitEnumCopy(g *cgen, b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static %s %s_copy(%s e) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    %s out;\n", mname)
	fmt.Fprintf(b, "    out.tag = e.tag;\n")
	fmt.Fprintf(b, "    switch (e.tag) {\n")
	for i := range t.Variants {
		payload := variantPayload(t, i)
		fmt.Fprintf(b, "    case %d:\n", i)
		if len(payload) == 0 {
			fmt.Fprintf(b, "        out.payload.p%d._empty = 0;\n", i)
		} else {
			for j, pt := range payload {
				fmt.Fprintf(b, "        out.payload.p%d.a%d = %s;\n",
					i, j, g.copyExpr(pt, fmt.Sprintf("e.payload.p%d.a%d", i, j)))
			}
		}
		fmt.Fprintf(b, "        break;\n")
	}
	fmt.Fprintf(b, "    default: break;\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")
}

// printEnumDisplayName returns the user-visible base name for an enum, used
// in the print helper. For non-generic enums the Name is already the bare
// form; for monomorphised generic enums the Name carries the bracket suffix
// (e.g. `Option[int]`) which the print path must drop per PLAN.md §Print
// parity. Diagnostic paths (Type.String) keep the suffix for disambiguation.
func printEnumDisplayName(t *syntax.Type) string {
	return stripGenericArgs(t)
}

// printStructDisplayName mirrors printEnumDisplayName for struct shapes.
// `Box[int]` prints as `Box { ... }` rather than `Box[int] { ... }`.
func printStructDisplayName(t *syntax.Type) string {
	return stripGenericArgs(t)
}

func stripGenericArgs(t *syntax.Type) string {
	if t == nil {
		return ""
	}
	for i, r := range t.Name {
		if r == '[' {
			return t.Name[:i]
		}
	}
	return t.Name
}

// variantPayload returns the per-position payload type slice for the i-th
// variant of t. Returns nil when the variant is bare or VariantPayloads is
// nil for that index (consistent with typeck's representation).
func variantPayload(t *syntax.Type, i int) []*syntax.Type {
	if t == nil || t.Kind != syntax.TypeEnum {
		return nil
	}
	if i < 0 || i >= len(t.VariantPayloads) {
		return nil
	}
	return t.VariantPayloads[i]
}

// variantIndex returns the index of variant `name` in enum t, or -1 when the
// variant is unknown (which should not happen post-typeck but we guard).
func variantIndex(t *syntax.Type, name string) int {
	if t == nil || t.Kind != syntax.TypeEnum {
		return -1
	}
	for i, v := range t.Variants {
		if v == name {
			return i
		}
	}
	return -1
}

// copyExpr returns a C expression that produces a deep-copy of expr (a C
// expression with type t). For primitives the copy is the expression itself
// (trivial copy via assignment); for composites we delegate to the per-shape
// _copy helper.
//
// v0.7: TypeChan and the synthetic WaitGroup are runtime-owned handles —
// pointer-sized shared state, never deep-copied. Treat them as primitives.
func (g *cgen) copyExpr(t *syntax.Type, expr string) string {
	if t == nil {
		return expr
	}
	if t.Kind == syntax.TypeChan {
		return expr
	}
	if t.Kind == syntax.TypeStruct && t.Name == "WaitGroup" {
		return expr
	}
	switch t.Kind {
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
		return fmt.Sprintf("%s_copy(%s)", g.mangleType(t), expr)
	}
	return expr
}

// printExpr returns a C *statement* (not an expression) that prints expr's
// value using the type-appropriate helper. Used inside list/tuple/struct
// print bodies where a single statement is the right level.
//
// NOTE: for primitives this is the inline `printf("%lld", ...)` form WITHOUT
// trailing newline — list/tuple/struct printing handles the surrounding
// punctuation. For top-level `print stmt` we use a different helper that
// adds the newline.
func (g *cgen) printExpr(t *syntax.Type, expr string) string {
	if t == nil {
		return "(void)0"
	}
	switch t {
	case syntax.TInt():
		return fmt.Sprintf("printf(\"%%lld\", (long long)(%s))", expr)
	case syntax.TFloat():
		// %.17g matches Go's strconv.FormatFloat(x, 'g', 17, 64) — see
		// runtime.go zerg_print_float for the reasoning.
		return fmt.Sprintf("{ char __b[32]; snprintf(__b, sizeof __b, \"%%.17g\", (double)(%s)); fputs(__b, stdout); }", expr)
	case syntax.TBool():
		return fmt.Sprintf("fputs((%s) ? \"true\" : \"false\", stdout)", expr)
	case syntax.TStr():
		return fmt.Sprintf("zerg_str_write(%s)", expr)
	case syntax.TByte():
		return fmt.Sprintf("printf(\"%%hhu\", (uint8_t)(%s))", expr)
	case syntax.TRune():
		return fmt.Sprintf("printf(\"%%d\", (int32_t)(%s))", expr)
	}
	switch t.Kind {
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
		return fmt.Sprintf("zerg_print_%s(%s)", g.mangleType(t), expr)
	}
	return "(void)0"
}

// collectShapes walks the typed AST to register every composite shape with
// the registry. Walks types reachable from variable bindings, fn signatures,
// expression Type()s, struct/enum decls, and pattern types.
func (g *cgen) collectShapes(prog *syntax.Program) error {
	// First, register every top-level struct and enum DECLARED in the
	// program even if it isn't referenced from an expression — the typedef
	// is needed by anything that names the type.
	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *syntax.StructDecl:
			// v0.6: skip generic decls. Their field TypeRefs name type-params
			// (`T`, `E`) which never resolve to a canonical *Type — only the
			// monomorphised instances (discovered via expression walks below)
			// produce concrete shapes the registry can emit.
			if len(s.TypeParams) > 0 {
				continue
			}
			// Resolve the struct type by name via the field type refs.
			// Each struct's first field has a TypeRef.Resolved that points
			// at the field's type — but the struct itself we need to look
			// up via any field reference of struct kind. Simpler: we
			// reconstruct the struct *Type pointer by inspecting any
			// TypeRef that names this struct elsewhere. Easiest path: the
			// struct decl's fields all reference resolved types, but the
			// struct *Type itself is what we care about. We can find one
			// by using the first occurrence of a StructLit / FieldAccess
			// expression — but a never-used struct still needs a typedef.
			//
			// Workaround: attach the struct type to the decl by walking
			// every TypeRef looking for one whose Resolved.Name matches.
			// In practice every struct has at least one TypeRef in its
			// field-list pointing at a primitive or composite, but the
			// struct ITSELF is referred to via TypeRefNamed elsewhere.
			//
			// Cleanest: use the StructDecl name and fields to construct a
			// canonical *Type lookup is via typeck-internal table, but
			// that table is private. We sidestep by reconstructing from
			// the AST: build a *Type using NewStructType on the resolved
			// field types.
			fields := make([]syntax.NamedField, len(s.Fields))
			for i, f := range s.Fields {
				if f.Type == nil || f.Type.Resolved == nil {
					return fmt.Errorf("codegen: unresolved field type for %s.%s at %s", s.Name, f.Name, f.Pos)
				}
				fields[i] = syntax.NamedField{Name: f.Name, Type: f.Type.Resolved}
			}
			st := syntax.NewStructType(s.Name, fields)
			g.shapes.addType(g, st)
		case *syntax.EnumDecl:
			// v0.6: same as StructDecl — generic enum decls have type-param
			// references in payloads; only the monomorphised instances reach
			// the registry through the expression walks.
			if len(s.TypeParams) > 0 {
				continue
			}
			variants := make([]string, len(s.Variants))
			payloads := make([][]*syntax.Type, len(s.Variants))
			for i, v := range s.Variants {
				variants[i] = v.Name
				if len(v.Payload) > 0 {
					p := make([]*syntax.Type, len(v.Payload))
					for j, pr := range v.Payload {
						if pr != nil {
							p[j] = pr.Resolved
						}
					}
					payloads[i] = p
				}
			}
			en := syntax.NewEnumType(s.Name, variants)
			en.VariantPayloads = payloads
			g.shapes.addType(g, en)
		}
	}
	// v0.6: walk every monomorphised FnDecl clone to collect shapes its
	// param / return types reference. The clones don't appear in
	// prog.Statements, so without this walk a struct used only inside a
	// generic-fn body would never reach the registry.
	for _, fn := range prog.MonoFns {
		if fn == nil {
			continue
		}
		for _, p := range fn.Params {
			if p.Type != nil && p.Type.Resolved != nil {
				g.shapes.addType(g, p.Type.Resolved)
			}
		}
		if fn.Return != nil && fn.Return.Resolved != nil {
			g.shapes.addType(g, fn.Return.Resolved)
		}
		g.collectBlock(fn.Body)
	}

	// Now walk all statements to pick up types reached via expressions and
	// type-refs. The walk is permissive — every visited type is added
	// (idempotent on the registry).
	for _, stmt := range prog.Statements {
		g.collectStmt(stmt)
	}
	return nil
}

func (g *cgen) collectStmt(stmt syntax.Stmt) {
	switch s := stmt.(type) {
	case *syntax.PrintStmt:
		g.collectExpr(s.Expr)
	case *syntax.LetStmt:
		if s.Type != nil && s.Type.Resolved != nil {
			g.shapes.addType(g, s.Type.Resolved)
			g.collectSpecsInType(s.Type.Resolved)
		}
		g.collectExpr(s.Value)
	case *syntax.MutStmt:
		if s.Type != nil && s.Type.Resolved != nil {
			g.shapes.addType(g, s.Type.Resolved)
			g.collectSpecsInType(s.Type.Resolved)
		}
		g.collectExpr(s.Value)
	case *syntax.ConstStmt:
		if s.Type != nil && s.Type.Resolved != nil {
			g.shapes.addType(g, s.Type.Resolved)
			g.collectSpecsInType(s.Type.Resolved)
		}
		g.collectExpr(s.Value)
	case *syntax.AssignStmt:
		g.collectExpr(s.Target)
		g.collectExpr(s.Value)
	case *syntax.ExprStmt:
		g.collectExpr(s.Expr)
	case *syntax.IfStmt:
		g.collectExpr(s.Cond)
		g.collectBlock(s.Then)
		for i := range s.Elifs {
			g.collectExpr(s.Elifs[i].Cond)
			g.collectBlock(s.Elifs[i].Body)
		}
		if s.Else != nil {
			g.collectBlock(s.Else)
		}
	case *syntax.ForStmt:
		switch s.Kind {
		case syntax.ForCond:
			g.collectExpr(s.Cond)
		case syntax.ForRange:
			g.collectExpr(s.Range.Start)
			g.collectExpr(s.Range.End)
		case syntax.ForIter:
			g.collectExpr(s.Iter)
		}
		g.collectBlock(s.Body)
	case *syntax.ReturnStmt:
		if s.Value != nil {
			g.collectExpr(s.Value)
		}
		if s.Guard != nil {
			g.collectExpr(s.Guard)
		}
	case *syntax.BreakStmt:
		if s.Guard != nil {
			g.collectExpr(s.Guard)
		}
	case *syntax.ContinueStmt:
		if s.Guard != nil {
			g.collectExpr(s.Guard)
		}
	case *syntax.FnDecl:
		for _, p := range s.Params {
			if p.Type != nil && p.Type.Resolved != nil {
				g.shapes.addType(g, p.Type.Resolved)
				g.collectSpecsInType(p.Type.Resolved)
			}
		}
		if s.Return != nil && s.Return.Resolved != nil {
			g.shapes.addType(g, s.Return.Resolved)
			g.collectSpecsInType(s.Return.Resolved)
		}
		g.collectBlock(s.Body)
	case *syntax.MatchStmt:
		g.collectExpr(s.Subject)
		for i := range s.Arms {
			arm := &s.Arms[i]
			g.collectPattern(arm.Pattern)
			if arm.Guard != nil {
				g.collectExpr(arm.Guard)
			}
			g.collectBlock(arm.Body)
		}
	case *syntax.SpecDecl:
		// Spec method default bodies may reference shapes the rest of the
		// program does not — walk them so the registry catches nested types.
		for _, m := range s.Methods {
			for _, p := range m.Params {
				if p.Type != nil && p.Type.Resolved != nil {
					g.shapes.addType(g, p.Type.Resolved)
				}
			}
			if m.Return != nil && m.Return.Resolved != nil {
				g.shapes.addType(g, m.Return.Resolved)
			}
			if m.Body != nil {
				g.collectBlock(m.Body)
			}
		}
	case *syntax.ImplDecl:
		for _, fn := range s.Methods {
			for _, p := range fn.Params {
				if p.Type != nil && p.Type.Resolved != nil {
					g.shapes.addType(g, p.Type.Resolved)
				}
			}
			if fn.Return != nil && fn.Return.Resolved != nil {
				g.shapes.addType(g, fn.Return.Resolved)
			}
			g.collectBlock(fn.Body)
		}
	}
}

func (g *cgen) collectBlock(b *syntax.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Statements {
		g.collectStmt(st)
	}
}

func (g *cgen) collectExpr(e syntax.Expr) {
	if e == nil {
		return
	}
	if t := e.Type(); t != nil {
		g.shapes.addType(g, t)
		g.collectSpecsInType(t)
	}
	switch x := e.(type) {
	case *syntax.UnaryExpr:
		g.collectExpr(x.Operand)
	case *syntax.BinaryExpr:
		g.collectExpr(x.Left)
		g.collectExpr(x.Right)
	case *syntax.ParenExpr:
		g.collectExpr(x.Inner)
	case *syntax.CallExpr:
		g.collectExpr(x.Callee)
		for _, a := range x.Args {
			g.collectExpr(a)
		}
	case *syntax.ListLit:
		for _, sub := range x.Elements {
			g.collectExpr(sub)
		}
	case *syntax.TupleLit:
		for _, sub := range x.Elements {
			g.collectExpr(sub)
		}
	case *syntax.StructLit:
		for _, f := range x.Fields {
			g.collectExpr(f.Value)
		}
	case *syntax.IndexExpr:
		g.collectExpr(x.Receiver)
		g.collectExpr(x.Index)
	case *syntax.SliceExpr:
		g.collectExpr(x.Receiver)
		if x.Low != nil {
			g.collectExpr(x.Low)
		}
		if x.High != nil {
			g.collectExpr(x.High)
		}
	case *syntax.FieldAccessExpr:
		g.collectExpr(x.Receiver)
		if x.Lowered != nil {
			g.collectExpr(x.Lowered)
		}
	case *syntax.MethodCallExpr:
		g.collectExpr(x.Receiver)
		for _, a := range x.Args {
			g.collectExpr(a)
		}
		if x.Lowered != nil {
			g.collectExpr(x.Lowered)
		}
		if x.LoweredCall != nil {
			g.collectExpr(x.LoweredCall)
		}
	case *syntax.ThisExpr:
		// nothing to collect; type is registered by the typed() walk above.
	case *syntax.EnumLit:
		for _, sub := range x.Payload {
			g.collectExpr(sub)
		}
	case *syntax.NilLit:
		// type is collected by the typed() walk above.
	case *syntax.PropagateExpr:
		g.collectExpr(x.Inner)
	case *syntax.CoalesceExpr:
		g.collectExpr(x.Left)
		g.collectExpr(x.Right)
	}
}

func (g *cgen) collectPattern(p syntax.Pattern) {
	switch x := p.(type) {
	case *syntax.LitPat:
		g.collectExpr(x.Lit)
	case *syntax.TuplePat:
		for _, sub := range x.Elements {
			g.collectPattern(sub)
		}
	case *syntax.StructPat:
		for _, f := range x.Fields {
			g.collectPattern(f.Pattern)
		}
	case *syntax.EnumPat:
		for _, sub := range x.Payload {
			g.collectPattern(sub)
		}
	}
}

// ---------------------------------------------------------------------------
// Statement emission.
// ---------------------------------------------------------------------------

func (g *cgen) emitStmt(stmt syntax.Stmt) error {
	switch s := stmt.(type) {
	case *syntax.NopStmt:
		g.writeIndent()
		g.b.WriteString("(void)0;\n")
		return nil
	case *syntax.PrintStmt:
		return g.emitPrint(s)
	case *syntax.LetStmt:
		if s.Tuple != nil {
			return g.emitTupleDestructure(s.Tuple, s.Value, false)
		}
		return g.emitDecl(s.Name, s.Type, s.Value, false)
	case *syntax.MutStmt:
		if s.Tuple != nil {
			return g.emitTupleDestructure(s.Tuple, s.Value, false)
		}
		return g.emitDecl(s.Name, s.Type, s.Value, false)
	case *syntax.ConstStmt:
		return g.emitDecl(s.Name, s.Type, s.Value, true)
	case *syntax.AssignStmt:
		return g.emitAssign(s)
	case *syntax.ExprStmt:
		expr, err := g.exprStr(s.Expr)
		if err != nil {
			return err
		}
		g.writeIndent()
		fmt.Fprintf(&g.b, "(void)(%s);\n", expr)
		return nil
	case *syntax.IfStmt:
		return g.emitIf(s)
	case *syntax.ForStmt:
		return g.emitFor(s)
	case *syntax.ReturnStmt:
		return g.emitReturn(s)
	case *syntax.BreakStmt:
		return g.emitFlow(s.Guard, "break")
	case *syntax.ContinueStmt:
		return g.emitFlow(s.Guard, "continue")
	case *syntax.FnDecl:
		return fmt.Errorf("internal: nested function %q at %s", s.Name, s.Pos)
	case *syntax.StructDecl, *syntax.EnumDecl:
		// Top-level type decls produce no executable code; their typedefs
		// were emitted by the runtime/shape pass. Reaching here at non-top
		// level is impossible — typeck rejects nested decls.
		return nil
	case *syntax.MatchStmt:
		return g.emitMatch(s)
	case *syntax.SpecDecl, *syntax.ImplDecl:
		// Spec / impl declarations produce no executable code at the
		// statement level — their per-method C functions and per-(Type,
		// Spec) vtable initialisers were emitted at file scope before
		// main(). Reaching here at non-top level is impossible because
		// typeck rejects nested decls.
		return nil
	case *syntax.SpawnStmt:
		return g.emitSpawn(s)
	case *syntax.SendStmt:
		return g.emitSend(s)
	case *syntax.DeferStmt:
		return g.emitDefer(s)
	case *syntax.SelectStmt:
		return g.emitSelect(s)
	case *syntax.AsmBlock:
		return g.emitAsmBlock(s)
	}
	return fmt.Errorf("codegen: unhandled statement %T at %s", stmt, stmt.StmtPos())
}

// emitPrint dispatches on the static type. Every printable v0.2 shape has a
// dedicated helper; we add `\n` after the value-printer runs.
func (g *cgen) emitPrint(s *syntax.PrintStmt) error {
	expr, err := g.exprStr(s.Expr)
	if err != nil {
		return err
	}
	t := s.Expr.Type()
	if t == nil {
		return fmt.Errorf("codegen: missing type for print at %s", s.Pos)
	}
	g.writeIndent()
	switch t {
	case syntax.TInt():
		fmt.Fprintf(&g.b, "zerg_print_int(%s);\n", expr)
		return nil
	case syntax.TFloat():
		fmt.Fprintf(&g.b, "zerg_print_float(%s);\n", expr)
		return nil
	case syntax.TBool():
		fmt.Fprintf(&g.b, "zerg_print_bool(%s);\n", expr)
		return nil
	case syntax.TStr():
		fmt.Fprintf(&g.b, "zerg_print_str(%s);\n", expr)
		return nil
	case syntax.TByte():
		fmt.Fprintf(&g.b, "zerg_print_byte(%s);\n", expr)
		return nil
	case syntax.TRune():
		fmt.Fprintf(&g.b, "zerg_print_rune(%s);\n", expr)
		return nil
	}
	switch t.Kind {
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
		// The composite print helpers do NOT add a newline; we add one here
		// so the v0.1 contract (every print line ends with '\n') stays.
		fmt.Fprintf(&g.b, "zerg_print_%s(%s);\n", g.mangleType(t), expr)
		g.writeIndent()
		g.b.WriteString("putchar('\\n');\n")
		return nil
	}
	return fmt.Errorf("codegen: cannot print value of type %s at %s", t, s.Pos)
}

// emitDecl lowers let/mut/const into a C local declaration. At v0.3 we
// do NOT wrap composite RHS values in `_copy` — the borrow checker has
// invalidated the source binding at the move site, so sharing the
// underlying buffer/struct is safe. clone() is the explicit opt-in for
// the v0.2-style deep copy.
func (g *cgen) emitDecl(name string, ref *syntax.TypeRef, value syntax.Expr, isConst bool) error {
	// v0.9 Phase 4 Fix 1: a `-> never` RHS (e.g. `x: int = os.exit(0)`)
	// typechecks via the bottom-type subtyping rule but the underlying C
	// trampoline returns void. Emit the call as a statement and emit a
	// zero-initialised stub binding so any subsequent references to the
	// name still compile — the call diverges so the stub is never read.
	if vt := value.Type(); vt != nil && vt.Kind == syntax.TypeNever {
		exprS, err := g.exprStr(value)
		if err != nil {
			return err
		}
		var declT *syntax.Type
		if ref != nil && ref.Resolved != nil {
			declT = ref.Resolved
		}
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s;\n", exprS)
		if declT != nil && declT.Kind != syntax.TypeNever {
			g.writeIndent()
			if isConst {
				g.b.WriteString("const ")
			}
			fmt.Fprintf(&g.b, "%s %s = (%s){0};\n", g.cTypeName(declT), mangle(name), g.cTypeName(declT))
		}
		return nil
	}
	t := value.Type()
	declT := t
	if ref != nil && ref.Resolved != nil {
		declT = ref.Resolved
	}
	if t == nil {
		t = declT
	}
	if declT == nil {
		return fmt.Errorf("codegen: missing type for %q", name)
	}
	exprS, err := g.exprStr(value)
	if err != nil {
		return err
	}
	// v0.4: a let/mut/const declared with a spec type widens the rhs into a
	// fat pointer at the bind site; nested specs (list[Spec], tuple[..., Spec])
	// recurse inside coerceCExpr.
	if shapeContainsSpec(declT) {
		exprS = g.coerceCExpr(exprS, t, declT)
	}
	g.writeIndent()
	if isConst {
		g.b.WriteString("const ")
	}
	fmt.Fprintf(&g.b, "%s %s = %s;\n", g.cTypeName(declT), mangle(name), exprS)
	return nil
}

// emitTupleDestructure lowers tuple-destructure binding `(a, b) := expr` into N variable decls
// reading from a fresh temp tuple. At v0.3 the elements are NOT deep-copied
// — the borrow checker invalidated the source pair at the destructure
// site so each name shares the underlying element value safely.
func (g *cgen) emitTupleDestructure(tb *syntax.TupleBinding, value syntax.Expr, isConst bool) error {
	t := value.Type()
	if t == nil || t.Kind != syntax.TypeTuple {
		return fmt.Errorf("codegen: tuple destructure rhs has non-tuple type at %s", tb.Pos)
	}
	exprS, err := g.exprStr(value)
	if err != nil {
		return err
	}
	tmp := g.freshTmp("tup")
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s %s = %s;\n", g.cTypeName(t), tmp, exprS)
	for i, name := range tb.Names {
		elemT := t.Tuple[i]
		g.writeIndent()
		if isConst {
			g.b.WriteString("const ")
		}
		fmt.Fprintf(&g.b, "%s %s = %s.e%d;\n",
			g.cTypeName(elemT), mangle(name), tmp, i)
	}
	return nil
}

// emitAssign lowers any assign-op to the C equivalent.
func (g *cgen) emitAssign(s *syntax.AssignStmt) error {
	// `xs[i] = v` lowers to a bounds-checked write through the list's data
	// pointer. Other LHS shapes are typeck/borrow-check rejected before they
	// reach codegen; only bare IdentExpr targets remain.
	if idx, ok := s.Target.(*syntax.IndexExpr); ok {
		return g.emitIndexAssign(s, idx)
	}
	target, ok := s.Target.(*syntax.IdentExpr)
	if !ok {
		return fmt.Errorf("codegen: unsupported assignment target %T at %s", s.Target, s.Pos)
	}
	rhs, err := g.exprStr(s.Value)
	if err != nil {
		return err
	}
	// v0.9 Phase 4 Fix 1: a `-> never` RHS (e.g. `x = os.exit(0)`)
	// typechecks via the bottom-type rule but the trampoline returns
	// void. Emit the call as a statement and skip the assignment;
	// control never returns from a diverging callee.
	if vt := s.Value.Type(); vt != nil && vt.Kind == syntax.TypeNever {
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s;\n", rhs)
		return nil
	}
	targetName := g.identName(target.Name)
	g.writeIndent()
	switch s.Op {
	case syntax.AssignSet:
		// At v0.3 plain `x = v` is only meaningful for primitive targets
		// (the borrow checker rejects composite rebind via `=` because
		// composite mut bindings reach the new value via `:=` rebinding
		// or via `xs[i] = v` indexing). No implicit deep-copy.
		fmt.Fprintf(&g.b, "%s = %s;\n", targetName, rhs)
	case syntax.AssignAdd:
		if target.Type() == syntax.TStr() {
			fmt.Fprintf(&g.b, "%s = zerg_str_concat(%s, %s);\n", targetName, targetName, rhs)
			return nil
		}
		fmt.Fprintf(&g.b, "%s += %s;\n", targetName, rhs)
	case syntax.AssignSub:
		fmt.Fprintf(&g.b, "%s -= %s;\n", targetName, rhs)
	case syntax.AssignMul:
		fmt.Fprintf(&g.b, "%s *= %s;\n", targetName, rhs)
	case syntax.AssignDiv:
		fmt.Fprintf(&g.b, "%s /= %s;\n", targetName, rhs)
	case syntax.AssignMod:
		if target.Type() == syntax.TFloat() {
			fmt.Fprintf(&g.b, "%s = fmod(%s, %s);\n", targetName, targetName, rhs)
			return nil
		}
		fmt.Fprintf(&g.b, "%s %%= %s;\n", targetName, rhs)
	case syntax.AssignAnd:
		fmt.Fprintf(&g.b, "%s &= %s;\n", targetName, rhs)
	case syntax.AssignOr:
		fmt.Fprintf(&g.b, "%s |= %s;\n", targetName, rhs)
	case syntax.AssignXor:
		fmt.Fprintf(&g.b, "%s ^= %s;\n", targetName, rhs)
	case syntax.AssignShl:
		fmt.Fprintf(&g.b, "%s <<= %s;\n", targetName, rhs)
	case syntax.AssignShr:
		fmt.Fprintf(&g.b, "%s >>= %s;\n", targetName, rhs)
	default:
		return fmt.Errorf("codegen: unknown assign op %s at %s", s.Op, s.Pos)
	}
	return nil
}

// emitIndexAssign lowers `xs[i] = v`. The receiver must be a bare named list
// (typeck and borrow check have enforced this); we look up its mangled name,
// bounds-check the index, and assign through the data pointer with a deep
// copy of the rhs so the source rhs binding stays independent of the slot.
//
// Only AssignSet is admitted on a list element; compound assigns through a
// list element are out of scope at v0.3.
func (g *cgen) emitIndexAssign(s *syntax.AssignStmt, idx *syntax.IndexExpr) error {
	id, ok := idx.Receiver.(*syntax.IdentExpr)
	if !ok {
		return fmt.Errorf("codegen: list-element assignment requires a named list at %s", s.Pos)
	}
	if s.Op != syntax.AssignSet {
		return fmt.Errorf("codegen: list-element compound assign %s not supported at %s", s.Op, s.Pos)
	}
	listT := idx.Receiver.Type()
	if listT == nil || listT.Kind != syntax.TypeList {
		return fmt.Errorf("codegen: list-element assign target is not a list at %s", s.Pos)
	}
	is, err := g.exprStr(idx.Index)
	if err != nil {
		return err
	}
	rhs, err := g.exprStr(s.Value)
	if err != nil {
		return err
	}
	posStr := fmt.Sprintf("%d:%d", s.Pos.Line, s.Pos.Column)
	nameS := mangle(id.Name)
	g.writeIndent()
	// Bounds-check then write through the slice header. We compute the index
	// once into a local so the bounds check sees the same value the write
	// uses, and so a side-effecting index expression is evaluated once.
	fmt.Fprintf(&g.b,
		"{ int64_t __i = %s; zerg_index_check(__i, %s.len, %q); %s.data[__i] = %s; }\n",
		is, nameS, posStr, nameS, rhs)
	return nil
}

// emitIf walks the if-elif-else chain.
func (g *cgen) emitIf(s *syntax.IfStmt) error {
	cond, err := g.exprStr(s.Cond)
	if err != nil {
		return err
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "if (%s) {\n", cond)
	if err := g.emitBlockBody(s.Then); err != nil {
		return err
	}
	for i := range s.Elifs {
		ec := &s.Elifs[i]
		c, err := g.exprStr(ec.Cond)
		if err != nil {
			return err
		}
		g.writeIndent()
		fmt.Fprintf(&g.b, "} else if (%s) {\n", c)
		if err := g.emitBlockBody(ec.Body); err != nil {
			return err
		}
	}
	if s.Else != nil {
		g.writeIndent()
		g.b.WriteString("} else {\n")
		if err := g.emitBlockBody(s.Else); err != nil {
			return err
		}
	}
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// emitBlockBody emits the statements between `{` and `}` with one extra level
// of indent. The braces themselves are emitted by the caller.
func (g *cgen) emitBlockBody(b *syntax.Block) error {
	g.indent++
	defer func() { g.indent-- }()
	for _, st := range b.Statements {
		if err := g.emitStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// emitFor lowers all four for shapes. ForIter (list iteration) is the v0.2
// addition: a C-level `for` walking xs.data[0..xs.len) with a per-iteration
// fresh deep-copy bound to the loop variable.
func (g *cgen) emitFor(s *syntax.ForStmt) error {
	switch s.Kind {
	case syntax.ForInfinite:
		g.writeIndent()
		g.b.WriteString("while (1) {\n")
		if err := g.emitBlockBody(s.Body); err != nil {
			return err
		}
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	case syntax.ForCond:
		cond, err := g.exprStr(s.Cond)
		if err != nil {
			return err
		}
		g.writeIndent()
		fmt.Fprintf(&g.b, "while (%s) {\n", cond)
		if err := g.emitBlockBody(s.Body); err != nil {
			return err
		}
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	case syntax.ForRange:
		start, err := g.exprStr(s.Range.Start)
		if err != nil {
			return err
		}
		end, err := g.exprStr(s.Range.End)
		if err != nil {
			return err
		}
		cmp := "<"
		if s.Range.Inclusive {
			cmp = "<="
		}
		v := mangle(s.Var)
		g.writeIndent()
		fmt.Fprintf(&g.b, "for (int64_t %s = %s; %s %s %s; ++%s) {\n", v, start, v, cmp, end, v)
		if err := g.emitBlockBody(s.Body); err != nil {
			return err
		}
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	case syntax.ForChan:
		return g.emitForChan(s)
	case syntax.ForIter:
		// Wrap in a brace-block so the temp + loop variable are scoped.
		iterT := s.Iter.Type()
		if iterT == nil || iterT.Kind != syntax.TypeList {
			return fmt.Errorf("codegen: for-in iter has non-list type at %s", s.Pos)
		}
		iterS, err := g.exprStr(s.Iter)
		if err != nil {
			return err
		}
		// Snapshot the iterable into a temp so a fn-call iterable is only
		// evaluated once. At v0.3 the borrow checker has BorrowedShared
		// the iterable for the body's duration and rejects in-body
		// mutation of it, so we don't need a deep-copy snapshot — a
		// shallow snapshot of the (data, len, cap) header suffices.
		listMangle := g.mangleType(iterT)
		tmp := g.freshTmp("iter")
		idx := g.freshTmp("i")
		v := mangle(s.Var)
		elemT := iterT.Element

		g.writeIndent()
		g.b.WriteString("{\n")
		g.indent++
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s %s = %s;\n", listMangle, tmp, iterS)
		g.writeIndent()
		fmt.Fprintf(&g.b, "for (size_t %s = 0; %s < %s.len; %s++) {\n", idx, idx, tmp, idx)
		g.indent++
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s %s = %s.data[%s];\n", g.cTypeName(elemT), v, tmp, idx)
		// Body statements (without the extra brace, but with indent already
		// raised once). Walk statements one-by-one without using
		// emitBlockBody to keep the indent layered correctly.
		for _, st := range s.Body.Statements {
			if err := g.emitStmt(st); err != nil {
				return err
			}
		}
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	}
	return fmt.Errorf("codegen: unknown for kind at %s", s.Pos)
}

// emitReturn handles bare return, return-with-value, and the guard form.
// At v0.3 we do NOT wrap composite return values in `_copy` — the borrow
// checker has invalidated the local binding at the return site, so the
// caller can take ownership of the underlying buffer/struct directly.
//
// v0.7: when the enclosing fn carries HasDefers, the return value is
// snapshotted into a temp, the defer stack is drained, and only then does
// the C `return` fire — so deferred actions observe an unmodified caller
// frame and the user's Result-shaped return value still reaches the caller.
func (g *cgen) emitReturn(s *syntax.ReturnStmt) error {
	body := "return;"
	if s.Value != nil {
		v, err := g.exprStr(s.Value)
		if err != nil {
			return err
		}
		// v0.9 Phase 4 Fix 1: `return os.exit(0)` from a non-never fn
		// typechecks via never <: T but the trampoline returns void.
		// Emit the call as a statement; the diverging callee never
		// returns so a following return is unreachable. The C compiler
		// is satisfied because the trampoline carries
		// __attribute__((noreturn)).
		if vt := s.Value.Type(); vt != nil && vt.Kind == syntax.TypeNever {
			body = fmt.Sprintf("%s;", v)
			if s.Guard == nil {
				g.writeIndent()
				g.b.WriteString(body)
				g.b.WriteString("\n")
				return nil
			}
			guard, err := g.exprStr(s.Guard)
			if err != nil {
				return err
			}
			g.writeIndent()
			fmt.Fprintf(&g.b, "if (%s) { %s }\n", guard, body)
			return nil
		}
		// v0.4: coerce to the declared fn return type if spec-typed.
		if g.currentFnRet != nil && shapeContainsSpec(g.currentFnRet) {
			v = g.coerceCExpr(v, s.Value.Type(), g.currentFnRet)
		}
		if g.currentHasDefers {
			// Snapshot value into a temp; drain defers; then return.
			retT := g.currentFnRet
			cType := "int"
			if retT != nil {
				cType = g.cTypeName(retT)
			}
			tmp := g.freshTmp("ret")
			body = fmt.Sprintf("{ %s %s = %s; zerg_defer_drain(__zerg_defer_marker); return %s; }",
				cType, tmp, v, tmp)
		} else {
			body = fmt.Sprintf("return %s;", v)
		}
	} else if g.currentHasDefers {
		body = "{ zerg_defer_drain(__zerg_defer_marker); return; }"
	}
	if s.Guard == nil {
		g.writeIndent()
		g.b.WriteString(body)
		g.b.WriteString("\n")
		return nil
	}
	guard, err := g.exprStr(s.Guard)
	if err != nil {
		return err
	}
	g.writeIndent()
	// Wrap the body in braces when it isn't already a brace block (the
	// HasDefers path emits a brace block already; the simple path is a
	// `return X;` token). The `{ ... }` form is byte-stable across v0.0–v0.7.
	if len(body) > 0 && body[0] == '{' {
		fmt.Fprintf(&g.b, "if (%s) %s\n", guard, body)
	} else {
		fmt.Fprintf(&g.b, "if (%s) { %s }\n", guard, body)
	}
	return nil
}

// emitFlow handles break/continue with optional guard.
func (g *cgen) emitFlow(guard syntax.Expr, kw string) error {
	if guard == nil {
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s;\n", kw)
		return nil
	}
	c, err := g.exprStr(guard)
	if err != nil {
		return err
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "if (%s) %s;\n", c, kw)
	return nil
}

// emitFn writes a complete static function definition. v0.5: switches the
// current-module context to the fn's owner so cross-module fn calls inside
// the body resolve through the owner's import table.
//
// v0.7: when fn.HasDefers is set, the body emits a per-frame defer-stack
// marker on entry and the fn epilogue jumps through `__zerg_fn_end:` /
// drains the stack on every exit. The `?` early-return path (propagateStr)
// jumps to that label so deferred actions fire on Err / None propagation.
func (g *cgen) emitFn(fn *syntax.FnDecl) error {
	// v0.8: __builtin fn-decls have no body. The trampoline forwards to a
	// runtime helper and wraps the result into the user-program's view of
	// the return type (Result / Option construction).
	if fn.BuiltinName != "" {
		return g.emitBuiltinFn(fn)
	}
	g.writeFnSig(fn)
	g.b.WriteString(" {\n")
	prevRet := g.currentFnRet
	prevMod := g.currentMod
	prevHasDef := g.currentHasDefers
	prevEndLabel := g.currentFnEndLabel
	if fn.Return != nil {
		g.currentFnRet = fn.Return.Resolved
	} else {
		g.currentFnRet = nil
	}
	if owner, ok := g.fnOwner[fn]; ok {
		g.currentMod = owner
	}
	g.currentHasDefers = fn.HasDefers
	if fn.HasDefers {
		g.fnEndCounter++
		g.currentFnEndLabel = fmt.Sprintf("__zerg_fn_end_%d", g.fnEndCounter)
		g.b.WriteString("    zerg_defer_rec *__zerg_defer_marker = zerg_defer_top;\n")
	} else {
		g.currentFnEndLabel = ""
	}
	defer func() {
		g.currentFnRet = prevRet
		g.currentMod = prevMod
		g.currentHasDefers = prevHasDef
		g.currentFnEndLabel = prevEndLabel
	}()
	if err := g.emitBlockBody(fn.Body); err != nil {
		return err
	}
	if fn.HasDefers {
		fmt.Fprintf(&g.b, "    %s: ;\n", g.currentFnEndLabel)
		g.b.WriteString("    zerg_defer_drain(__zerg_defer_marker);\n")
	}
	g.b.WriteString("}\n")
	return nil
}

// writeFnSig renders the C signature (no trailing punctuation).
//
// v0.5: every fn — including the entry module's — is name-mangled with its
// owning module's prefix so two modules' identically-named functions do
// not collide in the merged TU. Local-call site lowering (callStr) calls
// fnCName with the same owner so the call resolves to the prefixed name.
func (g *cgen) writeFnSig(fn *syntax.FnDecl) {
	b := &g.b
	ret := "void"
	if fn.Return != nil && fn.Return.Resolved != nil && fn.Return.Resolved != syntax.TVoid() {
		ret = g.cTypeName(fn.Return.Resolved)
	}
	b.WriteString("static ")
	// v0.9 Unit 1: `-> never` fn-decls cannot return; tell the C compiler
	// so it does not warn about missing trailing return / unreachable
	// fall-through paths.
	if fnReturnsNever(fn) {
		b.WriteString("__attribute__((noreturn)) ")
	}
	b.WriteString(ret)
	b.WriteByte(' ')
	b.WriteString(g.fnCName(fn))
	b.WriteByte('(')
	if len(fn.Params) == 0 {
		b.WriteString("void")
	} else {
		for i, p := range fn.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(g.cTypeName(p.Type.Resolved))
			b.WriteByte(' ')
			b.WriteString(mangle(p.Name))
		}
	}
	b.WriteByte(')')
}

// fnCName returns the C identifier for a top-level fn. Format:
// `z_<modmangle>__<name>`. Two modules' fns with the same name produce
// distinct identifiers because the module-mangle prefix differs.
//
// Built-ins (`len`, `clone`, `push`) and any fn whose owner cannot be
// recovered from g.fnOwner fall back to the bare `z_<name>` shape so v0.0–
// v0.4-style single-program calls keep working in the partial-init paths
// the test harness exercises.
//
// v0.6: monomorphised FnDecls carry a Name shaped like `id[int]` (the
// mono cache key). sanitizeGenericName converts the brackets / commas to
// the C-safe `__` / `_` per PLAN.md.
func (g *cgen) fnCName(fn *syntax.FnDecl) string {
	name := sanitizeGenericName(fn.Name)
	if g != nil {
		if owner, ok := g.fnOwner[fn]; ok && owner != "" {
			return "z_" + owner + "__" + name
		}
	}
	return mangle(name)
}

// ---------------------------------------------------------------------------
// Match.
// ---------------------------------------------------------------------------

// emitMatch lowers a match statement to a labelled brace-block:
//
//	{
//	    <SubjT> __zg_subj_<n> = <subj>;
//	    {  /* arm 1 */
//	        if (!<test for arm 1>) goto matcharm_<n>_1;
//	        <bind> ...
//	        if (guard) {
//	            if (!<guard>) goto matcharm_<n>_1;
//	        }
//	        <body>
//	        goto matchend_<n>;
//	    }
//	    matcharm_<n>_1:;
//	    {  /* arm 2 */ ... goto matcharm_<n>_2; ... }
//	    matcharm_<n>_2:;
//	    ...
//	    zerg_match_panic(<pos>);
//	    matchend_<n>: ;
//	}
//
// We use `goto` to jump to the next arm rather than `break`/do-while(0)
// because the body may itself contain `break` / `continue` that should
// affect an enclosing loop, not the match. (do-while(0) would swallow
// `break`.) `goto` is the simplest portable lowering that preserves
// statement transparency.
func (g *cgen) emitMatch(s *syntax.MatchStmt) error {
	g.matchCounter++
	id := g.matchCounter
	subjT := s.Subject.Type()
	if subjT == nil {
		return fmt.Errorf("codegen: missing type for match subject at %s", s.Pos)
	}
	subjStr, err := g.exprStr(s.Subject)
	if err != nil {
		return err
	}
	subjVar := fmt.Sprintf("__zg_subj_%d", id)
	endLabel := fmt.Sprintf("matchend_%d", id)
	posStr := fmt.Sprintf("%d:%d", s.Pos.Line, s.Pos.Column)

	g.writeIndent()
	g.b.WriteString("{\n")
	g.indent++
	// Snapshot the subject into a local so binding patterns and tests
	// reference a stable value without re-evaluating the subject (which
	// may have side effects). At v0.3 we don't deep-copy — the snapshot
	// shares the underlying buffer/struct with the subject, and the
	// borrow checker has BorrowedShared the subject for the duration.
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s %s = %s;\n",
		g.cTypeName(subjT), subjVar, subjStr)

	for i := range s.Arms {
		arm := &s.Arms[i]
		nextLabel := fmt.Sprintf("matcharm_%d_%d", id, i+1)

		g.writeIndent()
		g.b.WriteString("{\n")
		g.indent++

		// Pattern test: jump to the next-arm label on failure. Wildcard /
		// bind always pass, so we skip emitting the test entirely when it
		// would be a no-op.
		test := g.patternTest(arm.Pattern, subjVar, subjT)
		if test != "1" {
			g.writeIndent()
			fmt.Fprintf(&g.b, "if (!(%s)) goto %s;\n", test, nextLabel)
		}
		// Pattern bindings: declare locals from the matched parts.
		if err := g.emitPatternBindings(arm.Pattern, subjVar, subjT); err != nil {
			return err
		}
		// Guard: same fallthrough on false.
		if arm.Guard != nil {
			gs, err := g.exprStr(arm.Guard)
			if err != nil {
				return err
			}
			g.writeIndent()
			fmt.Fprintf(&g.b, "if (!(%s)) goto %s;\n", gs, nextLabel)
		}
		// Body:
		for _, st := range arm.Body.Statements {
			if err := g.emitStmt(st); err != nil {
				return err
			}
		}
		// Successful arm — skip remaining arms.
		g.writeIndent()
		fmt.Fprintf(&g.b, "goto %s;\n", endLabel)

		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
		// next-arm label, even on the last arm (cheaper than book-keeping
		// to skip the trailing label, and the C compiler folds away the
		// unreachable label).
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s: ;\n", nextLabel)
	}

	// Fall-through: no arm matched ⇒ panic.
	g.writeIndent()
	fmt.Fprintf(&g.b, "zerg_match_panic(%q);\n", posStr)

	// End label. Wrap in `;` so the label always has a statement after it
	// even when the surrounding block ends here.
	g.indent--
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s: ;\n", endLabel)
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// patternTest returns a C boolean expression that's true iff pat matches the
// scrutinee at C expression `scrut`. Returns "1" for wildcard/bind patterns
// (which always match).
func (g *cgen) patternTest(pat syntax.Pattern, scrut string, scrutT *syntax.Type) string {
	switch p := pat.(type) {
	case *syntax.WildcardPat, *syntax.BindPat:
		_ = p
		return "1"
	case *syntax.LitPat:
		// Lit is a primitive literal (optionally negated); emit a == compare.
		// Strings need zerg_str_eq.
		litS, err := g.exprStr(p.Lit)
		if err != nil {
			// Should not happen post-typeck; emit a guard that always fails
			// so the arm is skipped rather than miscompiled.
			return "0"
		}
		t := p.Lit.Type()
		if t == syntax.TStr() {
			return fmt.Sprintf("zerg_str_eq(%s, %s)", scrut, litS)
		}
		return fmt.Sprintf("(%s == %s)", scrut, litS)
	case *syntax.TuplePat:
		var parts []string
		for i, sub := range p.Elements {
			if scrutT == nil || scrutT.Kind != syntax.TypeTuple {
				return "0"
			}
			parts = append(parts, g.patternTest(sub,
				fmt.Sprintf("%s.e%d", scrut, i), scrutT.Tuple[i]))
		}
		return joinAnd(parts)
	case *syntax.StructPat:
		var parts []string
		for _, f := range p.Fields {
			fieldT := lookupFieldType(scrutT, f.Name)
			parts = append(parts, g.patternTest(f.Pattern,
				fmt.Sprintf("%s.%s", scrut, mangleField(f.Name)), fieldT))
		}
		return joinAnd(parts)
	case *syntax.EnumPat:
		// Variant tag test plus per-position payload pattern tests.
		idx := variantIndex(scrutT, p.VariantName)
		head := fmt.Sprintf("(%s.tag == %d)", scrut, idx)
		if len(p.Payload) == 0 {
			return head
		}
		payloadTypes := variantPayload(scrutT, idx)
		parts := []string{head}
		for i, sub := range p.Payload {
			var pt *syntax.Type
			if i < len(payloadTypes) {
				pt = payloadTypes[i]
			}
			access := fmt.Sprintf("%s.payload.p%d.a%d", scrut, idx, i)
			parts = append(parts, g.patternTest(sub, access, pt))
		}
		return joinAnd(parts)
	}
	return "0"
}

// joinAnd returns the C expression "(p1) && (p2) && ...". Returns "1" for the
// empty list (pattern with no sub-tests, e.g. `Point { .. }`).
func joinAnd(parts []string) string {
	out := []string{}
	for _, p := range parts {
		if p == "1" {
			continue
		}
		out = append(out, "("+p+")")
	}
	if len(out) == 0 {
		return "1"
	}
	return strings.Join(out, " && ")
}

// emitPatternBindings emits local-variable declarations for every BindPat
// nested in pat. The bound name receives a deep copy of the corresponding
// piece of scrut.
func (g *cgen) emitPatternBindings(pat syntax.Pattern, scrut string, scrutT *syntax.Type) error {
	switch p := pat.(type) {
	case *syntax.WildcardPat, *syntax.LitPat:
		_ = p
		return nil
	case *syntax.EnumPat:
		// Recurse into per-position payload sub-patterns so a BindPat at any
		// payload slot creates a local binding to that payload value.
		if len(p.Payload) == 0 {
			return nil
		}
		idx := variantIndex(scrutT, p.VariantName)
		payloadTypes := variantPayload(scrutT, idx)
		for i, sub := range p.Payload {
			var pt *syntax.Type
			if i < len(payloadTypes) {
				pt = payloadTypes[i]
			}
			access := fmt.Sprintf("%s.payload.p%d.a%d", scrut, idx, i)
			if err := g.emitPatternBindings(sub, access, pt); err != nil {
				return err
			}
		}
		return nil
	case *syntax.BindPat:
		// At v0.3 the bound name shares the matched value (no deep copy).
		// The borrow checker has flagged the scrutinee as Moved at exit
		// for BindPat arms, so the user can't observe aliasing.
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s %s = %s;\n",
			g.cTypeName(scrutT), mangle(p.Name), scrut)
		return nil
	case *syntax.TuplePat:
		if scrutT == nil || scrutT.Kind != syntax.TypeTuple {
			return fmt.Errorf("codegen: tuple pattern against non-tuple type")
		}
		for i, sub := range p.Elements {
			if err := g.emitPatternBindings(sub,
				fmt.Sprintf("%s.e%d", scrut, i), scrutT.Tuple[i]); err != nil {
				return err
			}
		}
		return nil
	case *syntax.StructPat:
		for _, f := range p.Fields {
			fieldT := lookupFieldType(scrutT, f.Name)
			if err := g.emitPatternBindings(f.Pattern,
				fmt.Sprintf("%s.%s", scrut, mangleField(f.Name)), fieldT); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// lookupFieldType returns the field type of t.Name. Returns nil if not found
// (which should not happen post-typeck).
func lookupFieldType(t *syntax.Type, fieldName string) *syntax.Type {
	if t == nil || t.Kind != syntax.TypeStruct {
		return nil
	}
	for _, f := range t.Fields {
		if f.Name == fieldName {
			return f.Type
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Expression rendering.
// ---------------------------------------------------------------------------

func (g *cgen) exprStr(expr syntax.Expr) (string, error) {
	switch e := expr.(type) {
	case *syntax.IntLit:
		return fmt.Sprintf("INT64_C(%d)", e.Int), nil
	case *syntax.FloatLit:
		return strconv.FormatFloat(e.Float, 'g', 17, 64), nil
	case *syntax.StringLit:
		return fmt.Sprintf("zerg_str_lit(%s, %d)", cQuote(e.Value), len(e.Value)), nil
	case *syntax.BoolLit:
		if e.Value {
			return "(_Bool)1", nil
		}
		return "(_Bool)0", nil
	case *syntax.IdentExpr:
		return g.identName(e.Name), nil
	case *syntax.ParenExpr:
		inner, err := g.exprStr(e.Inner)
		if err != nil {
			return "", err
		}
		return "(" + inner + ")", nil
	case *syntax.UnaryExpr:
		return g.unaryStr(e)
	case *syntax.BinaryExpr:
		return g.binaryStr(e)
	case *syntax.RuneLit:
		// Rune literal → integer constant. typeck classified it as byte
		// (codepoint < 128) or rune; cType picks the right C int width.
		if e.Type() == syntax.TByte() {
			return fmt.Sprintf("((uint8_t)%d)", e.Value), nil
		}
		return fmt.Sprintf("((int32_t)%d)", e.Value), nil
	case *syntax.ListLit:
		return g.listLitStr(e)
	case *syntax.TupleLit:
		return g.tupleLitStr(e)
	case *syntax.StructLit:
		return g.structLitStr(e)
	case *syntax.IndexExpr:
		return g.indexStr(e)
	case *syntax.SliceExpr:
		return g.sliceStr(e)
	case *syntax.FieldAccessExpr:
		return g.fieldAccessStr(e)
	case *syntax.CallExpr:
		return g.callStr(e)
	case *syntax.MethodCallExpr:
		return g.methodCallStr(e)
	case *syntax.ThisExpr:
		// `this` is the implicit receiver parameter inside a method body;
		// we emit it as a C identifier `z_this` (mangled like any local).
		return mangle("this"), nil
	case *syntax.EnumLit:
		return g.enumLitStr(e)
	case *syntax.NilLit:
		return g.nilLitStr(e)
	case *syntax.PropagateExpr:
		return g.propagateStr(e)
	case *syntax.CoalesceExpr:
		return g.coalesceStr(e)
	case *syntax.ChanConstructorExpr:
		return g.chanConstructorStr(e)
	case *syntax.RecvExpr:
		return g.recvStr(e)
	case *syntax.AnonFnExpr:
		return g.anonFnValueStr(e)
	}
	return "", fmt.Errorf("codegen: unhandled expression %T at %s", expr, expr.ExprPos())
}

// listLitStr emits a list literal as a C statement-expression that allocates
// a backing buffer, fills it with element values (each deep-copied), and
// returns a list-shape struct value.
//
// We use GCC/Clang's `({ ... })` statement-expression extension because
// constructing a list value inline in an arbitrary expression position
// otherwise requires a helper macro per shape. The PLAN's portability
// requirement is "compile under cc -fwrapv -O2 -lm" — both gcc and clang
// (the only two `cc` shipped today) support statement expressions.
func (g *cgen) listLitStr(e *syntax.ListLit) (string, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeList {
		return "", fmt.Errorf("codegen: list literal has non-list type at %s", e.Pos)
	}
	mname := g.mangleType(t)
	elem := g.cTypeName(t.Element)
	var b strings.Builder
	// cap == len at construction; the per-shape push helper doubles cap
	// when len catches up. Element values are NOT deep-copied at v0.3 —
	// the borrow checker has invalidated source bindings at any move
	// site, so sharing the underlying value is safe.
	fmt.Fprintf(&b, "({ %s __l; __l.len = %d; __l.cap = %d; ", mname, len(e.Elements), len(e.Elements))
	if len(e.Elements) == 0 {
		fmt.Fprintf(&b, "__l.data = (%s*)malloc(1); ", elem)
	} else {
		fmt.Fprintf(&b, "__l.data = (%s*)malloc(%d * sizeof(%s)); ", elem, len(e.Elements), elem)
		for i, sub := range e.Elements {
			s, err := g.exprStr(sub)
			if err != nil {
				return "", err
			}
			// v0.4: when the list element type is a spec, each concrete
			// element coerces to a fat pointer at the construction site.
			s = g.coerceCExpr(s, sub.Type(), t.Element)
			fmt.Fprintf(&b, "__l.data[%d] = %s; ", i, s)
		}
	}
	fmt.Fprintf(&b, "__l; })")
	return b.String(), nil
}

// tupleLitStr emits a tuple literal as a `(zerg_tuple_<...>){.e0 = ..., .e1 =
// ...}` compound literal. C99 designated initialisers handle the rest. At
// v0.3 we do NOT deep-copy composite elements — the borrow checker has
// invalidated source bindings at the move site.
func (g *cgen) tupleLitStr(e *syntax.TupleLit) (string, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeTuple {
		return "", fmt.Errorf("codegen: tuple literal has non-tuple type at %s", e.Pos)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "((%s){", g.mangleType(t))
	for i, sub := range e.Elements {
		if i > 0 {
			b.WriteString(", ")
		}
		s, err := g.exprStr(sub)
		if err != nil {
			return "", err
		}
		// v0.4: per-position spec coercion for tuple element types.
		if i < len(t.Tuple) {
			s = g.coerceCExpr(s, sub.Type(), t.Tuple[i])
		}
		fmt.Fprintf(&b, ".e%d = %s", i, s)
	}
	b.WriteString("})")
	return b.String(), nil
}

// structLitStr emits a struct literal via designated initialisers; field
// order follows declaration order so the C compiler's "missing field"
// warning would catch any drift.
func (g *cgen) structLitStr(e *syntax.StructLit) (string, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeStruct {
		return "", fmt.Errorf("codegen: struct literal has non-struct type at %s", e.Pos)
	}
	// Index user inits by name for lookup in declaration order.
	byName := map[string]syntax.Expr{}
	for _, init := range e.Fields {
		byName[init.Name] = init.Value
	}
	var b strings.Builder
	fmt.Fprintf(&b, "((%s){", g.mangleType(t))
	for i, f := range t.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		val, ok := byName[f.Name]
		if !ok {
			return "", fmt.Errorf("codegen: struct %q literal missing field %q at %s",
				t.Name, f.Name, e.Pos)
		}
		s, err := g.exprStr(val)
		if err != nil {
			return "", err
		}
		// v0.4: per-field spec coercion when the declared field type is a
		// spec (or contains one).
		s = g.coerceCExpr(s, val.Type(), f.Type)
		fmt.Fprintf(&b, ".%s = %s", mangleField(f.Name), s)
	}
	b.WriteString("})")
	return b.String(), nil
}

// indexStr emits `xs[i]` access. For lists it bounds-checks via a helper and
// returns a deep-copy of the element. For str it walks UTF-8 and returns the
// rune at codepoint position i.
func (g *cgen) indexStr(e *syntax.IndexExpr) (string, error) {
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	is, err := g.exprStr(e.Index)
	if err != nil {
		return "", err
	}
	rt := e.Receiver.Type()
	posStr := fmt.Sprintf("%d:%d", e.Pos.Line, e.Pos.Column)
	switch {
	case rt != nil && rt.Kind == syntax.TypeList:
		// Bounds-check via a statement-expression; the result aliases the
		// element in the underlying buffer (no implicit copy at v0.3).
		mname := g.mangleType(rt)
		return fmt.Sprintf(
			"({ %s __r = %s; int64_t __i = %s; zerg_index_check(__i, __r.len, %q); __r.data[__i]; })",
			mname, rs, is, posStr), nil
	case rt == syntax.TStr():
		return fmt.Sprintf("zerg_str_rune_at(%s, %s, %q)", rs, is, posStr), nil
	}
	return "", fmt.Errorf("codegen: cannot index %s at %s", rt, e.Pos)
}

// sliceStr lowers a SliceExpr to a per-shape slice helper call, taking care
// of open ends and inclusive bounds via small adjustments at the call site.
func (g *cgen) sliceStr(e *syntax.SliceExpr) (string, error) {
	rt := e.Receiver.Type()
	if rt == nil || rt.Kind != syntax.TypeList {
		return "", fmt.Errorf("codegen: cannot slice %s at %s", rt, e.Pos)
	}
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	// Build lo / hi expressions. We need to evaluate the receiver once to
	// reach .len for omitted high bounds, so wrap in a statement-expression.
	mname := g.mangleType(rt)
	posStr := fmt.Sprintf("%d:%d", e.Pos.Line, e.Pos.Column)
	var lo, hi string
	if e.Low != nil {
		s, err := g.exprStr(e.Low)
		if err != nil {
			return "", err
		}
		lo = s
	} else {
		lo = "INT64_C(0)"
	}
	if e.High != nil {
		s, err := g.exprStr(e.High)
		if err != nil {
			return "", err
		}
		if e.Inclusive {
			hi = fmt.Sprintf("(%s + INT64_C(1))", s)
		} else {
			hi = s
		}
	} else {
		// Use the receiver temp's .len; fold via statement-expression below.
		hi = "(int64_t)__rcv.len"
	}
	return fmt.Sprintf("({ %s __rcv = %s; %s_slice(__rcv, %s, %s, %q); })",
		mname, rs, mname, lo, hi, posStr), nil
}

// fieldAccessStr emits struct field access OR enum variant access. typeck
// has already disambiguated: a FieldAccessExpr whose receiver is a bare
// IdentExpr that resolves to an enum type is the variant form.
//
// v0.6: when the source spelled `?.`, the operator is safe-navigation —
// route to the dedicated lowering that produces the wrapped Option result.
func (g *cgen) fieldAccessStr(e *syntax.FieldAccessExpr) (string, error) {
	if e.Safe {
		return g.safeFieldAccessStr(e)
	}
	// v0.4: if typeck lowered this to an EnumLit (bare-variant construction),
	// route through the EnumLit emitter so the tag+union struct shape is
	// produced uniformly with the payloadful form.
	if e.Lowered != nil {
		return g.enumLitStr(e.Lowered)
	}
	if id, ok := e.Receiver.(*syntax.IdentExpr); ok {
		if rt := id.Type(); rt != nil && rt.Kind == syntax.TypeEnum {
			// Bare variant access without a Lowered EnumLit (e.g. inside a
			// match scrutinee or a context where typeck didn't lower).
			// Construct the compound literal directly.
			idx := variantIndex(rt, e.FieldName)
			return fmt.Sprintf("((%s){.tag = %d})", g.mangleType(rt), idx), nil
		}
	}
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	rt := e.Receiver.Type()
	if rt == nil || rt.Kind != syntax.TypeStruct {
		return "", fmt.Errorf("codegen: cannot access field on %s at %s", rt, e.Pos)
	}
	// Validate the field exists; the access itself is a direct member
	// reference (no implicit deep copy at v0.3).
	found := false
	for _, f := range rt.Fields {
		if f.Name == e.FieldName {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("codegen: struct %s has no field %q at %s", rt.Name, e.FieldName, e.Pos)
	}
	access := fmt.Sprintf("(%s).%s", rs, mangleField(e.FieldName))
	return access, nil
}

// callStr handles user-fn calls and the `len` / `clone` / `push` built-ins.
// At v0.3 fn-call composite args are implicit shared borrows — NO implicit
// deep copy at the call site. `clone(xs)` is the explicit opt-in for the
// v0.2-style deep copy; it remains the only call-site of the per-shape
// `_copy` helper.
func (g *cgen) callStr(e *syntax.CallExpr) (string, error) {
	// v0.7: anon-fn IIFE — `fn(args) -> R { body }(actual)`. The callee is
	// the AnonFnExpr itself; emit the env-on-stack + direct call to the
	// pre-registered top-level body fn. preregisterAnonFns has already
	// allocated the record in anonFnValue mode.
	if anon, ok := e.Callee.(*syntax.AnonFnExpr); ok {
		return g.iifeCallStr(anon, e.Args)
	}
	ident, ok := e.Callee.(*syntax.IdentExpr)
	if !ok {
		return "", fmt.Errorf("codegen: non-ident callee at %s", e.Pos)
	}
	// v0.7: a local binding may carry a TypeFn (an anon-fn captured in a
	// let). Calling such a binding routes through the fn-value pair:
	// cast .fn to the per-signature C fn pointer and invoke through .env.
	if t := ident.Type(); t != nil && t.Kind == syntax.TypeFn && g.lookupCurrentFn(ident.Name) == nil {
		return g.fnValueCallStr(ident, t, e.Args)
	}
	if ident.Name == "len" {
		if len(e.Args) != 1 {
			return "", fmt.Errorf("codegen: len expects 1 arg at %s", e.Pos)
		}
		argS, err := g.exprStr(e.Args[0])
		if err != nil {
			return "", err
		}
		// v0.14: len(str) and len(list) both report the size_t-backed
		// `.len` field as int64. For str that is the BYTE count (not
		// the rune count of the retired v0.2 reading) — matches
		// list[byte].len() semantics and what byte-oriented stdlib ops
		// (split, trim, replace) want. The zerg_str runtime layout
		// `{ const char *data; size_t len; }` makes the cast cheap.
		return fmt.Sprintf("((int64_t)((%s).len))", argS), nil
	}
	if ident.Name == "bytes" {
		// v0.14 str.bytes() — allocates a fresh list[byte] copy. Typeck
		// validated the arg is exactly one str; the helper handles
		// zero-length strs with a sentinel one-byte allocation.
		if len(e.Args) != 1 {
			return "", fmt.Errorf("codegen: bytes expects 1 arg at %s", e.Pos)
		}
		argS, err := g.exprStr(e.Args[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("zerg_str_bytes(%s)", argS), nil
	}
	if ident.Name == "to_str" {
		// v0.14 list[byte].to_str() — allocates a fresh str over a
		// byte copy. Typeck validated the arg is exactly one list[byte]
		// (the registry's tByte placeholder triggers assignableTo
		// rejection for list[int] / list[rune] / list[str]).
		if len(e.Args) != 1 {
			return "", fmt.Errorf("codegen: to_str expects 1 arg at %s", e.Pos)
		}
		argS, err := g.exprStr(e.Args[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("zerg_list_uint8_t_to_str(%s)", argS), nil
	}
	if ident.Name == "panic" {
		// v0.14 panic(msg) — writes "zerg: runtime: <msg>\n" to stderr
		// and exits with code 1. The runtime helper zerg_panic is in
		// the always-emitted runtime block (runtime.go); typeck pins
		// the arg as exactly one str.
		if len(e.Args) != 1 {
			return "", fmt.Errorf("codegen: panic expects 1 arg at %s", e.Pos)
		}
		argS, err := g.exprStr(e.Args[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("zerg_panic(%s)", argS), nil
	}
	if ident.Name == "clone" {
		// clone(x) returns a fresh deep copy of its composite argument.
		// typeck rejects primitives; the borrow checker has confirmed the
		// receiver is observed (not consumed). The emit is the existing
		// per-shape _copy helper — same path the v0.2 implicit-bind copy
		// took, just exposed under the user-visible builtin name.
		if len(e.Args) != 1 {
			return "", fmt.Errorf("codegen: clone expects 1 arg at %s", e.Pos)
		}
		argS, err := g.exprStr(e.Args[0])
		if err != nil {
			return "", err
		}
		return g.copyExpr(e.Args[0].Type(), argS), nil
	}
	if ident.Name == "close" {
		// v0.7 close(ch): route to the per-element chan_close helper.
		if len(e.Args) != 1 {
			return "", fmt.Errorf("codegen: close expects 1 arg at %s", e.Pos)
		}
		argT := e.Args[0].Type()
		if argT == nil || argT.Kind != syntax.TypeChan {
			return "", fmt.Errorf("codegen: close arg is not a channel at %s", e.Pos)
		}
		g.addChanShape(argT)
		argS, err := g.exprStr(e.Args[0])
		if err != nil {
			return "", err
		}
		cm := "zerg_chan_" + g.mangleType(argT.Element)
		return fmt.Sprintf("(%s_close(%s), 0)", cm, argS), nil
	}
	if ident.Name == "wait_group" {
		if len(e.Args) != 0 {
			return "", fmt.Errorf("codegen: wait_group expects 0 args at %s", e.Pos)
		}
		return "zerg_wait_group_make()", nil
	}
	if ident.Name == "push" {
		// push(xs, v) appends v to xs in place via the per-shape grow
		// helper, which doubles cap when len catches up. typeck has
		// required xs to be a top-level mut-bound list ident; the borrow
		// checker has validated state.
		if len(e.Args) != 2 {
			return "", fmt.Errorf("codegen: push expects 2 args at %s", e.Pos)
		}
		id, ok := e.Args[0].(*syntax.IdentExpr)
		if !ok {
			return "", fmt.Errorf("codegen: push first arg must be ident at %s", e.Pos)
		}
		valS, err := g.exprStr(e.Args[1])
		if err != nil {
			return "", err
		}
		listT := e.Args[0].Type()
		if listT == nil || listT.Kind != syntax.TypeList {
			return "", fmt.Errorf("codegen: push first arg must be list at %s", e.Pos)
		}
		nameS := mangle(id.Name)
		// `_push` returns void; we emit it as an expression so the caller
		// (an ExprStmt-wrapped call) compiles. Wrap in `(<call>, 0)` so
		// the comma expression has a non-void value, matching how the
		// previous lowering shaped the expression position.
		expr := fmt.Sprintf("(%s_push(&%s, %s), 0)", g.mangleType(listT), nameS, valS)
		return expr, nil
	}
	// v0.6: a CallExpr whose callee resolved to a generic-fn instantiation
	// carries the specialised FnDecl on Specialised. Route through it so
	// the emitted C symbol matches the monomorphised name.
	fn := e.Specialised
	if fn == nil {
		// v0.5: resolve the fn against the active module so the call site
		// uses the right module-mangled C symbol name and the right param
		// types for spec coercion. fnTable holds the entry module's fns;
		// otherwise the current module's fn list is the lookup source.
		fn = g.lookupCurrentFn(ident.Name)
	}
	var paramTypes []*syntax.Type
	if fn != nil {
		for _, p := range fn.Params {
			if p.Type != nil {
				paramTypes = append(paramTypes, p.Type.Resolved)
			} else {
				paramTypes = append(paramTypes, nil)
			}
		}
	}
	args, err := g.coerceArgs(e.Args, paramTypes)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if fn != nil {
		sb.WriteString(g.fnCName(fn))
	} else {
		sb.WriteString(mangle(ident.Name))
	}
	sb.WriteByte('(')
	for i, a := range args {
		if i > 0 {
			sb.WriteString(", ")
		}
		// At v0.3 fn-call composite args are implicit shared borrows —
		// no deep copy. The borrow checker has confirmed the caller's
		// binding remains valid for the call duration. v0.4 adds spec
		// coercion via coerceArgs above.
		sb.WriteString(a)
	}
	sb.WriteByte(')')
	return sb.String(), nil
}

// lookupCurrentFn returns the FnDecl with the given name in the currently
// emitting module (or in the entry module when no current module is set,
// e.g. mid-collectShapes). Returns nil for built-ins (`len`, `clone`,
// `push`) and unknown names.
func (g *cgen) lookupCurrentFn(name string) *syntax.FnDecl {
	if g == nil {
		return nil
	}
	mangle := g.currentMod
	if mangle == "" {
		mangle = g.entryMangle
	}
	if me := g.moduleByName[mangle]; me != nil {
		for _, stmt := range me.prog.Statements {
			if fn, ok := stmt.(*syntax.FnDecl); ok && fn.Name == name {
				return fn
			}
		}
	}
	if fn, ok := g.fnTable[name]; ok {
		return fn
	}
	return nil
}

// unaryStr lowers -, ~, not.
func (g *cgen) unaryStr(e *syntax.UnaryExpr) (string, error) {
	inner, err := g.exprStr(e.Operand)
	if err != nil {
		return "", err
	}
	switch e.Op {
	case syntax.UnaryNeg:
		return fmt.Sprintf("(-%s)", inner), nil
	case syntax.UnaryBitNot:
		return fmt.Sprintf("(~%s)", inner), nil
	case syntax.UnaryNot:
		return fmt.Sprintf("(!%s)", inner), nil
	}
	return "", fmt.Errorf("codegen: unknown unary op %s at %s", e.Op, e.Pos)
}

// binaryStr lowers each binary operator. Identical to v0.1 with the addition
// that byte/rune comparisons fall through to integer compares (already true
// for `==` because TByte/TRune are tracked as primitives in typeck).
func (g *cgen) binaryStr(e *syntax.BinaryExpr) (string, error) {
	left, err := g.exprStr(e.Left)
	if err != nil {
		return "", err
	}
	right, err := g.exprStr(e.Right)
	if err != nil {
		return "", err
	}
	lt := e.Left.Type()
	switch e.Op {
	case syntax.BinAdd:
		if lt == syntax.TStr() {
			return fmt.Sprintf("zerg_str_concat(%s, %s)", left, right), nil
		}
		return infix(left, "+", right), nil
	case syntax.BinSub:
		return infix(left, "-", right), nil
	case syntax.BinMul:
		return infix(left, "*", right), nil
	case syntax.BinDiv:
		return infix(left, "/", right), nil
	case syntax.BinFloorDiv:
		if lt == syntax.TFloat() {
			return fmt.Sprintf("floor((%s) / (%s))", left, right), nil
		}
		return infix(left, "/", right), nil
	case syntax.BinMod:
		if lt == syntax.TFloat() {
			return fmt.Sprintf("fmod((%s), (%s))", left, right), nil
		}
		return infix(left, "%", right), nil
	case syntax.BinBitAnd:
		return infix(left, "&", right), nil
	case syntax.BinBitOr:
		return infix(left, "|", right), nil
	case syntax.BinBitXor:
		return infix(left, "^", right), nil
	case syntax.BinShl:
		return infix(left, "<<", right), nil
	case syntax.BinShr:
		return infix(left, ">>", right), nil
	case syntax.BinEq:
		if lt == syntax.TStr() {
			return fmt.Sprintf("zerg_str_eq(%s, %s)", left, right), nil
		}
		if lt != nil {
			switch lt.Kind {
			case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
				return fmt.Sprintf("%s_eq(%s, %s)", g.mangleType(lt), left, right), nil
			}
		}
		return infix(left, "==", right), nil
	case syntax.BinNE:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(!zerg_str_eq(%s, %s))", left, right), nil
		}
		if lt != nil {
			switch lt.Kind {
			case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
				return fmt.Sprintf("(!%s_eq(%s, %s))", g.mangleType(lt), left, right), nil
			}
		}
		return infix(left, "!=", right), nil
	case syntax.BinLT:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) < 0)", left, right), nil
		}
		return infix(left, "<", right), nil
	case syntax.BinGT:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) > 0)", left, right), nil
		}
		return infix(left, ">", right), nil
	case syntax.BinLE:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) <= 0)", left, right), nil
		}
		return infix(left, "<=", right), nil
	case syntax.BinGE:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) >= 0)", left, right), nil
		}
		return infix(left, ">=", right), nil
	case syntax.BinAnd:
		return infix(left, "&&", right), nil
	case syntax.BinOr:
		return infix(left, "||", right), nil
	case syntax.BinXor:
		return fmt.Sprintf("((_Bool)(%s) ^ (_Bool)(%s))", left, right), nil
	}
	return "", fmt.Errorf("codegen: unknown binary op %s at %s", e.Op, e.Pos)
}

func infix(left, op, right string) string {
	return "(" + left + " " + op + " " + right + ")"
}

// ---------------------------------------------------------------------------
// Type-name and identifier mangling.
// ---------------------------------------------------------------------------

// cTypeName maps a Zerg type to its C representation.
//
//   - int   → int64_t
//   - float → double
//   - bool  → _Bool
//   - str   → zerg_str
//   - byte  → uint8_t
//   - rune  → int32_t
//   - list[T] / tuple[...] / struct Name / enum Name → mangled per-shape name
//
// v0.5: struct / enum / spec names carry the owning-module mangle prefix
// — see g.mangleType for the composition.
func (g *cgen) cTypeName(t *syntax.Type) string {
	if t == nil {
		return "void"
	}
	switch t {
	case syntax.TInt():
		return "int64_t"
	case syntax.TFloat():
		return "double"
	case syntax.TBool():
		return "_Bool"
	case syntax.TStr():
		return "zerg_str"
	case syntax.TByte():
		return "uint8_t"
	case syntax.TRune():
		return "int32_t"
	case syntax.TVoid():
		return "void"
	case syntax.TNever():
		// v0.9 Unit 1: `never` lowers to C `void`. The fn-decl's
		// `__attribute__((noreturn))` (writeFnSig) makes the C compiler
		// accept the absence of a return value.
		return "void"
	}
	switch t.Kind {
	case syntax.TypeStruct:
		// v0.7 synthetic WaitGroup is a runtime-owned handle, not a value
		// struct emitted by the shape registry.
		if t.Name == "WaitGroup" {
			return "zerg_wait_group_t *"
		}
		return g.mangleType(t)
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeEnum, syntax.TypeSpec:
		return g.mangleType(t)
	case syntax.TypeChan:
		// Channel handles are pointers to a per-element chan struct so the
		// runtime helpers can mutate the shared state.
		return "zerg_chan_" + g.mangleType(t.Element) + " *"
	case syntax.TypeFn:
		// v0.7 fn-values bind through a (fn-ptr, env-ptr) pair — the call
		// site casts .fn to a per-signature C fn-pointer type.
		return "zerg_fn_value"
	}
	return "void"
}

// mangleType returns a stable C identifier for any Zerg type, suitable for
// use as a typedef name and as a suffix on per-shape helpers.
//
// v0.5: composite types (struct / enum / spec) gain a module-mangle prefix
// per PLAN.md §Mangling for codegen — `zerg_struct_<modmangle>__<Name>`
// etc. The cgen consults g.typeOwner / g.specOwner to find the owning
// module for a canonical *Type pointer or a spec name. List / tuple
// mangling stays purely structural — they have no decl-site and no
// owning module.
//
// Mangling rules:
//
//   - int   → "int64_t"
//   - float → "double"
//   - bool  → "_Bool" — used inline only; not a valid typedef leaf, but composite
//     wrappers don't use it as the final component.
//   - str   → "zerg_str"
//   - byte  → "uint8_t"
//   - rune  → "int32_t"
//   - list[T] → "zerg_list_<mangle(T)>"
//   - tuple[T1,T2] → "zerg_tuple_<mangle(T1)>_<mangle(T2)>"
//   - struct Name (mod = M) → "zerg_struct_<M>__<Name>"
//   - enum Name (mod = M)   → "zerg_enum_<M>__<Name>"
//   - spec Name (mod = M)   → "zerg_dyn_<M>__<Name>"
//
// Mangling is purely structural for list/tuple — two `list[int]` constructed
// at different sites produce identical names. Struct/enum/spec mangle by
// (owner-mangle, name); typeck guarantees one canonical type per
// (owner, name).
func (g *cgen) mangleType(t *syntax.Type) string {
	if t == nil {
		return "void"
	}
	switch t {
	case syntax.TInt():
		return "int64_t"
	case syntax.TFloat():
		return "double"
	case syntax.TBool():
		return "bool" // _Bool is not a valid identifier suffix
	case syntax.TStr():
		return "zerg_str"
	case syntax.TByte():
		return "uint8_t"
	case syntax.TRune():
		return "int32_t"
	}
	switch t.Kind {
	case syntax.TypeList:
		return "zerg_list_" + g.mangleType(t.Element)
	case syntax.TypeTuple:
		var parts []string
		for _, e := range t.Tuple {
			parts = append(parts, g.mangleType(e))
		}
		return "zerg_tuple_" + strings.Join(parts, "_")
	case syntax.TypeStruct:
		return "zerg_struct_" + g.typeMangle(t) + "__" + sanitizeGenericName(t.Name)
	case syntax.TypeEnum:
		return "zerg_enum_" + g.typeMangle(t) + "__" + sanitizeGenericName(t.Name)
	case syntax.TypeSpec:
		return "zerg_dyn_" + g.specMangle(t.Name) + "__" + sanitizeGenericName(t.Name)
	case syntax.TypeChan:
		// Chan mangle is structural per element type, mirroring list/tuple.
		// The trailing `_t` distinguishes the chan struct from the element
		// type's own mangle so the C identifier is unique.
		return "zerg_chan_" + g.mangleType(t.Element) + "_handle"
	case syntax.TypeFn:
		// TypeFn shows up in capture lists for anon-fn analysis; codegen
		// never emits a fn-value handle at v0.7 (anon-fns are inlined or
		// captured-as-spawn). Fall through to a stub identifier.
		return "zerg_fn_value"
	}
	return "void"
}

// sanitizeGenericName converts a canonical type Name (which may carry the v0.6
// monomorphisation bracket suffix `Box[int]` or `Result[int,str]`) into a
// valid C identifier per PLAN.md §Generic monomorphization. The
// transformation: `[` → `__`, `]` → dropped, `,` → `_`, whitespace dropped.
//
// Examples:
//
//	Box[int]                       → Box__int
//	Result[int,str]                → Result__int_str
//	Option[Result[int,str]]        → Option__Result__int_str
//	Box[list[int]]                 → Box__zerg_list_int64_t — but list[]
//	                                  inside Name is the bare "list[int]"
//	                                  text, so we get Box__list__int (the
//	                                  list mangle is applied via mangleType
//	                                  on the *Type itself, not via the
//	                                  Name suffix).
//
// The Name suffix uses Type.String() per monoEnumArgsSig — that is the
// printable form, not the C-mangled form. We reuse it here because the
// cache key invariant guarantees identical instances share identical Names,
// and the resulting C identifier is structurally unique per instance.
func sanitizeGenericName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '[':
			b.WriteString("__")
		case ']':
			// drop
		case ',':
			b.WriteByte('_')
		case ' ', '\t':
			// drop whitespace
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// typeMangle returns the owning-module's mangle prefix for a struct/enum
// canonical *Type pointer. Falls back to the entry module's mangle (or a
// generic "main" stub when the cgen has no entry recorded) so the helper
// stays defined for tests that bypass EmitBundle.
//
// v0.6: built-in Option / Result instances live under the pseudo-module
// `<builtin>` and mangle to the literal `zerg_builtin` (no FNV hash) per
// PLAN.md §Built-in mangle. Detection is by Name prefix; the mono cache
// shape uses `Option[...]` / `Result[...]` for built-in instances.
func (g *cgen) typeMangle(t *syntax.Type) string {
	if isBuiltinGenericType(t) {
		return "zerg_builtin"
	}
	if g != nil {
		if m, ok := g.typeOwner[t]; ok && m != "" {
			return m
		}
		if g.entryMangle != "" {
			return g.entryMangle
		}
	}
	return mangleModule("main")
}

// isBuiltinGenericType reports whether t is a monomorphized instance of a
// built-in generic enum (Option or Result). The detection is by Name prefix
// — the built-in synthesis (typeck_v06_builtin.go) builds the canonical
// Name as `Option[...]` / `Result[...]` for every instantiation.
func isBuiltinGenericType(t *syntax.Type) bool {
	if t == nil || t.Kind != syntax.TypeEnum {
		return false
	}
	return strings.HasPrefix(t.Name, "Option[") || strings.HasPrefix(t.Name, "Result[")
}

// specMangle returns the owning-module's mangle prefix for a spec name.
// Falls back to the entry mangle when the spec is not registered (e.g.
// pre-bundle test paths).
func (g *cgen) specMangle(name string) string {
	if g != nil {
		if m, ok := g.specOwner[name]; ok && m != "" {
			return m
		}
		if g.entryMangle != "" {
			return g.entryMangle
		}
	}
	return mangleModule("main")
}

// mangle prefixes Zerg variable / function names with `z_` so they cannot
// clash with C keywords or runtime helpers.
func mangle(name string) string {
	return "z_" + name
}

// emitImportedModuleGlobals writes file-scope C statics for every
// non-entry module's top-level let / mut / const bindings. Symbol form
// is `z_<modmangle>__<name>` so identName's lookup table and the emit
// here agree. The initializer must be a C constant expression — the
// helper switches g.currentMod temporarily so g.exprStr resolves any
// self-referential top-level binding (e.g. `const NEXT := PREV + 1`)
// to the module-mangled form before the file-scope decl emits.
//
// Modules emit in declaration order within each module; ordering
// across modules follows g.modules. A non-constant initializer surfaces
// as a C compile error at the file-scope decl — the diagnostic is
// adequate for v0.14 (a runtime-init hook can land when a real use
// case demands non-constant module-init).
func (g *cgen) emitImportedModuleGlobals() error {
	prevMod := g.currentMod
	defer func() { g.currentMod = prevMod }()
	wroteAny := false
	for i := range g.modules {
		me := &g.modules[i]
		if me.mangle == g.entryMangle {
			continue
		}
		if len(me.topLevelVars) == 0 {
			continue
		}
		g.currentMod = me.mangle
		for _, stmt := range me.prog.Statements {
			var name string
			var ref *syntax.TypeRef
			var value syntax.Expr
			var isConst bool
			switch s := stmt.(type) {
			case *syntax.LetStmt:
				if s.Name == "" {
					continue
				}
				name, ref, value = s.Name, s.Type, s.Value
			case *syntax.MutStmt:
				if s.Name == "" {
					continue
				}
				name, ref, value = s.Name, s.Type, s.Value
			case *syntax.ConstStmt:
				if s.Name == "" {
					continue
				}
				name, ref, value, isConst = s.Name, s.Type, s.Value, true
			default:
				continue
			}
			t := value.Type()
			declT := t
			if ref != nil && ref.Resolved != nil {
				declT = ref.Resolved
			}
			if declT == nil {
				return fmt.Errorf("codegen: missing type for module-level %q in %s", name, me.mangle)
			}
			exprS, err := g.exprStr(value)
			if err != nil {
				return err
			}
			if isConst {
				g.b.WriteString("static const ")
			} else {
				g.b.WriteString("static ")
			}
			fmt.Fprintf(&g.b, "%s z_%s__%s = %s;\n", g.cTypeName(declT), me.mangle, name, exprS)
			wroteAny = true
		}
	}
	if wroteAny {
		g.b.WriteString("\n")
	}
	return nil
}

// identName returns the C identifier for a Zerg name as referenced inside
// the currently-emitting module's fn body. If the name resolves to a
// top-level let / mut / const of that module (registered in moduleEmit.
// topLevelVars), the module-mangled form `z_<mangle>__<name>` is returned
// so the C symbol matches the file-scope static emitted by
// emitImportedModuleGlobals. Otherwise the bare `z_<name>` form is used
// — that covers fn-locals, parameters, and any name that doesn't shadow
// a module-level binding. Local shadowing of a module-level binding is
// currently undefined under cgen (the binding promotion path doesn't
// track local scope); typeck would not have rejected the shadow but it
// will compile to the module-mangled global access.
func (g *cgen) identName(name string) string {
	if g.currentMod != "" && g.currentMod != g.entryMangle {
		if me := g.moduleByName[g.currentMod]; me != nil && me.topLevelVars[name] {
			return "z_" + g.currentMod + "__" + name
		}
	}
	return mangle(name)
}

// mangleField prefixes struct field names with `f_` so a field named `for`
// or `int` does not collide with a C keyword.
func mangleField(name string) string {
	return "f_" + name
}

// cQuote returns a C string literal whose runtime value equals s. Non-printable
// bytes are emitted as octal escapes so the output is portable across compilers.
func cQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ---------------------------------------------------------------------------
// v0.4 — specs, impls, vtables, method calls, fat pointers, NotImplemented.
//
// Layout:
//
//   * Per spec: a `zerg_dyn_<Spec>` struct typedef carrying (data, vt) plus
//     a `struct zerg_vtable_<Spec>` listing one function pointer per method
//     in declaration order (typeck pinned the order for determinism).
//
//   * Per (Type, Spec) pair an impl block exists for: a static const
//     `zerg_vt_<Type>_<Spec>` initialised with each method's resolution —
//     impl override > spec default specialised to this Type > NotImplemented
//     stub specialised to this Type / method / spec / pos.
//
//   * Per inherent impl method: a static C fn `zerg_struct_<T>__<method>` (or
//     `zerg_enum_...`) taking `<MangledType> z_this` as the first parameter.
//
//   * Per spec impl method: a static C fn
//     `zerg_struct_<T>__<Spec>__<method>` likewise. The fn pointer in the
//     vtable wraps this through a `void* this` adapter so the vtable type is
//     spec-uniform regardless of which Type provides the method.
//
//   * Spec coercion (let/arg/return/list-elem/tuple-pos/struct-field/
//     enum-payload of spec type) wraps the concrete value in a fat pointer.
//     The wrapped value is heap-boxed so the fat pointer can outlive the
//     stack frame the source value lives in (mirrors run.go's specVal which
//     holds *Value).
// ---------------------------------------------------------------------------

// collectSpecsImpls walks top-level statements once to populate the
// spec-decl, inherent-impl, and spec-impl tables that every later v0.4 emit
// pass consults. Mirrors the interpreter's two-pass collection in newInterp.
func (g *cgen) collectSpecsImpls(prog *syntax.Program) {
	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *syntax.SpecDecl:
			if _, dup := g.specs[s.Name]; !dup {
				g.specOrder = append(g.specOrder, s.Name)
			}
			g.specs[s.Name] = s
		case *syntax.ImplDecl:
			// v0.6: generic impl blocks (`impl[T] Box[T] for ...`) are
			// expanded by typeck into per-instantiation synthetic ImplDecls
			// surfaced via prog.MonoImpls. Skip the generic decl itself —
			// its TypeRefs reference impl-level type-params and have no
			// concrete C representation.
			if len(s.TypeParams) > 0 {
				continue
			}
			// Resolve the receiver type by name. Any impl reaching codegen
			// has been validated by typeck, so the type resolution lookup
			// below is best-effort — if it fails we fall back to the methods
			// alone, which still emit the C fn but with no vtable wrapper.
			//
			// Prefer the canonical *Type pointer typeck stamped on
			// s.Receiver (Unit 6.5). It is the same pointer keying every
			// dispatch site, so two modules' same-named receivers stay
			// distinct here even when neither module surfaces the type
			// from a value-site expression.
			var receiverT *syntax.Type
			if s.Receiver != nil {
				receiverT = s.Receiver
			} else {
				receiverT = g.lookupReceiverType(prog, s.Type)
			}
			implKeyName := s.Type
			if receiverT != nil {
				// Disambiguate same-name types across modules by qualifying
				// the impl-table key with the owning module's mangle.
				if owner, ok := g.typeOwner[receiverT]; ok && owner != "" {
					implKeyName = owner + "__" + s.Type
				}
				g.receiverTypes[implKeyName] = receiverT
			}
			if s.Spec == "" {
				if _, ok := g.inherent[implKeyName]; !ok {
					g.inherentTypeOrder = append(g.inherentTypeOrder, implKeyName)
				}
				g.inherent[implKeyName] = append(g.inherent[implKeyName], s.Methods...)
			} else {
				key := implKey{typeName: implKeyName, specName: s.Spec}
				if _, ok := g.specImpls[key]; !ok {
					g.specImplKeys = append(g.specImplKeys, key)
				}
				g.specImpls[key] = s
				// Mark the spec as used so the fat-pointer typedef + vtable
				// struct emit even if the program never binds a spec-typed
				// value directly.
				g.specsUsed[s.Spec] = true
			}
		}
	}
	// v0.6: walk per-instantiation synthetic ImplDecls produced by typeck's
	// generic-impl expansion. Each one carries a Receiver pointing at the
	// concrete monomorphised *Type and methods cloned with substituted
	// types. Routes through the same inherent / specImpls tables so the
	// downstream emit pipeline produces one C function per
	// (impl-method, mono-receiver) tuple.
	for _, s := range prog.MonoImpls {
		if s == nil || s.Receiver == nil {
			continue
		}
		receiverT := s.Receiver
		// Stamp typeOwner for the mono receiver so the implKey + later
		// mangle calls produce the owning module's prefix.
		if _, set := g.typeOwner[receiverT]; !set {
			for i := range g.modules {
				if g.modules[i].prog == prog {
					g.typeOwner[receiverT] = g.modules[i].mangle
					break
				}
			}
		}
		implKeyName := g.implKeyForType(receiverT)
		g.receiverTypes[implKeyName] = receiverT
		if s.Spec == "" {
			if _, ok := g.inherent[implKeyName]; !ok {
				g.inherentTypeOrder = append(g.inherentTypeOrder, implKeyName)
			}
			g.inherent[implKeyName] = append(g.inherent[implKeyName], s.Methods...)
		} else {
			key := implKey{typeName: implKeyName, specName: s.Spec}
			if _, ok := g.specImpls[key]; !ok {
				g.specImplKeys = append(g.specImplKeys, key)
			}
			g.specImpls[key] = s
			g.specsUsed[s.Spec] = true
		}
	}
}

// lookupReceiverType resolves a type name to its canonical *Type. Prefers
// the typeck-stamped canonical pointer recovered via findCanonicalTypeRef
// so the receiver type pointer matches the one used at every literal /
// field-access site in the program. Falls back to constructing a fresh
// stand-in *Type when no stamp is recoverable (e.g. an impl-only module
// that never uses its own type from a value site); the stand-in is
// registered into g.typeOwner so subsequent mangle calls produce the
// owner-prefixed name.
func (g *cgen) lookupReceiverType(prog *syntax.Program, name string) *syntax.Type {
	// Try the canonical stamp first — same module's program walk.
	if t := findCanonicalTypeRef(prog, name, syntax.TypeStruct); t != nil {
		return t
	}
	if t := findCanonicalTypeRef(prog, name, syntax.TypeEnum); t != nil {
		return t
	}
	// Owner-aware bundle-wide search: when the impl's local prog walk
	// finds nothing (impl body never references the receiver locally),
	// look across the typeOwner map for a *Type whose stamped owner is
	// the SAME module as `prog`. This keeps two modules' identically-
	// named structs from collapsing onto the first one found.
	progMangle := ""
	for i := range g.modules {
		if g.modules[i].prog == prog {
			progMangle = g.modules[i].mangle
			break
		}
	}
	if progMangle != "" {
		for tp, mg := range g.typeOwner {
			if mg != progMangle || tp == nil {
				continue
			}
			if tp.Name != name {
				continue
			}
			if tp.Kind == syntax.TypeStruct || tp.Kind == syntax.TypeEnum {
				return tp
			}
		}
	}
	// Bundle-wide search: typeck wires cross-module type references to the
	// declaring module's canonical *Type, so the same canonical pointer
	// might be reachable from another module's program (e.g. main calls
	// `util.Counter{...}` while util only declares Counter).
	for i := range g.modules {
		me := &g.modules[i]
		if t := findCanonicalTypeRef(me.prog, name, syntax.TypeStruct); t != nil {
			return t
		}
		if t := findCanonicalTypeRef(me.prog, name, syntax.TypeEnum); t != nil {
			return t
		}
	}
	// Fall back: build a stand-in. Register the stand-in's owner so
	// downstream mangle calls produce the right module prefix. The owner
	// for the stand-in is whichever module the prog argument belongs to —
	// recover the mangle by scanning g.modules.
	standin := func(t *syntax.Type) *syntax.Type {
		for i := range g.modules {
			if g.modules[i].prog == prog {
				g.typeOwner[t] = g.modules[i].mangle
				return t
			}
		}
		return t
	}
	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *syntax.StructDecl:
			if s.Name == name {
				fields := make([]syntax.NamedField, len(s.Fields))
				for i, f := range s.Fields {
					if f.Type != nil && f.Type.Resolved != nil {
						fields[i] = syntax.NamedField{Name: f.Name, Type: f.Type.Resolved}
					}
				}
				return standin(syntax.NewStructType(s.Name, fields))
			}
		case *syntax.EnumDecl:
			if s.Name == name {
				vs := make([]string, len(s.Variants))
				for i, v := range s.Variants {
					vs[i] = v.Name
				}
				return standin(syntax.NewEnumType(s.Name, vs))
			}
		}
	}
	return nil
}

// collectSpecsInType records every spec-typed leaf reachable from t into
// g.specsUsed. Walks list/tuple/struct/enum-payload composites recursively
// so a `list[Printable]` registers the Printable spec for fat-pointer +
// vtable emission.
func (g *cgen) collectSpecsInType(t *syntax.Type) {
	if t == nil {
		return
	}
	switch t.Kind {
	case syntax.TypeSpec:
		g.specsUsed[t.Name] = true
	case syntax.TypeList:
		g.collectSpecsInType(t.Element)
	case syntax.TypeTuple:
		for _, e := range t.Tuple {
			g.collectSpecsInType(e)
		}
	case syntax.TypeStruct:
		for _, f := range t.Fields {
			g.collectSpecsInType(f.Type)
		}
	case syntax.TypeEnum:
		for _, payload := range t.VariantPayloads {
			for _, pt := range payload {
				g.collectSpecsInType(pt)
			}
		}
	}
}

// emitSpecForwardDecls writes the fat-pointer typedef and vtable struct
// for every spec used by the program (declared by `spec ...`, referenced by
// `x: Spec = ...`, exposed by an `impl Type for Spec`, etc.). Method
// signatures inside the vtable struct take `void* this` so the vtable type
// is spec-uniform regardless of which Type provides the implementation.
func (g *cgen) emitSpecForwardDecls() {
	// Union the declared specs and the used specs into a single declaration-
	// order list; specs declared but never used still emit (the vtable struct
	// is harmless when no impl blocks reference it).
	seen := map[string]bool{}
	var order []string
	for _, n := range g.specOrder {
		if !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	for _, n := range g.specOrder {
		if g.specsUsed[n] && !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	if len(order) == 0 {
		return
	}
	g.b.WriteString("/* Spec fat-pointer typedefs and vtable struct definitions (v0.4). */\n")
	for _, name := range order {
		s := g.specs[name]
		specPrefix := g.specMangle(name) + "__" + name
		// Vtable struct: one function pointer per spec method in declaration
		// order; each takes `void* this` plus the declared param types.
		fmt.Fprintf(&g.b, "struct zerg_vtable_%s {\n", specPrefix)
		if s == nil || len(s.Methods) == 0 {
			// An empty spec still gets a struct so sizeof(zerg_dyn_<Spec>)
			// is well-defined; emit a placeholder field.
			fmt.Fprintf(&g.b, "    char _empty;\n")
		} else {
			for _, m := range s.Methods {
				ret := "void"
				if m.Return != nil && m.Return.Resolved != nil && m.Return.Resolved != syntax.TVoid() {
					ret = g.cTypeName(m.Return.Resolved)
				}
				fmt.Fprintf(&g.b, "    %s (*%s)(void* z_this", ret, m.Name)
				for _, p := range m.Params {
					if p.Type != nil && p.Type.Resolved != nil {
						fmt.Fprintf(&g.b, ", %s %s", g.cTypeName(p.Type.Resolved), mangle(p.Name))
					}
				}
				fmt.Fprintf(&g.b, ");\n")
			}
		}
		fmt.Fprintf(&g.b, "};\n")
		// Fat pointer: data + vtable.
		fmt.Fprintf(&g.b, "typedef struct { void* data; const struct zerg_vtable_%s* vt; } zerg_dyn_%s;\n",
			specPrefix, specPrefix)
	}
	g.b.WriteString("\n")
}

// emitSpecVtablesAndMethods is the v0.4 file-scope emit pass. Order:
//
//  1. Forward-declare every emitted method C function so they can reference
//     each other in any order.
//  2. Emit each method body — both inherent and per-(Type, Spec) override.
//     A spec impl method gets one C fn whose receiver is the concrete
//     mangled type, plus a small `void*` adapter wrapper that downcasts to
//     the concrete and forwards. The adapter is what the vtable points at.
//  3. Emit the per-(Type, Spec) static const vtable initialiser populated
//     with adapter pointers for impl overrides, type-specialised default
//     adapters for spec defaults, and NotImplemented stubs for the
//     remainder.
func (g *cgen) emitSpecVtablesAndMethods() {
	if len(g.inherentTypeOrder) == 0 && len(g.specImplKeys) == 0 && len(g.specOrder) == 0 {
		return
	}
	// Stable ordering: inherent first by declaration order of types, then
	// (Type, Spec) impls by declaration order.
	g.b.WriteString("/* v0.4 method functions, vtable adapters, and per-(Type, Spec) vtables. */\n")

	// Forward decls.
	for _, typeName := range g.inherentTypeOrder {
		recv := g.receiverTypes[typeName]
		if recv == nil {
			continue
		}
		for _, fn := range g.inherent[typeName] {
			g.writeMethodSig(recv, "", fn)
			g.b.WriteString(";\n")
		}
	}
	for _, key := range g.specImplKeys {
		recv := g.receiverTypes[key.typeName]
		if recv == nil {
			continue
		}
		decl := g.specImpls[key]
		spec := g.specs[key.specName]
		specPrefix := g.specMangle(key.specName) + "__" + key.specName
		for _, fn := range decl.Methods {
			g.writeMethodSig(recv, key.specName, fn)
			g.b.WriteString(";\n")
			// Adapter forward decl.
			fmt.Fprintf(&g.b, "static %s zerg_adapter_%s__%s__%s(void* z_this",
				g.returnCType(fn.Return), g.mangleType(recv), specPrefix, fn.Name)
			for _, p := range fn.Params {
				if p.Type != nil && p.Type.Resolved != nil {
					fmt.Fprintf(&g.b, ", %s %s", g.cTypeName(p.Type.Resolved), mangle(p.Name))
				}
			}
			g.b.WriteString(");\n")
		}
		// Default-method adapters: for any spec method NOT overridden, we
		// either need a Type-specialised default adapter (if the spec
		// supplies a default body) or a NotImplemented stub.
		if spec != nil {
			for _, sm := range spec.Methods {
				overridden := false
				for _, fn := range decl.Methods {
					if fn.Name == sm.Name {
						overridden = true
						break
					}
				}
				if overridden {
					continue
				}
				if sm.Body != nil {
					// Type-specialised default-body adapter.
					fmt.Fprintf(&g.b, "static %s zerg_default_%s__%s__%s(void* z_this",
						g.returnCType(sm.Return), g.mangleType(recv), specPrefix, sm.Name)
					for _, p := range sm.Params {
						if p.Type != nil && p.Type.Resolved != nil {
							fmt.Fprintf(&g.b, ", %s %s", g.cTypeName(p.Type.Resolved), mangle(p.Name))
						}
					}
					g.b.WriteString(");\n")
				} else {
					// NotImplemented stub.
					fmt.Fprintf(&g.b, "static %s zerg_not_impl_%s__%s__%s(void* z_this",
						g.returnCType(sm.Return), g.mangleType(recv), specPrefix, sm.Name)
					for _, p := range sm.Params {
						if p.Type != nil && p.Type.Resolved != nil {
							fmt.Fprintf(&g.b, ", %s %s", g.cTypeName(p.Type.Resolved), mangle(p.Name))
						}
					}
					g.b.WriteString(");\n")
				}
			}
		}
	}
	g.b.WriteString("\n")

	// Method bodies — inherent.
	for _, typeName := range g.inherentTypeOrder {
		recv := g.receiverTypes[typeName]
		if recv == nil {
			continue
		}
		for _, fn := range g.inherent[typeName] {
			if err := g.emitMethodFn(recv, "", fn); err != nil {
				// Should not happen post-typeck; emit a comment so the C
				// compiler error points at the right method.
				fmt.Fprintf(&g.b, "/* codegen error in %s::%s: %v */\n", typeName, fn.Name, err)
			}
			g.b.WriteString("\n")
		}
	}
	// Method bodies — spec impls + adapters.
	for _, key := range g.specImplKeys {
		recv := g.receiverTypes[key.typeName]
		if recv == nil {
			continue
		}
		decl := g.specImpls[key]
		spec := g.specs[key.specName]
		for _, fn := range decl.Methods {
			if err := g.emitMethodFn(recv, key.specName, fn); err != nil {
				fmt.Fprintf(&g.b, "/* codegen error in %s::%s::%s: %v */\n",
					key.typeName, key.specName, fn.Name, err)
			}
			g.b.WriteString("\n")
			g.emitSpecAdapter(recv, key.specName, fn)
			g.b.WriteString("\n")
		}
		// Default-body adapters / NotImplemented stubs for unfilled methods.
		if spec != nil {
			for _, sm := range spec.Methods {
				overridden := false
				for _, fn := range decl.Methods {
					if fn.Name == sm.Name {
						overridden = true
						break
					}
				}
				if overridden {
					continue
				}
				if sm.Body != nil {
					g.emitSpecDefaultAdapter(recv, key.specName, sm)
					g.b.WriteString("\n")
				} else {
					g.emitNotImplementedStub(recv, key.specName, sm)
					g.b.WriteString("\n")
				}
			}
		}
	}

	// Per-(Type, Spec) static vtables.
	for _, key := range g.specImplKeys {
		recv := g.receiverTypes[key.typeName]
		if recv == nil {
			continue
		}
		spec := g.specs[key.specName]
		decl := g.specImpls[key]
		specPrefix := g.specMangle(key.specName) + "__" + key.specName
		fmt.Fprintf(&g.b, "static const struct zerg_vtable_%s zerg_vt_%s_%s = {\n",
			specPrefix, g.mangleType(recv), specPrefix)
		if spec == nil || len(spec.Methods) == 0 {
			g.b.WriteString("    ._empty = 0,\n")
		} else {
			for _, sm := range spec.Methods {
				// Pick adapter target.
				overridden := false
				for _, fn := range decl.Methods {
					if fn.Name == sm.Name {
						overridden = true
						break
					}
				}
				switch {
				case overridden:
					fmt.Fprintf(&g.b, "    .%s = zerg_adapter_%s__%s__%s,\n",
						sm.Name, g.mangleType(recv), specPrefix, sm.Name)
				case sm.Body != nil:
					fmt.Fprintf(&g.b, "    .%s = zerg_default_%s__%s__%s,\n",
						sm.Name, g.mangleType(recv), specPrefix, sm.Name)
				default:
					fmt.Fprintf(&g.b, "    .%s = zerg_not_impl_%s__%s__%s,\n",
						sm.Name, g.mangleType(recv), specPrefix, sm.Name)
				}
			}
		}
		g.b.WriteString("};\n")
	}
	g.b.WriteString("\n")
}

// writeMethodSig writes the C signature of a method fn (no trailing punct).
// receiver is the resolved Type; specName is "" for inherent, otherwise the
// spec name (used for mangling).
func (g *cgen) writeMethodSig(receiver *syntax.Type, specName string, fn *syntax.FnDecl) {
	g.b.WriteString("static ")
	g.b.WriteString(g.returnCType(fn.Return))
	g.b.WriteByte(' ')
	g.b.WriteString(g.methodMangle(receiver, specName, fn.Name))
	g.b.WriteByte('(')
	fmt.Fprintf(&g.b, "%s %s", g.cTypeName(receiver), mangle("this"))
	for _, p := range fn.Params {
		fmt.Fprintf(&g.b, ", %s %s", g.cTypeName(p.Type.Resolved), mangle(p.Name))
	}
	g.b.WriteByte(')')
}

// methodMangle returns the C identifier for a method function on receiver.
//
// v0.5: the spec component carries its owning module's mangle prefix when
// the spec lives in a different module from the type. The format is
//
//	inherent: <typemangle>__<method>
//	spec impl: <typemangle>__<specModMangle>__<Spec>__<method>
//
// where <typemangle> already includes the type's owning module mangle
// (e.g. `zerg_struct_main_h<m>__Counter`). When the spec lives in the same
// module as the type the prefix is uniform — slightly redundant but keeps
// the mangle uniform across "in-module" and "cross-module" impls.
func (g *cgen) methodMangle(receiver *syntax.Type, specName, method string) string {
	if specName == "" {
		return g.mangleType(receiver) + "__" + method
	}
	return g.mangleType(receiver) + "__" + g.specMangle(specName) + "__" + specName + "__" + method
}

// returnCType returns the C return-type string for a method, mapping a nil
// or void TypeRef to "void".
func (g *cgen) returnCType(ref *syntax.TypeRef) string {
	if ref == nil || ref.Resolved == nil || ref.Resolved == syntax.TVoid() {
		return "void"
	}
	return g.cTypeName(ref.Resolved)
}

// emitMethodFn emits the body of an impl method. The receiver becomes the
// implicit first parameter `z_this`; the rest of the body lowers like a
// normal fn body via emitBlockBody.
func (g *cgen) emitMethodFn(receiver *syntax.Type, specName string, fn *syntax.FnDecl) error {
	g.writeMethodSig(receiver, specName, fn)
	g.b.WriteString(" {\n")
	prevRet := g.currentFnRet
	if fn.Return != nil {
		g.currentFnRet = fn.Return.Resolved
	} else {
		g.currentFnRet = nil
	}
	prevIndent := g.indent
	g.indent = 1
	defer func() {
		g.currentFnRet = prevRet
		g.indent = prevIndent
	}()
	if err := g.emitBlockBody(fn.Body); err != nil {
		return err
	}
	g.b.WriteString("}\n")
	return nil
}

// emitSpecAdapter emits the void* → concrete adapter that the vtable points
// at. The adapter casts the void pointer back to the concrete Type, derefs,
// and forwards to the real method fn.
func (g *cgen) emitSpecAdapter(receiver *syntax.Type, specName string, fn *syntax.FnDecl) {
	specPrefix := g.specMangle(specName) + "__" + specName
	fmt.Fprintf(&g.b, "static %s zerg_adapter_%s__%s__%s(void* z_this",
		g.returnCType(fn.Return), g.mangleType(receiver), specPrefix, fn.Name)
	for _, p := range fn.Params {
		fmt.Fprintf(&g.b, ", %s %s", g.cTypeName(p.Type.Resolved), mangle(p.Name))
	}
	g.b.WriteString(") {\n")
	hasReturn := fn.Return != nil && fn.Return.Resolved != nil && fn.Return.Resolved != syntax.TVoid()
	if hasReturn {
		g.b.WriteString("    return ")
	} else {
		g.b.WriteString("    ")
	}
	fmt.Fprintf(&g.b, "%s(*((%s*)z_this)", g.methodMangle(receiver, specName, fn.Name), g.cTypeName(receiver))
	for _, p := range fn.Params {
		fmt.Fprintf(&g.b, ", %s", mangle(p.Name))
	}
	g.b.WriteString(");\n")
	g.b.WriteString("}\n")
}

// emitSpecDefaultAdapter emits a Type-specialised version of a spec default
// method body. The default body refers to `this` as the implementing type;
// each (Type, Spec) pair that inherits the default produces its own copy of
// the lowered C function so `this` resolves to the right concrete type.
func (g *cgen) emitSpecDefaultAdapter(receiver *syntax.Type, specName string, sm *syntax.SpecMethod) {
	specPrefix := g.specMangle(specName) + "__" + specName
	ret := g.returnCType(sm.Return)
	fmt.Fprintf(&g.b, "static %s zerg_default_%s__%s__%s(void* __zg_this_raw",
		ret, g.mangleType(receiver), specPrefix, sm.Name)
	for _, p := range sm.Params {
		fmt.Fprintf(&g.b, ", %s %s", g.cTypeName(p.Type.Resolved), mangle(p.Name))
	}
	g.b.WriteString(") {\n")
	// Bind `this` to the concrete value so the body's `this` reference walks
	// the same path as inherent / impl method bodies.
	fmt.Fprintf(&g.b, "    %s %s = *((%s*)__zg_this_raw);\n", g.cTypeName(receiver), mangle("this"), g.cTypeName(receiver))
	prevRet := g.currentFnRet
	prevIndent := g.indent
	if sm.Return != nil {
		g.currentFnRet = sm.Return.Resolved
	} else {
		g.currentFnRet = nil
	}
	g.indent = 1
	defer func() {
		g.currentFnRet = prevRet
		g.indent = prevIndent
	}()
	if err := g.emitBlockBody(sm.Body); err != nil {
		fmt.Fprintf(&g.b, "    /* codegen error in default %s::%s::%s: %v */\n",
			receiver.Name, specName, sm.Name, err)
	}
	g.b.WriteString("}\n")
}

// emitNotImplementedStub emits a `__attribute__((noreturn))` C function for
// a spec method that has no impl override and no spec default. The function
// signature matches the vtable slot so it can be installed there directly.
// The diagnostic format is byte-identical to run.go's NotImplemented panic.
func (g *cgen) emitNotImplementedStub(receiver *syntax.Type, specName string, sm *syntax.SpecMethod) {
	specPrefix := g.specMangle(specName) + "__" + specName
	ret := g.returnCType(sm.Return)
	fmt.Fprintf(&g.b, "static %s zerg_not_impl_%s__%s__%s(void* z_this",
		ret, g.mangleType(receiver), specPrefix, sm.Name)
	for _, p := range sm.Params {
		fmt.Fprintf(&g.b, ", %s %s", g.cTypeName(p.Type.Resolved), mangle(p.Name))
	}
	g.b.WriteString(") {\n")
	g.b.WriteString("    (void)z_this;\n")
	for _, p := range sm.Params {
		fmt.Fprintf(&g.b, "    (void)%s;\n", mangle(p.Name))
	}
	posStr := fmt.Sprintf("%d:%d", sm.Pos.Line, sm.Pos.Column)
	fmt.Fprintf(&g.b, "    zerg_not_implemented(%q, %q, %q, %q);\n",
		receiver.Name, sm.Name, specName, posStr)
	// noreturn helper exits — but the C compiler still wants a path that
	// produces the declared return value. Add a defensive trap:
	if ret != "void" {
		fmt.Fprintf(&g.b, "    return (%s){0};\n", ret)
	}
	g.b.WriteString("}\n")
}

// ---------------------------------------------------------------------------
// Method-call expression lowering.
// ---------------------------------------------------------------------------

// methodCallStr lowers a MethodCallExpr. Routing precedence mirrors run.go:
//
//  1. typeck-lowered EnumLit (`Token.Ident("foo")`) → enumLitStr.
//  2. typeck-lowered builtin call (`xs.push(v)`) → existing callStr path.
//  3. v0.5 cross-module fn-call shape — receiver is an IdentExpr that
//     resolves to a module binding in the active module's import table,
//     and Method is a pub fn in that module → emit a direct call to the
//     foreign module's mangled fn name.
//  4. Spec-typed receiver → fat-pointer vtable dispatch.
//  5. Concrete receiver → resolve to inherent or unique spec impl method,
//     emit a direct C fn call.
func (g *cgen) methodCallStr(e *syntax.MethodCallExpr) (string, error) {
	if e.Lowered != nil {
		return g.enumLitStr(e.Lowered)
	}
	if e.LoweredCall != nil {
		return g.callStr(e.LoweredCall)
	}
	// v0.5 cross-module fn call — pattern-match the receiver against the
	// active module's import bindings. typeck has already validated pubness
	// and arity (see checkCrossModuleFnCall in typeck_v05.go); the codegen
	// only needs to route the call to the right mangled fn name.
	if id, ok := e.Receiver.(*syntax.IdentExpr); ok {
		if foreignMangle := g.lookupImportMangle(id.Name); foreignMangle != "" {
			if fn := g.lookupModuleFn(foreignMangle, e.Method); fn != nil {
				return g.crossModFnCallStr(fn, e)
			}
		}
	}
	rt := e.Receiver.Type()
	if rt == nil {
		return "", fmt.Errorf("codegen: method-call receiver has nil type at %s", e.Pos)
	}
	// v0.7 wait_group: receiver is the synthetic WaitGroup struct (handle is
	// a pointer to zerg_wait_group_t). Dispatch the three methods directly.
	if rt.Kind == syntax.TypeStruct && rt.Name == "WaitGroup" {
		rs, err := g.exprStr(e.Receiver)
		if err != nil {
			return "", err
		}
		switch e.Method {
		case "add":
			if len(e.Args) != 1 {
				return "", fmt.Errorf("codegen: WaitGroup.add expects 1 arg at %s", e.Pos)
			}
			vs, err := g.exprStr(e.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(zerg_wait_group_add(%s, %s), 0)", rs, vs), nil
		case "done":
			return fmt.Sprintf("(zerg_wait_group_done(%s), 0)", rs), nil
		case "wait":
			return fmt.Sprintf("(zerg_wait_group_wait(%s), 0)", rs), nil
		}
		return "", fmt.Errorf("codegen: unknown WaitGroup method %q at %s", e.Method, e.Pos)
	}
	if rt.Kind == syntax.TypeSpec {
		return g.dispatchSpec(e, rt)
	}
	return g.dispatchConcrete(e, rt)
}

// lookupImportMangle returns the target module's mangle for a local name
// in the currently-emitting module's import table. Returns "" when local
// is not a module binding in the active scope (so the caller falls back
// to the standard method-dispatch path).
func (g *cgen) lookupImportMangle(local string) string {
	if g == nil || g.currentMod == "" {
		return ""
	}
	me := g.moduleByName[g.currentMod]
	if me == nil {
		return ""
	}
	return me.imports[local]
}

// lookupModuleFn returns the FnDecl for `fnName` in the module identified
// by mangle, or nil when the lookup misses. Used by cross-module fn-call
// emission.
func (g *cgen) lookupModuleFn(mangle, fnName string) *syntax.FnDecl {
	me := g.moduleByName[mangle]
	if me == nil || me.prog == nil {
		return nil
	}
	for _, stmt := range me.prog.Statements {
		if fn, ok := stmt.(*syntax.FnDecl); ok && fn.Name == fnName {
			return fn
		}
	}
	return nil
}

// crossModFnCallStr emits a direct call to a foreign module's pub fn.
// Argument coercion follows the declared param types so spec-typed args
// widen at the call boundary, matching run.go's evalMethodCall path.
func (g *cgen) crossModFnCallStr(fn *syntax.FnDecl, e *syntax.MethodCallExpr) (string, error) {
	var paramTypes []*syntax.Type
	for _, p := range fn.Params {
		if p.Type != nil {
			paramTypes = append(paramTypes, p.Type.Resolved)
		} else {
			paramTypes = append(paramTypes, nil)
		}
	}
	args, err := g.coerceArgs(e.Args, paramTypes)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(g.fnCName(fn))
	sb.WriteByte('(')
	for i, a := range args {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(a)
	}
	sb.WriteByte(')')
	return sb.String(), nil
}

// dispatchSpec emits a fat-pointer vtable dispatch:
//
//	({ zerg_dyn_<Spec> __r = <recv>; __r.vt-><method>(__r.data, args...); })
//
// The receiver is snapshotted into a temp so a side-effecting receiver
// expression evaluates exactly once. typeck has validated that <method> is
// declared by the spec; codegen does not re-check.
func (g *cgen) dispatchSpec(e *syntax.MethodCallExpr, rt *syntax.Type) (string, error) {
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	specName := rt.Name
	spec := g.specs[specName]
	// Coerce each arg to the declared param type so spec-typed params widen.
	var paramTypes []*syntax.Type
	if spec != nil {
		for _, m := range spec.Methods {
			if m.Name == e.Method {
				for _, p := range m.Params {
					if p.Type != nil {
						paramTypes = append(paramTypes, p.Type.Resolved)
					} else {
						paramTypes = append(paramTypes, nil)
					}
				}
				break
			}
		}
	}
	args, err := g.coerceArgs(e.Args, paramTypes)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "({ %s __r = %s; __r.vt->%s(__r.data", g.mangleType(rt), rs, e.Method)
	for _, a := range args {
		sb.WriteString(", ")
		sb.WriteString(a)
	}
	sb.WriteString("); })")
	return sb.String(), nil
}

// implKeyForType returns the impl-table lookup key for a receiver *Type.
// When the receiver type's owning module is known via g.typeOwner the key
// is `<modmangle>__<TypeName>`; this disambiguates two modules' identically-
// named structs/enums in the merged TU. Falls back to the bare type name
// for receivers we never registered (entry-only single-module fixtures).
func (g *cgen) implKeyForType(rt *syntax.Type) string {
	if rt == nil {
		return ""
	}
	if owner, ok := g.typeOwner[rt]; ok && owner != "" {
		return owner + "__" + rt.Name
	}
	return rt.Name
}

// dispatchConcrete emits a direct C method-fn call with the receiver as the
// first argument. Resolution: inherent methods first, then unique spec-impl
// method by name. typeck has rejected ambiguity so the first match suffices.
//
// For spec-impl-via-concrete dispatch we must also handle the spec-default
// and NotImplemented cases: we route via the same Type-specialised default
// adapter / NotImplemented stub the vtable uses.
func (g *cgen) dispatchConcrete(e *syntax.MethodCallExpr, rt *syntax.Type) (string, error) {
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	typeName := g.implKeyForType(rt)
	// 1. Inherent.
	if methods, ok := g.inherent[typeName]; ok {
		for _, fn := range methods {
			if fn.Name == e.Method {
				return g.emitConcreteMethodCall(rt, "", fn, rs, e.Args)
			}
		}
	}
	// 2. Spec impl methods. Walk the spec impls for this type and find the
	// first one exposing the method (override or via default).
	for _, key := range g.specImplKeys {
		if key.typeName != typeName {
			continue
		}
		decl := g.specImpls[key]
		for _, fn := range decl.Methods {
			if fn.Name == e.Method {
				return g.emitConcreteMethodCall(rt, key.specName, fn, rs, e.Args)
			}
		}
		// No override — try default.
		spec := g.specs[key.specName]
		if spec != nil {
			for _, sm := range spec.Methods {
				if sm.Name != e.Method {
					continue
				}
				if sm.Body != nil {
					// Default body — call the type-specialised default
					// adapter.
					return g.emitConcreteSpecDefault(rt, key.specName, sm, rs, e.Args)
				}
				// Signature only — NotImplemented stub.
				return g.emitConcreteNotImpl(rt, key.specName, sm, rs, e.Args)
			}
		}
	}
	return "", fmt.Errorf("codegen: method %q on %s not resolvable at %s", e.Method, typeName, e.MethodPos)
}

// emitConcreteMethodCall renders a direct call to either an inherent or
// spec-impl method fn, passing the receiver value (NOT a pointer; cgen's
// method functions take the receiver by value, matching the v0.3 fn-call
// composite-arg convention).
func (g *cgen) emitConcreteMethodCall(rt *syntax.Type, specName string, fn *syntax.FnDecl, rs string, callArgs []syntax.Expr) (string, error) {
	var paramTypes []*syntax.Type
	for _, p := range fn.Params {
		if p.Type != nil {
			paramTypes = append(paramTypes, p.Type.Resolved)
		} else {
			paramTypes = append(paramTypes, nil)
		}
	}
	args, err := g.coerceArgs(callArgs, paramTypes)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(g.methodMangle(rt, specName, fn.Name))
	sb.WriteByte('(')
	sb.WriteString(rs)
	for _, a := range args {
		sb.WriteString(", ")
		sb.WriteString(a)
	}
	sb.WriteByte(')')
	return sb.String(), nil
}

// emitConcreteSpecDefault routes a concrete-receiver call to the per-(Type,
// Spec) default adapter — the same one the vtable points at when the impl
// inherits the spec's default. The adapter takes void* so we wrap the
// receiver in a temp and pass its address.
func (g *cgen) emitConcreteSpecDefault(rt *syntax.Type, specName string, sm *syntax.SpecMethod, rs string, callArgs []syntax.Expr) (string, error) {
	var paramTypes []*syntax.Type
	for _, p := range sm.Params {
		if p.Type != nil {
			paramTypes = append(paramTypes, p.Type.Resolved)
		} else {
			paramTypes = append(paramTypes, nil)
		}
	}
	args, err := g.coerceArgs(callArgs, paramTypes)
	if err != nil {
		return "", err
	}
	specPrefix := g.specMangle(specName) + "__" + specName
	var sb strings.Builder
	fmt.Fprintf(&sb, "({ %s __t = %s; zerg_default_%s__%s__%s(&__t",
		g.cTypeName(rt), rs, g.mangleType(rt), specPrefix, sm.Name)
	for _, a := range args {
		sb.WriteString(", ")
		sb.WriteString(a)
	}
	sb.WriteString("); })")
	return sb.String(), nil
}

// emitConcreteNotImpl routes a concrete-receiver call to the per-(Type,
// Spec, method) NotImplemented stub. Same shape as the default adapter
// path but the call traps before returning.
func (g *cgen) emitConcreteNotImpl(rt *syntax.Type, specName string, sm *syntax.SpecMethod, rs string, callArgs []syntax.Expr) (string, error) {
	var paramTypes []*syntax.Type
	for _, p := range sm.Params {
		if p.Type != nil {
			paramTypes = append(paramTypes, p.Type.Resolved)
		} else {
			paramTypes = append(paramTypes, nil)
		}
	}
	args, err := g.coerceArgs(callArgs, paramTypes)
	if err != nil {
		return "", err
	}
	specPrefix := g.specMangle(specName) + "__" + specName
	var sb strings.Builder
	fmt.Fprintf(&sb, "({ %s __t = %s; zerg_not_impl_%s__%s__%s(&__t",
		g.cTypeName(rt), rs, g.mangleType(rt), specPrefix, sm.Name)
	for _, a := range args {
		sb.WriteString(", ")
		sb.WriteString(a)
	}
	sb.WriteString("); })")
	return sb.String(), nil
}

// coerceArgs evaluates a slice of argument expressions and applies spec
// coercion to each one based on the matching declared param type. Used for
// both method-call and ordinary fn-call arg lowering when the callee carries
// spec-typed parameters.
func (g *cgen) coerceArgs(args []syntax.Expr, paramTypes []*syntax.Type) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		s, err := g.exprStr(a)
		if err != nil {
			return nil, err
		}
		var pt *syntax.Type
		if i < len(paramTypes) {
			pt = paramTypes[i]
		}
		out[i] = g.coerceCExpr(s, a.Type(), pt)
	}
	return out, nil
}

// coerceCExpr returns a C expression that produces a value of type `target`
// from a C expression of type `srcT`. The function is the codegen analogue
// of run.go's coerceToType: when srcT is a concrete struct/enum and target
// is a spec, we wrap the value in a fat pointer pointing at a heap-boxed
// copy of the source. Composite shapes (list/tuple/struct/enum-payload)
// recurse element-wise.
//
// Lifetime — at v0.4 the underlying value is heap-boxed so the fat pointer
// can outlive any local frame. malloc is leaked (consistent with the rest
// of the runtime which leaks list buffers and string concats too); a v0.5+
// arena will reclaim.
func (g *cgen) coerceCExpr(expr string, srcT, target *syntax.Type) string {
	if target == nil || srcT == nil {
		return expr
	}
	// If the source already has the same fully-resolved shape as the target
	// (including spec elements), no coercion is needed — every element
	// position has already been wrapped at the source-construction site
	// (list-lit / tuple-lit / struct-lit emit per-element coerces).
	if srcT.Equals(target) {
		return expr
	}
	if target.Kind == srcT.Kind && target.Kind != syntax.TypeSpec {
		// Same outer shape — only descend when recursion can hit a spec
		// inside (list[Spec], tuple[..., Spec], etc.).
		if !shapeContainsSpec(target) {
			return expr
		}
	}
	switch target.Kind {
	case syntax.TypeSpec:
		if srcT.Kind == syntax.TypeSpec {
			// Already wrapped — typeck rejects spec-to-different-spec at
			// v0.4 so just pass through.
			return expr
		}
		// Heap-box the concrete value, then wrap. specPrefix carries the
		// spec's owning module mangle so cross-module spec coercion picks
		// the right vtable type.
		concreteC := g.cTypeName(srcT)
		specPrefix := g.specMangle(target.Name) + "__" + target.Name
		return fmt.Sprintf(
			"({ %s* __p = (%s*)malloc(sizeof(%s)); *__p = (%s); (zerg_dyn_%s){.data = __p, .vt = &zerg_vt_%s_%s}; })",
			concreteC, concreteC, concreteC, expr, specPrefix, g.mangleType(srcT), specPrefix)
	case syntax.TypeList:
		if srcT.Kind != syntax.TypeList {
			return expr
		}
		if !shapeContainsSpec(target) {
			return expr
		}
		// Build a fresh list, copying each element with element-coerce.
		mname := g.mangleType(target)
		elemC := g.cTypeName(target.Element)
		tmp := g.freshTmp("co")
		coerced := g.coerceCExpr(fmt.Sprintf("__src.data[__i]"), srcT.Element, target.Element)
		return fmt.Sprintf(
			"({ %s __src = (%s); %s %s; %s.len = __src.len; %s.cap = __src.len; %s.data = (%s*)malloc(%s.len ? %s.len * sizeof(%s) : 1); for (size_t __i = 0; __i < __src.len; __i++) { %s.data[__i] = %s; } %s; })",
			g.cTypeName(srcT), expr, mname, tmp,
			tmp, tmp, tmp, elemC, tmp, tmp, elemC,
			tmp, coerced, tmp)
	case syntax.TypeTuple:
		if srcT.Kind != syntax.TypeTuple || len(target.Tuple) != len(srcT.Tuple) {
			return expr
		}
		if !shapeContainsSpec(target) {
			return expr
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "({ %s __src = (%s); ((%s){", g.cTypeName(srcT), expr, g.mangleType(target))
		for i := range target.Tuple {
			if i > 0 {
				sb.WriteString(", ")
			}
			coerced := g.coerceCExpr(fmt.Sprintf("__src.e%d", i), srcT.Tuple[i], target.Tuple[i])
			fmt.Fprintf(&sb, ".e%d = %s", i, coerced)
		}
		sb.WriteString("}); })")
		return sb.String()
	}
	return expr
}

// shapeContainsSpec returns true if t (or any composite leaf reached via
// list/tuple/struct/enum-payload recursion) has Kind TypeSpec. Drives the
// per-shape coercion descent decision.
func shapeContainsSpec(t *syntax.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case syntax.TypeSpec:
		return true
	case syntax.TypeList:
		return shapeContainsSpec(t.Element)
	case syntax.TypeTuple:
		for _, e := range t.Tuple {
			if shapeContainsSpec(e) {
				return true
			}
		}
	case syntax.TypeStruct:
		for _, f := range t.Fields {
			if shapeContainsSpec(f.Type) {
				return true
			}
		}
	case syntax.TypeEnum:
		for _, payload := range t.VariantPayloads {
			for _, pt := range payload {
				if shapeContainsSpec(pt) {
					return true
				}
			}
		}
	}
	return false
}

// enumLitStr lowers an EnumLit to a compound literal of the per-shape enum
// struct: `((zerg_enum_<Name>){.tag = idx, .payload.pN = {.aJ = ...}})`.
// Bare variants omit the per-variant payload init (the union slot stays
// zero-initialised).
func (g *cgen) enumLitStr(e *syntax.EnumLit) (string, error) {
	en := e.Type()
	if en == nil || en.Kind != syntax.TypeEnum {
		return "", fmt.Errorf("codegen: enum literal has non-enum type at %s", e.Pos)
	}
	idx := variantIndex(en, e.Variant)
	if idx < 0 {
		return "", fmt.Errorf("codegen: enum %s has no variant %s at %s", en.Name, e.Variant, e.VariantPos)
	}
	mname := g.mangleType(en)
	if len(e.Payload) == 0 {
		return fmt.Sprintf("((%s){.tag = %d})", mname, idx), nil
	}
	payloadTypes := variantPayload(en, idx)
	var sb strings.Builder
	fmt.Fprintf(&sb, "((%s){.tag = %d, .payload.p%d = {", mname, idx, idx)
	for i, sub := range e.Payload {
		if i > 0 {
			sb.WriteString(", ")
		}
		s, err := g.exprStr(sub)
		if err != nil {
			return "", err
		}
		// Coerce each payload element to the declared variant payload type
		// so a payload position declared as a spec widens the concrete arg.
		var pt *syntax.Type
		if i < len(payloadTypes) {
			pt = payloadTypes[i]
		}
		s = g.coerceCExpr(s, sub.Type(), pt)
		fmt.Fprintf(&sb, ".a%d = %s", i, s)
	}
	sb.WriteString("}})")
	return sb.String(), nil
}

// emitEqHelpers emits a per-shape `_eq` helper for every composite shape in
// the program. typeck has validated that == on composites only invokes
// these helpers when the operand types match exactly. Recursion bottoms out
// at primitives (== / zerg_str_eq) and at nested composites (which have
// their own _eq helper emitted alongside).
//
// Spec-typed bindings reject == at typeck per PLAN.md, so this function
// never needs an _eq path for TypeSpec.
func (g *cgen) emitEqHelpers() {
	r := g.shapes
	if len(r.listOrder)+len(r.tupleOrder)+len(r.structOrder)+len(r.enumOrder) == 0 {
		return
	}
	// Skip shapes that transitively reach a spec-typed leaf — typeck rejects
	// composite == on those at v0.4 so the helper would never be called and
	// emitting one fails to compile (no eq for spec types).
	listKeys := filterEqShapes(r.listOrder, r.listShapes)
	tupleKeys := filterEqShapes(r.tupleOrder, r.tupleShapes)
	structKeys := filterEqShapes(r.structOrder, r.structShapes)
	enumKeys := filterEqShapes(r.enumOrder, r.enumShapes)
	if len(listKeys)+len(tupleKeys)+len(structKeys)+len(enumKeys) == 0 {
		return
	}
	g.b.WriteString("/* Per-shape composite == helpers (v0.4). */\n")
	// Forward decls so helpers can mutually reference (list-of-struct calls
	// struct_eq which may itself call list_eq for an inner list field).
	for _, k := range listKeys {
		fmt.Fprintf(&g.b, "static _Bool %s_eq(%s a, %s b);\n", k, k, k)
	}
	for _, k := range tupleKeys {
		fmt.Fprintf(&g.b, "static _Bool %s_eq(%s a, %s b);\n", k, k, k)
	}
	for _, k := range structKeys {
		fmt.Fprintf(&g.b, "static _Bool %s_eq(%s a, %s b);\n", k, k, k)
	}
	for _, k := range enumKeys {
		fmt.Fprintf(&g.b, "static _Bool %s_eq(%s a, %s b);\n", k, k, k)
	}
	g.b.WriteString("\n")

	for _, k := range listKeys {
		t := r.listShapes[k]
		emitListEq(g, &g.b, k, t)
		g.b.WriteString("\n")
	}
	for _, k := range tupleKeys {
		t := r.tupleShapes[k]
		emitTupleEq(g, &g.b, k, t)
		g.b.WriteString("\n")
	}
	for _, k := range structKeys {
		t := r.structShapes[k]
		emitStructEq(g, &g.b, k, t)
		g.b.WriteString("\n")
	}
	for _, k := range enumKeys {
		t := r.enumShapes[k]
		emitEnumEq(g, &g.b, k, t)
		g.b.WriteString("\n")
	}
}

// filterEqShapes returns the keys whose shape does NOT transitively contain a
// spec-typed leaf. Spec shapes can't participate in == per typeck rules.
func filterEqShapes(order []string, shapes map[string]*syntax.Type) []string {
	out := make([]string, 0, len(order))
	for _, k := range order {
		if !shapeContainsSpec(shapes[k]) {
			out = append(out, k)
		}
	}
	return out
}

// eqExpr returns a C boolean expression that compares two operands of type
// t. Routes to the per-shape _eq helper for composite types and to the
// existing primitive == / zerg_str_eq for primitives.
func (g *cgen) eqExpr(t *syntax.Type, a, b string) string {
	if t == nil {
		return fmt.Sprintf("(%s == %s)", a, b)
	}
	if t == syntax.TStr() {
		return fmt.Sprintf("zerg_str_eq(%s, %s)", a, b)
	}
	switch t.Kind {
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
		return fmt.Sprintf("%s_eq(%s, %s)", g.mangleType(t), a, b)
	}
	return fmt.Sprintf("(%s == %s)", a, b)
}

func emitListEq(g *cgen, b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static _Bool %s_eq(%s a, %s b) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    if (a.len != b.len) return 0;\n")
	fmt.Fprintf(b, "    for (size_t i = 0; i < a.len; i++) {\n")
	fmt.Fprintf(b, "        if (!(%s)) return 0;\n", g.eqExpr(t.Element, "a.data[i]", "b.data[i]"))
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    return 1;\n")
	fmt.Fprintf(b, "}\n")
}

func emitTupleEq(g *cgen, b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static _Bool %s_eq(%s a, %s b) {\n", mname, mname, mname)
	for i, e := range t.Tuple {
		fmt.Fprintf(b, "    if (!(%s)) return 0;\n",
			g.eqExpr(e, fmt.Sprintf("a.e%d", i), fmt.Sprintf("b.e%d", i)))
	}
	fmt.Fprintf(b, "    return 1;\n")
	fmt.Fprintf(b, "}\n")
}

func emitStructEq(g *cgen, b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static _Bool %s_eq(%s a, %s b) {\n", mname, mname, mname)
	for _, f := range t.Fields {
		fname := mangleField(f.Name)
		fmt.Fprintf(b, "    if (!(%s)) return 0;\n",
			g.eqExpr(f.Type, "a."+fname, "b."+fname))
	}
	fmt.Fprintf(b, "    return 1;\n")
	fmt.Fprintf(b, "}\n")
}

func emitEnumEq(g *cgen, b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static _Bool %s_eq(%s a, %s b) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    if (a.tag != b.tag) return 0;\n")
	fmt.Fprintf(b, "    switch (a.tag) {\n")
	for i := range t.Variants {
		payload := variantPayload(t, i)
		fmt.Fprintf(b, "    case %d:\n", i)
		if len(payload) == 0 {
			fmt.Fprintf(b, "        return 1;\n")
			continue
		}
		for j, pt := range payload {
			access := fmt.Sprintf("payload.p%d.a%d", i, j)
			fmt.Fprintf(b, "        if (!(%s)) return 0;\n",
				g.eqExpr(pt, "a."+access, "b."+access))
		}
		fmt.Fprintf(b, "        return 1;\n")
	}
	fmt.Fprintf(b, "    default: return 0;\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "}\n")
}
