package module

import (
	"path"
	"sort"
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// entryCanonical is the reserved canonical name of the entry (main) module. Its
// items are never mangled, so the whole-program unit keeps the entry file's exact
// historic names — the byte-identical guarantee for a program with no import.
const entryCanonical = ""

// Loader resolves an import graph against an ordered list of source roots and
// flattens it into one compilation unit.
type Loader struct {
	roots []FileProvider
}

// NewLoader builds a loader that searches roots in order for each import
// (the entry directory tree first, the embedded stdlib second).
func NewLoader(roots ...FileProvider) *Loader { return &Loader{roots: roots} }

// loadedModule is one resolved non-entry module in the graph.
type loadedModule struct {
	canonical string
	// dir is where the module's files live — the module path itself for a directory module and
	// its parent for a one-file one. A `./` import written inside the module is relative to it.
	dir   string
	tag   string
	files []*ast.File
	deps  []string // canonical names of the modules it imports (graph edges)
}

// LoadSource parses src as the entry module, resolves its import graph against the
// loader's roots, detects cycles, and returns the whole-program *ast.File the
// existing pipeline consumes. A program that imports nothing returns exactly its
// own parsed file (no items appended, no name mangled).
func (l *Loader) LoadSource(src string) (*ast.File, []diag.Diagnostic) {
	file, _, diags := l.LoadProgram(src)
	return file, diags
}

// LoadProgram is LoadSource plus the whole-program initialization plan (Phase 1g
// S3): the flattened *ast.File and an InitPlan naming every module that owns an
// `init()` block or a module constant, in dependency (topological) order. A program
// with no init and no module constant returns an empty plan, so its C stays
// byte-identical.
func (l *Loader) LoadProgram(src string) (*ast.File, *InitPlan, []diag.Diagnostic) {
	var diags diag.List
	entry, pd := parser.Parse(src)
	if len(pd) > 0 {
		return nil, nil, pd
	}

	reg := map[string]*loadedModule{}
	edges := map[string][]string{}

	// Expand the graph to a fixpoint from the entry module's imports, resolving any
	// newly discovered module's own imports on the next pass; each spec is tagged.
	l.resolveImports(entryCanonical, "", importSpecs([]*ast.File{entry}), reg, edges, &diags)
	for changed := true; changed; {
		changed = false
		for _, m := range snapshot(reg) {
			if m.deps != nil {
				continue // its imports were already expanded
			}
			m.deps = l.resolveImports(m.canonical, m.dir, importSpecs(m.files), reg, edges, &diags)
			if m.deps == nil {
				m.deps = []string{} // mark expanded even when it imports nothing
			}
			changed = true
		}
	}
	if !diags.Empty() {
		return nil, nil, diags.Items()
	}
	if cyc := detectCycle(edges); cyc != nil {
		diags.Add(token.Span{}, "import cycle detected: %s", strings.Join(cyc, " -> "))
		return nil, nil, diags.Items()
	}

	l.wireReexports(entry, reg)
	// Build the init plan BEFORE flatten: flatten appends every module's items to the
	// entry file, so scanning entry.Items afterward would double-count an imported
	// module's init blocks and constants under the entry module. The plan holds node
	// pointers, and flatten mangles those same nodes in place, so the names read at
	// emit time are the mangled ones.
	plan := l.initPlan(entry, reg, edges)
	l.flatten(entry, reg)
	return entry, plan, nil
}

// wireReexports fills every import spec's Reexports with the tags the module it
// resolves to re-exports through its own `import pub "…"` specs (one level, S2), so
// sema can resolve a member reached through a namespace onto a re-exported module's
// public surface. It is computed after the graph is resolved (so every spec already
// carries its Module tag) and before flatten.
func (l *Loader) wireReexports(entry *ast.File, reg map[string]*loadedModule) {
	// reexports[tag] = the tags module `tag` re-exports (its pub imports' targets).
	reexports := map[string][]string{}
	record := func(owner string, files []*ast.File) {
		for _, spec := range importSpecs(files) {
			if spec.Pub && spec.Module != "" {
				reexports[owner] = append(reexports[owner], spec.Module)
			}
		}
	}
	record(mangleTag(entryCanonical), []*ast.File{entry})
	for _, m := range snapshot(reg) {
		record(m.tag, m.files)
	}
	assign := func(files []*ast.File) {
		for _, spec := range importSpecs(files) {
			spec.Reexports = reexports[spec.Module]
		}
	}
	assign([]*ast.File{entry})
	for _, m := range snapshot(reg) {
		assign(m.files)
	}
}

// initPlan builds the whole-program init plan in dependency (topological) order —
// a module's dependencies before the module itself, the entry module last — keeping
// only the modules that own an `init()` block or a module constant.
func (l *Loader) initPlan(entry *ast.File, reg map[string]*loadedModule, edges map[string][]string) *InitPlan {
	plan := &InitPlan{}
	for _, canonical := range topoOrder(edges) {
		var files []*ast.File
		tag := mangleTag(canonical)
		if canonical == entryCanonical {
			files = []*ast.File{entry}
		} else if m, ok := reg[canonical]; ok {
			files = m.files
		} else {
			continue
		}
		if im, ok := collectInit(tag, files); ok {
			plan.Modules = append(plan.Modules, im)
		}
	}
	return plan
}

// resolveImports resolves every import spec of a module, recording each resolved
// module in reg (parsing it on first sight), assigning the spec its canonical tag,
// and returning the importer's dependency edges.
func (l *Loader) resolveImports(
	importer string, importerDir string, specs []*ast.ImportSpec, reg map[string]*loadedModule, edges map[string][]string, diags *diag.List,
) []string {
	deps := []string{}
	for _, spec := range specs {
		// An import that NAMES a test file reaches the single-file arm, which the
		// directory arm's filter does not cover. Refusing it here rather than letting
		// it resolve to nothing keeps the complaint at the import.
		if IsTestFile(path.Base(spec.Path) + ".zg") {
			diags.Add(spec.Span(), "%q names a test file, and a normal build compiles none", spec.Path)
			continue
		}
		// THE STRING FIRST, and the disk only after it, so a path that is not an import path
		// is turned away for what it says rather than for what it failed to find.
		if why := PathIllFormed(spec.Path); why != "" {
			diags.Add(spec.Span(), "%q is not an import path: %s", spec.Path, why)
			continue
		}
		if RemotePath(spec.Path) {
			diags.Add(spec.Span(), "a remote package %q needs a package layer this compiler has not built", spec.Path)
			continue
		}
		canonical, dir, files, ok := l.resolve(spec.Path, importerDir)
		if !ok {
			diags.Add(spec.Span(), "cannot resolve import %q under any source root", spec.Path)
			continue
		}
		spec.Module = mangleTag(canonical)
		deps = append(deps, canonical)
		if _, seen := reg[canonical]; seen {
			continue
		}
		parsed, perr := parseModule(canonical, files, diags)
		if perr {
			continue
		}
		reg[canonical] = &loadedModule{canonical: canonical, dir: dir, tag: mangleTag(canonical), files: parsed}
	}
	edges[importer] = deps
	return deps
}

// resolve tries each source root in order for an import path, first match wins.
// resolve picks the ONE root the path names rather than searching all of them. A bare path
// names the standard library and a `./` path names this project (GRAMMAR#import-path), so the
// prefix decides where to look and nothing on disk can change that — which is what stops a
// file beside the importer from taking the name `io` away from the standard library.
//
// The roots are told apart by TYPE because that is what they are: OSProvider is the user tree
// and every other provider is bundled.
func (l *Loader) resolve(importPath, importerDir string) (string, string, []ModuleFile, bool) {
	body, local := LocalPath(importPath)

	// `./` IS RELATIVE TO THE FILE THAT WROTE IT and `/` to the package root, which is this
	// provider's Root. The two coincide for a file at the root and differ for every file below
	// it, which is why the importer's directory travels here rather than being assumed.
	if local && !RootPath(importPath) && importerDir != "" && importerDir != "." {
		body = path.Join(importerDir, body)
	}
	for _, root := range l.roots {
		_, isUser := root.(OSProvider)
		if isUser != local {
			continue
		}
		if canonical, dir, files, ok := root.Resolve(body); ok {
			return canonical, dir, files, true
		}
	}
	return "", "", nil, false
}

// flatten appends every resolved module's mangled items to the entry file, in a
// stable canonical order, producing the single whole-program compilation unit.
func (l *Loader) flatten(entry *ast.File, reg map[string]*loadedModule) {
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m := reg[name]
		r := &renamer{tag: m.tag, surface: collectSurface(m.files), variants: collectVariants(m.files)}
		entry.Items = append(entry.Items, r.rename(m.files)...)
	}
}

