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

// resultType builds 'Result[T]' — the sum 'Either[T, Err]' a guard block yields
// (DESIGN-1b §6).
func resultType(t Type) Type { return &types.Either{Left: t, Right: errType} }

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
