package syntax

import (
	"fmt"
	"strconv"
)

// ParseError is returned when the parser cannot fit the token stream into the
// v0.1 grammar.
//
// Incomplete is true when the failure was caused by the input running out
// mid-construct (EOF where more tokens were required) — e.g. a brace-block
// without its closing `}`, an expression that stops at EOF, a `let` whose
// `:=`/`: T =` is missing because the user has not typed the rest yet. The
// REPL uses this flag to keep accumulating input rather than reporting a
// hard error.
type ParseError struct {
	Pos        Position
	Message    string
	Incomplete bool
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at %s: %s", e.Pos, e.Message)
}

// IsIncomplete reports whether this error was caused by the input running
// out mid-construct. The REPL's try-parse loop uses it to decide whether to
// keep reading more input or to surface the error to the user.
func (e *ParseError) IsIncomplete() bool { return e != nil && e.Incomplete }

// Parse consumes a token slice (typically produced by Lex) and returns the
// program AST. Leading, trailing, and repeated NEWLINE tokens are tolerated;
// the v0.0 examples both contain a shebang+comment block followed by blank
// lines, so this is required for the parity test to even reach the
// statements.
//
// Comments are NOT threaded by Parse — the resulting Program has empty
// HeadComments / LeadingComments slices. Callers that need comments
// (`zerg fmt`) use ParseWithComments instead. Pre-Unit-1 callers continue
// to use Parse with no behavioural change.
func Parse(tokens []Token) (*Program, error) {
	p := newParser(tokens)
	return p.parseProgram()
}

// ParseWithComments is Parse plus a pre-collected slice of CommentTokens
// (typically produced by LexWithComments). Leading-line comments are
// attached to the next statement's LeadingComments; comments that appear
// at the very top of the file (before any statement) are collected on
// Program.HeadComments instead. Trailing inline comments — those marked
// Leading == false — are recorded on Program.Comments but NOT attached to
// any AST node; v0.10 fmt strips them per the documented limitation.
//
// Comment attachment rule (PLAN.md): a leading-line `#` comment attaches to
// the next non-blank statement on a following line. Blank lines between the
// comment block and the next statement do NOT break attribution — users
// often separate comment-blocks with blanks.
func ParseWithComments(tokens []Token, comments []CommentToken) (*Program, error) {
	p := newParser(tokens)
	p.comments = comments
	return p.parseProgram()
}

// ParseWithOptionsAndComments combines ParseWithOptions (stdlib gating) and
// ParseWithComments (comment threading). The stdlib loader does not need
// comments, so it stays on ParseWithOptions; this entry point exists so
// `zerg fmt` can format stdlib sources without a separate code path.
func ParseWithOptionsAndComments(tokens []Token, comments []CommentToken, opts ParseOptions) (*Program, error) {
	p := newParser(tokens)
	p.inStdlibFile = opts.InStdlibFile
	p.comments = comments
	return p.parseProgram()
}

// ParseOptions controls Parse-time gates that depend on where the source
// originated. Pre-Unit-1 callers used Parse directly; v0.8 adds the
// stdlib-only `__builtin` marker, so the loader (Unit 2) signals here when
// the file lives under the embedded `std/` tree. Tests construct one of
// these directly to exercise either path.
type ParseOptions struct {
	// InStdlibFile is true when the source is one of the toolchain-shipped
	// `std/...` modules. Only those files may use the `__builtin` fn-decl
	// marker; user-loaded sources see a focused diagnostic. Set by the
	// loader; defaults to false for hand-driven Parse callers.
	InStdlibFile bool
}

// ParseWithOptions is Parse with an explicit options record. The default
// Parse keeps its zero-arg shape so existing callers stay untouched.
func ParseWithOptions(tokens []Token, opts ParseOptions) (*Program, error) {
	p := newParser(tokens)
	p.inStdlibFile = opts.InStdlibFile
	return p.parseProgram()
}

// ParseStatement parses exactly one statement from a single line of input —
// the shape the REPL feeds in. Trailing whitespace/EOF is fine, but a second
// statement on the same line is rejected.
func ParseStatement(tokens []Token) (Stmt, error) {
	p := newParser(tokens)
	p.skipNewlines()
	if p.peek().Kind == KindEOF {
		return nil, nil
	}
	stmt, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	p.skipNewlines()
	if p.peek().Kind != KindEOF {
		t := p.peek()
		return nil, &ParseError{
			Pos:     t.Pos,
			Message: fmt.Sprintf("unexpected %s after statement", t.Kind),
		}
	}
	return stmt, nil
}

// ---------------------------------------------------------------------------
// parser state and primitives.
//
// `parenDepth` tracks open `(` and `[`. While > 0 the parser silently skips
// NEWLINE tokens, giving line-continuation semantics inside parens and
// brackets. This matches the lexer-emits-newlines-always design — we keep
// the lexer simple and let context-sensitive significance live here. PLAN
// allows either approach; this choice was made because realistic v0.1
// programs (e.g. multi-line argument lists) read better than forcing
// trailing-operator continuation.
// ---------------------------------------------------------------------------

type parser struct {
	tokens     []Token
	pos        int
	parenDepth int
	// blockDepth counts how many lexical brace-blocks (fn body, if/for/match
	// arm body, impl/spec method body) the parser is currently inside. The
	// depth is bumped by parseBlock on entry and decremented on exit. v0.5
	// Unit 1b uses it to reject `import` statements anywhere except the
	// file's top-level scope: parseImport reads blockDepth and produces a
	// dedicated diagnostic when it is non-zero. Other statement parsers do
	// not consult this field; it is dedicated to top-level-only constraints.
	blockDepth int
	// pendingImports holds desugared ImportDecls produced by `import (...)`
	// after the first entry. parseImport returns the first entry directly to
	// parseProgram, which then drains this queue before reading more tokens.
	// Keeping the queue on the parser (rather than threading a slice return)
	// lets parseStatement keep its single-Stmt signature unchanged and lets
	// the grouped form share parseImportEntry with the single-import form.
	pendingImports []*ImportDecl
	// fnBodyDepths is a stack of blockDepth values, one per currently-open
	// fn body (named, impl method, spec method default body, or anon-fn).
	// Each entry records the blockDepth that the fn body's parseBlock runs
	// at, so v0.7 `defer` can reject anywhere except the immediate fn-body
	// scope: the rule is `len(fnBodyDepths) > 0 && p.blockDepth ==
	// fnBodyDepths[last]`. Push happens just before the fn-body parseBlock
	// call; pop is paired in defer to keep the stack honest on errors.
	fnBodyDepths []int
	// inStdlibFile gates the v0.8 `__builtin` fn-decl marker. Set by the
	// loader (Unit 2) when the source is one of the embedded `std/...`
	// modules. Default zero ⇒ user code, which rejects `__builtin` with a
	// focused diagnostic so the marker stays a private toolchain primitive.
	inStdlibFile bool
	// comments is the lexer's side-channel of `#` line comments, in source
	// order. Nil for callers that used Parse / ParseWithOptions; non-nil
	// for ParseWithComments / ParseWithOptionsAndComments. The parser
	// drains entries by line as statements appear and attaches Leading ==
	// true comments to the next statement's LeadingComments slot. Trailing
	// inline comments (Leading == false) are recorded on Program.Comments
	// at the end but NOT attached to any AST node — v0.10 fmt strips them
	// per the documented limitation.
	comments    []CommentToken
	commentIdx  int  // index into comments — entries before this have been processed
	headDrained bool // true once the file-head comment block has been drained onto Program.HeadComments
}

func newParser(tokens []Token) *parser {
	return &parser{tokens: tokens}
}

// peek returns the current token without consuming it. While inside a paren
// or bracket group, NEWLINE tokens are transparent — peek skips past them.
func (p *parser) peek() Token {
	for {
		if p.pos >= len(p.tokens) {
			return Token{Kind: KindEOF}
		}
		t := p.tokens[p.pos]
		if p.parenDepth > 0 && t.Kind == KindNewline {
			p.pos++
			continue
		}
		return t
	}
}

// peekRaw returns the current token without skipping NEWLINE — used by the
// statement-level loop where NEWLINE is significant.
func (p *parser) peekRaw() Token {
	if p.pos >= len(p.tokens) {
		return Token{Kind: KindEOF}
	}
	return p.tokens[p.pos]
}

