// Package loader walks the import graph of a Zerg program from a single
// entry file, parses every reachable sibling module exactly once, and
// returns a Bundle that downstream layers (typeck, run, build) can iterate.
//
// The loader is the v0.5 superset of the v0.0–v0.4 single-file pipeline:
// passing it a file with no imports yields a one-module Bundle whose Entry
// behaves identically to the parsed *syntax.Program every prior unit
// already consumed. Multi-file programs are discovered by walking
// ImportDecl nodes from the entry, resolving each Path to a sibling .zg
// file, and recursing.
//
// Resolution rules (per PLAN.md §Resolution rules):
//
//   - Sibling-only at v0.5. `import "math"` from /abs/path/foo.zg resolves
//     to /abs/path/math.zg. The bare path must be a valid Zerg identifier
//     ([A-Za-z_][A-Za-z0-9_]*). Slashes, dots, leading digits, and any
//     other punctuation reject with the v0.5-flat-only diagnostic so v0.6+
//     can admit richer paths as a strict superset without breaking v0.5.
//   - The entry module is always named "main", regardless of the source
//     filename. Sibling modules carry their absolute resolved path as Name.
//   - Aliased imports (`import "x" as y`) bind the alias as the local name
//     in the importing module; resolution still operates on Path.
//   - Modules deduplicate by canonical absolute path. Two ImportDecls
//     resolving to the same file share one *Module pointer.
//   - Cycles (A→A, A→B→A, …) reject with a path-listing diagnostic
//     anchored on the closing ImportDecl. The path uses module short-names
//     (basename without .zg) for readability.
//   - Each module's `# requires: vX.Y` line is enforced against the
//     toolchain version through internal/version, exactly as the entry
//     file's is enforced by cmd/zerg's version gate.
//
// Diagnostics anchor on the offending ImportDecl's Pos. The message names
// the source file (relative to CWD when feasible, otherwise absolute).
package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
	"github.com/cmj/zerg/src/bootstrap/internal/version"
)

// Module is one parsed .zg file plus its module identity. Source is kept
// alongside Program so diagnostics that need a line lookup can re-walk the
// raw text without re-reading from disk.
type Module struct {
	// Name is "main" for the entry module; otherwise the canonical absolute
	// path of the .zg file. Unit 6's mangler walks this field.
	Name string
	// Path is the canonical absolute path of the .zg file. For the entry
	// module it is the entry file's resolved path on disk; the Name remains
	// the literal "main" regardless.
	Path string
	// ShortName is the basename of Path without the .zg extension. Used by
	// cycle diagnostics for readability ("a.zg imports b.zg ..."). For the
	// entry module ShortName == "main".
	ShortName string
	// Source is the raw bytes read from Path. Carried so future diagnostic
	// machinery (line lookup, snippet rendering) does not have to re-read
	// the file from disk.
	Source []byte
	// Program is the already-parsed AST.
	Program *syntax.Program
	// Imports is one ResolvedImport per ImportDecl in Program, in source
	// order. Group-form imports were desugared by the parser into one
	// ImportDecl each, so this slice mirrors what consumers see in
	// Program.Statements.
	Imports []*ResolvedImport
}

// ResolvedImport ties one ImportDecl to its loaded sibling Module. The
// LocalName is what the importing module addresses the sibling as (the
// alias if `as` was written, else the bare path). Unit 3's typeck consumes
// LocalName when resolving cross-module references.
type ResolvedImport struct {
	Decl      *syntax.ImportDecl
	Target    *Module
	LocalName string
}

// Bundle is the loader's return value. Entry is the entry module (Name ==
// "main"). Modules contains every loaded module — Entry plus every
// transitively imported sibling — deduplicated by canonical path. Order is
// post-DFS-discovery: Entry comes first, siblings follow in the order they
// were first reached. Iteration order is stable across runs only insofar
// as the source code's import order is stable; consumers that need a
// deterministic visit order should walk the Imports lists themselves.
type Bundle struct {
	Entry   *Module
	Modules []*Module
}

// BundleEntry / BundleModules implement syntax.BundleView so the loader's
// Bundle can be handed to syntax.CheckBundle without an import cycle.
func (b *Bundle) BundleEntry() syntax.ModuleView {
	if b == nil {
		return nil
	}
	return b.Entry
}

