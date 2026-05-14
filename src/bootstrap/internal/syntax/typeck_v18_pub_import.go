package syntax

// v0.18 — pub-import re-export propagation.
//
// `pub import "X"` inside module M means: callers of M see X's pub
// fns / structs / enums / specs flat under M's namespace. Locally,
// M still references X as `<local>.<name>` exactly like a plain
// import. The `as Y` rename is local-only — it doesn't change the
// exposed shape for callers.
//
// Implementation: after CheckBundle wires up the cross-module
// checkers map, each checker walks its own pub-import decls,
// finds the target module's pub names, and copies the table entries
// into this checker's own fns / structs / enums / specs tables.
// The original *FnDecl / *StructDecl / *EnumDecl / *SpecDecl
// pointers are preserved so cgen / run consult them for mangle and
// metadata — re-exporting is purely a name-resolution-layer
// concept; no synthetic decls are produced.
//
// Pub gating still fires through fnIsPub / specIsPub / inline AST
// Pub checks. Those helpers follow the reExportSource chain to find
// the canonical decl when a name was propagated. Re-export does not
// promote a non-pub name to pub.
//
// Collisions: two pub-import sources contributing the same name, or
// a pub-import contributing a name the host already declares locally,
// reject with a focused diagnostic anchored on the pub-import decl
// whose contribution would create the collision.
//
// Transitivity: when A pub-imports B and B pub-imports C, then C's
// pub names reach A. The propagation pass runs to a fixpoint so the
// transitive chain composes regardless of the module iteration order.

// propagateReExports walks c's pub-import decls and copies the target
// modules' pub names into c's tables. Returns (added, err) — added
// is true when at least one new entry was propagated this call,
// driving the bundle-level fixpoint loop in CheckBundle.
func (c *checker) propagateReExports() (bool, error) {
	if c.crossMod == nil || c.crossMod.self == nil {
		return false, nil
	}
	added := false
	for _, stmt := range c.crossMod.self.ModuleProgram().Statements {
		imp, ok := stmt.(*ImportDecl)
		if !ok || !imp.Pub {
			continue
		}
		local := importDeclLocalName(imp)
		target := c.crossMod.imports[local]
		if target == nil {
			continue
		}
		tc := c.crossMod.checkers[target]
		if tc == nil {
			continue
		}
		gained, err := c.propagateFromTarget(imp, target, tc)
		if err != nil {
			return false, err
		}
		if gained {
			added = true
		}
	}
	return added, nil
}