// advance consumes and returns the current significant token (after any
// NEWLINE-skip done by peek when parenDepth > 0).
func (p *parser) advance() Token {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

// peekAfterFnIs reports whether the token immediately after a `fn` head is of
// the given kind. Used by the v0.7 disambiguation rule: `fn (` ⇒ anon-fn
// expression, `fn IDENT` ⇒ fn-decl. The cursor is assumed to be sitting on
// the `fn` token; lookahead skips NEWLINEs only when parenDepth > 0 (matching
// p.peek's contract). At v0.7 callers always invoke this with parenDepth == 0
// (statement position), so the simple `+1` slot inspection is correct.
func (p *parser) peekAfterFnIs(k Kind) bool {
	i := p.pos + 1
	if p.parenDepth > 0 {
		for i < len(p.tokens) && p.tokens[i].Kind == KindNewline {
			i++
		}
	}
	if i >= len(p.tokens) {
		return false
	}
	return p.tokens[i].Kind == k
}

// skipNewlines drops any number of NEWLINE tokens at the current cursor.
func (p *parser) skipNewlines() {
	for p.pos < len(p.tokens) && p.tokens[p.pos].Kind == KindNewline {
		p.pos++
	}
}

// expect consumes a token of the given kind or returns a parse error tagged
// with the offending token's position. If the offending token is EOF the
// error is flagged Incomplete so the REPL can keep accumulating input.
func (p *parser) expect(k Kind, ctx string) (Token, error) {
	t := p.peek()
	if t.Kind != k {
		return Token{}, &ParseError{
			Pos:        t.Pos,
			Message:    fmt.Sprintf("expected %s%s, got %s", k, ctxSuffix(ctx), t.Kind),
			Incomplete: t.Kind == KindEOF,
		}
	}
	return p.advance(), nil
}

func ctxSuffix(ctx string) string {
	if ctx == "" {
		return ""
	}
	return " " + ctx
}

// errorAt builds a ParseError at the given position.
func errorAt(pos Position, format string, args ...any) error {
	return &ParseError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}

// errorAtTok builds a ParseError at the given token's position. If the
// offending token is EOF the error is flagged Incomplete — the REPL relies
// on that flag to decide whether to keep reading input.
func errorAtTok(t Token, format string, args ...any) error {
	return &ParseError{
		Pos:        t.Pos,
		Message:    fmt.Sprintf(format, args...),
		Incomplete: t.Kind == KindEOF,
	}
}

// setLeadingComments attaches the given slice as the stmt's LeadingComments.
// Concrete-type-switching keeps the field private to each node — the AST
// has no shared "Decl" supertype that owns the slot, so the helper
// enumerates every Stmt-implementing type that gained the field at v0.10
// Unit 1. Adding a new statement node means adding a case here.
//
// A nil/empty slice short-circuits to a no-op; the AST default is no
// comments and over-writing nil with nil is wasteful.
func setLeadingComments(stmt Stmt, comments []string) {
	if len(comments) == 0 {
		return
	}
	switch s := stmt.(type) {
	case *LetStmt:
		s.LeadingComments = comments
	case *MutStmt:
		s.LeadingComments = comments
	case *ConstStmt:
		s.LeadingComments = comments
	case *AssignStmt:
		s.LeadingComments = comments
	case *ExprStmt:
		s.LeadingComments = comments
	case *PrintStmt:
		s.LeadingComments = comments
	case *ReturnStmt:
		s.LeadingComments = comments
	case *BreakStmt:
		s.LeadingComments = comments
	case *ContinueStmt:
		s.LeadingComments = comments
	case *FnDecl:
		s.LeadingComments = comments
	case *IfStmt:
		s.LeadingComments = comments
	case *ForStmt:
		s.LeadingComments = comments
	case *NopStmt:
		s.LeadingComments = comments
	case *ImportDecl:
		s.LeadingComments = comments
	case *StructDecl:
		s.LeadingComments = comments
	case *EnumDecl:
		s.LeadingComments = comments
	case *MatchStmt:
		s.LeadingComments = comments
	case *SpecDecl:
		s.LeadingComments = comments
	case *ImplDecl:
		s.LeadingComments = comments
	case *SpawnStmt:
		s.LeadingComments = comments
	case *DeferStmt:
		s.LeadingComments = comments
	case *SendStmt:
		s.LeadingComments = comments
	case *SelectStmt:
		s.LeadingComments = comments
	}
}

// drainHeadComments collects the file-head block of leading-line comments —
// the contiguous run anchored at line 1 with no blank-line gaps between
// entries. PLAN.md §file-head: the `# requires:` line plus any license /
// attribution / shebang lines all flow through here. Comments that appear
// later in the file (separated from the head by a blank line) belong to
// the next statement's LeadingComments and are NOT collected here.
//
// Always advances p.commentIdx past every consumed entry. Returns nil when
// the file does not start with a leading comment on line 1.
func (p *parser) drainHeadComments() []string {
	if len(p.comments) == 0 || p.commentIdx >= len(p.comments) {
		return nil
	}
	first := p.comments[p.commentIdx]
	if !first.Leading || first.Pos.Line != 1 {
		return nil
	}
	var out []string
	prevLine := 0
	for p.commentIdx < len(p.comments) {
		c := p.comments[p.commentIdx]
		if !c.Leading {
			break
		}
		if prevLine != 0 && c.Pos.Line != prevLine+1 {
			// blank-line gap ⇒ end of the head block.
			break
		}
		out = append(out, c.Text)
		prevLine = c.Pos.Line
		p.commentIdx++
	}
	return out
}

// drainLeadingComments collects all leading-line `#` comments whose line is
// strictly before stmtLine. Trailing inline comments (Leading == false) and
// comments on the same line as the statement are skipped — they belong to
// the previous statement (if any) and are recorded on Program.Comments
// instead, where the formatter can ignore them at v0.10 (documented
// limitation).
//
// The drained comments are returned as a `[]string` of comment bodies (the
// `#` is stripped at lex time). Callers attach the slice to the statement
// node's LeadingComments field, OR for the very first call, route them to
// Program.HeadComments via the headDrained latch.
//
// Blank lines between a comment block and the next statement do NOT break
// attribution — users often separate comment groups with blanks. The drain
// walks comments[commentIdx:] in line order and stops at the first comment
// whose line is >= stmtLine OR at the first non-leading comment.
//
// Returns nil for callers parsing without comments threaded (p.comments
// is empty). Always advances p.commentIdx past every consumed entry.
func (p *parser) drainLeadingComments(stmtLine int) []string {
	if len(p.comments) == 0 {
		return nil
	}
	var out []string
	for p.commentIdx < len(p.comments) {
		c := p.comments[p.commentIdx]
		if c.Pos.Line >= stmtLine {
			break
		}
		// Trailing inline comments (Leading == false) belong to the
		// previous statement's same line; v0.10 strips them. Skip past
		// them without attaching anywhere.
		if !c.Leading {
			p.commentIdx++
			continue
		}
		out = append(out, c.Text)
		p.commentIdx++
	}
	return out
}

// ---------------------------------------------------------------------------
// Top-level program and statement terminator handling.
// ---------------------------------------------------------------------------

func (p *parser) parseProgram() (*Program, error) {
	prog := &Program{}
	for {
		p.skipNewlines()
		if p.peekRaw().Kind == KindEOF {
			// Comments-only file (no statements at all): everything
			// becomes the file-head block. Otherwise leading-line comments
			// past the last statement live on Program.Comments — the
			// formatter has no node to attach them to and v0.10 strips
			// the orphan tail per the documented limitation.
			if !p.headDrained && len(prog.Statements) == 0 {
				prog.HeadComments = p.drainHeadComments()
				if tail := p.drainLeadingComments(1 << 30); len(tail) > 0 {
					prog.HeadComments = append(prog.HeadComments, tail...)
				}
				p.headDrained = true
			}
			if len(p.comments) > 0 {
				prog.Comments = p.comments
			}
			return prog, nil
		}
		stmtLine := p.peekRaw().Pos.Line
		// Drain leading-line comments preceding this statement. On the
		// very first call we route the contiguous run anchored at line 1
		// (stopping at the first blank-line gap) to Program.HeadComments
		// — the file's preamble (`# requires:` + license / shebang).
		// Anything after that gap attaches to this first statement's
		// LeadingComments slot, matching the user's likely intent that a
		// comment immediately above a decl describes THAT decl. PLAN.md
		// §file-head pins the requires-line + headers on HeadComments.
		if !p.headDrained {
			prog.HeadComments = p.drainHeadComments()
			p.headDrained = true
		}
		leadingForStmt := p.drainLeadingComments(stmtLine)
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		// parseStatement may return nil to mean "produced zero statements"
		// — currently this is only used by `import ()` (empty group) which
		// is admitted as a user-friendly noop. nil is appended-then-stripped
		// here so callers don't have to special-case the shape.
		if stmt != nil {
			setLeadingComments(stmt, leadingForStmt)
			prog.Statements = append(prog.Statements, stmt)
		} else if len(leadingForStmt) > 0 {
			// `import ()` consumed leading comments — keep them attached
			// somewhere by routing onto HeadComments as a fallback so
			// nothing is silently dropped from the AST.
			prog.HeadComments = append(prog.HeadComments, leadingForStmt...)
		}
		// Drain any extra ImportDecls produced by `import (...)`. The grouped
		// form returns its first entry directly; the rest live on the parser
		// as a small FIFO. Each desugared entry is treated like its own
		// top-level statement for downstream layers.
		for _, extra := range p.pendingImports {
			prog.Statements = append(prog.Statements, extra)
		}
		p.pendingImports = nil
		if err := p.terminateStatement(); err != nil {
			return nil, err
		}
	}
}

// terminateStatement consumes the NEWLINE (or EOF) that ends a statement at
// either the top level or inside a block. It is NOT called for statements
// like FnDecl/IfStmt/ForStmt that end with `}` — those manage their own
// trailing whitespace.
func (p *parser) terminateStatement() error {
	t := p.peekRaw()
	switch t.Kind {
	case KindNewline:
		p.pos++
		return nil
	case KindEOF:
		return nil
	case KindRBrace:
		// A closing brace ends a block; the block parser handles consuming
		// it. The last statement before the brace is allowed to omit the
		// trailing NEWLINE.
		return nil
	default:
		return errorAtTok(t, "expected newline or end of statement, got %s", t.Kind)
	}
}

// ---------------------------------------------------------------------------
// Statements.
// ---------------------------------------------------------------------------

func (p *parser) parseStatement() (Stmt, error) {
	t := p.peek()
	switch t.Kind {
	case KindNop:
		p.advance()
		return &NopStmt{Pos: t.Pos}, nil
	case KindPrint:
		return p.parsePrint()
	case KindMut:
		return p.parseDecl(declMut)
	case KindConst:
		return p.parseDecl(declConst)
	case KindFn:
		// v0.7 disambiguation pin: `fn` followed by `(` is ALWAYS an anon-fn
		// expression at statement position (typically an IIFE — `fn() { ... }()`);
		// `fn` followed by IDENT is ALWAYS a fn-decl. The lookahead-1 token
		// after `fn` decides; the two cases never collide.
		if p.peekAfterFnIs(KindLParen) {
			return p.parseExprOrAssignStmt()
		}
		return p.parseFnDecl()
	case KindIf:
		return p.parseIfStmt()
	case KindFor:
		return p.parseForStmt()
	case KindReturn:
		return p.parseReturnStmt()
	case KindBreak:
		return p.parseBreakLikeStmt(true)
	case KindContinue:
		return p.parseBreakLikeStmt(false)
	case KindStruct:
		return p.parseStructDecl()
	case KindEnum:
		return p.parseEnumDecl()
	case KindMatch:
		return p.parseMatchStmt()
	case KindSpec:
		return p.parseSpecDecl()
	case KindImpl:
		return p.parseImplDecl()
	case KindPub:
		return p.parsePubDecl()
	case KindImport:
		// `import` is a top-level-only statement. parseProgram calls
		// parseStatement, so the dispatch is reachable here at the file top
		// level; parseBlock also routes through parseStatement, but
		// parseImport rejects with a precise diagnostic when invoked outside
		// the file's top-level scope. We track the nesting via blockDepth
		// (incremented in parseBlock and impl/spec body parsers) — a non-zero
		// depth means we are inside some block body, which is illegal.
		return p.parseImport()
	case KindSpawn:
		return p.parseSpawnStmt()
	case KindDefer:
		return p.parseDeferStmt()
	case KindSelect:
		return p.parseSelectStmt()
	case KindAsm:
		return p.parseAsmStmt()
	default:
		return p.parseExprOrAssignStmt()
	}
}

// parsePubDecl handles the `pub` visibility modifier on top-level
// declarations. Grammar:
//
//	'pub' ('fn' fn_decl | 'struct' struct_decl | 'enum' enum_decl | 'spec' spec_decl)
//
// Anything else after `pub` is rejected with a focused diagnostic. v0.5
// Unit 1a parses the bit but typeck does not yet consume it; programs
// continue to behave exactly as v0.4. v0.5 Unit 3 wires the bit into
// cross-module visibility gating.
func (p *parser) parsePubDecl() (Stmt, error) {
	pubTok := p.advance() // consume `pub`
	t := p.peek()
	switch t.Kind {
	case KindFn:
		fn, err := p.parseFnDecl()
		if err != nil {
			return nil, err
		}
		fn.(*FnDecl).Pub = true
		return fn, nil
	case KindStruct:
		st, err := p.parseStructDecl()
		if err != nil {
			return nil, err
		}
		st.(*StructDecl).Pub = true
		return st, nil
	case KindEnum:
		en, err := p.parseEnumDecl()
		if err != nil {
			return nil, err
		}
		en.(*EnumDecl).Pub = true
		return en, nil
	case KindSpec:
		sp, err := p.parseSpecDecl()
		if err != nil {
			return nil, err
		}
		sp.(*SpecDecl).Pub = true
		return sp, nil
	case KindLet, KindMut, KindConst, KindImpl:
		// `pub` is decl-level only and applies to the four shapes above —
		// emit a focused diagnostic rather than letting `parseDecl` /
		// `parseImplDecl` handle the keyword (which would lose the `pub`
		// context). `pub impl` is rejected here because the `pub` lives on
		// each inner method's `fn`, not on the impl itself. v0.11 retired
		// the `let` parser shape but kept `let` as a lexer keyword; `pub
		// let` still routes here so the error names the actually-reserved
		// pub targets.
		return nil, errorAt(pubTok.Pos, "pub may only modify fn / struct / enum / spec")
	case KindEOF:
		return nil, &ParseError{
			Pos:        pubTok.Pos,
			Message:    "expected fn / struct / enum / spec after 'pub'",
			Incomplete: true,
		}
	default:
		return nil, errorAt(pubTok.Pos, "expected fn / struct / enum / spec after 'pub'")
	}
}

// parseImport handles the v0.5 `import` statement in its three surface forms:
//
//	'import' STRING_LIT                              — single
//	'import' STRING_LIT 'as' IDENT                   — alias rename
//	'import' '(' (STRING_LIT ['as' IDENT] NEWLINE)* ')'  — grouped form
//
// The grouped form is desugared into one ImportDecl per entry; downstream
// layers (loader, typeck, run, build) only see the flat single-import shape.
//
// `import` is only legal at the file's top level. parseBlock and parseMatchStmt
// bump p.blockDepth before they invoke parseStatement on inner content, so
// reading blockDepth here detects every misplaced import without each block
// shape having to re-run a scoped check.
//
// Parse-time reserved-name rejection (PLAN.md §Resolution rules tenth-man pin):
// the binding name (Path for the bare form, Alias when `as` is written) must
// not collide with any keyword. We cross-check against the same `keywords`
// map the lexer uses, so adding a future keyword automatically tightens this.
func (p *parser) parseImport() (Stmt, error) {
	kw := p.advance() // consume `import`
	if p.blockDepth > 0 {
		return nil, errorAt(kw.Pos, "import is only allowed at the top of a file")
	}

	// Grouped form: `import ( ... )`. Desugar into a synthetic Block-like
	// sequence of single-import statements. The caller (parseProgram) appends
	// what we return to its statement list, so we can't return multiple
	// statements directly — instead we emit a synthetic GroupImport via a
	// helper that loops and adds each ImportDecl to a slice we return as a
	// `*ImportGroupResult` … simpler: rebuild parseProgram's loop to admit
	// many statements back from a single call. The cleanest fix that keeps
	// the existing program loop unchanged is to return the FIRST entry and
	// stash the remaining entries in the parser, to be drained on the next
	// parseProgram iteration. But that complicates state for a one-off.
	//
	// Simplest: parseImport for the grouped form parses every entry inside
	// `(...)`, builds a slice of ImportDecl, and returns a *importGroup
	// shim that parseProgram unpacks. We avoid the shim by keeping a small
	// pending queue on the parser itself.
	if p.peek().Kind == KindLParen {
		return p.parseImportGroup(kw.Pos)
	}

	// Single-import form: `import STRING_LIT [as IDENT]`.
	decl, err := p.parseImportEntry(kw.Pos)
	if err != nil {
		return nil, err
	}
	return decl, nil
}

// parseImportEntry consumes one `STRING_LIT [as IDENT]` import. The leading
// `import` keyword has already been consumed by the caller; declPos is the
// position the resulting ImportDecl should report (the `import` keyword for
// the single form, the entry's own string-literal position for grouped
// entries — caller chooses).
func (p *parser) parseImportEntry(declPos Position) (*ImportDecl, error) {
	pathTok := p.peek()
	if pathTok.Kind != KindString {
		return nil, errorAtTok(pathTok, "expected string literal after 'import', got %s", pathTok.Kind)
	}
	p.advance()

	decl := &ImportDecl{
		Pos:     declPos,
		Path:    pathTok.Value,
		PathPos: pathTok.Pos,
	}

	// Optional `as IDENT` alias. When absent, the binding name is the verbatim
	// path string itself; the reserved-name check below uses Path in that case.
	if p.peek().Kind == KindAs {
		p.advance() // consume `as`
		aliasTok := p.peek()
		if aliasTok.Kind != KindIdent {
			// The user wrote `as <something-not-an-identifier>`. Two prevalent
			// cases: `as` followed by EOF/newline (forgot the alias name), and
			// `as <keyword>` (collides with reserved-name rule). Distinguish
			// them so the diagnostic blames the right thing.
			if isKeywordKind(aliasTok.Kind) {
				return nil, errorAt(aliasTok.Pos, "cannot import as %s: name is reserved", keywordSpelling(aliasTok))
			}
			return nil, errorAtTok(aliasTok, "expected identifier after 'as', got %s", aliasTok.Kind)
		}
		p.advance()
		// Defensive: bare identifiers can't be lexer keywords (the lexer
		// promotes them), but cross-check against the keywords map directly
		// in case Token.Value happens to be a reserved word for any reason.
		if _, isKw := keywords[aliasTok.Value]; isKw {
			return nil, errorAt(aliasTok.Pos, "cannot import as %s: name is reserved", aliasTok.Value)
		}
		decl.Alias = aliasTok.Value
		decl.AliasPos = aliasTok.Pos
	} else {
		// Bare form: the binding name IS the path string. Reject when that
		// string is a reserved keyword — the user has no other handle on the
		// module without an explicit `as`. This matches the §Resolution-rules
		// tenth-man pin: the lexer's keyword set is the single source of truth.
		if _, isKw := keywords[pathTok.Value]; isKw {
			return nil, errorAt(pathTok.Pos, "cannot import as %s: name is reserved", pathTok.Value)
		}
	}
	return decl, nil
}

// parseImportGroup consumes the grouped form `( entry NEWLINE entry ... )`.
// The leading `import` keyword has already been consumed by parseImport; the
// cursor is at `(`. We desugar the group into one ImportDecl per entry and
// stash the trailing entries on the parser so parseProgram drains them on
// subsequent iterations. Each ImportDecl reports its own string-literal
// position as its Pos so diagnostics in later passes anchor on the entry,
// not on the group's `import` keyword.
//
// `import ()` is admitted as a user-friendly noop; it produces zero
// ImportDecls. parseImport returns nil in that case and parseProgram skips
// the empty result.
//
// Entries are separated by NEWLINE. parenDepth-aware peek would normally
// swallow NEWLINEs inside `(...)` — we intentionally do NOT bump parenDepth
// here so the separator stays significant. Comma is rejected with a focused
// diagnostic to nudge users toward the newline-separated form.
func (p *parser) parseImportGroup(importKwPos Position) (Stmt, error) {
	openTok := p.advance() // consume `(`
	_ = openTok

	var entries []*ImportDecl
	for {
		// Drop blank lines and comments-as-newlines between entries. We do
		// NOT use parenDepth here: NEWLINE is the entry separator and must
		// stay visible.
		for p.pos < len(p.tokens) && p.tokens[p.pos].Kind == KindNewline {
			p.pos++
		}
		if p.pos >= len(p.tokens) {
			return nil, &ParseError{
				Pos:        importKwPos,
				Message:    "unterminated import group (missing ')')",
				Incomplete: true,
			}
		}
		t := p.tokens[p.pos]
		if t.Kind == KindRParen {
			p.pos++ // consume `)`
			break
		}
		if t.Kind == KindComma {
			return nil, errorAt(t.Pos, "use newline (not comma) between imports in a group")
		}
		// Parse one entry. The entry's reported Pos is its string-literal
		// position so diagnostics anchor on the entry rather than on the
		// outer `import (` keyword.
		entryPos := t.Pos
		decl, err := p.parseImportEntry(entryPos)
		if err != nil {
			return nil, err
		}
		entries = append(entries, decl)
		// After a successful entry the next significant token must be a
		// newline (entry separator) or `)` (group end). Comma is the common
		// trailing-style mistake; reject it with the same diagnostic shape.
		if p.pos < len(p.tokens) {
			next := p.tokens[p.pos]
			if next.Kind == KindComma {
				return nil, errorAt(next.Pos, "use newline (not comma) between imports in a group")
			}
		}
	}

	if len(entries) == 0 {
		// `import ()` — admitted as a noop that produces zero ImportDecls.
		// Returning nil signals parseProgram to skip the append; the rest
		// of the file's parsing continues as if the `import ()` line wasn't
		// there. Other parseStatement callers never see nil because no other
		// statement shape can produce zero output.
		_ = importKwPos
		return nil, nil
	}
	// Return the first entry directly and stash the remainder on the parser
	// so parseProgram drains them on subsequent iterations. This avoids
	// re-shaping parseStatement to return a slice.
	first := entries[0]
	if len(entries) > 1 {
		p.pendingImports = append(p.pendingImports, entries[1:]...)
	}
	return first, nil
}

// isKeywordKind reports whether a Kind corresponds to a reserved keyword
// admitted by the lexer's keyword table. Used by the reserved-name diagnostic
// in parseImportEntry to detect `as <keyword>`.
func isKeywordKind(k Kind) bool {
	switch k {
	case KindNop, KindPrint, KindAnd, KindBreak, KindConst, KindContinue,
		KindElif, KindElse, KindFalse, KindFn, KindFor, KindIf, KindIn,
		KindLet, KindLoop, KindMut, KindNot, KindOr, KindReturn, KindTrue,
		KindWhile, KindXor, KindStruct, KindEnum, KindMatch, KindSpec,
		KindImpl, KindThis, KindPub, KindImport, KindAs, KindNil,
		KindSpawn, KindDefer:
		return true
	}
	return false
}

// keywordSpelling returns the textual spelling of a keyword token. For most
// kinds the lexer leaves Token.Value empty, so we map back to the keyword
// table by Kind — that table is the single source of truth.
func keywordSpelling(t Token) string {
	if t.Value != "" {
		return t.Value
	}
	for word, kind := range keywords {
		if kind == t.Kind {
			return word
		}
	}
	return t.Kind.String()
}

// parsePrint handles `print expr`. The expression is parsed by the full
// expression parser, so v0.1 programs can `print x + 1` etc.
func (p *parser) parsePrint() (Stmt, error) {
	pt := p.advance() // consume `print`
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &PrintStmt{Pos: pt.Pos, Expr: expr}, nil
}

type declKind int

const (
	declLet declKind = iota
	declMut
	declConst
)

// parseDecl handles the keyword-led mut/const declarations in three shapes
// (the immutable form is keyword-less since v0.11 and is parsed by
// parseBareInferredBinding / parseBareTypedBinding):
//
//   - `mut name := expr` / `const name := expr`
//   - `mut name : T = expr` (annotated)
//   - `mut (a, b, ...) := expr` — tuple-destructure declaration. The
//     parenthesised LHS introduces ≥ 2 fresh names in the current scope; the
//     RHS must be a tuple of matching arity (typeck enforces). The
//     destructure form admits no type annotation — typeck infers from
//     the RHS shape.
func (p *parser) parseDecl(kind declKind) (Stmt, error) {
	keyword := p.advance() // consume mut/const

	// Tuple-destructure LHS: `mut (a, b) := expr`.
	if p.peek().Kind == KindLParen {
		return p.parseTupleDestructureDecl(kind, keyword.Pos)
	}

	nameTok, err := p.expect(KindIdent, "after "+kindLabel(kind))
	if err != nil {
		return nil, err
	}

	var typeRef *TypeRef
	switch p.peek().Kind {
	case KindWalrus:
		// `:=` ⇒ no annotation, infer from RHS.
		p.advance()
	case KindColon:
		p.advance()
		tr, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		typeRef = tr
		if _, err := p.expect(KindAssign, "after type annotation"); err != nil {
			return nil, err
		}
	default:
		t := p.peek()
		return nil, errorAtTok(t, "expected ':=' or ': T =' after %s name, got %s", kindLabel(kind), t.Kind)
	}

	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	switch kind {
	case declLet:
		return &LetStmt{Pos: keyword.Pos, Name: nameTok.Value, Type: typeRef, Value: value}, nil
	case declMut:
		return &MutStmt{Pos: keyword.Pos, Name: nameTok.Value, Type: typeRef, Value: value}, nil
	case declConst:
		return &ConstStmt{Pos: keyword.Pos, Name: nameTok.Value, Type: typeRef, Value: value}, nil
	}
	// Unreachable: declKind exhausts to three values handled above.
	return nil, errorAt(keyword.Pos, "internal error: unknown decl kind")
}

// parseTupleDestructureDecl consumes the parenthesised LHS of a destructure
// declaration plus its `:=` and RHS. For declMut/declConst the leading
// keyword (`mut`/`const`) and its position have already been consumed by the
// caller; for declLet the bare-binding path passes the position of `(`
// directly (since v0.11 there is no leading keyword). The cursor is at `(`.
//
// Grammar: `'(' IDENT (',' IDENT)+ ','? ')' ':=' expr`. PLAN-pinned: ≥ 2
// names — `(a) := …` is a parse error (ParenExpr-grouping is reserved
// for expression position). Repeated names within the same LHS are a
// parse-time error so typeck doesn't have to catch a contradictory binding
// pair. The destructure form admits no type annotation — the parser
// rejects `: T` between `)` and `:=` so we do not have to teach typeck about
// annotated destructures.
func (p *parser) parseTupleDestructureDecl(kind declKind, keywordPos Position) (Stmt, error) {
	openTok, err := p.expectParen(KindLParen, "in destructure "+kindLabel(kind))
	if err != nil {
		return nil, err
	}
	var names []string
	var positions []Position
	seen := map[string]bool{}
	for {
		if p.peek().Kind == KindRParen {
			break
		}
		nameTok, err := p.expect(KindIdent, "in destructure name list")
		if err != nil {
			return nil, err
		}
		if seen[nameTok.Value] {
			return nil, errorAt(nameTok.Pos, "name %q repeated in destructure pattern", nameTok.Value)
		}
		seen[nameTok.Value] = true
		names = append(names, nameTok.Value)
		positions = append(positions, nameTok.Pos)
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRParen, "to close destructure pattern"); err != nil {
		return nil, err
	}
	if len(names) < 2 {
		return nil, errorAt(openTok.Pos, "destructure pattern requires at least 2 names (use the single-name form for one)")
	}
	// `:=` only — annotated destructure remains deferred on the v1.0+
	// reserved list (see LANGUAGE.md §Reserved for v1.0+). The rule is
	// permanent under the current surface, so the diagnostic is unstamped.
	if k := p.peek().Kind; k == KindColon {
		bad := p.peek()
		return nil, errorAt(bad.Pos, "type annotations on destructure declarations are not supported")
	}
	if _, err := p.expect(KindWalrus, "after destructure pattern"); err != nil {
		return nil, err
	}
	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	tb := &TupleBinding{Pos: openTok.Pos, Names: names, NamePos: positions}
	switch kind {
	case declLet:
		return &LetStmt{Pos: keywordPos, Tuple: tb, Value: value}, nil
	case declMut:
		return &MutStmt{Pos: keywordPos, Tuple: tb, Value: value}, nil
	case declConst:
		return &ConstStmt{Pos: keywordPos, Tuple: tb, Value: value}, nil
	}
	return nil, errorAt(keywordPos, "internal error: unknown decl kind")
}

func kindLabel(k declKind) string {
	switch k {
	case declLet:
		// v0.11: bare-binding form has no keyword; use a generic phrase
		// for diagnostics that route through this path (currently only
		// the bare tuple destructure under parseTupleDestructureDecl).
		return "binding"
	case declMut:
		return "mut"
	case declConst:
		return "const"
	}
	return "<decl>"
}

// parseTypeRef parses a type reference. v0.2 admits three shapes:
//
//   - bare identifier ("int", "Point") → TypeRefNamed
//   - "list" '[' type_ref ']'           → TypeRefList
//   - "tuple" '[' type_ref (',' type_ref)+ ','? ']' → TypeRefTuple (≥ 2)
//
// `list` and `tuple` are not reserved keywords (they remain regular
// identifiers so users can still bind names like `list := ...`); they
// trigger compound parsing only when they appear in type-ref position
// followed by `[`.
//
// v0.6 extends the grammar with two postfix shapes:
//
//   - generic type-args: any TypeRefNamed (and the qualified `mod.Type`
//     form) admits a `[arg, arg, ...]` tail that captures the use-site
//     instantiation. `Box[int]`, `Result[int, str]`, `mod.Map[str, int]`.
//   - nullable: a single trailing `?` on any type position desugars to
//     `Option[T]` at typeck. `int?`, `Box[int]?`, `list[int]?`. `T??` is
//     rejected at parse time — the user must spell the second layer
//     explicitly via `Option[T?]` if they really want nested nullability.
func (p *parser) parseTypeRef() (*TypeRef, error) {
	tr, err := p.parseTypeRefHead()
	if err != nil {
		return nil, err
	}
	// v0.6 nullable suffix. A single trailing `?` is admitted on any type
	// position and desugars to Option[T] at typeck. Double `?` is rejected
	// in two shapes: lexed as two `?` tokens (`? ?`) or as one `??` token
	// (the longest-match rule fuses them). Both produce the same diagnostic.
	switch p.peek().Kind {
	case KindQuestion:
		p.advance()
		tr.Nullable = true
		if p.peek().Kind == KindQuestion {
			bad := p.peek()
			return nil, errorAt(bad.Pos, "double-nullable types ('??') are not supported; nest with Option[T?] if intentional")
		}
	case KindCoalesce:
		// `T??` lexed as one token. Anchor the diagnostic at the same
		// position as the two-token form so message text is uniform.
		bad := p.peek()
		return nil, errorAt(bad.Pos, "double-nullable types ('??') are not supported; nest with Option[T?] if intentional")
	}
	return tr, nil
}

// parseTypeRefHead parses a type reference without the trailing `?` suffix.
// Splitting head/tail keeps the nullable check at parseTypeRef tidy and
// shares one path for every shape.
func (p *parser) parseTypeRefHead() (*TypeRef, error) {
	t := p.peek()
	if t.Kind != KindIdent {
		return nil, errorAtTok(t, "expected type name, got %s", t.Kind)
	}
	p.advance()
	// Compound shapes: `list[T]` and `tuple[T1, T2, ...]`. We lookahead one
	// token; if `[` follows the constructor name we parse the brackets,
	// otherwise the bare ident stands alone (lets the user keep `list` as a
	// plain user-defined type if they really want).
	if (t.Value == "list" || t.Value == "tuple") && p.peek().Kind == KindLBracket {
		if t.Value == "list" {
			return p.parseListTypeRef(t.Pos)
		}
		return p.parseTupleTypeRef(t.Pos)
	}
	// v0.5 cross-module type reference: `mod.Color` in a type position.
	// Only matches a strict `IDENT . IDENT` shape — any further dotting
	// (e.g. submodule paths) defers to v0.6+. The leading IDENT is treated
	// as a module binding by typeck; the trailing IDENT is the foreign
	// type name.
	if p.peek().Kind == KindDot {
		// Lookahead: only consume the dot if a bare IDENT follows. This
		// preserves the existing behaviour for names that aren't really
		// followed by a qualified-type tail.
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == KindIdent {
			p.advance() // consume `.`
			nameTok := p.advance() // consume the qualified type IDENT
			tr := &TypeRef{
				Kind:   TypeRefNamed,
				Module: t.Value,
				Name:   nameTok.Value,
				Pos:    t.Pos,
			}
			// v0.6 generic type-args on a qualified name: `mod.Map[str, int]`.
			if p.peek().Kind == KindLBracket {
				args, err := p.parseTypeArgList()
				if err != nil {
					return nil, err
				}
				tr.TypeArgs = args
			}
			return tr, nil
		}
	}
	tr := &TypeRef{Kind: TypeRefNamed, Name: t.Value, Pos: t.Pos}
	// v0.6 generic type-args on a bare name: `Box[int]`, `Result[int, str]`.
	if p.peek().Kind == KindLBracket {
		args, err := p.parseTypeArgList()
		if err != nil {
			return nil, err
		}
		tr.TypeArgs = args
	}
	return tr, nil
}

// parseListTypeRef consumes the `[ T ]` tail of a `list[T]` type reference.
// The opening `[` has been peeked but not yet consumed.
func (p *parser) parseListTypeRef(headPos Position) (*TypeRef, error) {
	if _, err := p.expectParen(KindLBracket, "in 'list' type"); err != nil {
		return nil, err
	}
	elt, err := p.parseTypeRef()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectParen(KindRBracket, "to close 'list' type"); err != nil {
		return nil, err
	}
	return &TypeRef{Kind: TypeRefList, Pos: headPos, Element: elt}, nil
}

// parseTupleTypeRef consumes the `[ T1, T2, ...]` tail of `tuple[...]`.
func (p *parser) parseTupleTypeRef(headPos Position) (*TypeRef, error) {
	if _, err := p.expectParen(KindLBracket, "in 'tuple' type"); err != nil {
		return nil, err
	}
	var elements []*TypeRef
	for {
		// Allow trailing comma by peeking for `]` first.
		if p.peek().Kind == KindRBracket {
			break
		}
		elt, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		elements = append(elements, elt)
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	closeTok, err := p.expectParen(KindRBracket, "to close 'tuple' type")
	if err != nil {
		return nil, err
	}
	if len(elements) < 2 {
		// PLAN: tuple types are ≥ 2 elements. Emit a precise diagnostic at
		// the closing bracket so users see exactly where the shape fails.
		return nil, errorAt(closeTok.Pos, "tuple type requires at least 2 element types, got %d", len(elements))
	}
	return &TypeRef{Kind: TypeRefTuple, Pos: headPos, Elements: elements}, nil
}

// parseFnDecl handles `fn name[type_params](p1: T1, p2: T2) -> R { body }`.
// The optional `[T: Bound]` type-parameter list is a v0.6 addition; pre-v0.6
// programs see the same code path with TypeParams left at its zero value.
func (p *parser) parseFnDecl() (Stmt, error) {
	kw := p.advance() // consume `fn`
	nameTok, err := p.expect(KindIdent, "after 'fn'")
	if err != nil {
		return nil, err
	}
	var typeParams []TypeParam
	if p.peek().Kind == KindLBracket {
		typeParams, err = p.parseTypeParams()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expectParen(KindLParen, "in function signature"); err != nil {
		return nil, err
	}

	var params []FnParam
	if p.peek().Kind != KindRParen {
		for {
			pname, err := p.expect(KindIdent, "in parameter list")
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(KindColon, "after parameter name"); err != nil {
				return nil, err
			}
			ptype, err := p.parseTypeRef()
			if err != nil {
				return nil, err
			}
			params = append(params, FnParam{
				Name: pname.Value,
				Type: ptype,
				Pos:  pname.Pos,
			})
			if p.peek().Kind == KindComma {
				p.advance()
				continue
			}
			break
		}
	}
	if _, err := p.expectParen(KindRParen, "in function signature"); err != nil {
		return nil, err
	}

	var ret *TypeRef
	if p.peek().Kind == KindArrow {
		p.advance()
		tr, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		ret = tr
	}

	// v0.8 Unit 1: a `__builtin <ident>` tail replaces the body. The keyword
	// only lexes when the file declares `# requires: v0.8` or higher; even
	// then it is reserved for embedded `std/` modules — user code referencing
	// it gets a focused diagnostic.
	if p.peek().Kind == KindBuiltin {
		bkw := p.advance()
		if !p.inStdlibFile {
			return nil, errorAt(bkw.Pos, "__builtin reserved for stdlib")
		}
		nameT, err := p.expect(KindIdent, "after '__builtin'")
		if err != nil {
			return nil, err
		}
		// A body block after the marker is rejected — the marker REPLACES
		// the body. Allow either an immediate NEWLINE / EOF or any other
		// statement-terminating token; an LBRACE is the specific ambiguity
		// to catch.
		if p.peek().Kind == KindLBrace {
			return nil, errorAt(p.peek().Pos, "__builtin fn-decl must not have a body block")
		}
		return &FnDecl{
			Pos:            kw.Pos,
			Name:           nameTok.Value,
			TypeParams:     typeParams,
			Params:         params,
			Return:         ret,
			BuiltinName:    nameT.Value,
			BuiltinNamePos: nameT.Pos,
		}, nil
	}

	body, err := p.parseFnBody("function body")
	if err != nil {
		return nil, err
	}
	return &FnDecl{
		Pos:        kw.Pos,
		Name:       nameTok.Value,
		TypeParams: typeParams,
		Params:     params,
		Return:     ret,
		Body:       body,
	}, nil
}

// parseStructDecl handles `struct Name { field: T, ... }`. NEWLINE between
// fields is allowed but not required; `parenDepth` is bumped for the brace
// region so multi-line decls work without the user juggling separators.
func (p *parser) parseStructDecl() (Stmt, error) {
	kw := p.advance() // consume `struct`
	nameTok, err := p.expect(KindIdent, "after 'struct'")
	if err != nil {
		return nil, err
	}
	var typeParams []TypeParam
	if p.peek().Kind == KindLBracket {
		typeParams, err = p.parseTypeParams()
		if err != nil {
			return nil, err
		}
	}
	open, err := p.expect(KindLBrace, "in struct declaration")
	if err != nil {
		return nil, err
	}
	// Treat the brace region like a paren region for NEWLINE skipping so
	// multi-line struct decls Just Work without the user having to align
	// commas at line ends. The open brace has already been consumed; we
	// bump parenDepth manually to mirror what expectParen would do for
	// brackets/parens.
	p.parenDepth++
	defer func() { p.parenDepth-- }()
	_ = open

	var fields []FieldDecl
	for {
		if p.peek().Kind == KindRBrace {
			break
		}
		nameT, err := p.expect(KindIdent, "in struct field list")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(KindColon, "after struct field name"); err != nil {
			return nil, err
		}
		fieldType, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		fields = append(fields, FieldDecl{
			Name: nameT.Value,
			Type: fieldType,
			Pos:  nameT.Pos,
		})
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(KindRBrace, "to close struct declaration"); err != nil {
		return nil, err
	}
	return &StructDecl{Pos: kw.Pos, Name: nameTok.Value, TypeParams: typeParams, Fields: fields}, nil
}

// parseEnumDecl handles `enum Name { Variant, Variant(T1, T2), ... }`. v0.2
// admitted only bare variant identifiers; v0.4 (Unit 2) extends each variant
// to carry an optional `( type_list )` payload.
//
// Bare form (no parens) and payload form (≥ 1 type, no trailing comma) are
// both admitted. An empty `()` is rejected with a focused diagnostic — bare
// variants MUST drop the parentheses entirely.
func (p *parser) parseEnumDecl() (Stmt, error) {
	kw := p.advance() // consume `enum`
	nameTok, err := p.expect(KindIdent, "after 'enum'")
	if err != nil {
		return nil, err
	}
	var typeParams []TypeParam
	if p.peek().Kind == KindLBracket {
		typeParams, err = p.parseTypeParams()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(KindLBrace, "in enum declaration"); err != nil {
		return nil, err
	}
	p.parenDepth++
	defer func() { p.parenDepth-- }()

	var variants []VariantDecl
	for {
		if p.peek().Kind == KindRBrace {
			break
		}
		v, err := p.expect(KindIdent, "in enum variant list")
		if err != nil {
			return nil, err
		}
		variant := VariantDecl{Name: v.Value, Pos: v.Pos}
		if p.peek().Kind == KindLParen {
			payload, err := p.parseVariantPayloadTypes()
			if err != nil {
				return nil, err
			}
			variant.Payload = payload
		}
		variants = append(variants, variant)
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(KindRBrace, "to close enum declaration"); err != nil {
		return nil, err
	}
	return &EnumDecl{Pos: kw.Pos, Name: nameTok.Value, TypeParams: typeParams, Variants: variants}, nil
}

// parseVariantPayloadTypes consumes the `( type ( ',' type )* )` tail of a
// variant declaration. The opening `(` has been peeked but not yet consumed.
//
// Empty parens (`V()`) are rejected: bare variants drop the parentheses
// entirely, so an empty payload is never the user's intent. A trailing comma
// (`V(int,)`) is also rejected — variant payload type lists are pinned to no
// trailing comma.
func (p *parser) parseVariantPayloadTypes() ([]*TypeRef, error) {
	openTok, err := p.expectParen(KindLParen, "in enum variant payload")
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == KindRParen {
		return nil, errorAt(openTok.Pos,
			"empty parentheses are not allowed; use the bare variant name")
	}
	var types []*TypeRef
	for {
		ty, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		types = append(types, ty)
		if p.peek().Kind == KindComma {
			commaTok := p.advance()
			if p.peek().Kind == KindRParen {
				return nil, errorAt(commaTok.Pos,
					"trailing comma not allowed in enum variant payload type list")
			}
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRParen, "to close enum variant payload"); err != nil {
		return nil, err
	}
	return types, nil
}

// parseSpecDecl handles `spec Name { method_decl* }`. A method decl is the
// `fn` form with an optional brace-block body: signature-only (no block)
// declares a method that every impl MUST provide; with-block declares a
// default that an impl may inherit or override.
//
// Empty bodies (`spec Empty {}`) are admitted — useful as marker specs.
// NEWLINEs between methods are absorbed by the brace-region parenDepth bump.
func (p *parser) parseSpecDecl() (Stmt, error) {
	kw := p.advance() // consume `spec`
	nameTok, err := p.expect(KindIdent, "after 'spec'")
	if err != nil {
		return nil, err
	}
	var typeParams []TypeParam
	if p.peek().Kind == KindLBracket {
		typeParams, err = p.parseTypeParams()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(KindLBrace, "in spec declaration"); err != nil {
		return nil, err
	}

	var methods []*SpecMethod
	for {
		p.skipNewlines()
		if p.peekRaw().Kind == KindRBrace {
			break
		}
		m, err := p.parseSpecMethod()
		if err != nil {
			return nil, err
		}
		methods = append(methods, m)
	}
	if _, err := p.expect(KindRBrace, "to close spec declaration"); err != nil {
		return nil, err
	}
	return &SpecDecl{Pos: kw.Pos, Name: nameTok.Value, TypeParams: typeParams, Methods: methods}, nil
}

// parseSpecMethod consumes one `[pub] fn IDENT (params?) (-> type)? block?`
// entry. The block is optional inside a spec — its absence marks the method
// as signature-only (must be implemented by every type that impls the spec).
//
// A leading `pub` records v0.5 visibility on the SpecMethod itself; only
// `fn` may follow `pub` inside a spec body, mirroring the top-level rule.
func (p *parser) parseSpecMethod() (*SpecMethod, error) {
	pub := false
	if p.peek().Kind == KindPub {
		pubTok := p.advance()
		if p.peek().Kind != KindFn {
			return nil, errorAt(pubTok.Pos, "expected fn / struct / enum / spec after 'pub'")
		}
		pub = true
	}
	kw, err := p.expect(KindFn, "in spec method")
	if err != nil {
		return nil, err
	}
	nameTok, err := p.expect(KindIdent, "after 'fn'")
	if err != nil {
		return nil, err
	}
	var typeParams []TypeParam
	if p.peek().Kind == KindLBracket {
		typeParams, err = p.parseTypeParams()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expectParen(KindLParen, "in spec method signature"); err != nil {
		return nil, err
	}
	var params []FnParam
	if p.peek().Kind != KindRParen {
		for {
			pname, err := p.expect(KindIdent, "in parameter list")
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(KindColon, "after parameter name"); err != nil {
				return nil, err
			}
			ptype, err := p.parseTypeRef()
			if err != nil {
				return nil, err
			}
			params = append(params, FnParam{
				Name: pname.Value,
				Type: ptype,
				Pos:  pname.Pos,
			})
			if p.peek().Kind == KindComma {
				p.advance()
				continue
			}
			break
		}
	}
	if _, err := p.expectParen(KindRParen, "in spec method signature"); err != nil {
		return nil, err
	}
	var ret *TypeRef
	if p.peek().Kind == KindArrow {
		p.advance()
		tr, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		ret = tr
	}
	// A `{` here means a default implementation; otherwise the method is
	// signature-only and the next token is either a NEWLINE separator or `}`
	// closing the spec body.
	var body *Block
	if p.peek().Kind == KindLBrace {
		b, err := p.parseFnBody("spec method default body")
		if err != nil {
			return nil, err
		}
		body = b
	}
	return &SpecMethod{
		Pos:        kw.Pos,
		Name:       nameTok.Value,
		TypeParams: typeParams,
		Params:     params,
		Return:     ret,
		Body:       body,
		Pub:        pub,
	}, nil
}

// parseImplDecl handles both inherent and for-spec impl blocks:
//
//   - `impl Type { fn ... fn ... }`               — inherent
//   - `impl Type for Spec { fn ... }`             — for-spec
//   - `impl Type for Spec {}`                     — for-spec, empty body
//
// We reject the bare `for` followed by `{` (missing spec name) with a focused
// diagnostic rather than letting the lower-level "expected identifier" leak.
func (p *parser) parseImplDecl() (Stmt, error) {
	kw := p.advance() // consume `impl`
	// v0.6: `impl[T: Bound] LocalType[T] for SomeSpec` — generic-impl
	// type-parameter list comes immediately after the keyword and before
	// the receiver-type identifier. Pre-v0.6 programs see no `[` here and
	// fall straight through with implTypeParams left at its zero value.
	var implTypeParams []TypeParam
	var err error
	if p.peek().Kind == KindLBracket {
		implTypeParams, err = p.parseTypeParams()
		if err != nil {
			return nil, err
		}
	}
	typeNameTok, err := p.expect(KindIdent, "after 'impl'")
	if err != nil {
		return nil, err
	}
	// v0.5: `impl mod.Type [...]` — module-qualified receiver type.
	typeModule := ""
	typeName := typeNameTok.Value
	if p.peek().Kind == KindDot {
		// Lookahead for IDENT — only consume the dot if a name follows,
		// matching parseTypeRef's policy.
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == KindIdent {
			p.advance() // dot
			qual := p.advance()
			typeModule = typeNameTok.Value
			typeName = qual.Value
		}
	}
	// v0.6: `impl Box[int] for ...` / `impl[T] Box[T] for ...` —
	// receiver-type generic type-args are recorded on ImplDecl.TypeArgs.
	var typeArgs []*TypeRef
	if p.peek().Kind == KindLBracket {
		typeArgs, err = p.parseTypeArgList()
		if err != nil {
			return nil, err
		}
	}
	specName := ""
	specModule := ""
	if p.peek().Kind == KindFor {
		forTok := p.advance()
		st := p.peek()
		if st.Kind != KindIdent {
			return nil, errorAt(forTok.Pos, "expected spec name after 'for', got %s", st.Kind)
		}
		p.advance()
		specName = st.Value
		// v0.5: `impl T for mod.Spec` — module-qualified spec name.
		if p.peek().Kind == KindDot {
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == KindIdent {
				p.advance() // dot
				qual := p.advance()
				specModule = specName
				specName = qual.Value
			}
		}
	}
	if _, err := p.expect(KindLBrace, "in impl declaration"); err != nil {
		return nil, err
	}

	var methods []*FnDecl
	for {
		p.skipNewlines()
		if p.peekRaw().Kind == KindRBrace {
			break
		}
		// Methods inside an impl reuse the FnDecl shape unchanged. Typeck
		// (Unit 3) routes them to the impl rather than to the global fn
		// table, so the parser doesn't need a separate node type.
		//
		// v0.5: a leading `pub` sets the visibility bit on the inner FnDecl.
		// `pub` on an impl method is the only way to mark an impl member
		// public — the impl block itself does not carry a `pub` (the PLAN
		// pins decl-level visibility on `fn` / `struct` / `enum` / `spec`).
		pub := false
		if p.peek().Kind == KindPub {
			pubTok := p.advance()
			if p.peek().Kind != KindFn {
				return nil, errorAt(pubTok.Pos, "expected fn / struct / enum / spec after 'pub'")
			}
			pub = true
		}
		stmt, err := p.parseFnDecl()
		if err != nil {
			return nil, err
		}
		fn, ok := stmt.(*FnDecl)
		if !ok {
			return nil, errorAt(stmt.StmtPos(), "internal: parseFnDecl produced %T", stmt)
		}
		fn.Pub = pub
		methods = append(methods, fn)
	}
	if _, err := p.expect(KindRBrace, "to close impl declaration"); err != nil {
		return nil, err
	}
	return &ImplDecl{
		Pos:        kw.Pos,
		Type:       typeName,
		TypeModule: typeModule,
		TypeArgs:   typeArgs,
		TypeParams: implTypeParams,
		Spec:       specName,
		SpecModule: specModule,
		Methods:    methods,
	}, nil
}

// parseMatchStmt handles `match expr { arm; arm; ... }`. Each arm is
// `pattern [if guard] => block-or-single-statement`. Arm separator is
// NEWLINE — we lift parenDepth across the brace region so newlines inside
// pattern parens (e.g. multi-line tuple patterns) stay transparent, then
// drop back to NEWLINE-significant mode at the arm-separator level by
// peeking at the raw stream when we need to.
func (p *parser) parseMatchStmt() (Stmt, error) {
	kw := p.advance() // consume `match`
	subject, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(KindLBrace, "in match"); err != nil {
		return nil, err
	}
	// match-arm bodies are inside a block: bump blockDepth so any v0.5
	// `import` that lands in single-statement arm position is rejected by
	// parseImport with the top-level-only diagnostic. parseBlock bumps the
	// depth itself for brace-bodies; the single-statement form doesn't go
	// through parseBlock.
	p.blockDepth++
	defer func() { p.blockDepth-- }()

	stmt := &MatchStmt{Pos: kw.Pos, Subject: subject}
	for {
		// Skip blank lines between arms.
		p.skipNewlines()
		if p.peekRaw().Kind == KindRBrace {
			p.pos++ // consume `}`
			return stmt, nil
		}
		if p.peekRaw().Kind == KindEOF {
			return nil, &ParseError{
				Pos:        kw.Pos,
				Message:    "unterminated match (missing '}')",
				Incomplete: true,
			}
		}
		arm, err := p.parseMatchArm()
		if err != nil {
			return nil, err
		}
		stmt.Arms = append(stmt.Arms, arm)
		// Arm separator: NEWLINE or `}`. The `}` case loops back to detect
		// and consume above.
		switch p.peekRaw().Kind {
		case KindNewline:
			p.pos++
		case KindRBrace, KindEOF:
			// Loop iteration handles both cases.
		default:
			t := p.peekRaw()
			return nil, errorAtTok(t, "expected newline or '}' between match arms, got %s", t.Kind)
		}
	}
}

// parseMatchArm reads one arm: `pattern [if guard] => body`. If body is a
// brace block we consume it directly; if it's a single statement we wrap it
// in a one-element Block so downstream consumers always see a Block.
func (p *parser) parseMatchArm() (MatchArm, error) {
	armPos := p.peek().Pos
	pat, err := p.parsePattern()
	if err != nil {
		return MatchArm{}, err
	}
	var guard Expr
	if p.peek().Kind == KindIf {
		p.advance()
		g, err := p.parseExpr()
		if err != nil {
			return MatchArm{}, err
		}
		guard = g
	}
	if _, err := p.expect(KindFatArrow, "in match arm"); err != nil {
		return MatchArm{}, err
	}
	// Body shape: block or single statement.
	if p.peek().Kind == KindLBrace {
		body, err := p.parseBlock("match arm body")
		if err != nil {
			return MatchArm{}, err
		}
		return MatchArm{Pos: armPos, Pattern: pat, Guard: guard, Body: body}, nil
	}
	// Single-statement body. Build a synthetic one-element Block so callers
	// don't have to special-case the shape. The synthetic block carries the
	// single statement's own position so diagnostics stay precise.
	stmtPos := p.peek().Pos
	stmt, err := p.parseStatement()
	if err != nil {
		return MatchArm{}, err
	}
	body := &Block{Pos: stmtPos, Statements: []Stmt{stmt}}
	return MatchArm{Pos: armPos, Pattern: pat, Guard: guard, Body: body}, nil
}

// parseIfStmt handles `if … {} [elif … {}]* [else {}]`.
func (p *parser) parseIfStmt() (Stmt, error) {
	kw := p.advance() // consume `if`
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	thenBlk, err := p.parseBlock("'if' body")
	if err != nil {
		return nil, err
	}
	stmt := &IfStmt{Pos: kw.Pos, Cond: cond, Then: thenBlk}

	for p.peek().Kind == KindElif {
		elif := p.advance()
		ec, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		body, err := p.parseBlock("'elif' body")
		if err != nil {
			return nil, err
		}
		stmt.Elifs = append(stmt.Elifs, ElifClause{Pos: elif.Pos, Cond: ec, Body: body})
	}
	if p.peek().Kind == KindElse {
		p.advance()
		body, err := p.parseBlock("'else' body")
		if err != nil {
			return nil, err
		}
		stmt.Else = body
	}
	return stmt, nil
}

// parseForStmt handles all four for-loop shapes.
func (p *parser) parseForStmt() (Stmt, error) {
	kw := p.advance() // consume `for`

	// `for { ... }` — infinite.
	if p.peek().Kind == KindLBrace {
		body, err := p.parseBlock("'for' body")
		if err != nil {
			return nil, err
		}
		return &ForStmt{Pos: kw.Pos, Kind: ForInfinite, Body: body}, nil
	}

	// Disambiguate `for x in ...` from `for cond { ... }` by trying the
	// `IDENT in` head first. This is a LL(2) check, which the grammar can
	// absorb without a backtrack. Once we see `IDENT in`, the head expr is
	// either a range (`expr..expr` / `expr..=expr`) or a list-typed expr;
	// parseForInHeader picks the right shape after parsing the start expr.
	if p.peek().Kind == KindIdent {
		// Look two ahead via the raw tokens. We can't use peek alone because
		// peek is only one-token lookahead; we want to know if the next
		// non-newline token after the ident is `in`.
		if k := p.peekKindAfterIdent(); k == KindIn {
			return p.parseForInHeader(kw.Pos)
		}
	}

	// `for cond { ... }`.
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock("'for' body")
	if err != nil {
		return nil, err
	}
	return &ForStmt{Pos: kw.Pos, Kind: ForCond, Cond: cond, Body: body}, nil
}

// peekKindAfterIdent peeks at the kind of the token after a leading ident,
// skipping any NEWLINEs (we do this transparently for parens elsewhere; the
// `for` head is a single line in practice but we stay tolerant).
func (p *parser) peekKindAfterIdent() Kind {
	i := p.pos + 1
	for i < len(p.tokens) && p.tokens[i].Kind == KindNewline {
		i++
	}
	if i >= len(p.tokens) {
		return KindEOF
	}
	return p.tokens[i].Kind
}

// parseForInHeader handles both v0.1's range form and v0.2's list-iter form:
//
//   - `for IDENT in EXPR..EXPR { ... }` / `for IDENT in EXPR..=EXPR { ... }`
//     — range iteration. RangeExprs are produced ONLY here — the general
//     expression parser refuses to construct them.
//   - `for IDENT in EXPR { ... }` — iterate over a list-typed expression,
//     binding IDENT to a deep copy of each element on each iteration.
//
// We parse the head expression with parseOr (not parseExpr) so the
// trailing-range guard inside parseExpr doesn't fire on the `..` / `..=` we
// might be about to consume. After the start expression we peek: if `..` or
// `..=` follows we're in the range form; otherwise we treat the expression
// as the iterable. typeck owns the "iterable must be a list" rule — the
// parser stays liberal so future iterables (str, etc.) need only a typeck
// extension.
func (p *parser) parseForInHeader(forPos Position) (Stmt, error) {
	nameTok, _ := p.expect(KindIdent, "in 'for' header") // already known ident
	if _, err := p.expect(KindIn, "in 'for' header"); err != nil {
		return nil, err
	}
	// parseOr (not parseExpr) so the trailing-range guard in parseExpr
	// doesn't fire on the `..` / `..=` we may be about to consume.
	headExpr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	switch p.peek().Kind {
	case KindRange, KindRangeEq:
		rangeTok := p.advance()
		inclusive := rangeTok.Kind == KindRangeEq
		end, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		body, err := p.parseBlock("'for' body")
		if err != nil {
			return nil, err
		}
		return &ForStmt{
			Pos:    forPos,
			Kind:   ForRange,
			Var:    nameTok.Value,
			VarPos: nameTok.Pos,
			Range: &RangeExpr{
				Pos:       rangeTok.Pos,
				Start:     headExpr,
				End:       end,
				Inclusive: inclusive,
			},
			Body: body,
		}, nil
	}
	// No range follows: list-iter form.
	body, err := p.parseBlock("'for' body")
	if err != nil {
		return nil, err
	}
	return &ForStmt{
		Pos:    forPos,
		Kind:   ForIter,
		Var:    nameTok.Value,
		VarPos: nameTok.Pos,
		Iter:   headExpr,
		Body:   body,
	}, nil
}

// parseReturnStmt handles `return [expr] [if cond]`.
func (p *parser) parseReturnStmt() (Stmt, error) {
	kw := p.advance() // consume `return`
	stmt := &ReturnStmt{Pos: kw.Pos}

	// Bare `return` (with optional guard) ⇒ Value is nil.
	switch p.peekRaw().Kind {
	case KindNewline, KindEOF, KindRBrace:
		return stmt, nil
	case KindIf:
		// `return if cond` ⇒ no value.
		p.advance()
		guard, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Guard = guard
		return stmt, nil
	}

	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	stmt.Value = val
	if p.peek().Kind == KindIf {
		p.advance()
		guard, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Guard = guard
	}
	return stmt, nil
}

// parseBreakLikeStmt handles `break [if cond]` / `continue [if cond]`. The
// boolean `isBreak` selects which AST node to return — the parsing logic is
// identical, so factoring keeps both grammars on one path.
func (p *parser) parseBreakLikeStmt(isBreak bool) (Stmt, error) {
	kw := p.advance() // consume `break` / `continue`
	var guard Expr
	if p.peek().Kind == KindIf {
		p.advance()
		g, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		guard = g
	}
	if isBreak {
		return &BreakStmt{Pos: kw.Pos, Guard: guard}, nil
	}
	return &ContinueStmt{Pos: kw.Pos, Guard: guard}, nil
}

// parseExprOrAssignStmt handles expression statements (function calls only at
// v0.1) and assignment statements. We parse the LHS as a full expression and
// then look for an assignment operator.
//
// v0.11: the keyword-led `let` was retired. The bare binding shapes
//
//	IDENT ':=' expr                          → inferred immutable
//	IDENT ':' type '=' expr                  → annotated immutable
//	'(' IDENT (',' IDENT)+ ','? ')' ':=' expr → tuple destructure
//
// are detected at statement start before parseExpr would otherwise consume the
// LHS as an expression. `mut` and `const` keep their keyword-led forms; `let`
// is now a regular identifier.
func (p *parser) parseExprOrAssignStmt() (Stmt, error) {
	startTok := p.peek()
	if startTok.Kind == KindIdent {
		switch p.peekRawAt(p.pos + 1) {
		case KindWalrus:
			return p.parseBareInferredBinding(startTok)
		case KindColon:
			return p.parseBareTypedBinding(startTok)
		}
	}
	if startTok.Kind == KindLParen && p.detectsBareTupleBinding() {
		return p.parseTupleDestructureDecl(declLet, startTok.Pos)
	}
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	// v0.7 send statement: `chan_expr <- value_expr`. The detector runs before
	// the assignment check because `<-` is not an assignment operator and
	// `expr <- value` only makes sense as a statement (a send produces no
	// value). Chained sends (`a <- b <- c`) reject at parse time per PLAN.md.
	if p.peek().Kind == KindLArrow {
		return p.parseSendStmt(expr)
	}

	// v0.11 focused diagnostics for almost-binding shapes. These run BEFORE
	// the assign-op check so `(a, b) = pair` does not get caught by the
	// generic "left-hand side of assignment must be an identifier or list[i]"
	// message. The bare-binding sniff at the top of this function already
	// captured the well-formed shapes; whatever falls through here is one of
	// the misuses listed in each branch's diagnostic.
	switch lhs := expr.(type) {
	case *ParenExpr:
		if p.peek().Kind == KindWalrus {
			return nil, errorAt(lhs.Pos, "destructure pattern requires at least 2 names (use the single-name form for one)")
		}
	case *TupleLit:
		switch p.peek().Kind {
		case KindAssign:
			return nil, errorAt(p.peek().Pos, "expected ':=' after destructure pattern")
		case KindColon:
			return nil, errorAt(p.peek().Pos, "type annotations on destructure declarations are not supported")
		}
	case *IdentExpr:
		if k := p.peek().Kind; k == KindNewline || k == KindEOF {
			return nil, errorAt(lhs.Pos, "expected ':=' or ': T =' after %q to bind, or '(' to call", lhs.Name)
		}
	}

	// Check for an assignment operator.
	if op, ok := assignOpFor(p.peek().Kind); ok {
		opTok := p.advance()
		// v0.3 admits two LHS shapes: a bare identifier (the v0.1 form) and a
		// single-level list index `xs[i]` (the v0.3 list-mutation surface).
		// Compound operators stay identifier-only at v0.3 — `xs[i] += 1` is
		// sugar that belongs to a later unit and is rejected here so the user
		// sees a focused diagnostic instead of an opaque downstream type
		// error.
		switch lhs := expr.(type) {
		case *IdentExpr:
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return &AssignStmt{
				Pos:    lhs.Pos,
				Target: lhs,
				Op:     op,
				Value:  val,
			}, nil
		case *IndexExpr:
			if op != AssignSet {
				// Permanent under the current surface — see LANGUAGE.md
				// §Reserved for v1.0+ "Compound assignment to list elements".
				return nil, errorAt(opTok.Pos, "compound assignment '%s' to a list element is not supported — use `xs[i] = ...` instead", op)
			}
			// We admit only single-level indexing (`xs[i] = v`). Chained
			// indexing (`xs[i][j] = v`) parses fine as a postfix chain, but
			// the borrow / mutation rules for nested mutation aren't in
			// scope, so reject early with a precise message.
			if _, nested := lhs.Receiver.(*IndexExpr); nested {
				return nil, errorAt(opTok.Pos, "left-hand side of assignment must be an identifier or list[i] (chained indexing is not supported)")
			}
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return &AssignStmt{
				Pos:    lhs.Pos,
				Target: lhs,
				Op:     op,
				Value:  val,
			}, nil
		default:
			return nil, errorAt(opTok.Pos, "left-hand side of assignment must be an identifier or list[i]")
		}
	}

	// Plain expression statement: only a CallExpr is meaningful at v0.1.
	// v0.4 broadens this to also accept a MethodCallExpr so `c.method()` is
	// a valid statement (it has side effects via the impl method body). v0.7
	// adds the `<- ch` receive-discard form (a bare RecvExpr) — a receive has
	// the side effect of advancing the channel even when the value is unused.
	switch expr.(type) {
	case *CallExpr, *MethodCallExpr, *RecvExpr:
		return &ExprStmt{Pos: startTok.Pos, Expr: expr}, nil
	}
	return nil, errorAt(startTok.Pos, "expression statements must be function calls")
}

// peekRawAt returns the kind of the token at absolute index i without
// consuming or advancing the cursor and without skipping NEWLINEs. Used by
// the v0.11 bare-binding sniff which fires at statement position where
// parenDepth is always 0; if a future caller needs paren-aware lookahead it
// should walk the slice with the parenDepth-skipping rules of peek().
func (p *parser) peekRawAt(i int) Kind {
	if i < 0 || i >= len(p.tokens) {
		return KindEOF
	}
	return p.tokens[i].Kind
}

// detectsBareTupleBinding reports whether the cursor is sitting at the start
// of a v0.11 bare tuple destructure binding:
//
//	'(' IDENT (',' IDENT)+ ','? ')' ':='
//
// Walks the token stream without consuming. Returns false for any shape that
// does not match — `(a)` (single-element parens), `(a + b)` (expression in
// parens), `(a, b)` followed by anything other than `:=`, etc. — letting
// parseExpr handle them as ordinary parenthesised / tuple-literal forms.
func (p *parser) detectsBareTupleBinding() bool {
	i := p.pos + 1
	if i >= len(p.tokens) || p.tokens[i].Kind != KindIdent {
		return false
	}
	i++
	if i >= len(p.tokens) || p.tokens[i].Kind != KindComma {
		return false
	}
	for i < len(p.tokens) {
		if p.tokens[i].Kind != KindComma {
			break
		}
		i++
		if i < len(p.tokens) && p.tokens[i].Kind == KindRParen {
			break
		}
		if i >= len(p.tokens) || p.tokens[i].Kind != KindIdent {
			return false
		}
		i++
	}
	if i >= len(p.tokens) || p.tokens[i].Kind != KindRParen {
		return false
	}
	i++
	return i < len(p.tokens) && p.tokens[i].Kind == KindWalrus
}

// parseBareInferredBinding handles `IDENT ':=' expr`. The cursor is at the
// IDENT; consumes through the RHS expression. Mirrors the no-annotation
// branch of parseDecl(declLet) without the keyword consumption.
func (p *parser) parseBareInferredBinding(idTok Token) (Stmt, error) {
	p.advance()
	p.advance()
	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &LetStmt{Pos: idTok.Pos, Name: idTok.Value, Value: value}, nil
}

// parseBareTypedBinding handles `IDENT ':' type '=' expr`. The cursor is at
// the IDENT; consumes everything through the RHS expression.
func (p *parser) parseBareTypedBinding(idTok Token) (Stmt, error) {
	p.advance()
	p.advance()
	tr, err := p.parseTypeRef()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(KindAssign, "after type annotation"); err != nil {
		return nil, err
	}
	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &LetStmt{Pos: idTok.Pos, Name: idTok.Value, Type: tr, Value: value}, nil
}

// assignOpFor maps a token Kind to the matching AssignOp value, if any.
func assignOpFor(k Kind) (AssignOp, bool) {
	switch k {
	case KindAssign:
		return AssignSet, true
	case KindPlusEq:
		return AssignAdd, true
	case KindMinusEq:
		return AssignSub, true
	case KindStarEq:
		return AssignMul, true
	case KindSlashEq:
		return AssignDiv, true
	case KindPctEq:
		return AssignMod, true
	case KindAmpEq:
		return AssignAnd, true
	case KindPipeEq:
		return AssignOr, true
	case KindCaretEq:
		return AssignXor, true
	case KindShlEq:
		return AssignShl, true
	case KindShrEq:
		return AssignShr, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Blocks.
// ---------------------------------------------------------------------------

// parseFnBody is parseBlock specialised for fn bodies (top-level fn, impl
// method, spec method default body, anon-fn). It records the body's block
// depth on p.fnBodyDepths so v0.7 `defer` can detect "we are at the
// immediate fn-body level". The push happens before parseBlock bumps
// blockDepth; the recorded value matches what blockDepth will be inside the
// body. Pop is paired in defer so an error path leaves the stack honest.
func (p *parser) parseFnBody(ctx string) (*Block, error) {
	p.fnBodyDepths = append(p.fnBodyDepths, p.blockDepth+1)
	defer func() { p.fnBodyDepths = p.fnBodyDepths[:len(p.fnBodyDepths)-1] }()
	return p.parseBlock(ctx)
}

// parseBlock consumes `{ statements }`. The optional `ctx` is a phrase
// inserted into error messages ("'if' body", "function body").
func (p *parser) parseBlock(ctx string) (*Block, error) {
	open, err := p.expect(KindLBrace, "for "+ctx)
	if err != nil {
		return nil, err
	}
	// Track lexical block nesting so parseImport can refuse imports that are
	// not at the file's top level. The bump/decrement pair stays balanced
	// across error returns via defer.
	p.blockDepth++
	defer func() { p.blockDepth-- }()
	blk := &Block{Pos: open.Pos}
	for {
		// Skip newlines between statements inside the block.
		p.skipNewlines()
		if p.peekRaw().Kind == KindRBrace {
			p.pos++ // consume `}`
			return blk, nil
		}
		if p.peekRaw().Kind == KindEOF {
			// Flagged Incomplete: the user has opened a block but not
			// closed it; the REPL keeps reading until `}` arrives.
			return nil, &ParseError{
				Pos:        open.Pos,
				Message:    "unterminated block (missing '}')",
				Incomplete: true,
			}
		}
		stmtLine := p.peekRaw().Pos.Line
		leading := p.drainLeadingComments(stmtLine)
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		setLeadingComments(stmt, leading)
		blk.Statements = append(blk.Statements, stmt)
		if err := p.terminateStatement(); err != nil {
			return nil, err
		}
	}
}

// expectParen consumes a `(` `)` `[` `]` and updates parenDepth so that
// NEWLINE tokens inside the bracketed region are skipped transparently.
func (p *parser) expectParen(k Kind, ctx string) (Token, error) {
	t, err := p.expect(k, ctx)
	if err != nil {
		return t, err
	}
	switch k {
	case KindLParen, KindLBracket:
		p.parenDepth++
	case KindRParen, KindRBracket:
		p.parenDepth--
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// Expressions: precedence climbing.
//
// The grammar table lives in PLAN.md; the Pratt-style structure here mirrors
// it level-for-level. Levels with non-associative operators (comparison)
// implement non-associativity by refusing to recurse on the same level after
// a successful parse — the second compare-op produces a clear diagnostic.
// ---------------------------------------------------------------------------

// parseExpr is the public entry into the expression grammar. It dispatches
// to parseCoalesce (the lowest-precedence rung at v0.6 — `??` sits below
// `or`) and then guards against `..` / `..=` appearing at the trailing edge
// of an otherwise complete expression — at v0.1 ranges are restricted to
// the head of `for x in ...` and any other appearance gets the dedicated
// diagnostic instead of a generic "expected newline" message.
//
// parseForRange bypasses this guard by calling parseOr directly so it can
// itself consume the `..` / `..=` token. Pre-v0.6 callers that wanted
// "everything except `??`" continue to call parseOr directly.
func (p *parser) parseExpr() (Expr, error) {
	expr, err := p.parseCoalesce()
	if err != nil {
		return nil, err
	}
	if k := p.peek().Kind; k == KindRange || k == KindRangeEq {
		bad := p.peek()
		return nil, errorAt(bad.Pos, "range expressions are only allowed in for-in heads")
	}
	return expr, nil
}

// parseCoalesce is the v0.6 nil-coalesce rung — `??`. Right-associative and
// the lowest-precedence operator in the grammar (sits below `or`). The body
// here is the only call site for the CoalesceExpr node; every other rung
// dispatches downward via parseOr.
func (p *parser) parseCoalesce() (Expr, error) {
	left, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == KindCoalesce {
		opTok := p.advance()
		// Reject the common malformed shape `lhs ??.field` early — the user
		// almost certainly meant `lhs?.field` (safe nav) and the generic
		// "expected expression" error from parseAtom would not point at the
		// fix. Other bad shapes fall through to the recursive call's own
		// diagnostic.
		if p.peek().Kind == KindDot {
			return nil, errorAt(p.peek().Pos, "'??' must be followed by an expression; did you mean '?.' for safe navigation?")
		}
		// Right-assoc: recurse into parseCoalesce so `a ?? b ?? c` parses
		// as `a ?? (b ?? c)`. Per PLAN.md §Null-safety semantics, the
		// fold-from-the-right shape mirrors Option/Result chaining and
		// matches reader expectations from other languages.
		right, err := p.parseCoalesce()
		if err != nil {
			return nil, err
		}
		return &CoalesceExpr{Pos: opTok.Pos, Left: left, Right: right}, nil
	}
	return left, nil
}

// Level 1: or, xor (left-assoc).
func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.peek().Kind {
		case KindOr:
			op = BinOr
		case KindXor:
			op = BinXor
		default:
			return left, nil
		}
		opTok := p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: op, Left: left, Right: right}
	}
}

// Level 2: and (left-assoc).
func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == KindAnd {
		opTok := p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: BinAnd, Left: left, Right: right}
	}
	return left, nil
}

// Level 3: not (prefix, right-assoc). Comparison binds tighter (PLAN level 4),
// so `not a == b` parses as `not (a == b)` — the recursive call to parseNot
// drops into parseComparison once the prefix runs out, giving the comparison
// the chance to grab `a == b` before `not` wraps the whole thing.
func (p *parser) parseNot() (Expr, error) {
	if p.peek().Kind == KindNot {
		opTok := p.advance()
		operand, err := p.parseNot() // allow stacking: `not not x`
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Pos: opTok.Pos, Op: UnaryNot, Operand: operand}, nil
	}
	return p.parseComparison()
}

// Level 4: comparison ==, !=, <, >, <=, >= (NON-associative).
func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseBitOr()
	if err != nil {
		return nil, err
	}
	op, ok := comparisonOpFor(p.peek().Kind)
	if !ok {
		return left, nil
	}
	opTok := p.advance()
	right, err := p.parseBitOr()
	if err != nil {
		return nil, err
	}
	// Non-associativity: a chained comparison is rejected with a precise
	// error pointing at the second operator.
	if next, ok2 := comparisonOpFor(p.peek().Kind); ok2 {
		bad := p.peek()
		return nil, errorAt(bad.Pos, "comparison operators are non-associative; '%s ... %s' must be parenthesised or split", op, next)
	}
	return &BinaryExpr{Pos: opTok.Pos, Op: op, Left: left, Right: right}, nil
}

func comparisonOpFor(k Kind) (BinaryOp, bool) {
	switch k {
	case KindEq:
		return BinEq, true
	case KindNE:
		return BinNE, true
	case KindLT:
		return BinLT, true
	case KindGT:
		return BinGT, true
	case KindLE:
		return BinLE, true
	case KindGE:
		return BinGE, true
	}
	return 0, false
}

// Level 5: bitwise OR (left-assoc).
func (p *parser) parseBitOr() (Expr, error) {
	left, err := p.parseBitXor()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == KindPipe {
		opTok := p.advance()
		right, err := p.parseBitXor()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: BinBitOr, Left: left, Right: right}
	}
	return left, nil
}

// Level 6: bitwise XOR (left-assoc).
func (p *parser) parseBitXor() (Expr, error) {
	left, err := p.parseBitAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == KindCaret {
		opTok := p.advance()
		right, err := p.parseBitAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: BinBitXor, Left: left, Right: right}
	}
	return left, nil
}

// Level 7: bitwise AND (left-assoc).
func (p *parser) parseBitAnd() (Expr, error) {
	left, err := p.parseShift()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == KindAmp {
		opTok := p.advance()
		right, err := p.parseShift()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: BinBitAnd, Left: left, Right: right}
	}
	return left, nil
}

// Level 8: shifts <<, >> (left-assoc).
func (p *parser) parseShift() (Expr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.peek().Kind {
		case KindShl:
			op = BinShl
		case KindShr:
			op = BinShr
		default:
			return left, nil
		}
		opTok := p.advance()
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: op, Left: left, Right: right}
	}
}

// Level 9: additive +, - (left-assoc).
func (p *parser) parseAdd() (Expr, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.peek().Kind {
		case KindPlus:
			op = BinAdd
		case KindMinus:
			op = BinSub
		default:
			return left, nil
		}
		opTok := p.advance()
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: op, Left: left, Right: right}
	}
}