// BundleModules returns every module in the bundle as a slice of
// syntax.ModuleView for typeck consumption.
func (b *Bundle) BundleModules() []syntax.ModuleView {
	if b == nil {
		return nil
	}
	out := make([]syntax.ModuleView, len(b.Modules))
	for i, m := range b.Modules {
		out[i] = m
	}
	return out
}

// ModuleName / ModuleProgram / ModuleImports implement syntax.ModuleView.
func (m *Module) ModuleName() string             { return m.Name }
func (m *Module) ModuleProgram() *syntax.Program { return m.Program }
func (m *Module) ModuleImports() []syntax.ImportView {
	if m == nil {
		return nil
	}
	out := make([]syntax.ImportView, len(m.Imports))
	for i, im := range m.Imports {
		out[i] = im
	}
	return out
}

// ImportLocalName / ImportTarget / ImportDecl implement syntax.ImportView.
func (r *ResolvedImport) ImportLocalName() string { return r.LocalName }
func (r *ResolvedImport) ImportTarget() syntax.ModuleView {
	if r == nil {
		return nil
	}
	return r.Target
}
func (r *ResolvedImport) ImportDecl() *syntax.ImportDecl {
	if r == nil {
		return nil
	}
	return r.Decl
}

// LoadError is the loader's error type. It carries the position of the
// offending ImportDecl (or the entry file's position 1:1 when the failure
// is at the entry-file level) so the CLI can render a uniform "file:line:
// col: <message>" prefix.
type LoadError struct {
	File    string
	Pos     syntax.Position
	Message string
}

// Error implements the error interface.
func (e *LoadError) Error() string {
	if e.File != "" && e.Pos.Line > 0 {
		return fmt.Sprintf("%s:%s: %s", displayPath(e.File), e.Pos, e.Message)
	}
	if e.File != "" {
		return fmt.Sprintf("%s: %s", displayPath(e.File), e.Message)
	}
	return e.Message
}

// errorAtImport builds a LoadError anchored on an ImportDecl in srcFile.
func errorAtImport(srcFile string, decl *syntax.ImportDecl, format string, args ...any) error {
	return &LoadError{
		File:    srcFile,
		Pos:     decl.Pos,
		Message: fmt.Sprintf(format, args...),
	}
}

// errorAtFile builds a LoadError anchored on a whole file (used when the
// failure isn't tied to a specific ImportDecl, e.g. parse errors on the
// entry file).
func errorAtFile(srcFile string, format string, args ...any) error {
	return &LoadError{
		File:    srcFile,
		Message: fmt.Sprintf(format, args...),
	}
}

// Load reads, lexes, parses, and resolves the entry file at entryPath plus
// every sibling reached by following ImportDecls. The returned Bundle
// always has Entry.Name == "main". Errors carry a LoadError when the
// failure was loader-level (cycle, missing sibling, invalid path); parse
// or lex errors come through verbatim from the syntax package.
//
// Load does not run typeck — Unit 3 wires that. Today the typical caller
// is run.go / build.go, which invokes Load and then passes
// Bundle.Entry.Program to syntax.Check for the existing single-file
// behaviour. Once Unit 3 is in, the same call site walks every Module in
// the Bundle.
func Load(entryPath string) (*Bundle, error) {
	abs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, errorAtFile(entryPath, "%v", err)
	}
	abs = filepath.Clean(abs)

	l := &loader{
		modules: map[string]*Module{},
		// visiting tracks the in-flight DFS stack — used by cycle
		// detection. Keys are canonical paths; values are the short
		// name we'll print in the diagnostic.
		visiting: map[string]bool{},
	}

	entry, err := l.loadEntry(abs)
	if err != nil {
		return nil, err
	}

	return &Bundle{
		Entry:   entry,
		Modules: l.ordered,
	}, nil
}

// loader carries the cross-recursion state for a single Load call.
type loader struct {
	// modules is the canonical-path → *Module map. A *Module is inserted
	// after its source has been read and parsed; resolution recurses into
	// it once and the cached pointer is reused on subsequent visits.
	modules map[string]*Module
	// ordered preserves the discovery order of modules. Entry is first;
	// transitively reached siblings follow in DFS-pre-order. Bundle.Modules
	// is set from this slice so callers can iterate deterministically.
	ordered []*Module
	// visiting tracks the active DFS stack as a path-stack for cycle
	// detection. The slice gives us an ordered cycle path for the
	// diagnostic; the map gives O(1) "is this path on the stack?".
	visiting     map[string]bool
	visitingPath []*Module
}

