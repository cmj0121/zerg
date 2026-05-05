package syntax

// v0.9 Unit 2 — extends the v0.8 closed __builtin registry with the std/time
// signatures. The registry is consulted at typeck time by validateBuiltinFnDecl
// in typeck_v08_builtin.go. We register the v0.9 entries via init() so the
// existing single-map lookup keeps working without a v0.8 / v0.9 fork.

func init() {
	v08BuiltinRegistry["time_now_ms"] = v08BuiltinSig{params: nil, ret: "int"}
	v08BuiltinRegistry["time_sleep_ms"] = v08BuiltinSig{params: []string{"int"}, ret: "bool"}
	v08BuiltinRegistry["os_argv"] = v08BuiltinSig{params: nil, ret: "list[str]"}
	v08BuiltinRegistry["os_exit"] = v08BuiltinSig{params: []string{"int"}, ret: "never"}
}
