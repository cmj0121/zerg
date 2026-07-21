// Equal is the comparison behind the fmt round-trip oracle (DESIGN-1a §7): two
// trees are equal when their structure and values match, ignoring source spans,
// with trivia compared by kind and text after collapsing consecutive blank
// lines. It is implemented over a canonical structural dump so a mismatch is
// easy to inspect from tests.
package ast

import (
	"fmt"
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// Equal reports whether two trees are structurally identical, ignoring spans and
// normalizing trivia (consecutive blank lines collapse to one).
func Equal(a, b Node) bool { return dump(a) == dump(b) }

// dump renders a node as a canonical S-expression carrying structure, values,
// and normalized trivia — but no spans.
func dump(n Node) string {
	var d dumper
	d.node(n)
	return d.sb.String()
}

type dumper struct{ sb strings.Builder }

func (d *dumper) printf(format string, args ...any) {
	fmt.Fprintf(&d.sb, format, args...)
}

// trivia renders a trivia list with consecutive BlankLine entries collapsed.
func (d *dumper) trivia(label string, list []token.Trivia) {
	if len(list) == 0 {
		return
	}
	d.printf(" %s=[", label)
	prevBlank := false
	for _, tr := range list {
		if tr.Kind == token.BlankLine {
			if prevBlank {
				continue
			}
			prevBlank = true
			d.printf("(blank)")
			continue
		}
		prevBlank = false
		d.printf("(%s %q)", tr.Kind, tr.Text)
	}
	d.printf("]")
}

// open starts a node form, emitting its name and lead/trail trivia.
func (d *dumper) open(name string, n Node) {
	d.printf("(%s", name)
	d.trivia("lead", n.Lead())
	d.trivia("trail", n.Trail())
}

func (d *dumper) close() { d.printf(")") }

func (d *dumper) node(n Node) {
	if n == nil {
		d.printf("(nil)")
		return
	}
	switch v := n.(type) {
	case *File:
		d.open("File", v)
		for _, dec := range v.Decls {
			d.node(dec)
		}
		d.trivia("end", v.End)
		d.close()
	case *FuncDecl:
		d.open("FuncDecl", v)
		d.printf(" pub=%t unsafe=%t mut=%t %q", v.Pub, v.Unsafe, v.Mut, v.Name)
		for i := range v.Params {
			d.param(&v.Params[i])
		}
		d.printf(" ret=")
		d.typeRef(v.Ret)
		d.node(v.Body)
		d.close()
	case *Block:
		d.open("Block", v)
		for _, s := range v.Stmts {
			d.node(s)
		}
		d.trivia("end", v.End)
		d.close()
	case *NopStmt:
		d.open("Nop", v)
		d.close()
	case *BindStmt:
		d.open("Bind", v)
		d.printf(" mut=%t const=%t %q ", v.Mut, v.Const, v.Name)
		d.typeRef(v.Type)
		d.expr(v.Value)
		d.close()
	case *Reassign:
		d.open("Reassign", v)
		d.target(v.Target)
		d.expr(v.Value)
		d.close()
	case *PrintStmt:
		d.open("Print", v)
		d.expr(v.Value)
		d.close()
	case *ReturnStmt:
		d.open("Return", v)
		d.expr(v.Value)
		d.expr(v.Cond)
		d.close()
	case *BreakStmt:
		d.open("Break", v)
		d.close()
	case *ContinueStmt:
		d.open("Continue", v)
		d.close()
	case *IfStmt:
		d.open("If", v)
		for _, br := range v.Branches {
			d.printf("(Branch")
			d.expr(br.Cond)
			d.node(br.Body)
			d.printf(")")
		}
		if v.Else != nil {
			d.printf(" else=")
			d.node(v.Else)
		}
		d.close()
	case *ForStmt:
		d.open("For", v)
		d.expr(v.Cond)
		d.node(v.Body)
		d.close()
	case *ExprStmt:
		d.open("ExprStmt", v)
		d.expr(v.X)
		d.close()
	case *IntLit:
		d.open("Int", v)
		d.printf(" %d %q", v.Value, v.Text)
		d.close()
	case *FloatLit:
		d.open("Float", v)
		d.printf(" %q", v.Text) // compare the surface lexeme so a base/grouping change is caught
		d.close()
	case *BoolLit:
		d.open("Bool", v)
		d.printf(" %t", v.Value)
		d.close()
	case *StrLit:
		d.open("Str", v)
		d.printf(" %q", v.Value)
		d.close()
	case *NilLit:
		d.open("Nil", v)
		d.close()
	case *Ident:
		d.open("Ident", v)
		d.printf(" %q", v.Name)
		d.close()
	case *Unary:
		d.open("Unary", v)
		d.printf(" %q", v.Op.String())
		d.expr(v.X)
		d.close()
	case *Binary:
		d.open("Binary", v)
		d.printf(" %q", v.Op.String())
		d.expr(v.L)
		d.expr(v.R)
		d.close()
	case *Call:
		d.open("Call", v)
		d.expr(v.Callee)
		for _, a := range v.Args {
			d.printf("(Arg %q", a.Name)
			d.expr(a.Value)
			d.printf(")")
		}
		d.close()
	case *MatchExpr:
		d.open("Match", v)
		d.expr(v.Subject)
		for i := range v.Arms {
			arm := &v.Arms[i]
			d.printf("(Arm")
			d.trivia("lead", arm.Lead())
			d.trivia("trail", arm.Trail())
			d.node(arm.Pat)
			d.expr(arm.Guard)
			d.expr(arm.Body)
			d.printf(")")
		}
		d.close()
	case *LitPattern:
		d.open("LitPat", v)
		d.printf(" neg=%t", v.Neg)
		d.expr(v.Lit)
		d.close()
	case *BindPattern:
		d.open("BindPat", v)
		d.printf(" %q", v.Name)
		d.close()
	case *WildPattern:
		d.open("WildPat", v)
		d.close()
	case *RuneLit:
		d.open("Rune", v)
		d.printf(" %q", v.Text)
		d.close()
	case *ByteLit:
		d.open("Byte", v)
		d.printf(" %q", v.Text)
		d.close()
	case *RawStrLit:
		d.open("RawStr", v)
		d.printf(" %q", v.Text)
		d.close()
	case *CmdLit:
		d.open("Cmd", v)
		d.printf(" %q", v.Text)
		d.close()
	case *Range:
		d.open("Range", v)
		d.printf(" inclusive=%t", v.Inclusive)
		d.expr(v.Lo)
		d.expr(v.Hi)
		d.close()
	case *IsExpr:
		d.open("Is", v)
		d.printf(" %q", v.TypeName)
		d.expr(v.X)
		d.close()
	case *Coalesce:
		d.open("Coalesce", v)
		d.expr(v.X)
		d.expr(v.Y)
		d.close()
	case *Diverge:
		d.open("Diverge", v)
		d.printf(" %q", v.Kw.String())
		d.expr(v.Value)
		d.expr(v.From)
		d.close()
	case *Try:
		d.open("Try", v)
		d.expr(v.X)
		d.close()
	case *Force:
		d.open("Force", v)
		d.expr(v.X)
		d.close()
	case *OptChain:
		d.open("OptChain", v)
		d.printf(" %q", v.Name)
		d.expr(v.X)
		d.close()
	case *Recv:
		d.open("Recv", v)
		d.expr(v.X)
		d.close()
	case *Field:
		d.open("Field", v)
		d.printf(" %q", v.Name)
		d.expr(v.X)
		d.close()
	case *TupleIndex:
		d.open("TupleIndex", v)
		d.printf(" %d %q", v.Index, v.Text)
		d.expr(v.X)
		d.close()
	case *Bracket:
		d.open("Bracket", v)
		d.printf(" comma=%t", v.Comma)
		d.expr(v.Base)
		for _, e := range v.Elems {
			d.expr(e)
		}
		d.close()
	case *TupleLit:
		d.open("TupleLit", v)
		for _, e := range v.Elems {
			d.expr(e)
		}
		d.close()
	case *ListLit:
		d.open("ListLit", v)
		for _, e := range v.Elems {
			d.expr(e)
		}
		d.close()
	case *ListFill:
		d.open("ListFill", v)
		d.expr(v.Value)
		d.expr(v.Count)
		d.close()
	case *MapLit:
		d.open("MapLit", v)
		for _, e := range v.Entries {
			d.printf("(Entry")
			d.expr(e.Key)
			d.expr(e.Value)
			d.printf(")")
		}
		d.close()
	case *FStr:
		d.open("FStr", v)
		d.fstrParts(v.Parts)
		d.close()
	case *FCmd:
		d.open("FCmd", v)
		d.fstrParts(v.Parts)
		d.close()
	case *FnExpr:
		d.open("FnExpr", v)
		for i := range v.Params {
			cp := &v.Params[i]
			d.printf(" (CParam ref=%t %q ", cp.Ref, cp.Name)
			d.typeRef(cp.Type)
			d.expr(cp.Default)
			d.printf(")")
		}
		d.printf(" ret=")
		d.typeRef(v.Ret)
		d.node(v.Body)
		d.close()
	case *FnType:
		d.open("FnType", v)
		d.printf(" unsafe=%t", v.Unsafe)
		for i := range v.Params {
			pt := &v.Params[i]
			d.printf(" (PType ref=%t ", pt.Ref)
			d.typeRef(pt.Type)
			d.printf(")")
		}
		d.printf(" ret=")
		d.typeRef(v.Ret)
		d.close()
	case *LValueTarget:
		d.open("LValueTarget", v)
		d.expr(v.X)
		d.close()
	case *TupleTarget:
		d.open("TupleTarget", v)
		for _, e := range v.Elems {
			d.target(e)
		}
		d.close()
	case *StructTarget:
		d.open("StructTarget", v)
		d.printf(" %q rest=%t", v.TypeName, v.Rest)
		for _, f := range v.Fields {
			d.printf("(FTarget %q", f.Name)
			d.target(f.Target)
			d.printf(")")
		}
		d.close()
	default:
		d.printf("(unknown %T)", n)
	}
}

// param dumps one function parameter with its 'mut &' flag and default.
func (d *dumper) param(p *Param) {
	d.printf(" (Param ref=%t %q ", p.Ref, p.Name)
	d.typeRef(p.Type)
	d.expr(p.Default)
	d.printf(")")
}

// target dumps an assignment target, tolerating a nil (a struct-field shorthand).
func (d *dumper) target(t AssignTarget) {
	if t == nil {
		d.printf("(nil)")
		return
	}
	d.node(t)
}

// fstrParts dumps the parts of an f-string or f-cmd.
func (d *dumper) fstrParts(parts []FStrPart) {
	for i := range parts {
		p := &parts[i]
		if p.Expr == nil {
			d.printf("(Text %q)", p.Text)
			continue
		}
		d.printf("(Hole debug=%t conv=%d spec=%q,%t", p.Debug, p.Conv, p.Spec, p.HasSpec)
		d.expr(p.Expr)
		d.printf(")")
	}
}

// expr dumps an expression, tolerating the nil optional slots (e.g. a bare
// return's value) without a typed-nil pitfall.
func (d *dumper) expr(e Expr) {
	if e == nil {
		d.printf("(nil)")
		return
	}
	d.node(e)
}

func (d *dumper) typeRef(t *TypeRef) {
	if t == nil {
		d.printf("(nil)")
		return
	}
	d.printf("(Type %q)", t.Name)
}