// loadEntry parses the entry file and walks its imports. The entry's
// canonical Name is the literal "main" per PLAN.md §Resolution rules; the
// canonical Path is the entry file's resolved absolute path. This special-
// cases the entry module so two `zerg run` invocations on different
// filenames don't accidentally produce different mangled symbols for the
// "main" namespace.
//
// v0.13 platform-suffix gate (entry-file half): if the entry's basename
// carries a recognized `_<platform>.zg` suffix that does not match the host
// platform, reject before touching the file with a wrong-platform
// diagnostic. The sibling-import half lives in resolveImports.
func (l *loader) loadEntry(absPath string) (*Module, error) {
	base := filepath.Base(absPath)
	if suffix := fileSuffixPlatform(base); suffix != "" && suffix != hostPlatform() {
		return nil, errorAtFile(absPath,
			"entry file %s is gated to %s but host is %s",
			base, suffix, hostName())
	}
	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, errorAtFile(absPath, "%v", err)
	}
	if err := checkRequires(absPath, src); err != nil {
		return nil, err
	}
	prog, err := parseSource(absPath, src)
	if err != nil {
		return nil, err
	}

	mod := &Module{
		Name:      "main",
		Path:      absPath,
		ShortName: "main",
		Source:    src,
		Program:   prog,
	}
	l.modules[absPath] = mod
	l.ordered = append(l.ordered, mod)

	l.visiting[absPath] = true
	l.visitingPath = append(l.visitingPath, mod)
	defer func() {
		delete(l.visiting, absPath)
		l.visitingPath = l.visitingPath[:len(l.visitingPath)-1]
	}()

	if err := l.resolveImports(mod); err != nil {
		return nil, err
	}
	return mod, nil
}

// loadSibling parses the sibling at absPath (resolved by the caller) and
// recurses into its imports. importer + decl supply the diagnostic anchor
// when the load fails. If the sibling is already in flight on the DFS
// stack we close a cycle and return the path-listing diagnostic.
func (l *loader) loadSibling(importer *Module, decl *syntax.ImportDecl, absPath string) (*Module, error) {
	if existing, ok := l.modules[absPath]; ok {
		// Already loaded (or in flight). If it's on the active visit
		// stack, this rediscovery closes a cycle — emit the path listing
		// anchored on this ImportDecl.
		if l.visiting[absPath] {
			return nil, l.cycleError(importer, decl, existing)
		}
		return existing, nil
	}

	src, err := os.ReadFile(absPath)
	if err != nil {
		// File not found / unreadable. The wording here matches the
		// "module '<name>' not found at '<resolved-path>'" specified by
		// PLAN.md §Resolution rules / the v0.5 task brief.
		bindName := decl.Path
		if os.IsNotExist(err) {
			return nil, errorAtImport(importer.Path, decl,
				"module %q not found at %s", bindName, displayPath(absPath))
		}
		return nil, errorAtImport(importer.Path, decl,
			"cannot read module %q at %s: %v", bindName, displayPath(absPath), err)
	}
	if err := checkRequiresImport(importer.Path, decl, absPath, src); err != nil {
		return nil, err
	}
	prog, err := parseSourceFromImport(importer.Path, decl, absPath, src)
	if err != nil {
		return nil, err
	}

	short := strings.TrimSuffix(filepath.Base(absPath), ".zg")
	mod := &Module{
		Name:      absPath,
		Path:      absPath,
		ShortName: short,
		Source:    src,
		Program:   prog,
	}
	l.modules[absPath] = mod
	l.ordered = append(l.ordered, mod)

	l.visiting[absPath] = true
	l.visitingPath = append(l.visitingPath, mod)
	defer func() {
		delete(l.visiting, absPath)
		l.visitingPath = l.visitingPath[:len(l.visitingPath)-1]
	}()

	if err := l.resolveImports(mod); err != nil {
		return nil, err
	}
	return mod, nil
}

