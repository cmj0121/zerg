package sema

import "github.com/cmj0121/zerg/src/bootstrap/internal/types"

// This file models the few built-in shapes the grammar needs but no standard
// library supplies yet (DESIGN-1b §6, FORK-4): the error placeholder that fills
// the Right side of a Result, and the Result constructor itself. Everything else
// stdlib provides — channel payloads, string indexing, container methods — stays
// Unknown so Phase 1b does not hardcode a type system it has not designed.

// errDef is the built-in error placeholder's declaration. With no stdlib Error
// spec yet, 'raise', a force-unwrap failure, and a guard's failure all carry this
// one opaque nominal error; a real error hierarchy is deferred (FORK-4).
//
//nolint:gochecknoglobals // a single interned built-in nominal type.
var errDef = &types.TypeDef{Name: "Err", Struct: &types.StructDef{}}

// errType is the use-site built-in error type 'Err'.
//
//nolint:gochecknoglobals // a single interned built-in nominal type.
var errType Type = &types.Struct{Def: errDef}

// errKind is one entry of the taxonomy: the discriminating integer, and whether the name
// is also a CONSTRUCTOR. Every name is an `is` target; `ctor` is the narrower permission
// to build one by hand with `Name(msg)`, which not every kind may grant (see errKinds).
type errKind struct {
	kind int
	ctor bool
}

// errKinds is the FIXED, built-in error taxonomy (docs/code/errors.md, GRAMMAR group 8):
// the nameable error types a program may CHOOSE from but cannot extend this phase. Each
// name is an `is` target on an Err, and all but one are also a constructor
// `Name(msg) -> Err` (its message payload). The integer value is the discriminating kind;
// it is MIRRORED by the runtime (src/runtime/csrc/zergrt.h ZRT_ERR_*) so a runtime abort
// ("ValueError: …", "IOError: …") and a `raise ValueError("…")` reify to the same kind.
// These are modeled as one common carrier (the erased `Err`, C `zrt_err`) tagged by kind
// rather than a user-extensible hierarchy — the minimal MVP shape that keeps the
// Result/Either lowering (emit_result.go) unchanged while making the named kinds real at
// the surface.
//
// `StopIteration` is the one name a program may TEST but not CONSTRUCT, and the asymmetry
// is load-bearing rather than tidy. It is not a failure but the end-of-stream sentinel a
// clean channel close carries (chan.c chan_stop_iteration), and the runtime tells a clean
// close from a crashing one by that kind alone — `ch->err.kind != ZRT_ERR_STOP_ITERATION`
// is the whole test, in the receive lowering's Right and in select's crash-close scan.
// Were the name constructible, `raise StopIteration("…")` in a sender would close its
// channel wearing the sentinel's kind, and every receiver would read a crash as a clean
// end. Denying the constructor is what keeps that reasoning true; `err is StopIteration`
// — the reason a consumer can tell the two apart at the surface — costs nothing and stays.
//
// `AssertionError` is a kind the seed KNOWS and a form the seed cannot READ, and the split is
// deliberate. `assert` is a keyword of the shipped compiler only (src/bootstrap/README.md's
// gap list records it), so nothing here lexes the word — but the kind numbering is an ABI
// shared with the runtime, and a table that stopped at 10 would let a later kind take 11 and
// make one number mean two things depending on which compiler built the program.
//
//nolint:gochecknoglobals // a fixed, compiler-owned lookup table.
var errKinds = map[string]errKind{
	"ValueError":        {1, true},
	"OverflowError":     {2, true},
	"IOError":           {3, true},
	"EncodingError":     {4, true},
	"IndexError":        {5, true},
	"KeyError":          {6, true},
	"DeadlockError":     {7, true},
	"SendOnClosedError": {8, true},
	"StopIteration":     {9, false},
	"DivideByZeroError": {10, true},
	"AssertionError":    {11, true},
}

// ErrKind reports the built-in error kind a name denotes and whether it is one, so the
// backend can lower `err is Name` to the shared kind constant. Every name in the taxonomy
// is testable this way; whether it can also be BUILT is ErrCtorKind's narrower question.
func ErrKind(name string) (int, bool) {
	k, ok := errKinds[name]
	return k.kind, ok
}

// ErrCtorKind reports the kind `Name(msg)` constructs, and whether the name is a
// constructor at all — the `is`-only names (StopIteration) answer false, so the call falls
// through to the ordinary paths and is reported as the undefined name it is.
func ErrCtorKind(name string) (int, bool) {
	k, ok := errKinds[name]
	return k.kind, ok && k.ctor
}

// isErr reports whether a type is the built-in erased error `Err` (the Right of a
// Result, and what every built-in error kind constructs).
func isErr(t Type) bool {
	s, ok := t.(*types.Struct)
	return ok && s.Def == errDef
}

// resultType builds 'Result[T]' — the sum 'Either[T, Err]' a guard block yields
// (DESIGN-1b §6).
func resultType(t Type) Type { return &types.Either{Left: t, Right: errType} }

// ResultOf builds 'Result[T]' for the backend. A `select` recv arm's bind is declared into
// the arm's scope rather than recorded in Info, so the emitter cannot read that type back
// and has to name it — and it must be the SAME type checkSelect gave the bind, or the
// carrier it looks up is a different one.
func ResultOf(t Type) Type { return resultType(t) }

// leftType extracts the success (Left) type of an Either, a Result, or an optional
// value, reporting whether t is one of those wrappers. An optional 'T?' is treated
// as 'Either[T, nil]', so its Left is the element type.
func leftType(t Type) (Type, bool) {
	switch x := t.(type) {
	case *types.Opt:
		return x.Elem, true
	case *types.Either:
		return x.Left, true
	}
	return nil, false
}

// isOptional reports whether t is a 'T?' optional (as opposed to an Either/Result),
// the shape whose empty case is written 'nil'.
func isOptional(t Type) bool {
	_, ok := t.(*types.Opt)
	return ok
}
