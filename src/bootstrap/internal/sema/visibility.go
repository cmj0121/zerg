package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// memberResolution is the outcome of resolving a namespace member `ns.member`
// against the whole-program surface (Phase 1g S2). Found is set when a
// pub-accessible member was located — either directly on the namespace's module or,
// failing that, on a one-level `import pub` re-exported module. Private is set when
// the module declares such a member but it is not `pub`, so the reference is
// rejected as a visibility error rather than an unknown name.
type memberResolution struct {
	Key     string  // the merged top-level name to look the member up under
	Sym     *Symbol // the resolved member symbol
	Found   bool    // a pub-accessible member was found (directly or via re-export)
	Private bool    // a member exists directly but is not pub
}

// resolveMember resolves `local.member` on the namespace symbol ns, enforcing `pub`
// visibility across the module boundary: a member the module declares without `pub`
// is Private (a clean error, not a silent success — the whole-program flatten put
// even a module-private item into the one unit, so visibility is checked here, not
// by presence). When the module exposes no such member, a one-level `import pub`
// re-export is consulted so a member re-exported through this namespace resolves
// onto the re-exported module's public surface.
func (c *checker) resolveMember(ns *Symbol, local, member string) memberResolution {
	key := moduleMember(nsTag(ns, local), member)
	if s := c.module.lookup(key); s != nil {
		if s.Pub {
			return memberResolution{Key: key, Sym: s, Found: true}
		}
		return memberResolution{Key: key, Sym: s, Private: true}
	}
	for _, tag := range ns.Reexports {
		k := moduleMember(tag, member)
		if s := c.module.lookup(k); s != nil && s.Pub {
			return memberResolution{Key: k, Sym: s, Found: true}
		}
	}
	return memberResolution{}
}

// recordMember records a namespace member access's resolved merged name, so mono
// and emit spell the C target sema chose (honoring re-export).
func (c *checker) recordMember(fld *ast.Field, key string) {
	if c.info.NsMembers != nil {
		c.info.NsMembers[fld] = key
	}
}

// memberError reports a rejected namespace member: a private one names the
// visibility violation, an absent one an unknown public member. Both are
// span-anchored at the member reference.
func (c *checker) memberError(fld *ast.Field, local, member string, private bool) {
	if private {
		c.errorf(fld.Span(), "%q is not a public member of module %q", member, local)
		return
	}
	c.errorf(fld.Span(), "module %q has no public member %q", local, member)
}

// --- module constants ---------------------------------------------------------

// checkModuleConsts type-checks every top-level binding (a module constant, a
// top-level `:=`, Phase 1g S3): it synthesizes the initializer's type, records it as
// the binding's type (so the backend can spell the module-level C global and its
// init-time assignment), and updates the surface symbol. A constant is evaluated at
// init, not at C static-init, so its initializer may be any runtime expression; here
// we only need its type. The initializer resolves in the module surface, where every
// top-level name is visible.
func (c *checker) checkModuleConsts(file *ast.File) {
	for _, it := range file.Items {
		c.checkModuleConst(it)
	}
}

func (c *checker) checkModuleConst(it ast.Stmt) {
	switch n := it.(type) {
	case *ast.BindStmt:
		c.pushScope()
		var t Type = types.Unknown
		if n.Value != nil {
			if n.Type != nil {
				t = c.resolveType(n.Type)
				c.check(n.Value, t)
			} else {
				t = c.synth(n.Value)
			}
		} else if n.Type != nil {
			t = c.resolveType(n.Type)
		}
		c.popScope()
		c.info.BindTypes[n] = t
		if sym := c.module.local(n.Name); sym != nil {
			sym.Type = t
		}
	case *ast.UnsafeGroup:
		for _, sub := range n.Items {
			c.checkModuleConst(sub)
		}
	}
}

// checkInit type-checks a module `init()` body (Phase 1g S3). It runs for effect
// (no parameters, no return), so it is checked like a void function body — every
// expression gets a type in the Info overlay the backend reads when it lowers the
// init function.
func (c *checker) checkInit(n *ast.InitDecl) {
	if n.Body == nil {
		return
	}
	savedFn := c.curFn
	c.curFn = nil
	c.checkBlock(n.Body)
	c.curFn = savedFn
}
