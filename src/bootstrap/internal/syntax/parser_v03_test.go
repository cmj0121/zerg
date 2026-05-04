package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.3 parser tests — Unit 1: list-element assignment surface.
//
// Unit 1 broadens AssignStmt.Target from *IdentExpr to Expr and admits
// IndexExpr targets (`xs[i] = v`) at parse time. The parser still narrows
// the LHS at parse time: only *IdentExpr and *IndexExpr are accepted, with
// single-level indexing only and the bare `=` operator only. Compound
// operators (`xs[i] += 1`) and chained indexing (`xs[i][j] = v`) are
// rejected here so Unit 3 (borrow checker) and beyond can assume a clean
// shape.
//
// Borrow / mut / typeck behaviour for IndexExpr LHS lands in later units;
// these tests cover the parser surface only.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Positive: parser accepts the v0.3 LHS shapes.
// ---------------------------------------------------------------------------

// TestParseAssignIndexLiteralIndex covers the simplest list-element write.
func TestParseAssignIndexLiteralIndex(t *testing.T) {
	prog := parseProgramSrc(t, "xs[0] = 5\n")
	s := expectOne[*AssignStmt](t, prog)
	if s.Op != AssignSet {
		t.Errorf("op = %v, want AssignSet", s.Op)
	}
	idx, ok := s.Target.(*IndexExpr)
	if !ok {
		t.Fatalf("Target is %T, want *IndexExpr", s.Target)
	}
	recv, ok := idx.Receiver.(*IdentExpr)
	if !ok {
		t.Fatalf("Receiver is %T, want *IdentExpr", idx.Receiver)
	}
	if recv.Name != "xs" {
		t.Errorf("receiver = %q, want xs", recv.Name)
	}
}

// TestParseAssignIndexIdentIndex covers `xs[i] = expr` where the index is a
// name rather than a literal — a common shape for loops and helpers.
func TestParseAssignIndexIdentIndex(t *testing.T) {
	prog := parseProgramSrc(t, "xs[i] = i + 1\n")
	s := expectOne[*AssignStmt](t, prog)
	idx, ok := s.Target.(*IndexExpr)
	if !ok {
		t.Fatalf("Target is %T, want *IndexExpr", s.Target)
	}
	if _, ok := idx.Index.(*IdentExpr); !ok {
		t.Errorf("Index is %T, want *IdentExpr", idx.Index)
	}
	if _, ok := s.Value.(*BinaryExpr); !ok {
		t.Errorf("Value is %T, want *BinaryExpr", s.Value)
	}
}

// TestParseAssignIndexAfterMutDecl walks the typical `mut xs := [..]; xs[i] = v`
// program — two statements at the top level, verifying both parse together.
func TestParseAssignIndexAfterMutDecl(t *testing.T) {
	prog := parseProgramSrc(t, "mut xs := [1, 2, 3]\nxs[1] = 99\n")
	if len(prog.Statements) != 2 {
		t.Fatalf("got %d statements, want 2", len(prog.Statements))
	}
	if _, ok := prog.Statements[0].(*MutStmt); !ok {
		t.Fatalf("statement 0 is %T, want *MutStmt", prog.Statements[0])
	}
	asg, ok := prog.Statements[1].(*AssignStmt)
	if !ok {
		t.Fatalf("statement 1 is %T, want *AssignStmt", prog.Statements[1])
	}
	if _, ok := asg.Target.(*IndexExpr); !ok {
		t.Errorf("Target is %T, want *IndexExpr", asg.Target)
	}
}

// TestParseAssignIndexCallExprIndex covers a call-result index expression
// like `xs[len(xs)-1] = 0` — the call sits inside the index, not in the
// receiver, which is fine.
func TestParseAssignIndexCallExprIndex(t *testing.T) {
	prog := parseProgramSrc(t, "xs[len(xs) - 1] = 0\n")
	s := expectOne[*AssignStmt](t, prog)
	idx, ok := s.Target.(*IndexExpr)
	if !ok {
		t.Fatalf("Target is %T, want *IndexExpr", s.Target)
	}
	if _, ok := idx.Index.(*BinaryExpr); !ok {
		t.Errorf("Index is %T, want *BinaryExpr", idx.Index)
	}
}

// TestParseAssignIdentRegression confirms the v0.1 simple form still parses
// — no regression while broadening Target's static type.
func TestParseAssignIdentRegression(t *testing.T) {
	prog := parseProgramSrc(t, "x = 5\n")
	s := expectOne[*AssignStmt](t, prog)
	ident, ok := s.Target.(*IdentExpr)
	if !ok {
		t.Fatalf("Target is %T, want *IdentExpr", s.Target)
	}
	if ident.Name != "x" {
		t.Errorf("target = %q, want x", ident.Name)
	}
}

// TestParseAssignIdentCompoundRegression confirms `+=` etc. on an identifier
// still parses through the broadened AssignStmt.
func TestParseAssignIdentCompoundRegression(t *testing.T) {
	prog := parseProgramSrc(t, "y += 1\n")
	s := expectOne[*AssignStmt](t, prog)
	if s.Op != AssignAdd {
		t.Errorf("op = %v, want AssignAdd", s.Op)
	}
	if _, ok := s.Target.(*IdentExpr); !ok {
		t.Errorf("Target is %T, want *IdentExpr", s.Target)
	}
}

// ---------------------------------------------------------------------------
// Negative: parser rejects the out-of-scope LHS shapes with a precise
// diagnostic so the user is steered to the supported surface.
// ---------------------------------------------------------------------------

// TestParseAssignChainedIndexRejected confirms `xs[i][j] = v` is rejected at
// parse with the "single-level" message — Unit 3 can assume LHS receivers
// are not themselves IndexExprs.
func TestParseAssignChainedIndexRejected(t *testing.T) {
	expectParseErr(t, "xs[0][1] = 5\n", "chained indexing is not supported at v0.3")
}

// TestParseAssignCallResultRejected confirms `f().x = 5` is rejected with
// the generic "must be an identifier or list[i]" message — call-result LHS
// is out of scope at v0.3 and gets the same diagnostic as any other unsupported
// shape.
func TestParseAssignCallResultRejected(t *testing.T) {
	expectParseErr(t, "f().x = 5\n", "must be an identifier or list[i]")
}

// TestParseAssignFieldAccessRejected confirms struct field assignment
// (`p.x = 1`) is also rejected at v0.3 — only Ident or single-level Index
// are admitted.
func TestParseAssignFieldAccessRejected(t *testing.T) {
	expectParseErr(t, "p.x = 1\n", "must be an identifier or list[i]")
}

// TestParseAssignIndexCompoundRejected confirms `xs[i] += 1` is rejected at
// parse with a focused "compound assignment to a list element" message.
func TestParseAssignIndexCompoundRejected(t *testing.T) {
	expectParseErr(t, "xs[0] += 1\n", "compound assignment")
}

// TestParseAssignIndexCompoundSubRejected sanity-checks that the rejection
// message names the operator so the user sees what was used.
func TestParseAssignIndexCompoundSubRejected(t *testing.T) {
	msg := expectParseErr(t, "xs[0] -= 1\n", "compound assignment")
	if !strings.Contains(msg, "-=") {
		t.Errorf("error %q does not mention -=", msg)
	}
}
