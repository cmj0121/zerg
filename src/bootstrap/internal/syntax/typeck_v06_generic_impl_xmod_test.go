package syntax_test

import (
	"testing"
)

// v0.6 Unit 3.5 — cross-module generic impls.
//
// The orphan rule extends to generic impls: at least one of (receiver-decl
// owner, spec-decl owner) must be the importing module. The check runs on
// the BASE decls, not the type-args.

func TestCheckBundleGenericImplOrphanBothForeign(t *testing.T) {
	loadErr(t, fixture{
		"util":  "pub struct Box[T] { value: T }\n",
		"other": "pub spec Printable { fn fmt() -> str }\n",
		"main": "" +
			"import \"util\"\n" +
			"import \"other\"\n" +
			"impl[T] util.Box[T] for other.Printable {\n" +
			"pub fn fmt() -> str { return \"b\" }\n" +
			"}\n",
	}, "main.zg", "cross-module orphan impl")
}

func TestCheckBundleGenericImplOrphanForeignInherent(t *testing.T) {
	loadErr(t, fixture{
		"util": "pub struct Box[T] { value: T }\n",
		"main": "" +
			"import \"util\"\n" +
			"impl[T] util.Box[T] {\n" +
			"fn ping() -> int { return 1 }\n" +
			"}\n",
	}, "main.zg", "cross-module orphan impl")
}

func TestCheckBundleGenericImplLocalTypeForeignSpec(t *testing.T) {
	// Local generic struct + foreign spec — orphan rule allows it.
	loadOk(t, fixture{
		"util": "pub spec Printable { fn fmt() -> str }\n",
		"main": "" +
			"import \"util\"\n" +
			"struct Box[T] { value: T }\n" +
			"impl[T] Box[T] for util.Printable {\n" +
			"pub fn fmt() -> str { return \"b\" }\n" +
			"}\n" +
			"let b: Box[int] = Box { value: 7 }\n" +
			"let s := b.fmt()\n",
	}, "main.zg")
}
