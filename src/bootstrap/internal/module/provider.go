// Package module is the whole-program module loader (Phase 1g, Decision F1/F2):
// it resolves an entry file's `import "a/b"` graph against a set of source roots,
// detects import cycles, and flattens every reachable module into a single
// *ast.File for the existing sema -> mono -> emit pipeline. Each imported module's
// top-level items are mangled by the module's CANONICAL PATH (not the local
// binding name), so two modules may share a member name and a module-private item
// never leaks into the entry module's unqualified scope. The entry (main) module
// is left untouched, so a program with no cross-module import flattens to exactly
// its own file and keeps its historic, byte-identical C.
//
// This generalizes the pre-1g single-unit stdlib bundle: only the resolver moved
// here; sema/mono/emit are unchanged apart from keying a cross-module member on
// the canonical tag (sema.NamespaceMemberName).
package module

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// ModuleFile is one source file of a module: its base name (for diagnostics) and
// its Zerg source. Files of one module share a single namespace.
type ModuleFile struct {
	Name string
	Src  string
}

// FileProvider is one source root the loader searches for an imported module. A
// program has two roots, tried in order (§2.1): the entry file's directory tree
// (OSProvider) and the embedded standard library (FSProvider over an embed.FS).
type FileProvider interface {
	// Resolve locates the module named by importPath (a '/'-separated path relative
	// to this root). It returns the module's CANONICAL identity (the dedup key and
	// mangling base), the module's source files, and whether it was found here.
	Resolve(importPath string) (canonical string, files []ModuleFile, ok bool)
}

// OSProvider resolves a module to a directory under Root: `import "util/text"`
// loads every `.zg` file in `<Root>/util/text/` as one module (a module is a
// directory; same-directory files share a namespace). Its canonical identity is
// the cleaned import path, so the same directory imported from two files (even
// under different `as` aliases) is one module.
type OSProvider struct {
	Root string
}

// Resolve reads the module's Zerg sources in a stable order — a directory of them, or a single
// `<path>.zg` file.
//
// IT USED TO BE DIRECTORY-ONLY, and that was this loader's defence against a file taking the
// name of a standard-library module from every file beside it. The defence is the `./` prefix
// now (GRAMMAR#import-path): a bare path cannot reach this root at all, so a single-file module
// here can shadow nothing, and refusing one would refuse a shape the language has.
func (p OSProvider) Resolve(importPath string) (string, []ModuleFile, bool) {
	return resolveModule(os.DirFS(p.Root), importPath, true)
}

// FSProvider resolves a module under an arbitrary fs.FS — the embedded standard
// library (Phase 1g S4). It routes stdlib resolution through the SAME module
// machinery as a user directory module, replacing the pre-1g flat bundle that read
// the source out of band. It accepts both a directory module (`<path>/`) and a
// single-file module (`<path>.zg`), so the flat embedded stdlib layout (`io.zg` at
// the root) resolves as a file module while a future directory-shaped stdlib module
// works with no change.
type FSProvider struct {
	FS fs.FS
}

// Resolve reads the module (directory or single file) under the embedded root.
func (p FSProvider) Resolve(importPath string) (string, []ModuleFile, bool) {
	return resolveModule(p.FS, importPath, true)
}

// resolveModule locates importPath under fsys as a module: a directory whose `.zg`
// files share one namespace, or — when allowFile — a single `<path>.zg` file. It
// returns the cleaned canonical path (the dedup + mangling key), the module's
// sources in a stable order, and whether it was found. OSProvider and FSProvider
// share this one code path, so the user tree and the embedded stdlib resolve
// identically.
func resolveModule(fsys fs.FS, importPath string, allowFile bool) (string, []ModuleFile, bool) {
	canonical := path.Clean(importPath)
	if canonical == "." || canonical == ".." || strings.HasPrefix(canonical, "../") || strings.HasPrefix(canonical, "/") {
		return "", nil, false
	}
	dirFiles, dirOK := resolveDirModule(fsys, canonical)
	fileFiles, fileOK := resolveFileModule(fsys, canonical)
	if !allowFile {
		fileOK = false
	}
	// BOTH SHAPES AT ONE NAME IS NO MODULE. A silent precedence — this used to read the
	// directory and never the file, while the shipping compiler read the file and never the
	// directory — is a question a reader eventually has to ask, and there should be nothing
	// to ask. The import is then refused as unresolved, which is a rejection either way.
	if dirOK && fileOK {
		return "", nil, false
	}
	if dirOK {
		return canonical, dirFiles, true
	}
	if fileOK {
		return canonical, fileFiles, true
	}
	return "", nil, false
}