// parseModule parses each of a module's files, reporting any parse failure.
func parseModule(canonical string, files []ModuleFile, diags *diag.List) ([]*ast.File, bool) {
	var out []*ast.File
	failed := false
	for _, f := range files {
		parsed, pd := parser.Parse(f.Src)
		if len(pd) > 0 {
			diags.Add(token.Span{}, "module %q: %s failed to parse: %s", canonical, f.Name, pd[0].Msg)
			failed = true
			continue
		}
		out = append(out, parsed)
	}
	return out, failed
}

// importSpecs gathers every import spec across a module's files (declaration
// order), the edges out of that module.
func importSpecs(files []*ast.File) []*ast.ImportSpec {
	var specs []*ast.ImportSpec
	for _, f := range files {
		for _, it := range f.Items {
			if imp, ok := it.(*ast.ImportStmt); ok {
				specs = append(specs, imp.Specs...)
			}
		}
	}
	return specs
}

// snapshot returns the registry's modules in a stable order (a copy so the caller
// may extend the registry while iterating).
func snapshot(reg map[string]*loadedModule) []*loadedModule {
	out := make([]*loadedModule, 0, len(reg))
	for _, m := range reg {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].canonical < out[j].canonical })
	return out
}

// detectCycle finds an import cycle in the dependency edges by DFS three-coloring,
// returning the cycle as a path of canonical names (its first node repeated at the
// end), or nil when the graph is acyclic. The entry module is the "" node.
func detectCycle(edges map[string][]string) []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var walk func(n string) []string
	walk = func(n string) []string {
		color[n] = gray
		stack = append(stack, n)
		for _, dep := range edges[n] {
			switch color[dep] {
			case gray:
				return append(cycleFrom(stack, dep), cycleLabel(dep))
			case white:
				if c := walk(dep); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return nil
	}
	for _, n := range sortedKeys(edges) {
		if color[n] == white {
			if c := walk(n); c != nil {
				return c
			}
		}
	}
	return nil
}

// cycleFrom returns the tail of stack starting at the first occurrence of node,
// naming each module in the cycle for the diagnostic. The entry module renders as
// "<entry>".
func cycleFrom(stack []string, node string) []string {
	var out []string
	collecting := false
	for _, s := range stack {
		if s == node {
			collecting = true
		}
		if collecting {
			out = append(out, cycleLabel(s))
		}
	}
	return out
}

func cycleLabel(canonical string) string {
	if canonical == entryCanonical {
		return "<entry>"
	}
	return canonical
}

// topoOrder returns the modules in dependency (topological) order — each module
// after every module it imports, so a dependency precedes its dependents and the
// entry module (which transitively imports the whole graph) lands last. The graph
// is already known acyclic (detectCycle ran), so a post-order DFS is a valid
// topological sort; it starts at the entry node, then sweeps any node the entry
// does not reach, for determinism.
//
// A NODE'S DEPS ARE VISITED BY CANONICAL NAME, not in the order their imports were
// written, and that is the whole of the tie-break docs/runtime/package.md names: what
// the dependency graph leaves unordered is decided by the module NAME. Visiting
// edges[n] as recorded made two independent modules initialize in the order their
// import lines appear, so moving a line inside an import block reordered two
// initializers — deterministic for a given source, but not the rule the chapter
// states (#70).
func topoOrder(edges map[string][]string) []string {
	visited := map[string]bool{}
	var order []string
	var visit func(n string)
	visit = func(n string) {
		if visited[n] {
			return
		}
		visited[n] = true
		deps := append([]string(nil), edges[n]...)
		sort.Strings(deps)
		for _, dep := range deps {
			visit(dep)
		}
		order = append(order, n)
	}
	visit(entryCanonical)
	for _, n := range sortedKeys(edges) {
		visit(n)
	}
	return order
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
