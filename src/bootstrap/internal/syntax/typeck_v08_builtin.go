package syntax

import "strings"

// v0.8 Unit 2 — closed registry of `__builtin <ident>` names.
//
// The parser admits any bareword after `__builtin`; typeck validates the name
// against this fixed table at FnDecl-encounter time. Two cases reject:
//
//   - The name is not in the registry. Surface "unknown builtin '<name>'"
//     anchored on FnDecl.BuiltinNamePos.
//   - The name is in the registry but the surrounding fn-decl's signature
//     does not match the registry's expected shape. Surface a focused
//     "builtin '<name>' signature mismatch: expected ..., got ..." anchored
//     on the same position.
//
// The "expected shape" is recorded structurally as the source-form
// stringification of each parameter type plus the return type. The check
// runs against the FnDecl's TypeRef.String() output — same renderer used by
// every other v0.6+ diagnostic — so the comparison is byte-equal between
// what the user wrote in std/<m>.zg and what the registry expects.
//
// Closed set: 15 entries spanning std/strings, std/math, std/os. std/io
// retired into pure Zerg at v0.14 (over the sys/syscall layer); its
// __builtin entries left this registry alongside the runtime helpers.
// PLAN.md §"In scope (v0.8)" enumerates the wire shapes; this file pins
// them in code so any drift between the table and the std/<m>.zg sources
// fails at typeck.

// v08BuiltinSig is the expected shape of one __builtin entry. Params and Ret
// are TypeRef.String() forms. Ret is empty when the registry entry returns
// no value (none today, but the field admits void to keep the type honest).
type v08BuiltinSig struct {
	params []string
	ret    string
}

// v08BuiltinRegistry maps a __builtin bareword to its expected fn-decl
// signature. Closed set per PLAN.md §"In scope (v0.8)".
var v08BuiltinRegistry = map[string]v08BuiltinSig{
	// std/strings
	"strings_split":       {params: []string{"str", "str"}, ret: "list[str]"},
	"strings_join":        {params: []string{"list[str]", "str"}, ret: "str"},
	"strings_trim":        {params: []string{"str"}, ret: "str"},
	"strings_starts_with": {params: []string{"str", "str"}, ret: "bool"},
	"strings_ends_with":   {params: []string{"str", "str"}, ret: "bool"},
	"strings_contains":    {params: []string{"str", "str"}, ret: "bool"},
	"strings_replace":     {params: []string{"str", "str", "str"}, ret: "str"},
	"strings_to_upper":    {params: []string{"str"}, ret: "str"},
	"strings_to_lower":    {params: []string{"str"}, ret: "str"},
	"strings_parse_int":   {params: []string{"str"}, ret: "Result[int, ParseError]"},

	// std/math
	"math_abs": {params: []string{"int"}, ret: "int"},
	"math_min": {params: []string{"int", "int"}, ret: "int"},
	"math_max": {params: []string{"int", "int"}, ret: "int"},
	"math_gcd": {params: []string{"int", "int"}, ret: "int"},

	// std/os
	"os_env": {params: []string{"str"}, ret: "Option[str]"},
}

// validateBuiltinFnDecl runs the v0.8 builtin-registry checks against fn.
// Caller must ensure fn.BuiltinName != "". Defensive: returns nil quickly if
// the precondition is violated so callers can be unconditional.
func validateBuiltinFnDecl(fn *FnDecl) error {
	if fn == nil || fn.BuiltinName == "" {
		return nil
	}
	if fn.Body != nil {
		return typeErr(fn.Pos,
			"internal: __builtin fn %q has a body block", fn.Name)
	}
	expected, ok := v08BuiltinRegistry[fn.BuiltinName]
	if !ok {
		return typeErr(fn.BuiltinNamePos,
			"unknown builtin %q", fn.BuiltinName)
	}
	got := builtinFnSigString(fn)
	want := expectedSigString(expected)
	if got != want {
		return typeErr(fn.BuiltinNamePos,
			"builtin %q signature mismatch: expected %s, got %s",
			fn.BuiltinName, want, got)
	}
	return nil
}

// builtinFnSigString renders fn's declared signature as
// "(p1, p2, ...) -> R" using the existing TypeRef.String() form. Void
// returns elide the `-> R` tail so the rendering matches expectedSigString
// for that case.
func builtinFnSigString(fn *FnDecl) string {
	var b strings.Builder
	b.WriteString("(")
	for i, p := range fn.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Type.String())
	}
	b.WriteString(")")
	if fn.Return != nil {
		b.WriteString(" -> ")
		b.WriteString(fn.Return.String())
	}
	return b.String()
}

// expectedSigString renders one registry entry into the same surface form as
// builtinFnSigString so a byte-equal compare validates the shape.
func expectedSigString(sig v08BuiltinSig) string {
	var b strings.Builder
	b.WriteString("(")
	for i, p := range sig.params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p)
	}
	b.WriteString(")")
	if sig.ret != "" {
		b.WriteString(" -> ")
		b.WriteString(sig.ret)
	}
	return b.String()
}