// resolveImports walks every ImportDecl in mod.Program and populates
// mod.Imports. Resolution: convert the bare path to a sibling absolute
// path, recurse via loadSibling. On the way back up we record the
// resolved sibling pointer in mod.Imports.
//
// Import paths beginning with `std/` or `sys/` resolve against the
// on-disk stdlib tree (see stdlib_root.go) and never fall through to
// the working directory. Misses surface as "stdlib module not found" /
// "sys module not found".
//
// v0.13: sibling resolution goes through resolveSiblingPath, which
// consults the platform-suffix table (`_macos.zg`, `_linux.zg`) before
// falling back to the unsuffixed name. Stdlib resolution is intentionally
// unaffected — see loadStdlib for the carve-out pin.
func (l *loader) resolveImports(mod *Module) error {
	siblingDir := filepath.Dir(mod.Path)
	for _, stmt := range mod.Program.Statements {
		decl, ok := stmt.(*syntax.ImportDecl)
		if !ok {
			continue
		}
		if fam := matchEmbeddedFamily(decl.Path); fam != nil {
			target, err := l.loadEmbeddedFamily(mod, decl, *fam)
			if err != nil {
				return err
			}
			localName := decl.Alias
			if localName == "" {
				localName = strings.TrimPrefix(decl.Path, fam.prefix)
			}
			mod.Imports = append(mod.Imports, &ResolvedImport{
				Decl:      decl,
				Target:    target,
				LocalName: localName,
			})
			continue
		}
		if !isValidIdentifier(decl.Path) {
			return errorAtImport(mod.Path, decl,
				"v0.5 supports flat sibling imports only: invalid module path %q", decl.Path)
		}
		siblingPath, err := resolveSiblingPath(siblingDir, decl.Path)
		if err != nil {
			return errorAtImport(mod.Path, decl, "%s", err.Error())
		}
		// filepath.Clean would suffice but Join already cleans.
		target, err := l.loadSibling(mod, decl, siblingPath)
		if err != nil {
			return err
		}
		localName := decl.Alias
		if localName == "" {
			localName = decl.Path
		}
		mod.Imports = append(mod.Imports, &ResolvedImport{
			Decl:      decl,
			Target:    target,
			LocalName: localName,
		})
	}
	return nil
}

// embeddedFamily describes a toolchain-shipped stdlib family — `std/*`
// or `sys/*` — so loadEmbeddedFamily can resolve either through one
// code path. New families add an entry to embeddedFamilies; the loader
// dispatch and miss-diagnostic shape come along for free.
type embeddedFamily struct {
	// prefix is the leading import-path segment plus slash, e.g. "std/".
	prefix string
	// label is the diagnostic word for misses ("stdlib" / "sys").
	label string
	// modulePath builds the on-disk path from the post-prefix module
	// name, e.g. stdlibModulePath / sysModulePath.
	modulePath func(name string) string
}

// embeddedFamilies is the registry of toolchain-shipped families.
//
// v0.13 carve-out pin for the std/* family: the platform-suffix table
// (`_macos.zg`, `_linux.zg`) does NOT apply to embedded families.
// Stdlib modules are platform-neutral; if one needs platform branching,
// it does so internally (e.g. via a `# requires:` arch line or an
// architecture-aware shim) rather than through filename-suffix routing.
// Deferred to v0.14+.
//
// The sys/* family uses a directory-with-mod.zg layout (Rust's mod.rs
// convention) so future platform-suffix variants can sit alongside
// mod.zg inside the module's directory; the std/* family keeps its
// flat <name>.zg layout for v0.5–v0.12 back-compat.
var embeddedFamilies = []embeddedFamily{
	{prefix: "std/", label: "stdlib", modulePath: stdlibModulePath},
	{prefix: "sys/", label: "sys", modulePath: sysModulePath},
}

// matchEmbeddedFamily returns the family whose prefix matches path, or
// nil if path is a sibling-import. Empty / malformed paths under a
// recognised prefix still match — loadEmbeddedFamily produces the
// uniform "<label> module not found" diagnostic for those so the
// surface is uniform regardless of why the lookup failed.
func matchEmbeddedFamily(path string) *embeddedFamily {
	for i := range embeddedFamilies {
		if strings.HasPrefix(path, embeddedFamilies[i].prefix) {
			return &embeddedFamilies[i]
		}
	}
	return nil
}

