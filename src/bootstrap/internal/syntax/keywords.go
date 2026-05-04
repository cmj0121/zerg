package syntax

// keywords maps the textual form of a reserved word to its Kind. Identifiers
// are first scanned as if they were ordinary names, then looked up here; if
// the lookup hits, the Kind is replaced. This keeps the scanner branch-free
// at the character level and concentrates the keyword list in one spot.
//
// `nop` and `print` carry over from v0.0; everything else is v0.1.
var keywords = map[string]Kind{
	"nop":      KindNop,
	"print":    KindPrint,
	"and":      KindAnd,
	"break":    KindBreak,
	"const":    KindConst,
	"continue": KindContinue,
	"elif":     KindElif,
	"else":     KindElse,
	"false":    KindFalse,
	"fn":       KindFn,
	"for":      KindFor,
	"if":       KindIf,
	"in":       KindIn,
	"let":      KindLet,
	"loop":     KindLoop,
	"mut":      KindMut,
	"not":      KindNot,
	"or":       KindOr,
	"return":   KindReturn,
	"true":     KindTrue,
	"while":    KindWhile,
	"xor":      KindXor,
	// v0.2 composite-data keywords.
	"struct": KindStruct,
	"enum":   KindEnum,
	"match":  KindMatch,
	// v0.4 polymorphism keywords. `this` is reserved everywhere: any
	// `let this := ...` or `fn this()` rejects at parse time.
	"spec": KindSpec,
	"impl": KindImpl,
	"this": KindThis,
	// v0.5 module keywords. `pub` is the top-level visibility modifier; it
	// applies to `fn`, `struct`, `enum`, `spec`, and impl methods. The bit
	// is parsed but inert at Unit 1a — Unit 3 wires it into typeck.
	"pub": KindPub,
	// `import` and `as` arrive in v0.5 Unit 1b. `import` introduces a top-level
	// module import statement (single, alias, or grouped form); `as` is the
	// alias-binding keyword inside an `import`. Reserving them as keywords
	// also implements the parse-time reserved-name rule: a module cannot be
	// imported under a name that collides with any keyword (PLAN.md
	// §Resolution rules). The parser cross-checks against this same `keywords`
	// map so the lexer table stays the single source of truth.
	"import": KindImport,
	"as":     KindAs,
	// v0.6 null-safety. `nil` is the absence-of-value literal in expression
	// position; the lexer promotes the bare word so the parser can distinguish
	// it from any user-defined identifier. The reserved-name rule extends
	// transitively: a module imported as `nil`, a let bound to the name, etc.
	// all reject at parse time via the same keywords-table cross-check used
	// by every other keyword.
	"nil": KindNil,
	// v0.7 concurrency keywords. `spawn` starts a fire-and-forget concurrent
	// task; `defer` registers code to run at fn-body exit in LIFO order. Both
	// are reserved everywhere starting v0.7 Unit 1a — the reserved-name rule
	// flows through the same keywords-table cross-check used by every other
	// keyword, so `let spawn := ...`, `import "x" as defer`, etc. all reject
	// at parse time.
	"spawn": KindSpawn,
	"defer": KindDefer,
}
