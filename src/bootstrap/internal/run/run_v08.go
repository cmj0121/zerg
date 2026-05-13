package run

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// v0.8 Unit 3 — interpreter dispatch for the toolchain-shipped `__builtin`
// host primitives. callFn checks fn.BuiltinName != "" and routes here; the
// per-name table translates the call into a host syscall and constructs a
// Zerg-typed Value against the user-program's view of the result type.
//
// Both halves agree on bucketing: a host error maps to one of IoError /
// ParseError variants per PLAN.md §"In scope (v0.8)". The cgen half (Unit
// 4) makes the same bucket choices against errno so the parity corpus
// matches by variant identity, not error text.
//
// Result/Option construction reuses the typeck-stamped *Type on the call's
// expression — that's the canonical *Type for the user program's view of
// Result[T, IoError] (or Option[str], etc.). The user-defined error enum
// (IoError, ParseError) is reached via the Result type's VariantPayloads
// — index 1 is the Err payload's element type, which is the canonical
// IoError / ParseError *Type pointer.

// callBuiltin executes a __builtin fn-decl in place of its (absent) body.
// Args have already been evaluated and coerced by callFn.
//
// resultType is the typeck-stamped Type of the call expression. For Result
// returns it carries VariantPayloads we use to look up the canonical
// IoError / ParseError *Type pointer; for Option returns it carries the
// Option[T] *Type directly.
func (in *interp) callBuiltin(fn *syntax.FnDecl, args []Value, resultType *syntax.Type, callPos syntax.Position) (Value, error) {
	if v, ok, err := callBuiltinV09(fn, args); ok {
		return v, err
	}
	if v, ok, err := in.callBuiltinV09ArgvExit(fn, args, resultType); ok {
		return v, err
	}
	switch fn.BuiltinName {
	case "strings_split":
		return execStringsSplit(args[0], args[1], resultType, callPos)
	case "strings_join":
		return execStringsJoin(args[0], args[1])
	case "strings_trim":
		return execStringsTrim(args[0])
	case "strings_starts_with":
		return execStringsStartsWith(args[0], args[1])
	case "strings_ends_with":
		return execStringsEndsWith(args[0], args[1])
	case "strings_contains":
		return execStringsContains(args[0], args[1])
	case "strings_replace":
		return execStringsReplace(args[0], args[1], args[2])
	case "strings_to_upper":
		return execStringsToUpper(args[0])
	case "strings_to_lower":
		return execStringsToLower(args[0])
	case "strings_parse_int":
		return execStringsParseInt(args[0], resultType, callPos)
	case "math_abs":
		return execMathAbs(args[0])
	case "math_min":
		return execMathMin(args[0], args[1])
	case "math_max":
		return execMathMax(args[0], args[1])
	case "math_gcd":
		return execMathGcd(args[0], args[1])
	case "os_env":
		return execOsEnv(args[0], resultType, callPos)
	}
	return Value{}, fmt.Errorf("internal: unknown __builtin %q at %s", fn.BuiltinName, callPos)
}

// resultOk constructs Result[T, E].Ok(payload) using resultType as the
// canonical Result *Type. Caller has already produced payload at the right
// element type.
func resultOk(resultType *syntax.Type, payload Value, pos syntax.Position) (Value, error) {
	if resultType == nil || resultType.Kind != syntax.TypeEnum {
		return Value{}, fmt.Errorf("internal: builtin Ok receiver lacks Result type at %s", pos)
	}
	idx := variantIndex(resultType, "Ok")
	if idx < 0 {
		return Value{}, fmt.Errorf("internal: %s has no Ok variant at %s", resultType, pos)
	}
	return enumVal(resultType, idx, "Ok", []Value{payload}), nil
}

// resultErrEnum constructs Result[T, E].Err(E.<variant>) where E is the
// user-defined error enum carried in resultType.VariantPayloads[1][0]. The
// helper looks up the canonical E *Type pointer through that path so the
// constructed value compares equal to the same variant the user might
// pattern-match against.
func resultErrEnum(resultType *syntax.Type, variantName string, pos syntax.Position) (Value, error) {
	if resultType == nil || resultType.Kind != syntax.TypeEnum {
		return Value{}, fmt.Errorf("internal: builtin Err receiver lacks Result type at %s", pos)
	}
	if len(resultType.VariantPayloads) < 2 || len(resultType.VariantPayloads[1]) != 1 {
		return Value{}, fmt.Errorf("internal: %s lacks Result Err payload at %s", resultType, pos)
	}
	errType := resultType.VariantPayloads[1][0]
	errIdx := variantIndex(errType, variantName)
	if errIdx < 0 {
		return Value{}, fmt.Errorf("internal: %s has no variant %q at %s", errType, variantName, pos)
	}
	errVal := enumVal(errType, errIdx, variantName, nil)
	idx := variantIndex(resultType, "Err")
	if idx < 0 {
		return Value{}, fmt.Errorf("internal: %s has no Err variant at %s", resultType, pos)
	}
	return enumVal(resultType, idx, "Err", []Value{errVal}), nil
}