// Level 10: multiplicative *, /, //, % (left-assoc).
func (p *parser) parseMul() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.peek().Kind {
		case KindStar:
			op = BinMul
		case KindSlash:
			op = BinDiv
		case KindFloorDiv:
			op = BinFloorDiv
		case KindPercent:
			op = BinMod
		default:
			return left, nil
		}
		opTok := p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: op, Left: left, Right: right}
	}
}

// Level 11: unary -, ~, <- (right-assoc). v0.7 adds prefix `<-` for channel
// receive, which sits at the same precedence rung as the other prefix unaries.
// Chained receives (`<- <- ch`) parse naturally via the recursive call into
// parseUnary; typeck rejects them because the inner receive yields `T?`,
// which is not itself a chan.
func (p *parser) parseUnary() (Expr, error) {
	switch p.peek().Kind {
	case KindMinus:
		opTok := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Pos: opTok.Pos, Op: UnaryNeg, Operand: operand}, nil
	case KindTilde:
		opTok := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Pos: opTok.Pos, Op: UnaryBitNot, Operand: operand}, nil
	case KindLArrow:
		opTok := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &RecvExpr{Pos: opTok.Pos, Chan: operand}, nil
	}
	return p.parsePostfix()
}

// Level 12: postfix call / index / slice / field access (left-assoc). The
// chain is built left-to-right so `xs[0].field(arg)` walks
// CallExpr ← FieldAccessExpr ← IndexExpr ← Ident.
func (p *parser) parsePostfix() (Expr, error) {
	expr, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().Kind {
		case KindLParen:
			// v0.6: `nil(...)` is meaningless — `nil` is a literal, not a
			// callable. Reject at parse time so the diagnostic blames the
			// shape, not the eventual "type 'Option' is not callable" form.
			if _, isNil := expr.(*NilLit); isNil {
				return nil, errorAt(p.peek().Pos, "cannot call 'nil'; nil is the absence-of-value literal, not a function")
			}
			callPos := p.peek().Pos
			if _, err := p.expectParen(KindLParen, "in call"); err != nil {
				return nil, err
			}
			var args []Expr
			if p.peek().Kind != KindRParen {
				for {
					a, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.peek().Kind == KindComma {
						p.advance()
						continue
					}
					break
				}
			}
			if _, err := p.expectParen(KindRParen, "in call"); err != nil {
				return nil, err
			}
			expr = &CallExpr{Pos: callPos, Callee: expr, Args: args}
		case KindLBracket:
			next, err := p.parseIndexOrSlice(expr)
			if err != nil {
				return nil, err
			}
			expr = next
		case KindQuestion:
			// v0.6 propagation: `expr?` — postfix on an Option/Result-typed
			// expression. Typeck (Unit 4) lowers to a match-and-early-return.
			// `nil?` (calling propagation on a bare nil literal) is admitted
			// at parse time because the diagnostic belongs at typeck where
			// the surrounding return-type can be inspected; the parser only
			// records the shape.
			qTok := p.advance()
			expr = &PropagateExpr{Pos: qTok.Pos, Inner: expr}
		case KindSafeDot:
			// v0.6 safe-navigation: `expr?.field`. Routed through the same
			// FieldAccessExpr node as `.` with the Safe bit set; the only
			// shape difference is the nullable lowering at typeck.
			safeTok := p.advance()
			nameTok, err := p.expect(KindIdent, "after '?.'")
			if err != nil {
				return nil, err
			}
			if p.peek().Kind == KindLParen {
				return nil, errorAt(p.peek().Pos, "method-form safe navigation ('?.method(...)') is not supported — use a bare binding `x := <recv>?.field`, then call the method on x")
			}
			expr = &FieldAccessExpr{
				Pos:       safeTok.Pos,
				Receiver:  expr,
				FieldName: nameTok.Value,
				NamePos:   nameTok.Pos,
				Safe:      true,
			}
		case KindDot:
			dotTok := p.advance()
			nameTok, err := p.expect(KindIdent, "after '.'")
			if err != nil {
				return nil, err
			}
			// v0.4: `expr DOT IDENT (` is a method call; otherwise the form
			// stays a field access (the v0.3 shape). The disambiguation is
			// purely additive — every existing field-access test continues to
			// match the no-paren branch.
			if p.peek().Kind == KindLParen {
				if _, err := p.expectParen(KindLParen, "in method call"); err != nil {
					return nil, err
				}
				var args []Expr
				if p.peek().Kind != KindRParen {
					for {
						a, err := p.parseExpr()
						if err != nil {
							return nil, err
						}
						args = append(args, a)
						if p.peek().Kind == KindComma {
							p.advance()
							continue
						}
						break
					}
				}
				if _, err := p.expectParen(KindRParen, "in method call"); err != nil {
					return nil, err
				}
				expr = &MethodCallExpr{
					Pos:       dotTok.Pos,
					Receiver:  expr,
					Method:    nameTok.Value,
					MethodPos: nameTok.Pos,
					Args:      args,
				}
				continue
			}
			// v0.5 cross-module struct literal: `mod.MyStruct { ... }`
			// only when the receiver was a bare IdentExpr (i.e. could
			// be a module binding) and the next tokens look like a
			// struct-literal body. Otherwise stay with a FieldAccessExpr
			// — typeck will resolve the receiver, and other shapes
			// (`receiver.field`, enum-variant access) keep their v0.4
			// shape unchanged.
			if id, isIdent := expr.(*IdentExpr); isIdent &&
				p.peek().Kind == KindLBrace &&
				p.looksLikeStructLitBody() {
				lit, err := p.parseStructLitBody(id.Pos, nameTok.Value)
				if err != nil {
					return nil, err
				}
				if sl, ok := lit.(*StructLit); ok {
					sl.Module = id.Name
				}
				expr = lit
				continue
			}
			expr = &FieldAccessExpr{
				Pos:       dotTok.Pos,
				Receiver:  expr,
				FieldName: nameTok.Value,
				NamePos:   nameTok.Pos,
			}
		default:
			return expr, nil
		}
	}
}

