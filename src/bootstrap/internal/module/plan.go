package module

import "github.com/cmj0121/zerg/src/bootstrap/internal/ast"

// InitPlan is the whole-program module-initialization plan (Phase 1g S3, Decision
// F3): the modules that own an `init()` block or a module constant, in dependency
// (topological) order — a module's dependencies before the module itself — so the
// program entry can run each module's setup exactly once, deps first. A program
// with no `init()` and no module constant has an empty plan and keeps its exact,
// byte-identical C (no init function, no init call).
type InitPlan struct {
	Modules []InitModule
}

// InitModule is one module's initialization content: its C-mangle tag (empty for
// the entry module), the module's `init()` blocks in declaration (FIFO) order, and
// its module constants (top-level `:=`) in declaration order. The consts are
// evaluated, then the init blocks run, inside the module's init function.
type InitModule struct {
	Tag    string
	Inits  []*ast.InitDecl
	Consts []*ast.BindStmt
}

// Empty reports whether the plan holds no module with any init block or constant.
func (p *InitPlan) Empty() bool { return p == nil || len(p.Modules) == 0 }

// collectInit gathers a module's `init()` blocks and module constants from its
// (already renamed) files, in declaration order, returning them only when the
// module owns at least one — an init-free, constant-free module contributes nothing
// to the plan.
func collectInit(tag string, files []*ast.File) (InitModule, bool) {
	im := InitModule{Tag: tag}
	var scan func(items []ast.Stmt)
	scan = func(items []ast.Stmt) {
		for _, it := range items {
			switch n := it.(type) {
			case *ast.InitDecl:
				im.Inits = append(im.Inits, n)
			case *ast.BindStmt:
				im.Consts = append(im.Consts, n)
			case *ast.UnsafeGroup:
				scan(n.Items)
			}
		}
	}
	for _, f := range files {
		scan(f.Items)
	}
	if len(im.Inits) == 0 && len(im.Consts) == 0 {
		return InitModule{}, false
	}
	return im, true
}
