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
		for _, it := range v.Items {
			d.node(it)
		}
		d.trivia("end", v.End)
		d.close()
	case *FuncDecl:
		d.open("FuncDecl", v)
		d.decorators(v.Decorators)
		d.printf(" pub=%t unsafe=%t mut=%t %q", v.Pub, v.Unsafe, v.Mut, v.Name)
		d.generics(v.Generics)
		for i := range v.Params {
			d.param(&v.Params[i])
		}
		d.printf(" ret=")
		d.typ(v.Ret)
		d.node(v.Body)
		d.close()
	case *StructDecl:
		d.open("StructDecl", v)
		d.decorators(v.Decorators)
		d.printf(" pub=%t %q", v.Pub, v.Name)
		d.generics(v.Generics)
		for _, f := range v.Fields {
			d.field(f)
		}
		d.trivia("end", v.End)
		d.close()
	case *EnumDecl:
		d.open("EnumDecl", v)
		d.decorators(v.Decorators)
		d.printf(" pub=%t %q", v.Pub, v.Name)
		d.generics(v.Generics)
		for _, vr := range v.Variants {
			d.variant(vr)
		}
		d.trivia("end", v.End)
		d.close()
	case *TypeDecl:
		d.open("TypeDecl", v)
		d.decorators(v.Decorators)
		d.printf(" pub=%t %q", v.Pub, v.Name)
		d.generics(v.Generics)
		d.printf(" alias=")
		d.typ(v.Alias)
		d.close()
	case *SpecDecl:
		d.open("SpecDecl", v)
		d.decorators(v.Decorators)
		d.printf(" pub=%t %q", v.Pub, v.Name)
		d.generics(v.Generics)
		if v.Super != nil {
			d.printf(" super=")
			d.bound(v.Super)
		}
		for _, m := range v.Members {
			d.node(m)
		}
		d.trivia("end", v.End)
		d.close()
	case *ImplDecl:
		d.open("ImplDecl", v)
		d.decorators(v.Decorators)
		d.generics(v.Generics)
		d.printf(" spec=")
		d.typ(v.Spec)
		d.printf(" target=")
		d.typ(v.Target)
		for _, it := range v.Items {
			d.node(it)
		}
		d.trivia("end", v.End)
		d.close()
	case *InitDecl:
		d.open("InitDecl", v)
		d.decorators(v.Decorators)
		d.node(v.Body)
		d.close()
	case *FnSig:
		d.open("FnSig", v)
		d.printf(" unsafe=%t mut=%t %q", v.Unsafe, v.Mut, v.Name)
		d.generics(v.Generics)
		for i := range v.Params {
			d.param(&v.Params[i])
		}
		d.printf(" ret=")
		d.typ(v.Ret)
		d.close()
	case *AssocType:
		d.open("AssocType", v)
		d.printf(" %q", v.Name)
		if v.Bound != nil {
			d.printf(" bound=")
			d.bound(v.Bound)
		}
		d.close()
	case *AssocVal:
		d.open("AssocVal", v)
		d.printf(" %q ", v.Name)
		d.typ(v.Type)
		d.close()
	case *AssocBind:
		d.open("AssocBind", v)
		d.printf(" %q ", v.Name)
		d.typ(v.Type)
		d.close()
	case *ValBind:
		d.open("ValBind", v)
		d.printf(" %q ", v.Name)
		d.constExpr(v.Value)
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
		d.typ(v.Type)
		if v.Target != nil {
			d.node(v.Target)
		}
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
		d.expr(v.Cond)
		d.close()
	case *ContinueStmt:
		d.open("Continue", v)
		d.expr(v.Cond)
		d.close()
	case *IfStmt:
		d.open("If", v)
		d.branches(v.Branches)
		if v.Else != nil {
			d.printf(" else=")
			d.node(v.Else)
		}
		d.close()
	case *IfExpr:
		d.open("IfExpr", v)
		d.branches(v.Branches)
		d.printf(" else=")
		d.node(v.Else)
		d.close()
	case *GuardExpr:
		d.open("Guard", v)
		d.node(v.Body)
		d.close()
	case *ForStmt:
		d.open("For", v)
		d.printf(" mut=%t var=%q", v.Mut, v.Var)
		d.expr(v.Cond)
		d.expr(v.Iter)
		d.node(v.Body)
		d.close()
	case *WithStmt:
		d.open("With", v)
		d.printf(" var=%q", v.Var)
		d.expr(v.Resource)
		d.node(v.Body)
		d.close()
	case *RaiseStmt:
		d.open("Raise", v)
		d.expr(v.Value)
		d.expr(v.From)
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
	case *NamePattern:
		d.open("NamePat", v)
		d.printf(" %q", v.Name)
		d.close()
	case *WildPattern:
		d.open("WildPat", v)
		d.close()
	case *VariantPattern:
		d.open("VariantPat", v)
		d.printf(" %q", v.Name)
		for _, e := range v.Elems {
			d.node(e)
		}
		d.close()
	case *StructPattern:
		d.open("StructPat", v)
		d.printf(" %q rest=%t", v.Name, v.Rest)
		for _, f := range v.Fields {
			d.printf("(FPat %q", f.Name)
			if f.Pat != nil {
				d.node(f.Pat)
			}
			d.printf(")")
		}
		d.close()
	case *TuplePattern:
		d.open("TuplePat", v)
		for _, e := range v.Elems {
			d.node(e)
		}
		d.close()
	case *ListPattern:
		d.open("ListPat", v)
		for _, el := range v.Elems {
			if el.Rest {
				d.printf("(Rest %q)", el.Name)
				continue
			}
			d.node(el.Pat)
		}
		d.close()
	case *AsPattern:
		d.open("AsPat", v)
		d.printf(" %q", v.Name)
		d.node(v.Inner)
		d.close()
	case *OrPattern:
		d.open("OrPat", v)
		for _, alt := range v.Alts {
			d.node(alt)
		}
		d.close()
	case *RangeArm:
		d.open("RangeArm", v)
		d.printf(" inclusive=%t", v.Inclusive)
		d.expr(v.Lo)
		d.expr(v.Hi)
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
			d.typ(cp.Type)
			d.expr(cp.Default)
			d.printf(")")
		}
		d.printf(" ret=")
		d.typ(v.Ret)
		d.node(v.Body)
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
	case *SpawnStmt:
		d.open("Spawn", v)
		d.expr(v.Call)
		d.close()
	case *SendStmt:
		d.open("Send", v)
		d.expr(v.Chan)
		d.expr(v.Value)
		d.close()
	case *DeferStmt:
		d.open("Defer", v)
		d.expr(v.Call)
		d.close()
	case *DelStmt:
		d.open("Del", v)
		d.printf(" %q", v.Name)
		d.close()
	case *ImportStmt:
		d.open("Import", v)
		d.printf(" grouped=%t", v.Grouped)
		for _, s := range v.Specs {
			d.printf("(Spec")
			d.trivia("lead", s.Lead())
			d.trivia("trail", s.Trail())
			d.printf(" pub=%t %q as=%q)", s.Pub, s.Path, s.Alias)
		}
		d.trivia("end", v.End)
		d.close()
	case *SelectStmt:
		d.open("Select", v)
		for i := range v.Arms {
			arm := &v.Arms[i]
			d.printf("(SArm")
			d.trivia("lead", arm.Lead())
			d.trivia("trail", arm.Trail())
			d.printf(" kind=%d bind=%q hasbind=%t", arm.Kind, arm.Bind, arm.HasBind)
			d.expr(arm.Chan)
			d.expr(arm.Value)
			d.expr(arm.Body)
			d.printf(")")
		}
		d.close()
	case *ChanNew:
		d.open("ChanNew", v)
		d.typ(v.Elem)
		d.expr(v.Cap)
		d.close()
	case *UnsafeExpr:
		d.open("UnsafeExpr", v)
		d.node(v.Body)
		d.close()
	case *UnsafeGroup:
		d.open("UnsafeGroup", v)
		d.decorators(v.Decorators)
		for _, it := range v.Items {
			d.node(it)
		}
		d.trivia("end", v.End)
		d.close()
	case *AsmExpr:
		d.open("Asm", v)
		d.printf(" %q", v.Template)
		for _, op := range v.Operands {
			d.node(op)
		}
		d.close()
	case *AsmOperand:
		d.open("AsmOp", v)
		d.printf(" kind=%d constraint=%q", v.Kind, v.Constraint)
		d.expr(v.Value)
		for _, c := range v.Clobbers {
			d.printf(" %q", c)
		}
		d.close()
	default:
		d.printf("(unknown %T)", n)
	}
}