// parseIndexOrSlice handles `[ ... ]` after a postfix receiver. Inside the
// brackets `..` and `..=` are admitted (and ONLY here at v0.2 outside the
// for-in head), giving slicing its own context-sensitive corner of the
// grammar without loosening the global ban on free-standing ranges.
func (p *parser) parseIndexOrSlice(receiver Expr) (Expr, error) {
	open, err := p.expectParen(KindLBracket, "in index/slice")
	if err != nil {
		return nil, err
	}

	// `[..]`, `[..b]`, `[..=b]` — slice with no low bound.
	if k := p.peek().Kind; k == KindRange || k == KindRangeEq {
		rangeTok := p.advance()
		inclusive := rangeTok.Kind == KindRangeEq
		var high Expr
		if p.peek().Kind != KindRBracket {
			h, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			high = h
		} else if inclusive {
			// `[..=]` is meaningless — `..=` requires an upper bound.
			return nil, errorAt(rangeTok.Pos, "'..=' requires an upper bound in slice expression")
		}
		if _, err := p.expectParen(KindRBracket, "to close slice"); err != nil {
			return nil, err
		}
		return &SliceExpr{
			Pos:       open.Pos,
			Receiver:  receiver,
			Low:       nil,
			High:      high,
			Inclusive: inclusive,
		}, nil
	}

	// `[expr ...]`. We parse the first expression with parseOr (not parseExpr)
	// so the trailing-range guard inside parseExpr does not fire on the `..`
	// / `..=` we may be about to consume.
	first, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	switch p.peek().Kind {
	case KindRBracket:
		// Plain index. Use expectParen to keep parenDepth bookkeeping
		// symmetric with the opening `[`.
		if _, err := p.expectParen(KindRBracket, "to close index"); err != nil {
			return nil, err
		}
		return &IndexExpr{Pos: open.Pos, Receiver: receiver, Index: first}, nil
	case KindRange, KindRangeEq:
		rangeTok := p.advance()
		inclusive := rangeTok.Kind == KindRangeEq
		var high Expr
		if p.peek().Kind != KindRBracket {
			h, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			high = h
		} else if inclusive {
			return nil, errorAt(rangeTok.Pos, "'..=' requires an upper bound in slice expression")
		}
		if _, err := p.expectParen(KindRBracket, "to close slice"); err != nil {
			return nil, err
		}
		return &SliceExpr{
			Pos:       open.Pos,
			Receiver:  receiver,
			Low:       first,
			High:      high,
			Inclusive: inclusive,
		}, nil
	}
	t := p.peek()
	return nil, errorAtTok(t, "expected ']' or '..' in index/slice, got %s", t.Kind)
}

