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
}