// propagateFromTarget walks tc's program statements and copies tc's
// pub decls (and any already-re-exported names — to allow transitive
// re-export composition) into c's tables, recording the original
// source module on c.reExportSource. Collisions raise a focused
// diagnostic anchored on the pub-import decl.
func (c *checker) propagateFromTarget(imp *ImportDecl, target ModuleView, tc *checker) (bool, error) {
	added := false
	// Local pub decls in target: walk tc's source statements.
	for _, ts := range tc.crossMod.self.ModuleProgram().Statements {
		switch td := ts.(type) {
		case *FnDecl:
			if !td.Pub {
				continue
			}
			gained, err := c.propagateName(imp, target, td.Name, "fn", func() bool {
				if sig, ok := tc.fns[td.Name]; ok {
					c.fns[td.Name] = sig
					return true
				}
				return false
			})
			if err != nil {
				return false, err
			}
			added = added || gained
		case *StructDecl:
			if !td.Pub {
				continue
			}
			gained, err := c.propagateName(imp, target, td.Name, "struct", func() bool {
				if t, ok := tc.structs[td.Name]; ok {
					c.structs[td.Name] = t
					c.structAST[td.Name] = td
					return true
				}
				return false
			})
			if err != nil {
				return false, err
			}
			added = added || gained
		case *EnumDecl:
			if !td.Pub {
				continue
			}
			gained, err := c.propagateName(imp, target, td.Name, "enum", func() bool {
				if t, ok := tc.enums[td.Name]; ok {
					c.enums[td.Name] = t
					c.enumAST[td.Name] = td
					return true
				}
				return false
			})
			if err != nil {
				return false, err
			}
			added = added || gained
		case *SpecDecl:
			if !td.Pub {
				continue
			}
			gained, err := c.propagateName(imp, target, td.Name, "spec", func() bool {
				if sp, ok := tc.specs[td.Name]; ok {
					c.specs[td.Name] = sp
					return true
				}
				return false
			})
			if err != nil {
				return false, err
			}
			added = added || gained
		}
	}
	// Transitive: also copy names that target ITSELF re-exported
	// (its reExportSource entries). The original source is preserved.
	for name, origin := range tc.reExportSource {
		if _, dup := c.reExportSource[name]; dup {
			continue
		}
		// Find the kind by probing tc's tables.
		propagated := false
		if sig, ok := tc.fns[name]; ok {
			if _, exists := c.fns[name]; exists && c.reExportSource[name] != origin {
				return false, c.reExportCollisionErr(imp, name)
			}
			c.fns[name] = sig
			c.reExportSource[name] = origin
			propagated = true
		}
		if t, ok := tc.structs[name]; ok {
			if _, exists := c.structs[name]; exists && c.reExportSource[name] != origin {
				return false, c.reExportCollisionErr(imp, name)
			}
			c.structs[name] = t
			c.structAST[name] = tc.structAST[name]
			c.reExportSource[name] = origin
			propagated = true
		}
		if t, ok := tc.enums[name]; ok {
			if _, exists := c.enums[name]; exists && c.reExportSource[name] != origin {
				return false, c.reExportCollisionErr(imp, name)
			}
			c.enums[name] = t
			c.enumAST[name] = tc.enumAST[name]
			c.reExportSource[name] = origin
			propagated = true
		}
		if sp, ok := tc.specs[name]; ok {
			if _, exists := c.specs[name]; exists && c.reExportSource[name] != origin {
				return false, c.reExportCollisionErr(imp, name)
			}
			c.specs[name] = sp
			c.reExportSource[name] = origin
			propagated = true
		}
		if propagated {
			added = true
		}
	}
	return added, nil
}

// propagateName copies one name from target → host. tablePut is a
// closure that performs the actual table write (and returns true
// when the source table had the entry). Returns (newlyAdded, err).
// Idempotent: if the name was already re-exported from the same
// source on a prior pass, it's a no-op rather than a collision.
func (c *checker) propagateName(
	imp *ImportDecl,
	target ModuleView,
	name string,
	kind string,
	tablePut func() bool,
) (bool, error) {
	if c.reExportSource == nil {
		c.reExportSource = map[string]ModuleView{}
	}
	if prev, dup := c.reExportSource[name]; dup {
		if prev == target {
			return false, nil // already propagated from same source
		}
		return false, c.reExportCollisionErr(imp, name)
	}
	if c.hasLocalDecl(name) {
		return false, typeErr(imp.Pos,
			"pub import collision: %q is already declared locally in this module",
			name)
	}
	if !tablePut() {
		return false, nil // target's table didn't have this name
	}
	c.reExportSource[name] = target
	_ = kind
	return true, nil
}

// hasLocalDecl returns true when c's own source statements declare a
// top-level fn / struct / enum / spec named `name` — used to reject
// collisions between a pub-import and a host-local pub decl.
func (c *checker) hasLocalDecl(name string) bool {
	if c.crossMod == nil || c.crossMod.self == nil {
		return false
	}
	for _, stmt := range c.crossMod.self.ModuleProgram().Statements {
		switch s := stmt.(type) {
		case *FnDecl:
			if s.Name == name {
				return true
			}
		case *StructDecl:
			if s.Name == name {
				return true
			}
		case *EnumDecl:
			if s.Name == name {
				return true
			}
		case *SpecDecl:
			if s.Name == name {
				return true
			}
		}
	}
	return false
}

// reExportCollisionErr formats the focused diagnostic for two
// pub-imports contributing the same name into this module.
func (c *checker) reExportCollisionErr(imp *ImportDecl, name string) error {
	return typeErr(imp.Pos,
		"pub import collision: %q is exported by two different modules — rename or drop the `pub` on one",
		name)
}

// importDeclLocalName mirrors the loader's ImportLocalName but works
// directly on an *ImportDecl. The alias wins when present; otherwise
// the basename of the path (last `/`-separated segment) is the local
// binding name.
func importDeclLocalName(imp *ImportDecl) string {
	if imp.Alias != "" {
		return imp.Alias
	}
	p := imp.Path
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
