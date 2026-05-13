package run

import (
	"os"
	"strings"
	"syscall"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// run_v14_syscall.go — interpreter parity bridge for sys/syscall.
//
// The wrapper fns in src/std/sys/syscall/mod_<host>.zg consist of a
// single `asm { svc #0x80 ... }` block, which the interpreter cannot
// execute (pin 6). This file's intrinsic dispatch fires BEFORE the body
// walk in callFn: when the callee is a fn from a sys/syscall module
// and the name matches a known wrapper, the corresponding Go shim is
// invoked against the host's `syscall` package. The shim returns a
// signed int with the same convention the asm wrappers expose: >=0 on
// success, -errno on error. The build half stays untouched — cgen
// emits real svc traps; only the run half uses these shims.
//
// Mutation semantics matter for read(): the wrapper takes a list[byte]
// buffer and writes incoming bytes into its storage. Zerg's borrow
// model normally rejects mutation through a shared composite arg, but
// the asm body bypasses the borrow pass entirely; the interpreter's
// args sharing the same backing slice as the caller's binding means
// in-place writes in sysReadShim are visible to the caller.

// invokeSysSyscallIntrinsic checks whether fn is one of the sys/syscall
// wrapper fns. If so, the matching host shim runs and (value, true,
// err) is returned; callFn must then skip the body walk. Returning
// (Value{}, false, nil) means "this fn is not a syscall wrapper —
// proceed with the body walk."
func (in *interp) invokeSysSyscallIntrinsic(fn *syntax.FnDecl, args []Value) (Value, bool, error) {
	owner := in.fnOwner[fn]
	if owner == nil {
		return Value{}, false, nil
	}
	// The loader stamps Module.Name with the .zg file path. Disk-served
	// modules carry an absolute path (e.g. /abs/.../sys/syscall/mod_*.zg)
	// while embed-served modules carry a virtual path (sys/syscall/mod_*.zg).
	// Both contain the "sys/syscall/" segment; user files would only hit
	// this match if their layout AND a fn name happen to coincide, which
	// the intrinsic dispatch's name switch below makes practically
	// impossible.
	if !strings.Contains(owner.name, "sys/syscall/") {
		return Value{}, false, nil
	}
	switch fn.Name {
	case "write":
		return sysWriteShim(args), true, nil
	case "read":
		return sysReadShim(args), true, nil
	case "open":
		return sysOpenShim(args), true, nil
	case "close_fd":
		return sysCloseFdShim(args), true, nil
	case "exit":
		return sysExitShim(args), true, nil
	}
	return Value{}, false, nil
}

// errnoResult turns a Go syscall error into the wrapper's signed-errno
// convention. A nil error yields the successful byte count / fd / 0 in
// `n`; a syscall.Errno yields the negative magnitude.
func errnoResult(n int, err error) Value {
	if err == nil {
		return intVal(int64(n))
	}
	if errno, ok := err.(syscall.Errno); ok {
		return intVal(-int64(errno))
	}
	return intVal(-int64(syscall.EIO))
}

// sysWriteShim implements write(fd: int, buf: list[byte], len: int).
// The buffer's first `len` bytes are copied into a Go []byte and
// handed to syscall.Write so the kernel writes EXACTLY `len` bytes
// (the wrapper's contract — the buf is allowed to be longer).
func sysWriteShim(args []Value) Value {
	fd := int(args[0].Int)
	buf := args[1].List
	n := int(args[2].Int)
	if n > len(buf) {
		n = len(buf)
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = byte(buf[i].Int)
	}
	wrote, err := syscall.Write(fd, out)
	return errnoResult(wrote, err)
}

// sysReadShim implements read(fd: int, buf: list[byte], cap: int). The
// buffer must already hold at least `cap` entries; the shim reads up
// to `cap` bytes into a scratch slice and writes them back into the
// first `got` positions of buf.List so the caller's binding observes
// the kernel-supplied bytes.
func sysReadShim(args []Value) Value {
	fd := int(args[0].Int)
	buf := args[1].List
	capN := int(args[2].Int)
	if capN > len(buf) {
		capN = len(buf)
	}
	scratch := make([]byte, capN)
	got, err := syscall.Read(fd, scratch)
	if err != nil {
		return errnoResult(0, err)
	}
	for i := 0; i < got; i++ {
		buf[i] = byteVal(int64(scratch[i]))
	}
	return intVal(int64(got))
}

// sysOpenShim implements open(path: list[byte], flags: int, mode: int).
// The wrapper's contract requires the caller to NUL-terminate the
// path; the shim strips that trailing byte before handing the slice
// to syscall.Open (Go's wrapper adds its own terminator internally).
func sysOpenShim(args []Value) Value {
	path := args[0].List
	flags := int(args[1].Int)
	mode := uint32(args[2].Int)
	end := len(path)
	if end > 0 && path[end-1].Int == 0 {
		end--
	}
	pathStr := make([]byte, end)
	for i := 0; i < end; i++ {
		pathStr[i] = byte(path[i].Int)
	}
	fd, err := syscall.Open(string(pathStr), flags, mode)
	return errnoResult(fd, err)
}

// sysCloseFdShim implements close_fd(fd: int). Returns 0 on success
// or -errno on error, matching the asm wrapper.
func sysCloseFdShim(args []Value) Value {
	fd := int(args[0].Int)
	return errnoResult(0, syscall.Close(fd))
}

// sysExitShim implements exit(code: int) -> never. The asm wrapper
// traps SYS_exit which terminates the process; the interpreter parity
// is os.Exit (skips deferred funcs, exits immediately with the given
// status). The Value return is unreachable but typed correctly so the
// caller's coerce-to-never path is structurally consistent.
func sysExitShim(args []Value) Value {
	os.Exit(int(args[0].Int))
	return intVal(0)
}