// Level 13: atoms — literals, identifiers, parenthesised expressions.
//
// Range tokens (`..`, `..=`) MUST NOT appear here. v0.1 admits them only in
// the head of `for x in ...`; anywhere else is a parse error with a clear
// message so the user knows the form is reserved, not a typo.
func (p *parser) parseAtom() (Expr, error) {
	t := p.peek()
	switch t.Kind {
	case KindInt:
		p.advance()
		return &IntLit{Pos: t.Pos, Text: t.Value}, nil
	case KindFloat:
		p.advance()
		return &FloatLit{Pos: t.Pos, Text: t.Value}, nil
	case KindString:
		p.advance()
		return &StringLit{Pos: t.Pos, Value: t.Value}, nil
	case KindTrue:
		p.advance()
		return &BoolLit{Pos: t.Pos, Value: true}, nil
	case KindFalse:
		p.advance()
		return &BoolLit{Pos: t.Pos, Value: false}, nil
	case KindIdent:
		// v0.7 chan constructor: the IDENT `chan` followed immediately by `[`
		// in expression position is the channel constructor (`chan[T]()` /
		// `chan[T](N)`). The parser commits eagerly because `chan[...]` cannot
		// be a meaningful index expression — `chan` is reserved for the built-
		// in constructor and any user-defined binding with that name is
		// rejected at typeck.
		if t.Value == "chan" && p.peekAfterIdentIs(KindLBracket) {
			return p.parseChanConstructorExpr()
		}
		p.advance()
		// Struct-literal disambiguation (v0.2). `Ident '{' ...` is a struct
		// literal only when the inside is empty (`{}`) or starts with
		// `IDENT ':'`. Otherwise the `{` belongs to the surrounding
		// statement (an `if`/`for`/`match` body) and we leave it alone.
		if p.peek().Kind == KindLBrace && p.looksLikeStructLitBody() {
			return p.parseStructLitBody(t.Pos, t.Value)
		}
		return &IdentExpr{Pos: t.Pos, Name: t.Value}, nil
	case KindRune:
		// Token.Value is the codepoint as a decimal string; the lexer guards
		// it. We parse here so downstream consumers don't have to. Overflow
		// of an int64 from a 21-bit Unicode codepoint is impossible.
		p.advance()
		v, err := strconv.ParseInt(t.Value, 10, 64)
		if err != nil {
			return nil, errorAt(t.Pos, "invalid rune codepoint: %v", err)
		}
		return &RuneLit{Pos: t.Pos, Value: v}, nil
	case KindLParen:
		return p.parseTupleOrParen()
	case KindLBracket:
		return p.parseListLit()
	case KindThis:
		p.advance()
		return &ThisExpr{Pos: t.Pos}, nil
	case KindFn:
		// v0.7 anon-fn expression. The disambiguation pin says `fn` followed
		// by `(` is always an anon-fn, never a fn-decl. parseAtom reaches
		// here only in expression context, so anything other than `fn (` is
		// a misuse — the diagnostic blames the lookahead-1 token.
		if p.peekAfterFnIs(KindLParen) {
			return p.parseAnonFnExpr()
		}
		return nil, errorAt(t.Pos, "'fn' in expression position must be followed by '(' for an anonymous function")
	case KindNil:
		// v0.6 nil literal. Typeck (Unit 4) resolves the type from the
		// surrounding context; outside an inferable position the diagnostic
		// is `cannot infer type of nil — annotate the binding`.
		p.advance()
		return &NilLit{Pos: t.Pos}, nil
	case KindRange, KindRangeEq:
		return nil, errorAt(t.Pos, "range expressions are only allowed in for-in heads or slice brackets")
	case KindBang:
		return nil, errorAt(t.Pos, "use 'not' for boolean negation; '!' is reserved")
	case KindQuestion:
		// v0.6: `?` is a postfix-only operator (propagation). A leading `?`
		// at expression-start is meaningless; produce a focused diagnostic
		// instead of the generic "expected expression, got '?'" form.
		return nil, errorAt(t.Pos, "'?' must follow an expression (propagation is postfix); did you mean '?.' for safe navigation or 'T?' for a nullable type?")
	case KindSafeDot:
		// v0.6: `?.` likewise needs a receiver. A leading `?.` is the same
		// shape error as a leading `?` and gets a parallel diagnostic.
		return nil, errorAt(t.Pos, "'?.' must follow an expression (safe navigation is postfix)")
	case KindCoalesce:
		// v0.6: `??` is an infix-only operator. A leading `??` reports
		// directly rather than relying on the parseCoalesce loop.
		return nil, errorAt(t.Pos, "'??' must appear between two expressions (nil-coalesce is infix)")
	}
	return nil, errorAtTok(t, "expected expression, got %s", t.Kind)
}

