// v0.8 Unit 4 — codegen for the toolchain-shipped `__builtin` host
// primitives in std/io, std/strings, std/math, std/os.
//
// Two halves stay in lock-step. The interpreter (run_v08.go) routes each
// __builtin fn-decl call to a per-name dispatch table; cgen here emits a
// trampoline body for every __builtin fn-decl encountered in the typed
// AST. The trampoline forwards to a small embedded C runtime (runtimeV08C
// below) that does the actual host syscall and returns a small struct
// of (tag, value) — the runtime stays decoupled from the user-program's
// view of the result-enum mangle. The trampoline body translates the
// runtime's tag-discriminated struct into the user-program's Result /
// Option enum value, constructed against fn.Return.Resolved (the
// canonical *Type the typeck stamped).
//
// Bucket rule (matches run_v08.go's bucketIoError):
//   ENOENT  -> NotFound
//   EACCES  -> PermissionDenied
//   EEXIST  -> AlreadyExists
//   EINVAL  -> InvalidPath
//   default -> Other
//
// parse_int buckets:
//   ErrSyntax  -> InvalidDigit  (any non-digit, after-trim non-empty)
//   ErrRange   -> Overflow
//   empty (after trim) -> Empty
//
// programUsesV08 mirrors programUsesV07: any reachable fn-call whose
// callee resolves to a FnDecl with BuiltinName!="" flips the gate.
//
// list[str] return shape: strings_split returns list[str]. The shape
// is collected through the existing collectShapes path because every
// FnDecl's param/return type is registered via collectStmt's FnDecl
// branch — the std/* modules' fn-decls land in their prog.Statements
// the same way user fns do, so no force-monomorphisation walker is
// needed at v0.8.

package build