// loadEmbeddedFamily resolves a `<prefix><name>` import against the
// on-disk stdlib tree (see stdlibRoot in stdlib_root.go) using the
// family-supplied modulePath builder. When the on-disk read misses,
// the loader falls through to stdlibFallback — the bootstrap-provided
// built-in copy of the stdlib — before surfacing the uniform
// "<label> module not found" diagnostic. The stdlib-file parser flag
// is used so `__builtin <ident>` markers parse regardless of whether
// the source came from disk or fallback.
//
// Names that fail the v0.5 identifier rule, and leading-underscore
// names (reserved for internal scaffolding files like
// `_placeholder.zg`), short-circuit to the not-found diagnostic
// without consulting either disk or fallback so they stay invisible
// to user code.
func (l *loader) loadEmbeddedFamily(importer *Module, decl *syntax.ImportDecl, fam embeddedFamily) (*Module, error) {
	name := strings.TrimPrefix(decl.Path, fam.prefix)
	if name == "" || strings.HasPrefix(name, "_") || !isValidIdentifier(name) {
		return nil, errorAtImport(importer.Path, decl,
			"%s module not found: %s", fam.label, decl.Path)
	}
	diskPath := fam.modulePath(name)
	if existing, ok := l.modules[diskPath]; ok {
		if l.visiting[diskPath] {
			return nil, l.cycleError(importer, decl, existing)
		}
		return existing, nil
	}
	src, err := os.ReadFile(diskPath)
	if err != nil {
		fallback, ok := stdlibFallback(fam.label, name)
		if !ok {
			return nil, errorAtImport(importer.Path, decl,
				"%s module not found: %s", fam.label, decl.Path)
		}
		src = fallback
	}
	if err := checkRequiresImport(importer.Path, decl, diskPath, src); err != nil {
		return nil, err
	}
	prog, err := parseStdlibSource(importer.Path, decl, diskPath, src)
	if err != nil {
		return nil, err
	}

	mod := &Module{
		Name:      diskPath,
		Path:      diskPath,
		ShortName: name,
		Source:    src,
		Program:   prog,
	}
	l.modules[diskPath] = mod
	l.ordered = append(l.ordered, mod)

	l.visiting[diskPath] = true
	l.visitingPath = append(l.visitingPath, mod)
	defer func() {
		delete(l.visiting, diskPath)
		l.visitingPath = l.visitingPath[:len(l.visitingPath)-1]
	}()

	if err := l.resolveImports(mod); err != nil {
		return nil, err
	}
	return mod, nil
}

// parseStdlibSource lexes + parses an on-disk stdlib source under the
// `InStdlibFile: true` parser flag so `__builtin <ident>` markers parse.
// Errors anchor on the importing module's decl, mirroring the sibling-load
// diagnostic shape.
func parseStdlibSource(importerPath string, decl *syntax.ImportDecl, diskPath string, src []byte) (*syntax.Program, error) {
	tokens, err := syntax.Lex(src)
	if err != nil {
		return nil, errorAtImport(importerPath, decl,
			"failed to lex stdlib module %q (%s): %v",
			decl.Path, diskPath, err)
	}
	prog, err := syntax.ParseWithOptions(tokens, syntax.ParseOptions{InStdlibFile: true})
	if err != nil {
		return nil, errorAtImport(importerPath, decl,
			"failed to parse stdlib module %q (%s): %v",
			decl.Path, diskPath, err)
	}
	return prog, nil
}

