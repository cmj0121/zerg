// U5 printing: the group 7 declarations and type expressions. Declarations use a
// canonical layout — decorators each on their own line above the declaration,
// generics '[T: Bound]', and fields / variants / members one per line with their
// doc-comments and trailing comments preserved as node-attached trivia. A
// discriminant or other const-expr reprints its inner expression, whose literals
// keep their surface Text verbatim, so '0xFF' and '1_000' survive unchanged.
package fmt

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// --- declaration dispatch -----------------------------------------------------

// decl prints one declaration: its decorators (each on its own line), then the
// declaration itself at the current indent.
func (p *printer) decl(d ast.Decl) {
	p.decorators(declDecorators(d))
	p.ind()
	p.declDispatch(d)
}

// declDispatch prints a declaration's body at the current cursor (decorators and
// indent already emitted), so an unsafe group can reuse it for its own items.
func (p *printer) declDispatch(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FuncDecl:
		p.funcDecl(n)
	case *ast.StructDecl:
		p.structDecl(n)
	case *ast.EnumDecl:
		p.enumDecl(n)
	case *ast.TypeDecl:
		p.typeDecl(n)
	case *ast.SpecDecl:
		p.specDecl(n)
	case *ast.ImplDecl:
		p.implDecl(n)
	case *ast.InitDecl:
		p.initDecl(n)
	case *ast.UnsafeGroup:
		p.unsafeGroup(n)
	}
}

// declDecorators returns a declaration's decorator prefix.
func declDecorators(d ast.Decl) []*ast.Decorator {
	switch n := d.(type) {
	case *ast.FuncDecl:
		return n.Decorators
	case *ast.StructDecl:
		return n.Decorators
	case *ast.EnumDecl:
		return n.Decorators
	case *ast.TypeDecl:
		return n.Decorators
	case *ast.SpecDecl:
		return n.Decorators
	case *ast.ImplDecl:
		return n.Decorators
	case *ast.InitDecl:
		return n.Decorators
	case *ast.UnsafeGroup:
		return n.Decorators
	}
	return nil
}

// decorators prints each decorator on its own line at the current indent.
func (p *printer) decorators(ds []*ast.Decorator) {
	for _, d := range ds {
		p.ind()
		p.decorator(d)
		p.nl()
	}
}

// decorator prints '#[item, …]'.
func (p *printer) decorator(d *ast.Decorator) {
	p.write("#[")
	for i, it := range d.Items {
		if i > 0 {
			p.write(", ")
		}
		p.write(it.Name)
		if len(it.Args) > 0 {
			p.write("(")
			for j, a := range it.Args {
				if j > 0 {
					p.write(", ")
				}
				p.constExpr(a)
			}
			p.write(")")
		}
	}
	p.write("]")
}

// --- brace-body helpers -------------------------------------------------------

// openBody writes ' {' and steps one indent deeper.
func (p *printer) openBody() {
	p.write(" {")
	p.nl()
	p.indent++
}

// itemLine prints one body item with its node-attached trivia on its own line.
func (p *printer) itemLine(node ast.Node, render func()) {
	p.leadTrivia(node.Lead())
	p.ind()
	render()
	p.trailTrivia(node.Trail())
	p.nl()
}

// endBrace prints the dangling trivia before '}', steps back one indent, and
// writes the closing brace.
func (p *printer) endBrace(end []token.Trivia) {
	p.leadTrivia(end)
	p.indent--
	p.ind()
	p.write("}")
}

// --- struct / enum / type-alias -----------------------------------------------

func (p *printer) structDecl(n *ast.StructDecl) {
	if n.Pub {
		p.write("pub ")
	}
	p.write("struct ")
	p.write(n.Name)
	p.generics(n.Generics)
	p.openBody()
	for _, f := range n.Fields {
		f := f
		p.itemLine(f, func() { p.field(f) })
	}
	p.endBrace(n.End)
}

func (p *printer) field(f *ast.StructField) {
	if f.Pub {
		p.write("pub ")
	}
	p.write(f.Name)
	p.write(": ")
	p.typ(f.Type)
	if f.Default != nil {
		p.write(" = ")
		p.expr(f.Default, precLowest)
	}
}

func (p *printer) enumDecl(n *ast.EnumDecl) {
	if n.Pub {
		p.write("pub ")
	}
	p.write("enum ")
	p.write(n.Name)
	p.generics(n.Generics)
	p.openBody()
	for _, v := range n.Variants {
		v := v
		p.itemLine(v, func() { p.variant(v) })
	}
	p.endBrace(n.End)
}

func (p *printer) variant(v *ast.Variant) {
	p.write(v.Name)
	if len(v.Payload) > 0 {
		p.write("(")
		for i, t := range v.Payload {
			if i > 0 {
				p.write(", ")
			}
			p.typ(t)
		}
		p.write(")")
	}
	if v.Discr != nil {
		p.write(" = ")
		p.constExpr(v.Discr)
	}
}

func (p *printer) typeDecl(n *ast.TypeDecl) {
	if n.Pub {
		p.write("pub ")
	}
	p.write("type ")
	p.write(n.Name)
	p.generics(n.Generics)
	p.write(" = ")
	p.typ(n.Alias)
}

// --- spec / impl / init -------------------------------------------------------