import (
	"fmt"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// programUsesV08 reports whether any module in the bundle references a
// v0.8 stdlib builtin. The result gates emission of the runtimeV08C
// prelude so v0.0-v0.7 programs continue to compile to byte-identical
// output. A reference is any fn-call whose callee resolves to an FnDecl
// carrying BuiltinName!="" — which the loader/typeck only admits for
// std/* modules.
func (g *cgen) programUsesV08() bool {
	for i := range g.modules {
		if g.programUsesV08Walk(g.modules[i].prog) {
			return true
		}
	}
	return false
}

func (g *cgen) programUsesV08Walk(prog *syntax.Program) bool {
	if prog == nil {
		return false
	}
	found := false
	// Build a per-module fn lookup for the call-site resolver. The walker
	// has access to every module's FnDecl set via g.modules so cross-module
	// calls (`io.read_file(p)`) resolve to the foreign FnDecl with
	// BuiltinName!="".
	resolveCall := func(callee syntax.Expr) *syntax.FnDecl {
		switch c := callee.(type) {
		case *syntax.IdentExpr:
			// Same-module call.
			return g.lookupFnByName(c.Name, prog)
		}
		return nil
	}
	resolveMethodCall := func(e *syntax.MethodCallExpr) *syntax.FnDecl {
		// Cross-module fn call: receiver is an IdentExpr that aliases an
		// imported module; method name is the foreign fn.
		id, ok := e.Receiver.(*syntax.IdentExpr)
		if !ok {
			return nil
		}
		// Find the host module to resolve the import alias.
		host := g.findModuleForProg(prog)
		if host == nil {
			return nil
		}
		foreignMangle, ok := host.imports[id.Name]
		if !ok {
			return nil
		}
		return g.lookupModuleFn(foreignMangle, e.Method)
	}
	var walkE func(syntax.Expr)
	var walkS func(syntax.Stmt)
	walkE = func(e syntax.Expr) {
		if e == nil || found {
			return
		}
		switch x := e.(type) {
		case *syntax.CallExpr:
			if fn := resolveCall(x.Callee); fn != nil && fn.BuiltinName != "" {
				found = true
				return
			}
			walkE(x.Callee)
			for _, a := range x.Args {
				walkE(a)
			}
		case *syntax.MethodCallExpr:
			if fn := resolveMethodCall(x); fn != nil && fn.BuiltinName != "" {
				found = true
				return
			}
			walkE(x.Receiver)
			for _, a := range x.Args {
				walkE(a)
			}
			if x.Lowered != nil {
				walkE(x.Lowered)
			}
			if x.LoweredCall != nil {
				walkE(x.LoweredCall)
			}
		case *syntax.UnaryExpr:
			walkE(x.Operand)
		case *syntax.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.ParenExpr:
			walkE(x.Inner)
		case *syntax.IndexExpr:
			walkE(x.Receiver)
			walkE(x.Index)
		case *syntax.FieldAccessExpr:
			walkE(x.Receiver)
			if x.Lowered != nil {
				walkE(x.Lowered)
			}
		case *syntax.ListLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.TupleLit:
			for _, sub := range x.Elements {
				walkE(sub)
			}
		case *syntax.StructLit:
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *syntax.EnumLit:
			for _, sub := range x.Payload {
				walkE(sub)
			}
		case *syntax.PropagateExpr:
			walkE(x.Inner)
		case *syntax.CoalesceExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *syntax.AnonFnExpr:
			walkBlock(x.Body, walkS)
		}
	}
	walkS = func(s syntax.Stmt) {
		if s == nil || found {
			return
		}
		switch n := s.(type) {
		case *syntax.PrintStmt:
			walkE(n.Expr)
		case *syntax.ExprStmt:
			walkE(n.Expr)
		case *syntax.LetStmt:
			walkE(n.Value)
		case *syntax.MutStmt:
			walkE(n.Value)
		case *syntax.ConstStmt:
			walkE(n.Value)
		case *syntax.AssignStmt:
			walkE(n.Target)
			walkE(n.Value)
		case *syntax.MultiAssignStmt:
			for _, t := range n.Targets {
				walkE(t)
			}
			walkE(n.Value)
		case *syntax.IfStmt:
			walkE(n.Cond)
			walkBlock(n.Then, walkS)
			for _, ec := range n.Elifs {
				walkE(ec.Cond)
				walkBlock(ec.Body, walkS)
			}
			if n.Else != nil {
				walkBlock(n.Else, walkS)
			}
		case *syntax.ForStmt:
			if n.Iter != nil {
				walkE(n.Iter)
			}
			if n.Cond != nil {
				walkE(n.Cond)
			}
			if n.Range != nil {
				walkE(n.Range.Start)
				walkE(n.Range.End)
			}
			walkBlock(n.Body, walkS)
		case *syntax.ReturnStmt:
			if n.Value != nil {
				walkE(n.Value)
			}
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.MatchStmt:
			walkE(n.Subject)
			for _, arm := range n.Arms {
				if arm.Guard != nil {
					walkE(arm.Guard)
				}
				walkBlock(arm.Body, walkS)
			}
		case *syntax.FnDecl:
			walkBlock(n.Body, walkS)
		case *syntax.SpawnStmt:
			walkE(n.Call)
		case *syntax.SendStmt:
			walkE(n.Chan)
			walkE(n.Value)
		case *syntax.DeferStmt:
			walkBlock(n.Body, walkS)
		case *syntax.SelectStmt:
			for _, arm := range n.Arms {
				if arm.Chan != nil {
					walkE(arm.Chan)
				}
				if arm.Value != nil {
					walkE(arm.Value)
				}
				walkBlock(arm.Body, walkS)
			}
		case *syntax.BreakStmt:
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.ContinueStmt:
			if n.Guard != nil {
				walkE(n.Guard)
			}
		case *syntax.ImplDecl:
			for _, m := range n.Methods {
				if m != nil {
					walkBlock(m.Body, walkS)
				}
			}
		}
	}
	for _, st := range prog.Statements {
		walkS(st)
		if found {
			return true
		}
	}
	for _, fn := range prog.MonoFns {
		if fn == nil || found {
			continue
		}
		walkBlock(fn.Body, walkS)
	}
	for _, im := range prog.MonoImpls {
		if im == nil || found {
			continue
		}
		for _, m := range im.Methods {
			if m != nil {
				walkBlock(m.Body, walkS)
			}
		}
	}
	return found
}

// listOfStrType returns a synthetic *Type for list[str]. Used by the v0.8
// pre-pass to force-monomorphise the shape so the strings_split runtime
// helper can reference zerg_list_zerg_str regardless of whether user code
// literal-constructs a list[str].
func listOfStrType() *syntax.Type {
	return &syntax.Type{
		Kind:    syntax.TypeList,
		Element: syntax.TStr(),
	}
}

// findModuleForProg returns the moduleEmit whose prog pointer matches the
// argument. Used by programUsesV08Walk to recover a host module's import
// table from inside the per-module walker.
func (g *cgen) findModuleForProg(prog *syntax.Program) *moduleEmit {
	for i := range g.modules {
		if g.modules[i].prog == prog {
			return &g.modules[i]
		}
	}
	return nil
}

// lookupFnByName scans prog's top-level statements for a FnDecl with the
// given name. Mirrors lookupCurrentFn but for an arbitrary program — the
// programUsesV08 walker visits every module and needs name resolution
// scoped to each.
func (g *cgen) lookupFnByName(name string, prog *syntax.Program) *syntax.FnDecl {
	if prog == nil {
		return nil
	}
	for _, stmt := range prog.Statements {
		if fn, ok := stmt.(*syntax.FnDecl); ok && fn.Name == name {
			return fn
		}
	}
	return nil
}

// emitBuiltinFn writes a trampoline body for fn (whose BuiltinName is
// non-empty). The body forwards to the runtime helper and wraps the
// result into the user-program's view of the return type. Per-builtin
// dispatch.
func (g *cgen) emitBuiltinFn(fn *syntax.FnDecl) error {
	g.writeFnSig(fn)
	g.b.WriteString(" {\n")
	prevMod := g.currentMod
	if owner, ok := g.fnOwner[fn]; ok {
		g.currentMod = owner
	}
	defer func() { g.currentMod = prevMod }()

	body, err := g.builtinBodyStr(fn)
	if err != nil {
		return err
	}
	g.b.WriteString(body)
	g.b.WriteString("}\n")
	return nil
}

// builtinBodyStr returns the C body (statements only — no surrounding
// braces) for a __builtin fn-decl. Each branch dispatches to the
// runtime helper and constructs the user-program's return type.
func (g *cgen) builtinBodyStr(fn *syntax.FnDecl) (string, error) {
	// v0.9 time builtins forward to the v09 emitter.
	if body, ok := emitV09TimeBuiltinBody(fn.BuiltinName); ok {
		return body, nil
	}
	// v0.9 Unit 3 argv / exit builtins.
	if body, ok := emitV09ArgvExitBuiltinBody(fn.BuiltinName); ok {
		return body, nil
	}
	retT := fn.Return.Resolved
	var b strings.Builder
	switch fn.BuiltinName {
	// std/strings — non-fallible except parse_int.
	case "strings_split":
		// list[str] return; runtime returns the list directly.
		fmt.Fprintf(&b, "    return zerg_strings_split(z_s, z_sep);\n")
	case "strings_join":
		fmt.Fprintf(&b, "    return zerg_strings_join(z_parts, z_sep);\n")
	case "strings_trim":
		fmt.Fprintf(&b, "    return zerg_strings_trim(z_s);\n")
	case "strings_starts_with":
		fmt.Fprintf(&b, "    return zerg_strings_starts_with(z_s, z_prefix);\n")
	case "strings_ends_with":
		fmt.Fprintf(&b, "    return zerg_strings_ends_with(z_s, z_suffix);\n")
	case "strings_contains":
		fmt.Fprintf(&b, "    return zerg_strings_contains(z_s, z_needle);\n")
	case "strings_replace":
		fmt.Fprintf(&b, "    return zerg_strings_replace(z_s, z_old, z_new);\n")
	case "strings_to_upper":
		fmt.Fprintf(&b, "    return zerg_strings_to_upper(z_s);\n")
	case "strings_to_lower":
		fmt.Fprintf(&b, "    return zerg_strings_to_lower(z_s);\n")
	case "strings_parse_int":
		emitParseIntResult(&b, g, retT)

	// std/math.
	case "math_abs":
		fmt.Fprintf(&b, "    return zerg_math_abs(z_x);\n")
	case "math_min":
		fmt.Fprintf(&b, "    return zerg_math_min(z_a, z_b);\n")
	case "math_max":
		fmt.Fprintf(&b, "    return zerg_math_max(z_a, z_b);\n")
	case "math_gcd":
		fmt.Fprintf(&b, "    return zerg_math_gcd(z_a, z_b);\n")

	default:
		return "", fmt.Errorf("codegen: unknown __builtin %q", fn.BuiltinName)
	}
	return b.String(), nil
}

// emitParseIntResult writes the body for strings_parse_int.
func emitParseIntResult(b *strings.Builder, g *cgen, retT *syntax.Type) {
	resMang := g.mangleType(retT)
	errT, errMang := resultErrEnumInfo(g, retT)
	okIdx := variantIndex(retT, "Ok")
	errIdx := variantIndex(retT, "Err")
	fmt.Fprintf(b, "    zerg_parse_int_result __r = zerg_strings_parse_int(z_s);\n")
	fmt.Fprintf(b, "    if (__r.tag == 0) {\n")
	fmt.Fprintf(b, "        return ((%s){.tag = %d, .payload.p%d = {.a0 = __r.value}});\n",
		resMang, okIdx, okIdx)
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    int __v = %s;\n", parseErrTagToVariantSwitch(errT))
	fmt.Fprintf(b, "    return ((%s){.tag = %d, .payload.p%d = {.a0 = ((%s){.tag = __v})}});\n",
		resMang, errIdx, errIdx, errMang)
}

// resultErrEnumInfo extracts the user-defined error enum carried in the
// Err payload of resultType (Result[T, E].VariantPayloads[1][0]) and
// returns its canonical *Type plus mangled C name.
func resultErrEnumInfo(g *cgen, resultType *syntax.Type) (*syntax.Type, string) {
	if resultType == nil || resultType.Kind != syntax.TypeEnum {
		return nil, ""
	}
	if len(resultType.VariantPayloads) < 2 || len(resultType.VariantPayloads[1]) != 1 {
		return nil, ""
	}
	errT := resultType.VariantPayloads[1][0]
	return errT, g.mangleType(errT)
}

// parseErrTagToVariantSwitch returns a C ternary chain mapping the
// runtime's ParseError tag (1..3) to errT's variant index.
//   1=Empty, 2=InvalidDigit, 3=Overflow.
func parseErrTagToVariantSwitch(errT *syntax.Type) string {
	if errT == nil {
		return "0"
	}
	tagToName := map[int]string{
		1: "Empty",
		2: "InvalidDigit",
		3: "Overflow",
	}
	var b strings.Builder
	b.WriteString("(")
	first := true
	for tag := 1; tag <= 3; tag++ {
		idx := variantIndex(errT, tagToName[tag])
		if idx < 0 {
			idx = 0
		}
		if first {
			fmt.Fprintf(&b, "__r.tag == %d ? %d", tag, idx)
			first = false
		} else {
			fmt.Fprintf(&b, " : __r.tag == %d ? %d", tag, idx)
		}
	}
	fmt.Fprintf(&b, " : 0)")
	return b.String()
}

// runtimeV08C is the embedded C runtime for v0.8 stdlib builtins. Gated
// on programUsesV08; the v0.0-v0.7 emit stays byte-identical when no
// builtin is referenced.
//
// The runtime defines a small set of {tag, value} structs so the runtime
// stays decoupled from the user-program enum mangle. Trampolines emitted
// by emitBuiltinFn translate the tag-discriminated struct into the
// user-program's Result / Option enum value.
//
// Bucket rules match run_v08.go's Go-side bucketing exactly so the parity
// corpus matches by variant identity, not error text.
const runtimeV08C = `#include <errno.h>
#include <ctype.h>

/* ---------------- v0.8 stdlib runtime ----------------------------------- */

typedef struct { int tag; int64_t value; } zerg_parse_int_result;

/* ---------------- strings ----------------------------------------------- */

/* zerg_strings_split: splits s on sep. Empty sep panics (matches the
   interpreter's runtime panic). The returned list is the same shape cgen
   would produce for a list[str] literal: zerg_list_zerg_str. */
static zerg_list_zerg_str zerg_strings_split(zerg_str s, zerg_str sep) {
    if (sep.len == 0) {
        fprintf(stderr, "zerg: runtime: split: empty separator\n");
        exit(1);
    }
    zerg_list_zerg_str out;
    out.len = 0;
    out.cap = 0;
    out.data = 0;
    size_t start = 0;
    size_t i = 0;
    while (i + sep.len <= s.len) {
        if (memcmp(s.data + i, sep.data, sep.len) == 0) {
            size_t plen = i - start;
            char *p = (char *)malloc(plen + 1);
            if (plen) memcpy(p, s.data + start, plen);
            p[plen] = 0;
            zerg_list_zerg_str_push(&out, (zerg_str){p, plen});
            start = i + sep.len;
            i = start;
        } else {
            i++;
        }
    }
    size_t plen = s.len - start;
    char *p = (char *)malloc(plen + 1);
    if (plen) memcpy(p, s.data + start, plen);
    p[plen] = 0;
    zerg_list_zerg_str_push(&out, (zerg_str){p, plen});
    return out;
}

static zerg_str zerg_strings_join(zerg_list_zerg_str parts, zerg_str sep) {
    if (parts.len == 0) {
        char *p = (char *)malloc(1);
        p[0] = 0;
        return (zerg_str){p, 0};
    }
    size_t total = 0;
    for (size_t i = 0; i < parts.len; i++) total += parts.data[i].len;
    if (parts.len > 1) total += sep.len * (parts.len - 1);
    char *out = (char *)malloc(total + 1);
    size_t off = 0;
    for (size_t i = 0; i < parts.len; i++) {
        if (i > 0 && sep.len) {
            memcpy(out + off, sep.data, sep.len);
            off += sep.len;
        }
        if (parts.data[i].len) {
            memcpy(out + off, parts.data[i].data, parts.data[i].len);
            off += parts.data[i].len;
        }
    }
    out[off] = 0;
    return (zerg_str){out, off};
}

/* ASCII whitespace per Go's unicode.IsSpace ASCII subset: ' ', '\t',
   '\n', '\v', '\f', '\r'. Matches strings.TrimSpace's ASCII-only
   behaviour for byte parity with the interpreter (which uses
   strings.TrimSpace; the corpus stays in ASCII). */
static int zerg_v08_is_ascii_space(unsigned char c) {
    return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r';
}

static zerg_str zerg_strings_trim(zerg_str s) {
    size_t lo = 0;
    size_t hi = s.len;
    while (lo < hi && zerg_v08_is_ascii_space((unsigned char)s.data[lo])) lo++;
    while (hi > lo && zerg_v08_is_ascii_space((unsigned char)s.data[hi - 1])) hi--;
    size_t n = hi - lo;
    char *p = (char *)malloc(n + 1);
    if (n) memcpy(p, s.data + lo, n);
    p[n] = 0;
    return (zerg_str){p, n};
}

static _Bool zerg_strings_starts_with(zerg_str s, zerg_str prefix) {
    if (prefix.len > s.len) return 0;
    return prefix.len == 0 || memcmp(s.data, prefix.data, prefix.len) == 0;
}

static _Bool zerg_strings_ends_with(zerg_str s, zerg_str suffix) {
    if (suffix.len > s.len) return 0;
    return suffix.len == 0 || memcmp(s.data + (s.len - suffix.len), suffix.data, suffix.len) == 0;
}

static _Bool zerg_strings_contains(zerg_str s, zerg_str needle) {
    if (needle.len == 0) return 1;
    if (needle.len > s.len) return 0;
    for (size_t i = 0; i + needle.len <= s.len; i++) {
        if (memcmp(s.data + i, needle.data, needle.len) == 0) return 1;
    }
    return 0;
}

/* ReplaceAll: left-to-right, non-overlapping. When old is empty, Go's
   strings.ReplaceAll inserts new between every byte (and at both ends);
   match that for parity. */
static zerg_str zerg_strings_replace(zerg_str s, zerg_str oldp, zerg_str newp) {
    if (oldp.len == 0) {
        size_t n = s.len + newp.len * (s.len + 1);
        char *out = (char *)malloc(n + 1);
        size_t off = 0;
        if (newp.len) { memcpy(out + off, newp.data, newp.len); off += newp.len; }
        for (size_t i = 0; i < s.len; i++) {
            out[off++] = s.data[i];
            if (newp.len) { memcpy(out + off, newp.data, newp.len); off += newp.len; }
        }
        out[off] = 0;
        return (zerg_str){out, off};
    }
    /* First pass: count occurrences. */
    size_t count = 0;
    for (size_t i = 0; i + oldp.len <= s.len; ) {
        if (memcmp(s.data + i, oldp.data, oldp.len) == 0) {
            count++;
            i += oldp.len;
        } else {
            i++;
        }
    }
    if (count == 0) {
        char *out = (char *)malloc(s.len + 1);
        if (s.len) memcpy(out, s.data, s.len);
        out[s.len] = 0;
        return (zerg_str){out, s.len};
    }
    size_t outLen = s.len + count * newp.len - count * oldp.len;
    char *out = (char *)malloc(outLen + 1);
    size_t off = 0;
    size_t i = 0;
    while (i < s.len) {
        if (i + oldp.len <= s.len && memcmp(s.data + i, oldp.data, oldp.len) == 0) {
            if (newp.len) { memcpy(out + off, newp.data, newp.len); off += newp.len; }
            i += oldp.len;
        } else {
            out[off++] = s.data[i++];
        }
    }
    out[off] = 0;
    return (zerg_str){out, off};
}

static zerg_str zerg_strings_to_upper(zerg_str s) {
    char *p = (char *)malloc(s.len + 1);
    for (size_t i = 0; i < s.len; i++) {
        unsigned char c = (unsigned char)s.data[i];
        p[i] = (char)((c >= 'a' && c <= 'z') ? c - ('a' - 'A') : c);
    }
    p[s.len] = 0;
    return (zerg_str){p, s.len};
}

static zerg_str zerg_strings_to_lower(zerg_str s) {
    char *p = (char *)malloc(s.len + 1);
    for (size_t i = 0; i < s.len; i++) {
        unsigned char c = (unsigned char)s.data[i];
        p[i] = (char)((c >= 'A' && c <= 'Z') ? c + ('a' - 'A') : c);
    }
    p[s.len] = 0;
    return (zerg_str){p, s.len};
}

/* parse_int: trim ASCII whitespace, optional sign, decimal digits.
   Buckets to (1=Empty, 2=InvalidDigit, 3=Overflow). int64 boundary
   uses INT64_MAX/INT64_MIN comparisons against the running value. */
static zerg_parse_int_result zerg_strings_parse_int(zerg_str s) {
    size_t lo = 0;
    size_t hi = s.len;
    while (lo < hi && zerg_v08_is_ascii_space((unsigned char)s.data[lo])) lo++;
    while (hi > lo && zerg_v08_is_ascii_space((unsigned char)s.data[hi - 1])) hi--;
    if (lo == hi) return (zerg_parse_int_result){.tag = 1, .value = 0};
    int neg = 0;
    if (s.data[lo] == '+' || s.data[lo] == '-') {
        neg = (s.data[lo] == '-');
        lo++;
        if (lo == hi) return (zerg_parse_int_result){.tag = 2, .value = 0};
    }
    /* INT64_MIN's absolute value is unrepresentable as positive int64;
       accumulate as unsigned and bail at the overflow boundary. */
    uint64_t acc = 0;
    uint64_t cap = neg ? (uint64_t)9223372036854775808ULL : (uint64_t)9223372036854775807ULL;
    for (size_t i = lo; i < hi; i++) {
        unsigned char c = (unsigned char)s.data[i];
        if (c < '0' || c > '9') return (zerg_parse_int_result){.tag = 2, .value = 0};
        uint64_t d = (uint64_t)(c - '0');
        if (acc > (cap - d) / 10) return (zerg_parse_int_result){.tag = 3, .value = 0};
        acc = acc * 10 + d;
    }
    int64_t out;
    if (neg) {
        if (acc == (uint64_t)9223372036854775808ULL) out = (int64_t)(-9223372036854775807LL - 1);
        else out = -(int64_t)acc;
    } else {
        out = (int64_t)acc;
    }
    return (zerg_parse_int_result){.tag = 0, .value = out};
}

/* ---------------- math --------------------------------------------------- */

static int64_t zerg_math_abs(int64_t x) {
    return x < 0 ? -x : x;
}

static int64_t zerg_math_min(int64_t a, int64_t b) {
    return a < b ? a : b;
}

static int64_t zerg_math_max(int64_t a, int64_t b) {
    return a > b ? a : b;
}

static int64_t zerg_math_gcd(int64_t a, int64_t b) {
    if (a < 0) a = -a;
    if (b < 0) b = -b;
    while (b != 0) {
        int64_t t = a % b;
        a = b;
        b = t;
    }
    return a;
}
`
