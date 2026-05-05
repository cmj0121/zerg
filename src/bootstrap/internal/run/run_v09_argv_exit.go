package run

import (
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// v0.9 Unit 3 — interpreter dispatch for std/os.argv and std/os.exit.
//
// os_argv: returns a list[str] built from the host-supplied argv. The
// list type is recovered from the call expression's typeck-stamped Type
// (resultType) so the value compares equal to other list[str] values
// the user constructs.
//
// os_exit: panics exitErr{Code:n}. Unit 1's recover hook at RunBundle
// catches it and surfaces the code via Options-returning entry. The fn
// is declared `-> never`; the panic ensures no value is ever returned to
// the caller, matching the type contract.

func (in *interp) execOsArgv(resultType *syntax.Type) (Value, error) {
	out := make([]Value, len(in.argv))
	for i, s := range in.argv {
		out[i] = strVal(s)
	}
	if resultType != nil && resultType.Kind == syntax.TypeList && resultType.Element != nil {
		return Value{Type: resultType, List: out}, nil
	}
	return listVal(syntax.TStr(), out), nil
}

func execOsExit(codeV Value) (Value, error) {
	panic(exitErr{Code: int(codeV.Int)})
}

// callBuiltinV09ArgvExit dispatches the v0.9 Unit 3 builtins. Returns
// (value, true, nil) when handled; (_, false, nil) when the name is not
// one of ours so the caller continues to the time / v0.8 tables.
func (in *interp) callBuiltinV09ArgvExit(fn *syntax.FnDecl, args []Value, resultType *syntax.Type) (Value, bool, error) {
	switch fn.BuiltinName {
	case "os_argv":
		v, err := in.execOsArgv(resultType)
		return v, true, err
	case "os_exit":
		v, err := execOsExit(args[0])
		return v, true, err
	}
	return Value{}, false, nil
}