// branches dumps an if/if-expr chain's head branches, each with its binding
// name (empty for a plain-expr head), condition, and body.
func (d *dumper) branches(brs []IfBranch) {
	for _, br := range brs {
		d.printf("(Branch bind=%q", br.Bind)
		d.expr(br.Cond)
		d.node(br.Body)
		d.printf(")")
	}
}

// param dumps one function parameter with its 'mut &' flag and default.
func (d *dumper) param(p *Param) {
	d.printf(" (Param ref=%t %q ", p.Ref, p.Name)
	d.typ(p.Type)
	d.expr(p.Default)
	d.printf(")")
}

// decorators dumps a declaration's decorator prefix.
func (d *dumper) decorators(ds []*Decorator) {
	for _, dec := range ds {
		d.printf("(Deco")
		for _, it := range dec.Items {
			d.printf("(Item %q", it.Name)
			for _, a := range it.Args {
				d.constExpr(a)
			}
			d.printf(")")
		}
		d.printf(")")
	}
}

// generics dumps a generic parameter list, tolerating a nil (non-generic).
func (d *dumper) generics(g *Generics) {
	if g == nil {
		return
	}
	d.printf(" (Generics")
	for _, tp := range g.Params {
		d.printf("(TP %q", tp.Name)
		if tp.Bound != nil {
			d.bound(tp.Bound)
		}
		d.printf(")")
	}
	d.printf(")")
}

