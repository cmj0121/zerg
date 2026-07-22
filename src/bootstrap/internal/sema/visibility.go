package sema

import (
	"strings"

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

// checkModuleConsts type-checks the module constants (top-level `:=`, Phase 1g S3)
// in DEPENDENCY order — a constant after every module constant its initializer
// references — so a forward reference (`a := b + 1` declared before `b := 10`)
// infers b's real type before a is typed rather than reading a not-yet-set Unknown.
// The dependency graph the resolver recorded is topologically sorted; a cycle among
// the constants is a compile error (a constant cannot be evaluated before itself).
// The resulting order is published on Info.ConstOrder so the backend evaluates and
// emits the constants in the same order the checker typed them.
//
// A constant is evaluated at init, not at C static-init, so its initializer may be
// any runtime expression; here we only need its type. The initializer resolves in
// the module surface, where every top-level name is visible.
func (c *checker) checkModuleConsts(file *ast.File) {
	consts := gatherModuleConsts(file.Items)
	if len(consts) == 0 {
		return
	}
	inSet := make(map[*ast.BindStmt]bool, len(consts))
	for _, b := range consts {
		inSet[b] = true
	}
	order, cycle := topoConsts(consts, c.constEdges, inSet)
	if cycle != nil {
		c.errorf(cycle[0].Span(), "module-constant cycle: %s", constCycleNames(cycle))
		// Type in declaration order as a best effort; diagnostics are non-empty, so the
		// program is not emitted and no `void` global escapes to the C compiler.
		for _, b := range consts {
			c.checkModuleConstBind(b)
		}
		c.info.ConstOrder = consts
		return
	}
	for _, b := range order {
		c.checkModuleConstBind(b)
	}
	c.info.ConstOrder = order
}

// checkModuleConstBind types one module constant, recording its type on the binding
// (Info.BindTypes) and its surface symbol so a later constant that references it
// resolves to the real type.
func (c *checker) checkModuleConstBind(n *ast.BindStmt) {
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
}

// gatherModuleConsts collects every module constant (a top-level `:=`) in
// declaration order, descending into module-level `unsafe { }` groups.
func gatherModuleConsts(items []ast.Stmt) []*ast.BindStmt {
	var out []*ast.BindStmt
	var scan func([]ast.Stmt)
	scan = func(items []ast.Stmt) {
		for _, it := range items {
			switch n := it.(type) {
			case *ast.BindStmt:
				out = append(out, n)
			case *ast.UnsafeGroup:
				scan(n.Items)
			}
		}
	}
	scan(items)
	return out
}

// topoConsts topologically sorts the module constants so each constant comes after
// every constant it depends on (a post-order DFS over the dependency edges). Roots
// are visited in declaration order and edges in the order the resolver recorded
// them, so the order is deterministic. It returns the sorted order, or — when the
// dependency graph has a cycle — a nil order and the cycle as a path of constants
// (its first node repeated at the end).
func topoConsts(
	consts []*ast.BindStmt, edges map[*ast.BindStmt][]*ast.BindStmt, inSet map[*ast.BindStmt]bool,
) (order, cycle []*ast.BindStmt) {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[*ast.BindStmt]int{}
	var stack []*ast.BindStmt
	var visit func(b *ast.BindStmt) []*ast.BindStmt
	visit = func(b *ast.BindStmt) []*ast.BindStmt {
		color[b] = gray
		stack = append(stack, b)
		for _, dep := range edges[b] {
			if !inSet[dep] {
				continue
			}
			switch color[dep] {
			case gray:
				return constCycleFrom(stack, dep)
			case white:
				if c := visit(dep); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[b] = black
		order = append(order, b)
		return nil
	}
	for _, b := range consts {
		if color[b] == white {
			if c := visit(b); c != nil {
				return nil, c
			}
		}
	}
	return order, nil
}

// constCycleFrom returns the tail of stack starting at the first occurrence of node,
// with node repeated at the end to close the cycle.
func constCycleFrom(stack []*ast.BindStmt, node *ast.BindStmt) []*ast.BindStmt {
	var out []*ast.BindStmt
	collecting := false
	for _, s := range stack {
		if s == node {
			collecting = true
		}
		if collecting {
			out = append(out, s)
		}
	}
	return append(out, node)
}

// constCycleNames renders a constant cycle as `p -> q -> p`.
func constCycleNames(cycle []*ast.BindStmt) string {
	parts := make([]string, len(cycle))
	for i, b := range cycle {
		parts[i] = b.Name
	}
	return strings.Join(parts, " -> ")
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