func (p *printer) specDecl(n *ast.SpecDecl) {
	if n.Pub {
		p.write("pub ")
	}
	p.write("spec ")
	p.write(n.Name)
	p.generics(n.Generics)
	if n.Super != nil {
		p.write(": ")
		p.bound(n.Super)
	}
	p.openBody()
	for _, m := range n.Members {
		m := m
		p.itemLine(m, func() { p.specMember(m) })
	}
	p.endBrace(n.End)
}

func (p *printer) specMember(m ast.SpecMember) {
	switch n := m.(type) {
	case *ast.FuncDecl:
		p.funcDecl(n)
	case *ast.FnSig:
		p.fnSig(n)
	case *ast.AssocType:
		p.write("type ")
		p.write(n.Name)
		if n.Bound != nil {
			p.write(": ")
			p.bound(n.Bound)
		}
	case *ast.AssocVal:
		p.write(n.Name)
		p.write(": ")
		p.typ(n.Type)
	}
}

// fnSig prints a required method signature (no body).
func (p *printer) fnSig(n *ast.FnSig) {
	if n.Unsafe {
		p.write("unsafe ")
	}
	if n.Mut {
		p.write("mut ")
	}
	p.write("fn ")
	p.write(n.Name)
	p.generics(n.Generics)
	p.paramList(n.Params)
	if n.Ret != nil {
		p.write(" -> ")
		p.typ(n.Ret)
	}
}

func (p *printer) implDecl(n *ast.ImplDecl) {
	p.write("impl")
	p.generics(n.Generics)
	p.write(" ")
	if n.Spec != nil {
		p.typ(n.Spec)
		p.write(" for ")
	}
	p.typ(n.Target)
	p.openBody()
	for _, it := range n.Items {
		it := it
		p.itemLine(it, func() { p.implItem(it) })
	}
	p.endBrace(n.End)
}

func (p *printer) implItem(it ast.ImplItem) {
	switch n := it.(type) {
	case *ast.FuncDecl:
		p.funcDecl(n)
	case *ast.AssocBind:
		p.write("type ")
		p.write(n.Name)
		p.write(" = ")
		p.typ(n.Type)
	case *ast.ValBind:
		p.write(n.Name)
		p.write(" := ")
		p.constExpr(n.Value)
	}
}

func (p *printer) initDecl(n *ast.InitDecl) {
	p.write("init() ")
	p.block(n.Body)
}

// --- generics & bounds --------------------------------------------------------

// generics prints a '[T: Bound, N: int]' parameter list, or nothing when nil.
func (p *printer) generics(g *ast.Generics) {
	if g == nil {
		return
	}
	p.write("[")
	for i, tp := range g.Params {
		if i > 0 {
			p.write(", ")
		}
		p.write(tp.Name)
		if tp.Bound != nil {
			p.write(": ")
			p.bound(tp.Bound)
		}
	}
	p.write("]")
}

// bound prints a spec conjunction 'A + B'.
func (p *printer) bound(b *ast.Bound) {
	for i, name := range b.Names {
		if i > 0 {
			p.write(" + ")
		}
		p.write(name)
	}
}

// --- type expressions ---------------------------------------------------------

// typ prints a type expression in canonical form.
func (p *printer) typ(t ast.Type) {
	switch n := t.(type) {
	case *ast.TypeRef:
		p.write(n.Name)
		if len(n.Args) > 0 {
			p.write("[")
			for i, a := range n.Args {
				if i > 0 {
					p.write(", ")
				}
				p.typ(a)
			}
			p.write("]")
		}
		for _, seg := range n.Proj {
			p.write(".")
			p.write(seg)
		}
	case *ast.OptType:
		p.typ(n.Elem)
		p.write("?")
	case *ast.TupleType:
		p.write("(")
		for i, e := range n.Elems {
			if i > 0 {
				p.write(", ")
			}
			p.typ(e)
		}
		p.write(")")
	case *ast.ArrayType:
		p.write("[")
		p.typ(n.Elem)
		p.write("; ")
		p.constExpr(n.Count)
		p.write("]")
	case *ast.ChanType:
		p.chanType(n)
	case *ast.FnType:
		p.fnType(n)
	case *ast.PtrType:
		p.write("ptr")
		if n.Elem != nil {
			p.write("[")
			p.typ(n.Elem)
			p.write("]")
		}
	case *ast.ConstExpr:
		p.constExpr(n)
	}
}

func (p *printer) chanType(n *ast.ChanType) {
	if n.Dir == ast.ChanRecv {
		p.write("<-")
	}
	p.write("chan[")
	p.typ(n.Elem)
	p.write("]")
	if n.Dir == ast.ChanSend {
		p.write("<-")
	}
}

func (p *printer) fnType(n *ast.FnType) {
	if n.Unsafe {
		p.write("unsafe ")
	}
	p.write("fn(")
	for i, pt := range n.Params {
		if i > 0 {
			p.write(", ")
		}
		if pt.Ref {
			p.write("mut &")
		}
		p.typ(pt.Type)
	}
	p.write(")")
	if n.Ret != nil {
		p.write(" -> ")
		p.typ(n.Ret)
	}
}

// constExpr prints the inner expression of a const-expr wrapper.
func (p *printer) constExpr(c *ast.ConstExpr) { p.expr(c.X, precLowest) }
