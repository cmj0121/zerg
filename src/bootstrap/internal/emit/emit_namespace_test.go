package emit

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/module"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
)

// oneModule is a source root holding a single importable module, which is the smallest
// arrangement that can exercise a QUALIFIED spelling: a form written `mod.X` needs a real
// `mod` behind it, so parsing one string can never reach these paths.
type oneModule struct {
	path string
	src  string
}

func (p oneModule) Resolve(importPath string) (string, string, []module.ModuleFile, bool) {
	if importPath != p.path {
		return "", "", nil, false
	}
	return importPath, importPath, []module.ModuleFile{{Name: "mod.zg", Src: p.src}}, true
}

// emitCross loads `entry` against a source root holding `lib`, and emits the flattened
// program's C — the seed's whole front end, driven the way a real build drives it.
func emitCross(t *testing.T, lib, entry string) string {
	t.Helper()
	l := module.NewLoader(oneModule{path: "lib", src: lib})
	file, ldiags := l.LoadSource(entry)
	if len(ldiags) != 0 {
		t.Fatalf("loader errors: %v", ldiags)
	}
	info, sdiags := sema.Check(file)
	if len(sdiags) != 0 {
		t.Fatalf("sema errors: %v", sdiags)
	}
	code, _, ediags := Emit(mono.Build(file, info))
	if len(ediags) != 0 {
		t.Fatalf("emit errors: %v", ediags)
	}
	return code
}

// A QUALIFIED CONSTRUCTOR IS STILL A CONSTRUCTOR. Both forms below are the ordinary
// spelling with a module in front of it, and neither had a lowering:
//
//   - `lib.E.B(3)` — sema read the enum as a type used as a value. The NULLARY half
//     (`lib.E.A`) already worked, so the gap was exactly the payload.
//   - `lib.P(1, 2)` — the emitter put the C TYPE name in callee position, `zg_lib__P(…)`,
//     which is not an expression: the failure was `cc` complaining about generated code.
//
// #57's migration is what found them. A file that names its sibling through an import
// writes every cross-file constructor this way, so both forms went from unused to used on
// every line of the compiler at once.
func TestQualifiedConstructors(t *testing.T) {
	const lib = "pub enum E {\n  A\n  B(int)\n}\n" +
		"pub struct P {\n  pub x: int\n  pub y: int\n}\n"

	t.Run("a payload variant through its module", func(t *testing.T) {
		code := emitCross(t, lib, "import \"lib\"\nfn main() {\n  e := lib.E.B(3)\n  nop\n}\n")
		if !strings.Contains(code, ".u.lib__B = {.f0 = 3}") {
			t.Fatalf("want the merged variant's payload slot in the lowering\n---\n%s", tail(code))
		}
	})

	t.Run("a nullary variant through its module", func(t *testing.T) {
		code := emitCross(t, lib, "import \"lib\"\nfn main() {\n  e := lib.E.A\n  nop\n}\n")
		if !strings.Contains(code, "zg_lib__E") {
			t.Fatalf("want the merged enum in the lowering\n---\n%s", tail(code))
		}
	})

	t.Run("a struct through its module is a construction, not a call", func(t *testing.T) {
		code := emitCross(t, lib, "import \"lib\"\nfn main() {\n  p := lib.P(1, 2)\n  print p.x\n}\n")
		// The C type name in callee position is what the fall-through emitted, and it is
		// not an expression — so the assertion is on the SHAPE, which a compound literal
		// has and a call does not.
		if strings.Contains(code, "zg_lib__P(") {
			t.Fatalf("a construction must not be lowered as a call\n---\n%s", tail(code))
		}
		if !strings.Contains(code, "(zg_lib__P){") {
			t.Fatalf("want a compound literal for the construction\n---\n%s", tail(code))
		}
	})
}

// tail is the last part of a lowering, which is where a `main` that failed to lower is.
func tail(code string) string {
	if len(code) < 2000 {
		return code
	}
	return code[len(code)-2000:]
}
