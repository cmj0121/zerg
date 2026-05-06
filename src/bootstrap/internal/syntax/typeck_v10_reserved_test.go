package syntax

import (
	"strings"
	"testing"
)

// v0.10 — reservation rule covers the primitive scalar names
// (int / float / bool / str / byte / rune) and the composite-constructor
// names (list / tuple) per docs/LANGUAGE.md §"Reserved type names".
// User struct / enum / spec declarations of any of those names reject at
// typeck with the standard `name %q is reserved (built-in)` diagnostic.

func TestV10ReservedTypeNames(t *testing.T) {
	names := []string{"int", "float", "bool", "str", "byte", "rune", "list", "tuple"}
	shapes := []struct {
		label string
		fmt   string
	}{
		{"struct", "struct %s { x: int }\n"},
		{"enum", "enum %s { Foo, Bar }\n"},
		{"spec", "spec %s { fn m() -> int }\n"},
	}
	for _, name := range names {
		for _, sh := range shapes {
			src := strings.Replace(sh.fmt, "%s", name, 1)
			label := sh.label + "_" + name
			t.Run(label, func(t *testing.T) {
				got := checkErr(t, src, "is reserved")
				if !strings.Contains(got, name) {
					t.Errorf("error %q does not mention %q", got, name)
				}
			})
		}
	}
}
