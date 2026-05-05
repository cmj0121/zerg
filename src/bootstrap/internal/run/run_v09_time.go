package run

// v0.9 Unit 2 — interpreter dispatch for std/time builtins.
//
// time_now_ms: lazy-zero-on-first-call epoch. The first call captures
// time.Now() into a process-global, mutex-guarded so the v0.7-spawn case
// (multiple goroutines) still produces a single deterministic epoch.
// First call returns 0; subsequent calls return ms-since-epoch.
//
// time_sleep_ms: blocks at least ms ms; returns true. Negative ms clamps
// to zero (no error).

import (
	"sync"
	"time"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// timeFirstCall is the process-global monotonic epoch. The zero-value
// time.Time tested via IsZero is the "uninitialised" sentinel. timeFirstCallMu
// guards the lazy-init transition; once set, reads are race-tolerant under
// the std-library guarantees but the mutex keeps the first-call init
// well-defined under a v0.7 spawn fan-out.
var (
	timeFirstCallMu sync.Mutex
	timeFirstCall   time.Time
)

func execTimeNowMs() (Value, error) {
	timeFirstCallMu.Lock()
	if timeFirstCall.IsZero() {
		timeFirstCall = time.Now()
		timeFirstCallMu.Unlock()
		return intVal(0), nil
	}
	epoch := timeFirstCall
	timeFirstCallMu.Unlock()
	return intVal(time.Since(epoch).Milliseconds()), nil
}

func execTimeSleepMs(msV Value) (Value, error) {
	ms := msV.Int
	if ms <= 0 {
		return boolVal(true), nil
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return boolVal(true), nil
}

// callBuiltinV09 dispatches the v0.9-introduced __builtin names. Returns
// (value, true, nil) when handled, (_, false, nil) when the name is not a
// v0.9 builtin so the caller can fall through to v0.8 / older tables.
func callBuiltinV09(fn *syntax.FnDecl, args []Value) (Value, bool, error) {
	switch fn.BuiltinName {
	case "time_now_ms":
		v, err := execTimeNowMs()
		return v, true, err
	case "time_sleep_ms":
		v, err := execTimeSleepMs(args[0])
		return v, true, err
	}
	return Value{}, false, nil
}