// parseTupleOrParen handles `(` after a postfix-position dispatch. We parse
// the first expression; if a comma follows we collect the rest as a tuple
// literal, otherwise we close as a ParenExpr. PLAN.md: 1-tuples are not
// admitted at v0.2, so a single `(e,)` form produces a parse error.
func (p *parser) parseTupleOrParen() (Expr, error) {
	open := p.peek()
	if _, err := p.expectParen(KindLParen, ""); err != nil {
		return nil, err
	}
	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == KindComma {
		// Tuple literal: collect remaining elements (≥ 1 more makes ≥ 2 total).
		elements := []Expr{first}
		for p.peek().Kind == KindComma {
			p.advance()
			// Trailing comma allowed: `(a, b,)`.
			if p.peek().Kind == KindRParen {
				break
			}
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			elements = append(elements, e)
		}
		if _, err := p.expectParen(KindRParen, "to close tuple literal"); err != nil {
			return nil, err
		}
		// PLAN: at least 2 elements. The collection above guarantees ≥ 2 if
		// at least one extra element was parsed; the trailing-comma case
		// `(a,)` produces exactly 1 element.
		if len(elements) < 2 {
			return nil, errorAt(open.Pos, "tuple literal requires at least 2 elements (use parentheses for grouping)")
		}
		return &TupleLit{Pos: open.Pos, Elements: elements}, nil
	}
	if _, err := p.expectParen(KindRParen, "to close '('"); err != nil {
		return nil, err
	}
	return &ParenExpr{Pos: open.Pos, Inner: first}, nil
}

