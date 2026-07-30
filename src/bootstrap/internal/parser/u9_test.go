package parser

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// firstStmt parses a single-function source and returns its first body statement.
func firstStmt(t *testing.T, body string) ast.Stmt {
	t.Helper()
	fn := onlyFunc(t, "fn f() {\n"+body+"\n}")
	if len(fn.Body.Stmts) == 0 {
		t.Fatalf("no statements parsed from %q", body)
	}
	return fn.Body.Stmts[0]
}

// TestSpawnSendDeferDel checks the simple group 9/11 statements parse to their
// nodes.
func TestSpawnSendDeferDel(t *testing.T) {
	if _, ok := firstStmt(t, "spawn work()").(*ast.SpawnStmt); !ok {
		t.Fatal("spawn should parse to *ast.SpawnStmt")
	}
	send, ok := firstStmt(t, "ch <- v").(*ast.SendStmt)
	if !ok {
		t.Fatal("send should parse to *ast.SendStmt")
	}
	if id, ok := send.Chan.(*ast.Ident); !ok || id.Name != "ch" {
		t.Fatalf("send channel = %+v, want ident ch", send.Chan)
	}
	if _, ok := firstStmt(t, "defer close(ch)").(*ast.DeferStmt); !ok {
		t.Fatal("defer should parse to *ast.DeferStmt")
	}
	del, ok := firstStmt(t, "del ch").(*ast.DelStmt)
	if !ok || del.Name != "ch" {
		t.Fatalf("del = %+v, want DelStmt ch", firstStmt(t, "del ch"))
	}
}

// TestChanNew checks the chan constructor expression carries its element type
// and optional capacity.
func TestChanNew(t *testing.T) {
	bind := firstStmt(t, "c := chan[int](8)").(*ast.BindStmt)
	cn, ok := bind.Value.(*ast.ChanNew)
	if !ok {
		t.Fatalf("value is %T, want *ast.ChanNew", bind.Value)
	}
	if cn.Cap == nil {
		t.Fatal("chan[int](8) should carry a capacity")
	}
	unbuf := firstStmt(t, "c := chan[str]()").(*ast.BindStmt).Value.(*ast.ChanNew)
	if unbuf.Cap != nil {
		t.Fatal("chan[str]() should carry no capacity")
	}
}

// TestSelectArms checks each select-arm shape parses to the right kind and binds.
func TestSelectArms(t *testing.T) {
	sel := firstStmt(t, "select {\nx := <-ch => a()\n<-q => b()\nout <- v => c()\n"+
		"_ => e()\n}").(*ast.SelectStmt)
	if len(sel.Arms) != 4 {
		t.Fatalf("got %d arms, want 4", len(sel.Arms))
	}
	want := []struct {
		kind    ast.SelectArmKind
		bind    string
		hasBind bool
	}{
		{ast.SelectRecv, "x", true},
		{ast.SelectRecv, "", false},
		{ast.SelectSend, "", false},
		{ast.SelectDefault, "", false},
	}
	for i, w := range want {
		a := sel.Arms[i]
		if a.Kind != w.kind || a.Bind != w.bind || a.HasBind != w.hasBind {
			t.Errorf("arm %d = {kind:%d bind:%q hasBind:%t}, want {kind:%d bind:%q hasBind:%t}",
				i, a.Kind, a.Bind, a.HasBind, w.kind, w.bind, w.hasBind)
		}
	}
}

// TestImport checks both import forms and their specs.
func TestImport(t *testing.T) {
	single := parseOK(t, "import \"a/text\" as at").Items[0].(*ast.ImportStmt)
	if single.Grouped || len(single.Specs) != 1 {
		t.Fatalf("single import = %+v", single)
	}
	if s := single.Specs[0]; s.Path != "a/text" || s.Alias != "at" || s.Pub {
		t.Fatalf("spec = %+v", s)
	}
	grouped := parseOK(t, "import ( pub \"a/b\"; \"c/d\" as e )").Items[0].(*ast.ImportStmt)
	if !grouped.Grouped || len(grouped.Specs) != 2 {
		t.Fatalf("grouped import = %+v", grouped)
	}
	if !grouped.Specs[0].Pub || grouped.Specs[0].Path != "a/b" {
		t.Fatalf("grouped spec 0 = %+v", grouped.Specs[0])
	}
	if grouped.Specs[1].Alias != "e" {
		t.Fatalf("grouped spec 1 = %+v", grouped.Specs[1])
	}
}

// TestTopLevelStmtList checks the finalized top level accepts an import, a
// module-level binding, and a declaration in one file.
func TestTopLevelStmtList(t *testing.T) {
	file := parseOK(t, "import \"util/text\"\nversion := 3\nfn main() {\nnop\n}")
	if len(file.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(file.Items))
	}
	if _, ok := file.Items[0].(*ast.ImportStmt); !ok {
		t.Fatalf("item 0 = %T, want *ast.ImportStmt", file.Items[0])
	}
	if b, ok := file.Items[1].(*ast.BindStmt); !ok || b.Name != "version" {
		t.Fatalf("item 1 = %+v, want binding version", file.Items[1])
	}
	if _, ok := file.Items[2].(*ast.FuncDecl); !ok {
		t.Fatalf("item 2 = %T, want *ast.FuncDecl", file.Items[2])
	}
}
