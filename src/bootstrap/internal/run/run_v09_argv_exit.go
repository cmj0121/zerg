package run

import (
	"os"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// v0.9 Unit 3 — interpreter dispatch for std/os accessor primitives.
//
// v0.14 T2 retired the coupled os_argv / os_env / os_exit shims. The
// remaining surface is four atomic accessor primitives consumed by the
// pure-Zerg src/std/os.zg layer; exit is now a pure-Zerg wrapper over
// sys.syscall.exit and routes through the existing syscall intrinsic.
//
//   os_argv_len  → len(in.argv)
//   os_argv_at   → strVal(in.argv[i])
//   os_envp_len  → len(in.envp())
//   os_envp_at   → strVal(in.envp()[i])
//
// envp() lazy-caches os.Environ() on the interp struct so envp_len and
// envp_at observe the same index space across a single env() loop,
// even if a test changes the host env between separate interpreter
// runs. The cache is per-interpreter, not process-global, so each
// test run sees a fresh snapshot tied to its own RunBundle invocation.

func (in *interp) envp() []string {
	if in.envpCache == nil {
		in.envpCache = os.Environ()
	}
	return in.envpCache
}

// callBuiltinV09ArgvExit dispatches the v0.9 std/os primitives. Returns
// (value, true, nil) when handled; (_, false, nil) when the name is not
// one of ours so the caller continues to the time / v0.8 tables.
func (in *interp) callBuiltinV09ArgvExit(fn *syntax.FnDecl, args []Value, resultType *syntax.Type) (Value, bool, error) {
	switch fn.BuiltinName {
	case "os_argv_len":
		return intVal(int64(len(in.argv))), true, nil
	case "os_argv_at":
		return strVal(in.argv[args[0].Int]), true, nil
	case "os_envp_len":
		return intVal(int64(len(in.envp()))), true, nil
	case "os_envp_at":
		return strVal(in.envp()[args[0].Int]), true, nil
	}
	return Value{}, false, nil
}
