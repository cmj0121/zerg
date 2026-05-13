package run

import "fmt"

// v0.9 Unit 1 — exitErr sentinel and RunBundle recover hook.
//
// `os.exit(code)` (Unit 3) panics an exitErr through every fn-call frame to
// the top-level RunBundle boundary. RunBundle recovers, surfaces the code as
// the program's exit code, and returns nil so the host process can decide
// what to do (CLI driver: os.Exit; REPL: print "process exited with code N"
// and keep running).
//
// **Defer × exit deviation (intentional, PLAN.md §"Defer × exit"):** v0.7
// guarantees deferred actions drain on every fn-exit path INCLUDING `?`
// propagation. v0.9's exit() is an explicit deviation — the exitErr panic
// SKIPS deferred-stack drainage on its way up, matching Go's `os.Exit`
// semantics. Spawned tasks are NOT joined either; the WaitGroup wait at the
// top-level main is bypassed. User code that needs cleanup before exit must
// invoke it explicitly.
//
// Unit 1 stages the sentinel and the recover hook. The exit fn that raises
// the sentinel lands in Unit 3.

// exitErr is the sentinel an exit-style call panics. Code holds the
// requested exit code. sysExitShim raises this from syscall.exit;
// the RunBundle recover hook catches it at the top-level boundary.
type exitErr struct {
	Code int
}

func (e exitErr) Error() string {
	return fmt.Sprintf("process exited with code %d", e.Code)
}

// catchExit recovers from a panic carrying an exitErr value. Returns the
// recovered exitErr and true when the panic value matches; returns the zero
// exitErr and false otherwise. A non-nil non-exitErr panic is re-raised so
// genuine bugs still surface.
//
// Callers wrap the top-level body in:
//
//	defer func() {
//	    if r := recover(); r != nil {
//	        if ee, ok := r.(exitErr); ok {
//	            // record ee.Code
//	            return
//	        }
//	        panic(r)
//	    }
//	}()
func catchExit(r any) (exitErr, bool) {
	if r == nil {
		return exitErr{}, false
	}
	if ee, ok := r.(exitErr); ok {
		return ee, true
	}
	return exitErr{}, false
}
