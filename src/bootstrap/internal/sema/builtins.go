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

// errKinds is the FIXED, built-in error taxonomy (docs/code/errors.md, GRAMMAR group 8):
// the nameable error types a program may CHOOSE from but cannot extend this phase. Each
// name is both a constructor `Name(msg) -> Err` (its message payload) and an `is`
// target on an Err. The integer value is the discriminating kind; it is MIRRORED by the
// runtime (src/runtime/csrc/zergrt.h ZRT_ERR_*) so a runtime abort ("ValueError: …",
// "IOError: …") and a `raise ValueError("…")` reify to the same kind. These are modeled
// as one common carrier (the erased `Err`, C `zrt_err`) tagged by kind rather than a
// user-extensible hierarchy — the minimal MVP shape that keeps the Result/Either
// lowering (emit_result.go) unchanged while making the named kinds real at the surface.
//
//nolint:gochecknoglobals // a fixed, compiler-owned lookup table.
var errKinds = map[string]int{
	"ValueError":    1,
	"OverflowError": 2,
	"IOError":       3,
	"EncodingError": 4,
	"IndexError":    5,
	"KeyError":      6,
}

// ErrKind reports the built-in error kind a name denotes and whether it is one, so the
// backend can lower `Name(msg)` and `err is Name` to the shared kind constant.
func ErrKind(name string) (int, bool) {
	k, ok := errKinds[name]
	return k, ok
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

// isChan reports whether a type is a channel, whose `del` gives up the send end rather
// than revoking the name (docs/code/coroutine.md).
func isChan(t Type) bool {
	_, ok := t.(*types.Chan)
	return ok
}

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
