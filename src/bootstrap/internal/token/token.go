// Package token defines the lexical tokens of the Zerg surface grammar and the
// source positions attached to them. The bootstrap compiler lexes a source file
// into a stream of these tokens; every later stage (parser, sema, emit) reports
// diagnostics through the spans recorded here.
//
// The token set follows GRAMMAR group 2 (Lexical). Phase 0 of the roadmap only
// parses a subset of the language, but the token vocabulary is kept close to the
// full surface so the lexer can grow into groups 1-12 without renaming kinds.
package token

import "fmt"

// Kind enumerates the lexical categories a token can belong to.
type Kind int

const (
	EOF     Kind = iota // end of input
	Illegal             // an unrecognized or unsupported lexeme
	Semi                // a statement separator: a literal ';' or an inserted newline (ASI)

	// literals and names
	Ident  // an identifier (also the wildcard '_')
	Int    // an integer literal: 42, 0xFF, 0o755, 0b1010, 1_000
	Float  // a floating-point literal: 3.14, 6.022e23
	Str    // a string literal: "text", """multi-line"""
	RawStr // a raw string literal: r"C:\tmp"
	Rune   // a rune literal: 'a', '\n', '\u{1F600}'
	Byte   // a byte literal: b'a', b'\x41'

	// keywords (GRAMMAR group 2); each reserved word is its own kind
	Fn
	Return
	If
	Else
	For
	In
	Break
	Continue
	Nop
	Mut
	Const
	True
	False
	Nil
	Print
	And
	Or
	Not
	Is
	This
	As
	// reserved words not yet handled by the Phase 0 parser; the lexer still
	// classifies them so diagnostics can name them precisely.
	Pub
	Import
	From
	Match
	Spawn
	Select
	Struct
	Enum
	Spec
	Chan
	Type
	Impl
	Package
	Init
	Defer
	Del
	Raise
	Guard
	With
	Unsafe
	Ptr
	Asm

	// operators and punctuation
	Plus     // +
	Minus    // -
	Star     // *
	Slash    // /
	Percent  // %
	PlusMod  // +%
	MinusMod // -%
	StarMod  // *%
	Amp      // &
	Pipe     // |
	Caret    // ^
	Tilde    // ~
	Shl      // <<
	Shr      // >>

	EqEq // ==
	Ne   // !=
	Lt   // <
	Gt   // >
	Le   // <=
	Ge   // >=

	Assign   // =
	Walrus   // :=
	Colon    // :
	Arrow    // ->
	LArrow   // <-
	Coalesce // ??
	Question // ?
	Bang     // !
	OptDot   // ?.

	Dot      // .
	DotDot   // ..
	DotDotEq // ..=
	Comma    // ,

	LParen // (
	RParen // )
	LBrack // [
	RBrack // ]
	LBrace // {
	RBrace // }
)

var names = map[Kind]string{
	EOF:     "EOF",
	Illegal: "ILLEGAL",
	Semi:    ";",
	Ident:   "IDENT",
	Int:     "INT",
	Float:   "FLOAT",
	Str:     "STR",
	RawStr:  "RAWSTR",
	Rune:    "RUNE",
	Byte:    "BYTE",

	Fn: "fn", Return: "return", If: "if", Else: "else", For: "for", In: "in",
	Break: "break", Continue: "continue", Nop: "nop", Mut: "mut", Const: "const",
	True: "true", False: "false", Nil: "nil", Print: "print", And: "and", Or: "or",
	Not: "not", Is: "is", This: "this", As: "as", Pub: "pub", Import: "import",
	From: "from", Match: "match", Spawn: "spawn", Select: "select", Struct: "struct",
	Enum: "enum", Spec: "spec", Chan: "chan", Type: "type", Impl: "impl",
	Package: "package", Init: "init", Defer: "defer", Del: "del", Raise: "raise",
	Guard: "guard", With: "with", Unsafe: "unsafe", Ptr: "ptr", Asm: "asm",

	Plus: "+", Minus: "-", Star: "*", Slash: "/", Percent: "%",
	PlusMod: "+%", MinusMod: "-%", StarMod: "*%",
	Amp: "&", Pipe: "|", Caret: "^", Tilde: "~", Shl: "<<", Shr: ">>",
	EqEq: "==", Ne: "!=", Lt: "<", Gt: ">", Le: "<=", Ge: ">=",
	Assign: "=", Walrus: ":=", Colon: ":", Arrow: "->", LArrow: "<-",
	Coalesce: "??", Question: "?", Bang: "!", OptDot: "?.",
	Dot: ".", DotDot: "..", DotDotEq: "..=", Comma: ",",
	LParen: "(", RParen: ")", LBrack: "[", RBrack: "]", LBrace: "{", RBrace: "}",
}

// keywords maps a reserved word's spelling to its token kind.
var keywords = map[string]Kind{
	"fn": Fn, "return": Return, "if": If, "else": Else, "for": For, "in": In,
	"break": Break, "continue": Continue, "nop": Nop, "mut": Mut, "const": Const,
	"true": True, "false": False, "nil": Nil, "print": Print, "and": And, "or": Or,
	"not": Not, "is": Is, "this": This, "as": As, "pub": Pub, "import": Import,
	"from": From, "match": Match, "spawn": Spawn, "select": Select, "struct": Struct,
	"enum": Enum, "spec": Spec, "chan": Chan, "type": Type, "impl": Impl,
	"package": Package, "init": Init, "defer": Defer, "del": Del, "raise": Raise,
	"guard": Guard, "with": With, "unsafe": Unsafe, "ptr": Ptr, "asm": Asm,
}

// Lookup returns the keyword kind for an identifier spelling, or Ident when the
// word is not reserved.
func Lookup(ident string) Kind {
	if k, ok := keywords[ident]; ok {
		return k
	}
	return Ident
}

// String renders a token kind for diagnostics and test output.
func (k Kind) String() string {
	if s, ok := names[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Pos is a byte offset paired with a 1-based line and column, so diagnostics can
// point at the exact source location.
type Pos struct {
	Offset int
	Line   int
	Col    int
}

// String renders a position as line:col.
func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Span is a half-open [Start, End) range of source text.
type Span struct {
	Start Pos
	End   Pos
}

// Token is a single lexical unit with its source text and location.
type Token struct {
	Kind   Kind
	Lexeme string // the exact source text of the token
	Str    string // decoded content for Str/RawStr/Rune/Byte literals
	Span   Span
}

// String renders a token compactly for tests and debugging.
func (t Token) String() string {
	switch t.Kind {
	case EOF, Semi:
		return t.Kind.String()
	case Int, Float, Ident:
		return fmt.Sprintf("%s(%s)", t.Kind, t.Lexeme)
	case Str, RawStr, Rune, Byte:
		return fmt.Sprintf("%s(%q)", t.Kind, t.Str)
	default:
		return t.Kind.String()
	}
}

// EndsItem reports whether a token of this kind can be the last token of a
// grammatical item, which is what lets the lexer insert a ';' at a line break
// (GRAMMAR group 2, ASI). A newline after such a token separates statements;
// after any other token it is insignificant.
func (k Kind) EndsItem() bool {
	switch k {
	case Ident, Int, Float, Str, RawStr, Rune, Byte,
		RParen, RBrack, RBrace, Question, Bang,
		True, False, Nil, This, Return, Break, Continue, Nop:
		return true
	default:
		return false
	}
}
