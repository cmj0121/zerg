package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.5 Unit 1b — parser tests for `import`, `as`, and grouped imports.
//
// The parser produces flat ImportDecl nodes for all three surface shapes.
// The grouped form `import (...)` is desugared at parse time into one
// ImportDecl per entry; downstream layers never see a group AST node.
//
// Reserved-name rejection (PLAN.md §Resolution rules tenth-man pin) lives at
// parse time so the diagnostic carries a parser position, not a typeck one.
// The check cross-references the same `keywords` map the lexer uses; adding
// a future keyword automatically tightens this.
// ---------------------------------------------------------------------------

func TestParseImportSingle(t *testing.T) {
	prog := parseProgramSrc(t, "import \"math\"\n")
	im := expectOne[*ImportDecl](t, prog)
	if im.Path != "math" {
		t.Errorf("Path = %q, want %q", im.Path, "math")
	}
	if im.Alias != "" {
		t.Errorf("Alias = %q, want \"\" (no `as` clause)", im.Alias)
	}
}

func TestParseImportWithAlias(t *testing.T) {
	prog := parseProgramSrc(t, "import \"math\" as m\n")
	im := expectOne[*ImportDecl](t, prog)
	if im.Path != "math" {
		t.Errorf("Path = %q, want %q", im.Path, "math")
	}
	if im.Alias != "m" {
		t.Errorf("Alias = %q, want %q", im.Alias, "m")
	}
	if im.AliasPos == (Position{}) {
		t.Errorf("AliasPos was zero; expected the alias token's position")
	}
}

func TestParseImportGroupTwoEntries(t *testing.T) {
	src := "import (\n    \"math\"\n    \"greeting\" as g\n)\n"
	prog := parseProgramSrc(t, src)
	if len(prog.Statements) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(prog.Statements), prog.Statements)
	}
	im0, ok := prog.Statements[0].(*ImportDecl)
	if !ok {
		t.Fatalf("statement 0 is %T, want *ImportDecl", prog.Statements[0])
	}
	if im0.Path != "math" || im0.Alias != "" {
		t.Errorf("entry 0: Path=%q Alias=%q, want Path=math Alias=\"\"", im0.Path, im0.Alias)
	}
	im1, ok := prog.Statements[1].(*ImportDecl)
	if !ok {
		t.Fatalf("statement 1 is %T, want *ImportDecl", prog.Statements[1])
	}
	if im1.Path != "greeting" || im1.Alias != "g" {
		t.Errorf("entry 1: Path=%q Alias=%q, want Path=greeting Alias=g", im1.Path, im1.Alias)
	}
}

func TestParseImportEmptyGroup(t *testing.T) {
	// `import ()` is admitted as a user-friendly noop and produces zero
	// ImportDecls in the program. Verifies the parser doesn't synthesise
	// a placeholder statement either.
	prog := parseProgramSrc(t, "import ()\n")
	if len(prog.Statements) != 0 {
		t.Fatalf("got %d statements, want 0: %#v", len(prog.Statements), prog.Statements)
	}
}

func TestParseImportFollowedByDecl(t *testing.T) {
	// The parser admits any decl after an import (v0.5 doesn't enforce the
	// "imports must precede decls" ordering — Unit 2's loader doesn't care).
	prog := parseProgramSrc(t, "import \"math\"\nfn main() {}\n")
	if len(prog.Statements) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(prog.Statements), prog.Statements)
	}
	if _, ok := prog.Statements[0].(*ImportDecl); !ok {
		t.Errorf("statement 0 is %T, want *ImportDecl", prog.Statements[0])
	}
	if _, ok := prog.Statements[1].(*FnDecl); !ok {
		t.Errorf("statement 1 is %T, want *FnDecl", prog.Statements[1])
	}
}

func TestParseTwoConsecutiveImports(t *testing.T) {
	prog := parseProgramSrc(t, "import \"a\"\nimport \"b\" as bb\n")
	if len(prog.Statements) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(prog.Statements), prog.Statements)
	}
	im0 := prog.Statements[0].(*ImportDecl)
	if im0.Path != "a" || im0.Alias != "" {
		t.Errorf("import 0: Path=%q Alias=%q, want a/\"\"", im0.Path, im0.Alias)
	}
	im1 := prog.Statements[1].(*ImportDecl)
	if im1.Path != "b" || im1.Alias != "bb" {
		t.Errorf("import 1: Path=%q Alias=%q, want b/bb", im1.Path, im1.Alias)
	}
}

// ---------------------------------------------------------------------------
// Negative cases.
// ---------------------------------------------------------------------------

func TestParseImportMissingStringRejected(t *testing.T) {
	expectParseErr(t, "import\n", "expected string literal after 'import'")
}

func TestParseImportAliasWithoutIdentRejected(t *testing.T) {
	// `import "math" as` with the next significant token being NEWLINE/EOF.
	expectParseErr(t, "import \"math\" as\n", "expected identifier after 'as'")
}

func TestParseImportAliasNotIdentRejected(t *testing.T) {
	// A numeric literal is neither an identifier nor a keyword.
	expectParseErr(t, "import \"math\" as 123\n", "expected identifier after 'as'")
}

func TestParseImportBareKeywordPathRejected(t *testing.T) {
	// The bare form `import "if"` would bind the module as `if`, which is a
	// reserved keyword — reject at parse time with the dedicated diagnostic.
	expectParseErr(t, "import \"if\"\n", "cannot import as if: name is reserved")
}

func TestParseImportAliasAsIfRejected(t *testing.T) {
	expectParseErr(t, "import \"math\" as if\n", "cannot import as if: name is reserved")
}

func TestParseImportAliasAsForRejected(t *testing.T) {
	expectParseErr(t, "import \"math\" as for\n", "cannot import as for: name is reserved")
}

func TestParseImportAliasAsPubRejected(t *testing.T) {
	expectParseErr(t, "import \"math\" as pub\n", "cannot import as pub: name is reserved")
}

func TestParseImportAliasAsAsRejected(t *testing.T) {
	// `import "math" as as` — the alias collides with the `as` keyword
	// itself. The lexer emits KindAs for `as`, so the parser sees the
	// reserved-name shape via the keyword check.
	expectParseErr(t, "import \"math\" as as\n", "cannot import as as: name is reserved")
}

func TestParseImportAliasAsImportRejected(t *testing.T) {
	expectParseErr(t, "import \"math\" as import\n", "cannot import as import: name is reserved")
}

func TestParseImportInsideFnBodyRejected(t *testing.T) {
	src := "fn main() {\n    import \"math\"\n}\n"
	expectParseErr(t, src, "import is only allowed at the top of a file")
}

func TestParseImportGroupWithCommaRejected(t *testing.T) {
	// The group form uses NEWLINE between entries; a comma is the common
	// trailing-style mistake. The parser rejects with a focused diagnostic.
	src := "import (\n    \"math\", \"x\"\n)\n"
	expectParseErr(t, src, "use newline (not comma) between imports")
}
