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
	"os"
	"path"
	"path/filepath"
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
// (OSProvider) and the embedded standard library (supplied by the caller).
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

// Resolve reads the module directory's Zerg sources in a stable order.
func (p OSProvider) Resolve(importPath string) (string, []ModuleFile, bool) {
	canonical := path.Clean(importPath)
	if canonical == "." || strings.HasPrefix(canonical, "..") {
		return "", nil, false
	}
	dir := filepath.Join(p.Root, filepath.FromSlash(canonical))
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", nil, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, false
	}
	var files []ModuleFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zg") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // a module source under the chosen root
		if err != nil {
			return "", nil, false
		}
		files = append(files, ModuleFile{Name: e.Name(), Src: string(data)})
	}
	if len(files) == 0 {
		return "", nil, false
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return canonical, files, true
}

// mangleTag turns a canonical module path into its C-mangle tag: '/' becomes '_'
// ("util/text" -> "util_text"), so the tag is a legal C identifier fragment. A
// flat single-segment module (a stdlib "io") is its own tag, which keeps the
// stdlib bundle's historic `io__println` spelling byte-identical.
func mangleTag(canonical string) string {
	return strings.ReplaceAll(canonical, "/", "_")
}