// LocalPath reports whether an import path names THIS PROJECT — the `./` prefix — and returns
// the package path with that prefix taken off. A bare path names the standard library and a
// path whose first segment holds a '.' names a host; a package name may not hold one, which is
// what makes the three disjoint (GRAMMAR#import-path).
func LocalPath(importPath string) (string, bool) {
	if strings.HasPrefix(importPath, "./") {
		return strings.TrimPrefix(importPath, "./"), true
	}
	return importPath, false
}

// RemotePath reports whether an import path names a remote package: a first segment that holds
// a '.', which a package name never can.
func RemotePath(importPath string) bool {
	if _, local := LocalPath(importPath); local {
		return false
	}
	first := importPath
	if i := strings.IndexByte(first, '/'); i >= 0 {
		first = first[:i]
	}
	return strings.Contains(first, ".")
}

// PathIllFormed answers why a string is not an import path, or "" when it is one. The seed
// holds the same grammar the shipping compiler does; what it does not hold is that compiler's
// sentences, which are the language's contract and not the seed's (docs/conformance.md).
func PathIllFormed(importPath string) string {
	if importPath == "" {
		return "an import path may not be empty"
	}
	if strings.HasPrefix(importPath, "/") {
		return "an import path may not begin with `/`"
	}
	body, local := LocalPath(importPath)
	if local && body == "" {
		return "`./` names no module on its own"
	}
	if strings.HasSuffix(importPath, ".zg") {
		return "an import names a module, not a file"
	}
	segs := strings.Split(body, "/")
	for _, seg := range segs {
		if seg == "." || seg == ".." {
			return "`..` is not part of an import path"
		}
	}
	remote := RemotePath(importPath)
	for i, seg := range segs {
		if remote && i == 0 {
			continue
		}
		if seg == "" {
			return "an import path is written one way, and this one has an empty segment"
		}
		if !packageName(seg) {
			return fmt.Sprintf("%q is not a package name", seg)
		}
	}
	return ""
}

// packageName is `[a-z] [a-z0-9_]*`. Upper case is outside it so that a case-folding
// filesystem cannot decide whether a program builds.
func packageName(seg string) bool {
	if seg == "" || seg[0] < 'a' || seg[0] > 'z' {
		return false
	}
	for i := 1; i < len(seg); i++ {
		c := seg[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// IsTestFile reports whether a source is a TEST FILE by the build tool's convention,
// which is its name (docs/runtime/package.md). A normal build compiles none of them,
// so nothing a test declares reaches a shipped artifact or a module's surface. The
// shipping compiler states the same rule in zergc.zg's is_test_file.
func IsTestFile(name string) bool {
	return strings.HasSuffix(name, "_test.zg")
}

// resolveDirModule reads canonical as a directory module: every `.zg` file directly
// in it that is not a test file, in a stable (name-sorted) order.
func resolveDirModule(fsys fs.FS, canonical string) ([]ModuleFile, bool) {
	info, err := fs.Stat(fsys, canonical)
	if err != nil || !info.IsDir() {
		return nil, false
	}
	entries, err := fs.ReadDir(fsys, canonical)
	if err != nil {
		return nil, false
	}
	var files []ModuleFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zg") || IsTestFile(e.Name()) {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(canonical, e.Name()))
		if err != nil {
			return nil, false
		}
		files = append(files, ModuleFile{Name: e.Name(), Src: string(data)})
	}
	if len(files) == 0 {
		return nil, false
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, true
}

// resolveFileModule reads `<canonical>.zg` as a single-file module.
func resolveFileModule(fsys fs.FS, canonical string) ([]ModuleFile, bool) {
	data, err := fs.ReadFile(fsys, canonical+".zg")
	if err != nil {
		return nil, false
	}
	return []ModuleFile{{Name: path.Base(canonical) + ".zg", Src: string(data)}}, true
}

// mangleTag turns a canonical module path into its C-mangle tag: '/' becomes '_'
// ("util/text" -> "util_text"), so the tag is a legal C identifier fragment. A
// flat single-segment module (a stdlib "io") is its own tag, which keeps the
// stdlib bundle's historic `io__println` spelling byte-identical.
func mangleTag(canonical string) string {
	return strings.ReplaceAll(canonical, "/", "_")
}