// parseListLit handles `[` in expression position. Empty list is allowed at
// the parser level; typeck rejects empty lists outside annotated contexts.
func (p *parser) parseListLit() (Expr, error) {
	open, err := p.expectParen(KindLBracket, "in list literal")
	if err != nil {
		return nil, err
	}
	var elements []Expr
	for {
		if p.peek().Kind == KindRBracket {
			break
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elements = append(elements, e)
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRBracket, "to close list literal"); err != nil {
		return nil, err
	}
	return &ListLit{Pos: open.Pos, Elements: elements}, nil
}

// looksLikeStructLitBody peeks past the `{` (without consuming) to decide
// whether `Ident { ... }` is a struct literal or a brace block (i.e. the
// caller's statement body). Rule: empty `{}` OR opens with `IDENT ':'`
// where `:` is not `:=` (a walrus would mean the inside is a binding,
// which structurally cannot happen in expression position but happens
// inside an `if x { y := ... }` body).
//
// The function inspects raw token positions and never advances. We treat
// NEWLINEs as transparent inside the brace because struct literals can
// legitimately span lines.
func (p *parser) looksLikeStructLitBody() bool {
	// p.pos currently sits on `{`. Walk forward.
	i := p.pos + 1
	skipNL := func() {
		for i < len(p.tokens) && p.tokens[i].Kind == KindNewline {
			i++
		}
	}
	skipNL()
	if i >= len(p.tokens) {
		return false
	}
	if p.tokens[i].Kind == KindRBrace {
		return true // empty struct literal `Name {}`
	}
	if p.tokens[i].Kind != KindIdent {
		return false
	}
	i++
	skipNL()
	if i >= len(p.tokens) {
		return false
	}
	// `:` (struct field) yes; `:=` (walrus) no.
	return p.tokens[i].Kind == KindColon
}

// parseStructLitBody consumes the `{ field: value, ... }` tail of a struct
// literal. The opening `{` is at p.peek(); typeName is the already-consumed
// type identifier.
func (p *parser) parseStructLitBody(headPos Position, typeName string) (Expr, error) {
	open, err := p.expect(KindLBrace, "in struct literal")
	if err != nil {
		return nil, err
	}
	// Brace region behaves like a paren region for NEWLINE skipping.
	p.parenDepth++
	defer func() { p.parenDepth-- }()
	_ = open

	var fields []FieldInit
	for {
		if p.peek().Kind == KindRBrace {
			break
		}
		nameT, err := p.expect(KindIdent, "in struct literal field list")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(KindColon, "after struct literal field name"); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		fields = append(fields, FieldInit{
			Name:  nameT.Value,
			Value: val,
			Pos:   nameT.Pos,
		})
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(KindRBrace, "to close struct literal"); err != nil {
		return nil, err
	}
	return &StructLit{Pos: headPos, TypeName: typeName, Fields: fields}, nil
}

// ---------------------------------------------------------------------------
// Match patterns.
// ---------------------------------------------------------------------------

// parsePattern is the entry point for the pattern grammar. Top-level
// dispatch is by the first token: `_` is wildcard, `(` opens a tuple
// pattern, an IDENT is followed by lookahead to disambiguate Bind vs
// Struct vs Enum, and any literal kind starts a literal pattern.
//
// NEWLINE significance inside patterns is handled the same way as inside
// expressions: parens/brackets bump parenDepth and silence NEWLINEs, and
// outside those the pattern is a single line by construction (the arm
// parser does not intentionally span newlines on a flat pattern).
func (p *parser) parsePattern() (Pattern, error) {
	t := p.peek()
	switch t.Kind {
	case KindIdent:
		if t.Value == "_" {
			p.advance()
			return &WildcardPat{Pos: t.Pos}, nil
		}
		// IDENT alone, IDENT '{' — struct, IDENT '.' IDENT — enum, otherwise bind.
		p.advance()
		switch p.peek().Kind {
		case KindLBrace:
			return p.parseStructPatBody(t.Pos, t.Value)
		case KindDot:
			p.advance()
			vT, err := p.expect(KindIdent, "in enum pattern after '.'")
			if err != nil {
				return nil, err
			}
			ep := &EnumPat{Pos: t.Pos, TypeName: t.Value, VariantName: vT.Value}
			// v0.4 (Unit 2): optional payload destructure
			// `Token.Ident(name)`, `Token.Number(0, _)`. Empty parens are
			// rejected — bare variants drop the parentheses entirely.
			if p.peek().Kind == KindLParen {
				payload, err := p.parseVariantPayloadPatterns()
				if err != nil {
					return nil, err
				}
				ep.Payload = payload
			}
			return ep, nil
		}
		return &BindPat{Pos: t.Pos, Name: t.Value}, nil
	case KindLParen:
		return p.parseTuplePat()
	case KindMinus:
		// Optional unary `-` for numeric literal patterns.
		minus := p.advance()
		next := p.peek()
		if next.Kind != KindInt && next.Kind != KindFloat {
			return nil, errorAt(minus.Pos, "unary '-' in a pattern must precede a numeric literal, got %s", next.Kind)
		}
		litExpr, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		return &LitPat{
			Pos: minus.Pos,
			Lit: &UnaryExpr{Pos: minus.Pos, Op: UnaryNeg, Operand: litExpr},
		}, nil
	case KindInt, KindFloat, KindString, KindTrue, KindFalse, KindRune:
		// Literal patterns. We re-use parseAtom to read the literal so we
		// pick up the same Token.Value parsing as expressions do.
		litExpr, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		return &LitPat{Pos: t.Pos, Lit: litExpr}, nil
	}
	return nil, errorAtTok(t, "expected pattern, got %s", t.Kind)
}

// parseTuplePat handles `( pat, pat, ... )`. PLAN.md: ≥ 2 elements are
// required; a single-paren pattern is a parse error rather than silent
// grouping (patterns don't have a grouping operator at v0.2).
func (p *parser) parseTuplePat() (Pattern, error) {
	open, err := p.expectParen(KindLParen, "in tuple pattern")
	if err != nil {
		return nil, err
	}
	var elements []Pattern
	for {
		if p.peek().Kind == KindRParen {
			break
		}
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		elements = append(elements, pat)
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRParen, "to close tuple pattern"); err != nil {
		return nil, err
	}
	if len(elements) < 2 {
		return nil, errorAt(open.Pos, "tuple pattern requires at least 2 elements")
	}
	return &TuplePat{Pos: open.Pos, Elements: elements}, nil
}

// parseVariantPayloadPatterns consumes the `( pat ( ',' pat )* )` tail of an
// enum variant pattern. The opening `(` has been peeked but not yet consumed.
//
// Empty parens (`Token.Ident()`) are rejected: bare variants drop the
// parentheses, mirroring the variant-decl rule. A trailing comma is also
// rejected — payload pattern lists carry no trailing comma.
func (p *parser) parseVariantPayloadPatterns() ([]Pattern, error) {
	openTok, err := p.expectParen(KindLParen, "in enum variant pattern")
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == KindRParen {
		return nil, errorAt(openTok.Pos,
			"empty parentheses are not allowed; use the bare variant name")
	}
	var pats []Pattern
	for {
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		pats = append(pats, pat)
		if p.peek().Kind == KindComma {
			commaTok := p.advance()
			if p.peek().Kind == KindRParen {
				return nil, errorAt(commaTok.Pos,
					"trailing comma not allowed in enum variant pattern")
			}
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRParen, "to close enum variant pattern"); err != nil {
		return nil, err
	}
	return pats, nil
}

// parseStructPatBody consumes the `{ field: pat, field, ..., .. }` tail of
// a struct pattern. The opening `{` has been peeked but not consumed; the
// type name has already been parsed.
//
// Shorthand: `Point { x, y }` desugars at parse time to
// `Point { x: BindPat{x}, y: BindPat{y} }` so downstream consumers walk a
// uniform shape. PLAN.md §10 (acted-upon) pins this behaviour.
func (p *parser) parseStructPatBody(headPos Position, typeName string) (Pattern, error) {
	if _, err := p.expect(KindLBrace, "in struct pattern"); err != nil {
		return nil, err
	}
	p.parenDepth++
	defer func() { p.parenDepth-- }()

	var fields []StructPatField
	rest := false
	for {
		if p.peek().Kind == KindRBrace {
			break
		}
		// `..` rest marker. Must be the last token before `}`.
		if p.peek().Kind == KindRange {
			rest = true
			p.advance()
			break
		}
		nameT, err := p.expect(KindIdent, "in struct pattern field list")
		if err != nil {
			return nil, err
		}
		var sub Pattern
		if p.peek().Kind == KindColon {
			p.advance()
			s, err := p.parsePattern()
			if err != nil {
				return nil, err
			}
			sub = s
		} else {
			// Shorthand: bind to a same-named local.
			sub = &BindPat{Pos: nameT.Pos, Name: nameT.Value}
		}
		fields = append(fields, StructPatField{
			Name:    nameT.Value,
			Pattern: sub,
			Pos:     nameT.Pos,
		})
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(KindRBrace, "to close struct pattern"); err != nil {
		return nil, err
	}
	return &StructPat{Pos: headPos, TypeName: typeName, Fields: fields, Rest: rest}, nil
}