// cycleError builds the v0.5 cycle diagnostic. The path listing walks the
// active DFS stack from the rediscovered module forward, finishing with
// the closing edge introduced by decl. Anchor on the offending decl.
func (l *loader) cycleError(importer *Module, decl *syntax.ImportDecl, target *Module) error {
	// Find the position of `target` on the visiting stack.
	startIdx := -1
	for i, m := range l.visitingPath {
		if m == target {
			startIdx = i
			break
		}
	}
	// Should always be found — visiting[target.Path] is true on this branch.
	// Defence-in-depth: degrade gracefully if the invariant ever drifts.
	if startIdx < 0 {
		return errorAtImport(importer.Path, decl,
			"import cycle detected involving %s", target.ShortName)
	}

	var b strings.Builder
	b.WriteString("import cycle detected:\n")
	for i := startIdx; i < len(l.visitingPath)-1; i++ {
		from := l.visitingPath[i]
		to := l.visitingPath[i+1]
		fmt.Fprintf(&b, "  %s.zg imports %s.zg\n", from.ShortName, to.ShortName)
	}
	// The closing edge that reintroduces target.
	fmt.Fprintf(&b, "  %s.zg imports %s.zg  <-- cycle here", importer.ShortName, target.ShortName)

	return errorAtImport(importer.Path, decl, "%s", b.String())
}

// isValidIdentifier reports whether s is a syntactically valid Zerg
// identifier ([A-Za-z_][A-Za-z0-9_]*). v0.5 sibling imports use this rule
// to reject paths with slashes, dots, leading digits, or non-ASCII bytes.
// v0.6+ will admit richer paths as a strict superset; reusing the check
// here keeps the v0.5 surface deliberately narrow.
func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// checkRequires gates the entry file's `# requires: vX.Y` marker against
// the toolchain version. The cmd/zerg gate already runs once before the
// loader is invoked from the CLI, but tests and library callers exercise
// Load directly — running the gate here closes that gap so the loader
// never returns a Bundle whose entry exceeds the toolchain version.
func checkRequires(absPath string, src []byte) error {
	maj, min, ok := version.ScanRequires(src)
	if !ok {
		return nil
	}
	if version.Less(version.Major, version.Minor, maj, min) {
		return errorAtFile(absPath,
			"requires v%d.%d (current is v%d.%d)",
			maj, min, version.Major, version.Minor)
	}
	return nil
}

// checkRequiresImport gates an imported module's `# requires:` line. The
// diagnostic anchors on the importing module's ImportDecl, names the
// offending sibling by its bare import path, and follows the same
// "<name> requires v<X.Y> (current is v<A.B>)" wording the CLI gate uses.
func checkRequiresImport(importerPath string, decl *syntax.ImportDecl, absPath string, src []byte) error {
	maj, min, ok := version.ScanRequires(src)
	if !ok {
		return nil
	}
	if version.Less(version.Major, version.Minor, maj, min) {
		return errorAtImport(importerPath, decl,
			"module %q requires v%d.%d (current is v%d.%d)",
			decl.Path, maj, min, version.Major, version.Minor)
	}
	return nil
}

// parseSource lexes + parses src, wrapping syntax errors with a "<file>: "
// prefix so callers can return the error verbatim. The returned error
// preserves the underlying syntax error for tests that match on its text.
func parseSource(absPath string, src []byte) (*syntax.Program, error) {
	tokens, err := syntax.Lex(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", displayPath(absPath), err)
	}
	prog, err := syntax.Parse(tokens)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", displayPath(absPath), err)
	}
	return prog, nil
}

// parseSourceFromImport is parseSource for sibling files: lex/parse errors
// are surfaced as LoadErrors anchored on the importing decl so the user
// sees "(importer):line:col: failed to parse imported module 'name': …".
func parseSourceFromImport(importerPath string, decl *syntax.ImportDecl, absPath string, src []byte) (*syntax.Program, error) {
	tokens, err := syntax.Lex(src)
	if err != nil {
		return nil, errorAtImport(importerPath, decl,
			"failed to lex imported module %q (%s): %v",
			decl.Path, displayPath(absPath), err)
	}
	prog, err := syntax.Parse(tokens)
	if err != nil {
		return nil, errorAtImport(importerPath, decl,
			"failed to parse imported module %q (%s): %v",
			decl.Path, displayPath(absPath), err)
	}
	return prog, nil
}

// displayPath returns a relative path from CWD if that's reasonable,
// otherwise the absolute path. Pure cosmetics — keeps diagnostics short
// for in-repo files without misleading users about where the file lives.
func displayPath(absPath string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return absPath
	}
	rel, err := filepath.Rel(cwd, absPath)
	if err != nil {
		return absPath
	}
	// If the rel path walks back up out of CWD it's not actually shorter
	// or clearer — fall back to the absolute path.
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return absPath
	}
	return rel
}
