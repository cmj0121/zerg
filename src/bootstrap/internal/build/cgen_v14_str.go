package build

import (
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// cgen_v14_str.go hosts the v0.14 str ↔ list[byte] bridge primitives:
// `s.bytes()` (lowered as `bytes(s)`) and `buf.to_str()` (lowered as
// `to_str(buf)`). Both are byte-by-byte allocating copies; str is
// immutable so a returned list[byte] must not alias s.data, and the
// reverse direction has no other safe surface either (the caller of
// to_str may mutate buf after the call without affecting the new str).
//
// The C helpers are emitted as part of the standard runtime block when
// programUsesV14StrPrims() reports a hit. The list[byte] shape is
// force-monomorphised at the same gate so `zerg_list_uint8_t` is in
// scope when the helpers reference it.

// runtimeV14StrPrimsC is the C source for the v0.14 str-prim helpers.
// Emitted at the same point as the v0.8 stdlib runtime — after the
// shape registry's typedef pass and before user-fn emission — so
// `zerg_list_uint8_t` is defined when the bytes helper returns one.
const runtimeV14StrPrimsC = `
/* zerg_str_bytes returns a fresh list[byte] containing s.data's bytes.
   v0.14 str ↔ list[byte] bridge. The copy is unconditional so callers
   may mutate the returned list without affecting the (immutable) str.
   A zero-length str yields a list with len=0 and cap=1 (one-byte
   sentinel allocation so the data pointer is non-NULL — matches the
   per-shape grow helpers' invariants). */
static zerg_list_uint8_t zerg_str_bytes(zerg_str s) {
    zerg_list_uint8_t out;
    out.len = s.len;
    out.cap = s.len > 0 ? s.len : 1;
    out.data = (uint8_t*)malloc(out.cap);
    if (out.len > 0) memcpy(out.data, s.data, out.len);
    return out;
}

/* zerg_list_uint8_t_to_str returns a fresh zerg_str owning a copy of
   xs.data's bytes. v0.14 str ↔ list[byte] bridge. UTF-8 validity is
   NOT checked — the caller's responsibility, mirroring the v0.10
   io.read_file contract that "non-UTF-8 bytes are returned verbatim
   and may break downstream string operations". */
static zerg_str zerg_list_uint8_t_to_str(zerg_list_uint8_t xs) {
    zerg_str out;
    out.len = xs.len;
    char *buf = (char*)malloc(xs.len > 0 ? xs.len : 1);
    if (xs.len > 0) memcpy(buf, xs.data, xs.len);
    out.data = buf;
    return out;
}
`

// listOfByteType returns a synthetic *Type for list[byte]. Used by the
// v0.14 pre-pass to force-monomorphise the shape so the str-prim
// runtime helpers can reference zerg_list_uint8_t regardless of
// whether user code constructs a list[byte] literal.
func listOfByteType() *syntax.Type {
	return &syntax.Type{
		Kind:    syntax.TypeList,
		Element: syntax.TByte(),
	}
}

// programUsesV14StrPrims reports whether any module in the bundle
// references the v0.14 `bytes` or `to_str` builtins. The gate scans
// the AST for ident-callees with either reserved name so v0.0–v0.13
// programs preserve their byte-identical emit (the helpers + the
// forced list[byte] shape only land when a program actually reaches
// for the bridge). Mirrors programUsesV08's walk shape.
func (g *cgen) programUsesV14StrPrims() bool {
	for i := range g.modules {
		if programUsesV14StrPrimsWalk(g.modules[i].prog) {
			return true
		}
	}
	return false
}

func programUsesV14StrPrimsWalk(prog *syntax.Program) bool {
	if prog == nil {
		return false
	}
	found := false
	isStrPrim := func(name string) bool { return name == "bytes" || name == "to_str" }
	var walkE func(syntax.Expr)
	var walkS func(syntax.Stmt)
	walkE = func(e syntax.Expr) {
		if e == nil || found {
			return
		}
		switch x := e.(type) {
		case *syntax.CallExpr:
			if id, ok := x.Callee.(*syntax.IdentExpr); ok && isStrPrim(id.Name) {
				found = true
				return
			}
			walkE(x.Callee)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.MethodCallExpr:
			// Typeck lowers `s.bytes()` etc. to a synthetic CallExpr
			// stashed on LoweredCall; the CallExpr arm above picks it
			// up. Recurse through the method-call's own children too
			// so anything not lowered (or freshly synthesised) gets
			// visited.
			walkE(x.Receiver)
			for _, a := range x.Args {
				walkE(a)
			}
			if x.Lowered != nil {
				walkE(x.Lowered)
			}
			if x.LoweredCall != nil {
				walkE(x.LoweredCall)
			}
		case *syntax.UnaryExpr:
			walkE(x.Operand)
		case *syntax.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.ParenExpr:
			walkE(x.Inner)
		case *syntax.IndexExpr:
			walkE(x.Receiver)
			walkE(x.Index)
		case *syntax.FieldAccessExpr:
			walkE(x.Receiver)
			if x.Lowered != nil {
				walkE(x.Lowered)
			}
		case *syntax.ListLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.TupleLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.StructLit:
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *syntax.EnumLit:
			for _, sub := range x.Payload {
				walkE(sub)
			}
		case *syntax.PropagateExpr:
			walkE(x.Inner)
		case *syntax.CoalesceExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.AnonFnExpr:
			walkBlockStmts(x.Body, walkS)
		}
	}
	walkS = func(s syntax.Stmt) {
		if s == nil || found {
			return
		}
		switch n := s.(type) {
		case *syntax.PrintStmt:
			walkE(n.Expr)
		case *syntax.ExprStmt:
			walkE(n.Expr)
		case *syntax.LetStmt:
			walkE(n.Value)
		case *syntax.MutStmt:
			walkE(n.Value)
		case *syntax.ConstStmt:
			walkE(n.Value)
		case *syntax.AssignStmt:
			walkE(n.Target)
			walkE(n.Value)
		case *syntax.IfStmt:
			walkE(n.Cond)
			walkBlockStmts(n.Then, walkS)
			for _, ec := range n.Elifs {
				walkE(ec.Cond)
				walkBlockStmts(ec.Body, walkS)
			}
			if n.Else != nil {
				walkBlockStmts(n.Else, walkS)
			}
		case *syntax.ForStmt:
			if n.Iter != nil {
				walkE(n.Iter)
			}
			if n.Cond != nil {
				walkE(n.Cond)
			}
			if n.Range != nil {
				walkE(n.Range.Start)
				walkE(n.Range.End)
			}
			walkBlockStmts(n.Body, walkS)
		case *syntax.ReturnStmt:
			if n.Value != nil {
				walkE(n.Value)
			}
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.MatchStmt:
			walkE(n.Subject)
			for _, arm := range n.Arms {
				if arm.Guard != nil {
					walkE(arm.Guard)
				}
				walkBlockStmts(arm.Body, walkS)
			}
		case *syntax.FnDecl:
			walkBlockStmts(n.Body, walkS)
		case *syntax.SpawnStmt:
			walkE(n.Call)
		case *syntax.SendStmt:
			walkE(n.Chan)
			walkE(n.Value)
		case *syntax.DeferStmt:
			walkBlockStmts(n.Body, walkS)
		case *syntax.SelectStmt:
			for _, arm := range n.Arms {
				if arm.Chan != nil {
					walkE(arm.Chan)
				}
				if arm.Value != nil {
					walkE(arm.Value)
				}
				walkBlockStmts(arm.Body, walkS)
			}
		case *syntax.BreakStmt:
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.ContinueStmt:
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.ImplDecl:
			for _, m := range n.Methods {
				if m != nil {
					walkBlockStmts(m.Body, walkS)
				}
			}
		}
	}
	for _, st := range prog.Statements {
		walkS(st)
	}
	return found
}

// walkBlockStmts feeds every statement of a Block through walkS. Mirrors
// the v0.8 walker's internal helper of the same shape; defined locally
// here so the v0.14 walker stays self-contained.
func walkBlockStmts(b *syntax.Block, walkS func(syntax.Stmt)) {
	if b == nil {
		return
	}
	for _, st := range b.Statements {
		walkS(st)
	}
}
