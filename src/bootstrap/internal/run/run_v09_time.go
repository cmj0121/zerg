package run

// v0.9 Unit 2 — interpreter dispatch for std/time atomic primitives.
//
// time_clock_us: returns walltime microseconds since the UNIX epoch
// (time.Now().UnixMicro on the Go side; clock_gettime CLOCK_REALTIME
// on the cgen side). No state — the epoch-zero-on-first-call contract
// lives in src/std/time.zg's now_ms over P1 module-level mut.
//
// time_sleep_ns: blocks for sec seconds + nsec nanoseconds. Negative
// inputs return -EINVAL to match the cgen half; src/std/time.zg's
// sleep_ms clamps non-positive ms before calling here so this path
// only fires for valid durations.

import (
	"syscall"
	"time"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

func execTimeClockUs() (Value, error) {
	return intVal(time.Now().UnixMicro()), nil
}

func execTimeSleepNs(secV, nsecV Value) (Value, error) {
	sec := secV.Int
	nsec := nsecV.Int
	if sec < 0 || nsec < 0 {
		return intVal(-int64(syscall.EINVAL)), nil
	}
	time.Sleep(time.Duration(sec)*time.Second + time.Duration(nsec)*time.Nanosecond)
	return intVal(0), nil
}

// callBuiltinV09 dispatches the v0.9-introduced __builtin names. Returns
// (value, true, nil) when handled, (_, false, nil) when the name is not a
// v0.9 builtin so the caller can fall through to v0.8 / older tables.
func callBuiltinV09(fn *syntax.FnDecl, args []Value) (Value, bool, error) {
	switch fn.BuiltinName {
	case "time_clock_us":
		v, err := execTimeClockUs()
		return v, true, err
	case "time_sleep_ns":
		v, err := execTimeSleepNs(args[0], args[1])
		return v, true, err
	}
	return Value{}, false, nil
}