// bound dumps a spec conjunction.
func (d *dumper) bound(b *Bound) {
	d.printf("(Bound")
	for _, n := range b.Names {
		d.printf(" %q", n)
	}
	d.printf(")")
}

// field dumps one struct field with its trivia.
func (d *dumper) field(f *StructField) {
	d.printf("(Field")
	d.trivia("lead", f.Lead())
	d.trivia("trail", f.Trail())
	d.printf(" pub=%t %q ", f.Pub, f.Name)
	d.typ(f.Type)
	d.expr(f.Default)
	d.printf(")")
}

// variant dumps one enum variant with its trivia.
func (d *dumper) variant(v *Variant) {
	d.printf("(Variant")
	d.trivia("lead", v.Lead())
	d.trivia("trail", v.Trail())
	d.printf(" %q", v.Name)
	for _, t := range v.Payload {
		d.typ(t)
	}
	if v.Discr != nil {
		d.printf(" discr=")
		d.constExpr(v.Discr)
	}
	d.printf(")")
}

// constExpr dumps a const-expr wrapper, tolerating a nil.
func (d *dumper) constExpr(c *ConstExpr) {
	if c == nil {
		d.printf("(nil)")
		return
	}
	d.printf("(Const")
	d.expr(c.X)
	d.printf(")")
}

// typ dumps a type expression, tolerating a nil optional (an absent return type).
func (d *dumper) typ(t Type) {
	if t == nil {
		d.printf("(nil)")
		return
	}
	switch v := t.(type) {
	case *TypeRef:
		d.printf("(Type %q", v.Name)
		for _, a := range v.Args {
			d.typ(a)
		}
		for _, seg := range v.Proj {
			d.printf(" .%s", seg)
		}
		d.printf(")")
	case *OptType:
		d.printf("(Opt")
		d.typ(v.Elem)
		d.printf(")")
	case *TupleType:
		d.printf("(TupleType")
		for _, e := range v.Elems {
			d.typ(e)
		}
		d.printf(")")
	case *ArrayType:
		d.printf("(ArrayType")
		d.typ(v.Elem)
		d.constExpr(v.Count)
		d.printf(")")
	case *ChanType:
		d.printf("(ChanType dir=%d", v.Dir)
		d.typ(v.Elem)
		d.printf(")")
	case *FnType:
		d.printf("(FnType unsafe=%t", v.Unsafe)
		for i := range v.Params {
			pt := &v.Params[i]
			d.printf(" (PType ref=%t ", pt.Ref)
			d.typ(pt.Type)
			d.printf(")")
		}
		d.printf(" ret=")
		d.typ(v.Ret)
		d.printf(")")
	case *PtrType:
		d.printf("(PtrType")
		if v.Elem != nil {
			d.typ(v.Elem)
		}
		d.printf(")")
	case *ConstExpr:
		d.constExpr(v)
	default:
		d.printf("(unknown-type %T)", t)
	}
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
