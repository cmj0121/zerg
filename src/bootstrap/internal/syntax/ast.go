package syntax

// Program is the root AST node — a flat sequence of statements.
type Program struct {
	Statements []Stmt
}

// Stmt is the common interface for v0.0 statements. The interface is
// deliberately empty (a marker) — switching on concrete type is fine while
// the language has two statements.
type Stmt interface {
	stmtNode()
	StmtPos() Position
}

// NopStmt represents the literal `nop` keyword.
type NopStmt struct {
	Pos Position
}

func (*NopStmt) stmtNode()           {}
func (s *NopStmt) StmtPos() Position { return s.Pos }

// PrintStmt represents `print STRING`. The Value field holds the already-
// unescaped string contents.
type PrintStmt struct {
	Pos   Position
	Value string
}

func (*PrintStmt) stmtNode()           {}
func (s *PrintStmt) StmtPos() Position { return s.Pos }
