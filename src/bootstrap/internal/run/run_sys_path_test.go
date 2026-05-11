// End-to-end interpreter tests for the v0.14 sys/path Step-1 shell.
//
// Each test materialises a user program that imports "sys/path", drives
// it through the full loader → CheckBundle → RunBundle pipeline, and
// asserts the resulting stdout. This exercises the new sys/* loader
// family + the Path struct's two Step-1 methods (`to_str`, `is_abs`) +
// the free `new` constructor end-to-end.
//
// Step 2 (basename/dirname/ext/stem) and Step 3 (parent/parts/join)
// will extend this file with the methods they introduce.
package run

import (
	"strings"
	"testing"
)

func TestSysPathNewToStr(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"main.zg": `import "sys/path"
p := path.new("/usr/bin/zerg")
print p.to_str()
`,
	}, "/usr/bin/zerg\n")
}

func TestSysPathIsAbsTrue(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"main.zg": `import "sys/path"
p := path.new("/foo")
print p.is_abs()
`,
	}, "true\n")
}

func TestSysPathIsAbsFalse(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"main.zg": `import "sys/path"
p := path.new("foo/bar")
print p.is_abs()
`,
	}, "false\n")
}

func TestSysPathIsAbsEmpty(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"main.zg": `import "sys/path"
p := path.new("")
print p.is_abs()
`,
	}, "false\n")
}

// Exercises both methods on a single instance in sequence — guards
// against any state aliasing between method dispatches that the
// per-method tests wouldn't surface.
func TestSysPathCombinedShape(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"main.zg": `import "sys/path"
p := path.new("/etc/hosts")
print p.to_str()
print p.is_abs()
`,
	}, "/etc/hosts\ntrue\n")
}

func TestSysPathAliased(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"main.zg": `import "sys/path" as fs
p := fs.new("/var/log")
print p.is_abs()
`,
	}, "true\n")
}

// Diagnostic survives the loader -> CheckBundle -> RunBundle chain
// without being wrapped or swallowed. The loader unit test owns the
// wording; this one owns the end-to-end propagation.
func TestSysPathMissingDiagnostic(t *testing.T) {
	_, err := runBundleFiles(t, "main.zg", map[string]string{
		"main.zg": `import "sys/bogus"
print 1
`,
	})
	if err == nil {
		t.Fatal("expected loader error for missing sys module, got nil")
	}
	if !strings.Contains(err.Error(), "sys module not found: sys/bogus") {
		t.Errorf("error missing 'sys module not found: sys/bogus': %s", err.Error())
	}
}
