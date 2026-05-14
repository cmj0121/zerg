package syntax

// v0.18 — pub-import re-export propagation.
//
// `pub import "X"` inside module M means: callers of M see X's pub fns
// / structs / enums / specs flat under M's namespace. The `as Y`
// rename stays local-only — it doesn't change the exposed shape for
// callers. Two pub imports contributing the same name (or a pub
// import colliding with the host's own pub decl) reject at module-
// bind time. Transitive re-exports (A → B → C) compose: propagation
// iterates to fixpoint so iteration order is irrelevant.
//
// Implementation: after CheckBundle wires up the cross-module
// checkers map and resolves fn signatures, propagateReExports copies
// the target module's table entries into the host's same-shaped
// tables — same *FnDecl / *StructDecl / *EnumDecl / *SpecDecl
// pointers, so cgen / run see the canonical decl for mangle and
// metadata. Provenance is recorded on c.reExportSource so the pub-
// check helpers (fnIsPub / specIsPub / inline AST checks) walk back
// to the source decl for visibility gating.

// propagateReExports walks c's pub-import decls and copies each
// target's pub names into c's tables. Returns added=true when at
// least one new entry was propagated this call, driving the bundle-
// level fixpoint loop in CheckBundle.
func (c *checker) propagateReExports() (bool, error) {
	if c.crossMod == nil || c.crossMod.self == nil {
		return false, nil
	}
	if c.reExportSource == nil {
		c.reExportSource = map[string]ModuleView{}
	}
	added := false
	for _, imp := range c.crossMod.self.ModuleImports() {
		decl := imp.ImportDecl()
		if decl == nil || !decl.Pub {
			continue
		}
		target := imp.ImportTarget()
		if target == nil {
			continue
		}
		tc := c.crossMod.checkers[target]
		if tc == nil {
			continue
		}
		gained, err := c.propagateFromTarget(decl, target, tc)
		if err != nil {
			return false, err
		}
		added = added || gained
	}
	return added, nil
}

// propagateFromTarget copies tc's pub decls (both local and already-
// re-exported, to admit transitive composition) into c's tables.
// Records each propagated name on c.reExportSource with the canonical
// owning module — never tc itself, when tc had already re-exported
// the name from elsewhere.
func (c *checker) propagateFromTarget(imp *ImportDecl, target ModuleView, tc *checker) (bool, error) {
	added := false
	for _, ts := range tc.crossMod.self.ModuleProgram().Statements {
		var name string
		var pub bool
		var put func() bool
		switch td := ts.(type) {
		case *FnDecl:
			name, pub = td.Name, td.Pub
			put = func() bool {
				sig, ok := tc.fns[name]
				if !ok {
					return false
				}
				c.fns[name] = sig
				return true
			}
		case *StructDecl:
			name, pub = td.Name, td.Pub
			put = func() bool {
				t, ok := tc.structs[name]
				if !ok {
					return false
				}
				c.structs[name] = t
				c.structAST[name] = td
				return true
			}
		case *EnumDecl:
			name, pub = td.Name, td.Pub
			put = func() bool {
				t, ok := tc.enums[name]
				if !ok {
					return false
				}
				c.enums[name] = t
				c.enumAST[name] = td
				return true
			}
		case *SpecDecl:
			name, pub = td.Name, td.Pub
			put = func() bool {
				sp, ok := tc.specs[name]
				if !ok {
					return false
				}
				c.specs[name] = sp
				return true
			}
		default:
			continue
		}
		if !pub {
			continue
		}
		gained, err := c.propagateName(imp, target, name, put)
		if err != nil {
			return false, err
		}
		added = added || gained
	}
	// Transitive: copy names that target ITSELF re-exported. The
	// canonical owner (in tc.reExportSource) is preserved rather than
	// rewritten to tc, so a third-tier consumer sees the same source.
	for name, origin := range tc.reExportSource {
		if _, dup := c.reExportSource[name]; dup {
			continue
		}
		if c.hasLocalDecl(name) {
			return false, typeErr(imp.Pos,
				"pub import collision: %q is already declared locally in this module",
				name)
		}
		gained := false
		if sig, ok := tc.fns[name]; ok {
			c.fns[name] = sig
			gained = true
		}
		if t, ok := tc.structs[name]; ok {
			c.structs[name] = t
			c.structAST[name] = tc.structAST[name]
			gained = true
		}
		if t, ok := tc.enums[name]; ok {
			c.enums[name] = t
			c.enumAST[name] = tc.enumAST[name]
			gained = true
		}
		if sp, ok := tc.specs[name]; ok {
			c.specs[name] = sp
			gained = true
		}
		if gained {
			c.reExportSource[name] = origin
			added = true
		}
	}
	return added, nil
}

// propagateName runs a single put + provenance write. Idempotent: a
// repeat propagation from the same source is a no-op; a different
// source is a two-modules-same-name collision.
func (c *checker) propagateName(
	imp *ImportDecl,
	target ModuleView,
	name string,
	tablePut func() bool,
) (bool, error) {
	if prev, dup := c.reExportSource[name]; dup {
		if prev == target {
			return false, nil
		}
		return false, c.reExportCollisionErr(imp, name)
	}
	if c.hasLocalDecl(name) {
		return false, typeErr(imp.Pos,
			"pub import collision: %q is already declared locally in this module",
			name)
	}
	if !tablePut() {
		return false, nil
	}
	c.reExportSource[name] = target
	return true, nil
}

// hasLocalDecl returns true when c declares `name` locally — i.e., the
// entry in one of c's decl tables came from c's own source rather than
// a prior pub-import. Re-exported names are tracked on
// c.reExportSource; anything in the local tables but absent from
// reExportSource is local.
func (c *checker) hasLocalDecl(name string) bool {
	if _, dup := c.reExportSource[name]; dup {
		return false
	}
	if _, ok := c.fns[name]; ok {
		return true
	}
	if _, ok := c.structs[name]; ok {
		return true
	}
	if _, ok := c.enums[name]; ok {
		return true
	}
	if _, ok := c.specs[name]; ok {
		return true
	}
	return false
}

// reExportCollisionErr is the diagnostic for two pub-imports
// contributing the same name. Host-local collisions use a distinct
// message in propagateName.
func (c *checker) reExportCollisionErr(imp *ImportDecl, name string) error {
	return typeErr(imp.Pos,
		"pub import collision: %q is exported by two different modules — rename or drop the `pub` on one",
		name)
}

// ImportPathBasename returns the last `/`-separated segment of an
// import path string. cgen / run walks use this to derive the local
// binding name from a raw ImportDecl when no `as` alias is present —
// matching the loader's ImportLocalName convention. typeck's own
// pub-import iteration uses ModuleImports() directly, which carries
// the loader-computed local name already.
func ImportPathBasename(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
