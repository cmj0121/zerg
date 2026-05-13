package syntax

// v0.9 Unit 2 — extends the v0.8 closed __builtin registry with the std/time
// signatures. The registry is consulted at typeck time by validateBuiltinFnDecl
// in typeck_v08_builtin.go. We register the v0.9 entries via init() so the
// existing single-map lookup keeps working without a v0.8 / v0.9 fork.

func init() {
	// std/time primitives. v0.14 retired the coupled time_now_ms /
	// time_sleep_ms shims in favour of two atomic libc-backed primitives
	// — the epoch-zero-on-first-call, ms math, EINTR loop, and negative
	// clamp now live in src/std/time.zg over module-level mut state
	// (introduced by the v0.14 P1 module-init landing).
	v08BuiltinRegistry["time_clock_us"] = v08BuiltinSig{params: nil, ret: "int"}
	v08BuiltinRegistry["time_sleep_ns"] = v08BuiltinSig{params: []string{"int", "int"}, ret: "int"}
	// std/os primitives. v0.14 retired the coupled os_env / os_argv shims
	// in favour of pair-of-accessor primitives — argv length + per-index,
	// envp length + per-index. The user-facing env / argv / exit live in
	// pure-Zerg src/std/os.zg over these primitives plus sys.syscall.exit.
	v08BuiltinRegistry["os_argv_len"] = v08BuiltinSig{params: nil, ret: "int"}
	v08BuiltinRegistry["os_argv_at"] = v08BuiltinSig{params: []string{"int"}, ret: "str"}
	v08BuiltinRegistry["os_envp_len"] = v08BuiltinSig{params: nil, ret: "int"}
	v08BuiltinRegistry["os_envp_at"] = v08BuiltinSig{params: []string{"int"}, ret: "str"}
}