// --- std/strings ----------------------------------------------------------

func execStringsSplit(sV, sepV Value, resultType *syntax.Type, pos syntax.Position) (Value, error) {
	if sepV.Str == "" {
		return Value{}, fmt.Errorf("runtime error at %s: split: empty separator", pos)
	}
	parts := strings.Split(sV.Str, sepV.Str)
	out := make([]Value, len(parts))
	for i, p := range parts {
		out[i] = strVal(p)
	}
	// resultType is list[str]; element type is its Element field.
	if resultType != nil && resultType.Kind == syntax.TypeList && resultType.Element != nil {
		return Value{Type: resultType, List: out}, nil
	}
	return listVal(syntax.TStr(), out), nil
}

func execStringsJoin(partsV, sepV Value) (Value, error) {
	parts := make([]string, len(partsV.List))
	for i, e := range partsV.List {
		parts[i] = e.Str
	}
	return strVal(strings.Join(parts, sepV.Str)), nil
}

func execStringsTrim(sV Value) (Value, error) {
	return strVal(strings.TrimSpace(sV.Str)), nil
}

func execStringsStartsWith(sV, prefixV Value) (Value, error) {
	return boolVal(strings.HasPrefix(sV.Str, prefixV.Str)), nil
}

func execStringsEndsWith(sV, suffixV Value) (Value, error) {
	return boolVal(strings.HasSuffix(sV.Str, suffixV.Str)), nil
}

func execStringsContains(sV, needleV Value) (Value, error) {
	return boolVal(strings.Contains(sV.Str, needleV.Str)), nil
}

func execStringsReplace(sV, oldV, newV Value) (Value, error) {
	return strVal(strings.ReplaceAll(sV.Str, oldV.Str, newV.Str)), nil
}

// asciiToUpper / asciiToLower adjust only [a-z] / [A-Z] so the cgen
// libc-free implementation can produce byte-identical output. Non-ASCII
// bytes pass through unchanged.
func asciiToUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}

func asciiToLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func execStringsToUpper(sV Value) (Value, error) {
	return strVal(asciiToUpper(sV.Str)), nil
}

func execStringsToLower(sV Value) (Value, error) {
	return strVal(asciiToLower(sV.Str)), nil
}

func execStringsParseInt(sV Value, resultType *syntax.Type, pos syntax.Position) (Value, error) {
	trimmed := strings.TrimSpace(sV.Str)
	if trimmed == "" {
		return resultErrEnum(resultType, "Empty", pos)
	}
	n, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		switch {
		case errors.Is(err, strconv.ErrRange):
			return resultErrEnum(resultType, "Overflow", pos)
		case errors.Is(err, strconv.ErrSyntax):
			return resultErrEnum(resultType, "InvalidDigit", pos)
		}
		return resultErrEnum(resultType, "InvalidDigit", pos)
	}
	return resultOk(resultType, intVal(n), pos)
}

// --- std/math -------------------------------------------------------------

func execMathAbs(xV Value) (Value, error) {
	x := xV.Int
	if x < 0 {
		x = -x
	}
	return intVal(x), nil
}

func execMathMin(aV, bV Value) (Value, error) {
	if aV.Int < bV.Int {
		return intVal(aV.Int), nil
	}
	return intVal(bV.Int), nil
}

func execMathMax(aV, bV Value) (Value, error) {
	if aV.Int > bV.Int {
		return intVal(aV.Int), nil
	}
	return intVal(bV.Int), nil
}

func execMathGcd(aV, bV Value) (Value, error) {
	a, b := aV.Int, bV.Int
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return intVal(a), nil
}

// --- std/os ---------------------------------------------------------------

func execOsEnv(nameV Value, resultType *syntax.Type, pos syntax.Position) (Value, error) {
	v, ok := os.LookupEnv(nameV.Str)
	if !ok {
		return optionNone(resultType, pos)
	}
	return optionSome(resultType, strVal(v), pos)
}
