package emit

import (
	"strings"
	"testing"
)

// TestDeferMethodCallLowering is the loud-gap fix oracle for `defer recv.method(args)`:
// a method callee is captured like a direct call, but the borrowed receiver is the
// leading env field (f0, captured plainly — a method never releases its receiver) and
// the thunk dispatches to the resolved impl-method instance, receiver first.
func TestDeferMethodCallLowering(t *testing.T) {
	src := "struct Lock {\n\tpub n: int\n}\n" +
		"impl Lock {\n\tpub fn teardown() {\n\t\tprint this.n\n\t}\n}\n" +
		"fn main() {\n\ta := Lock(n: 1)\n\tdefer a.teardown()\n\tprint 0\n}\n"
	code, manifest := emitWithManifest(t, src)
	if !manifest.NeedsRuntime {
		t.Fatalf("a defer must pull in the runtime\n%s", code)
	}
	for _, want := range []string{
		"typedef struct { zg_Lock f0; } zg_deferenv_0;", // receiver is the leading env field
		"zg_deferenv_0 *zg_c = (zg_deferenv_0 *)p;",
		"zg_c->f0);",                        // dispatched receiver-first
		"zg_deferenv_0 zg_denv = { zg_a };", // borrowed receiver captured plainly (no retain)
		"zrt_defer(zg_deferfn_0, &zg_denv);",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("defer-method C missing %q\n%s", want, code)
		}
	}
}
