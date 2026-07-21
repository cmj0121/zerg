// Equal is the comparison behind the fmt round-trip oracle (DESIGN-1a §7): two
// trees are equal when their structure and values match, ignoring source spans,
// with trivia compared by kind and text after collapsing consecutive blank
// lines. It is implemented over a canonical structural dump so a mismatch is
// easy to inspect from tests.
package ast

import (
	"fmt"
	"strconv"
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
		d.printf(" pub=%t %q", v.Pub, v.Name)
		for i := range v.Params {
			p := &v.Params[i]
			d.printf(" (Param %q ", p.Name)
			d.typeRef(p.Type)
			d.printf(")")
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
	case *AssignStmt:
		d.open("Assign", v)
		d.printf(" %q", v.Name)
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
		d.printf(" %s", strconv.FormatFloat(v.Value, 'g', -1, 64))
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
			d.expr(a)
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
	default:
		d.printf("(unknown %T)", n)
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
