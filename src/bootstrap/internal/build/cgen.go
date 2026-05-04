// Package build emits a C source file from a parsed Zerg program and shells
// out to the system C compiler to produce a native binary.
//
// At v0.2 the codegen lowers the full primitive-typed surface PLUS composite
// data: tuples, lists, structs, enums, match. The runtime helpers live inline
// in runtime.go and are emitted once at the top of the generated .c file.
// Stdout produced by the compiled binary must equal stdout produced by the
// interpreter for every program in the parity corpus — mirror run.go's
// semantics, do not freelance.
package build

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// Emit writes the C source for prog to w. The output is a complete,
// self-contained .c file with `int main(void)` as the entry point. Emit
// assumes prog has already been type-checked: every Expr's Type() must be
// non-nil. Callers that go through Build / EmitSource get this for free
// because both call syntax.Check before reaching here.
func Emit(prog *syntax.Program, w io.Writer) error {
	g := &cgen{
		indent:            1,
		fnTable:           map[string]*syntax.FnDecl{},
		specs:             map[string]*syntax.SpecDecl{},
		inherent:          map[string][]*syntax.FnDecl{},
		specImpls:         map[implKey]*syntax.ImplDecl{},
		receiverTypes:     map[string]*syntax.Type{},
		specsUsed:         map[string]bool{},
	}
	for _, stmt := range prog.Statements {
		if fn, ok := stmt.(*syntax.FnDecl); ok {
			g.fnTable[fn.Name] = fn
		}
	}
	g.shapes = newShapeRegistry()

	// Collect spec / impl declarations first so the shape walk and the method
	// emitter both have access to the v0.4 tables.
	g.collectSpecsImpls(prog)

	// Walk the program (typed AST) to collect every concrete composite shape
	// that needs a per-shape typedef and helpers (list, tuple, struct, enum).
	if err := g.collectShapes(prog); err != nil {
		return err
	}

	// 1. Runtime header.
	g.b.WriteString(runtimeC)
	g.b.WriteString("\n")
	g.b.WriteString(runtimeV04C)
	g.b.WriteString("\n")

	// 2. Composite-shape typedefs and helpers (forward decls then bodies).
	g.shapes.emitForwardDecls(&g.b)
	g.emitSpecForwardDecls()
	g.shapes.emitTypedefs(&g.b)
	g.shapes.emitHelpers(&g.b)
	g.emitEqHelpers()
	g.emitSpecVtablesAndMethods(prog)

	// 3. Top-level fn forward decls then bodies. Forward decls let any fn call
	// any other regardless of textual order — same as the interpreter's
	// two-pass collect.
	for _, stmt := range prog.Statements {
		fn, ok := stmt.(*syntax.FnDecl)
		if !ok {
			continue
		}
		writeFnSig(&g.b, fn)
		g.b.WriteString(";\n")
	}
	if hasFn(prog) {
		g.b.WriteString("\n")
	}

	for _, stmt := range prog.Statements {
		fn, ok := stmt.(*syntax.FnDecl)
		if !ok {
			continue
		}
		if err := g.emitFn(fn); err != nil {
			return err
		}
		g.b.WriteString("\n")
	}

	// 4. main(). Top-level type decls (struct/enum) are NOT executable; they
	// produced typedefs above and are skipped here.
	g.b.WriteString("int main(void) {\n")
	for _, stmt := range prog.Statements {
		switch stmt.(type) {
		case *syntax.FnDecl, *syntax.StructDecl, *syntax.EnumDecl:
			continue
		case *syntax.SpecDecl, *syntax.ImplDecl:
			// v0.4 Unit 1: parser-only landing. Typeck rejects these shapes
			// before codegen sees them; skip at the top level for symmetry
			// with the other declaration shapes.
			continue
		case *syntax.ImportDecl:
			// v0.5 Unit 1b: imports are resolved by the loader before
			// codegen sees the merged program. A stray ImportDecl at this
			// layer is a no-op so existing single-file programs keep
			// compiling unchanged.
			continue
		}
		if err := g.emitStmt(stmt); err != nil {
			return err
		}
	}
	g.b.WriteString("    return 0;\n")
	g.b.WriteString("}\n")

	_, err := io.WriteString(w, g.b.String())
	return err
}

// hasFn reports whether prog declares any top-level function.
func hasFn(prog *syntax.Program) bool {
	for _, s := range prog.Statements {
		if _, ok := s.(*syntax.FnDecl); ok {
			return true
		}
	}
	return false
}

// cgen is the per-Emit codegen state.
type cgen struct {
	b      strings.Builder
	indent int
	shapes *shapeRegistry

	// matchCounter generates unique labels per match statement. Each match
	// lowers to a labeled block; arms use `goto matchend_<n>` on success.
	matchCounter int

	// tmpCounter is for fresh local variable names inside generated blocks
	// (slice receivers, match scrutinees, etc.).
	tmpCounter int

	// fnTable indexes top-level FnDecl by name so callStr can coerce args
	// to declared param types (spec coercion at the call site).
	fnTable map[string]*syntax.FnDecl

	// currentFnRet is the resolved return type of the FnDecl whose body is
	// being emitted, used to coerce return-value expressions when the
	// declared return is spec-typed. nil at top level / inside a method body.
	currentFnRet *syntax.Type

	// v0.4 spec / impl bookkeeping. Populated in collectShapes from top-level
	// SpecDecl / ImplDecl statements; used by the vtable / method emitters.
	specs       map[string]*syntax.SpecDecl       // spec name → AST
	specOrder   []string                          // declaration order
	inherent    map[string][]*syntax.FnDecl       // type name → inherent methods
	inherentTypeOrder []string                    // declaration order
	specImpls   map[implKey]*syntax.ImplDecl      // (type, spec) → AST
	specImplKeys []implKey                        // declaration order
	receiverTypes map[string]*syntax.Type         // type name → resolved type (for impl receivers)

	// Spec types referenced anywhere in the program (let : Spec, list[Spec],
	// fn arg/return of Spec, etc.). Order is declaration order; emitForwardDecls
	// uses it to emit the fat-pointer typedef + vtable struct definitions.
	specsUsed map[string]bool
}

// implKey deduplicates impls by (type, spec). Mirrors run.go's implKey.
type implKey struct {
	typeName string
	specName string
}

// freshTmp returns a unique C identifier safe to introduce as a local.
func (g *cgen) freshTmp(prefix string) string {
	g.tmpCounter++
	return fmt.Sprintf("__zg_%s_%d", prefix, g.tmpCounter)
}

// writeIndent writes the current indent prefix (4 spaces per level).
func (g *cgen) writeIndent() {
	for i := 0; i < g.indent; i++ {
		g.b.WriteString("    ")
	}
}

// ---------------------------------------------------------------------------
// Shape registry — collects every composite type that needs a C typedef and
// per-shape helpers (copy / print / slice / index-check).
// ---------------------------------------------------------------------------

type shapeRegistry struct {
	// listShapes is the set of every concrete list element type seen, keyed
	// by the canonical mangled name of the LIST type itself.
	listShapes   map[string]*syntax.Type
	tupleShapes  map[string]*syntax.Type
	structShapes map[string]*syntax.Type
	enumShapes   map[string]*syntax.Type

	// listOrder, tupleOrder, etc. preserve insertion order so the emitted
	// .c file is deterministic. (A map walk is not deterministic in Go.)
	listOrder   []string
	tupleOrder  []string
	structOrder []string
	enumOrder   []string
}

func newShapeRegistry() *shapeRegistry {
	return &shapeRegistry{
		listShapes:   map[string]*syntax.Type{},
		tupleShapes:  map[string]*syntax.Type{},
		structShapes: map[string]*syntax.Type{},
		enumShapes:   map[string]*syntax.Type{},
	}
}

// addType registers t and all its sub-shapes recursively. Primitives are
// skipped — they map to canonical C primitives and need no per-shape helpers.
func (r *shapeRegistry) addType(t *syntax.Type) {
	if t == nil {
		return
	}
	switch t.Kind {
	case syntax.TypeList:
		r.addType(t.Element)
		key := mangleType(t)
		if _, ok := r.listShapes[key]; !ok {
			r.listShapes[key] = t
			r.listOrder = append(r.listOrder, key)
		}
	case syntax.TypeTuple:
		for _, e := range t.Tuple {
			r.addType(e)
		}
		key := mangleType(t)
		if _, ok := r.tupleShapes[key]; !ok {
			r.tupleShapes[key] = t
			r.tupleOrder = append(r.tupleOrder, key)
		}
	case syntax.TypeStruct:
		// Add the struct itself; recurse into field types so nested composites
		// are picked up even when they are only used inside a struct.
		key := mangleType(t)
		if _, ok := r.structShapes[key]; !ok {
			r.structShapes[key] = t
			r.structOrder = append(r.structOrder, key)
			for _, f := range t.Fields {
				r.addType(f.Type)
			}
		}
	case syntax.TypeEnum:
		key := mangleType(t)
		if _, ok := r.enumShapes[key]; !ok {
			r.enumShapes[key] = t
			r.enumOrder = append(r.enumOrder, key)
			for _, payload := range t.VariantPayloads {
				for _, pt := range payload {
					r.addType(pt)
				}
			}
		}
	case syntax.TypeSpec:
		// TypeSpec is registered separately on the cgen so the spec
		// fat-pointer typedef + vtable struct emit at the v0.4 stage.
		// Recording it here is a no-op — the shape registry handles only
		// the existing shape kinds.
	}
}

// emitForwardDecls writes a `typedef struct ...;` for every composite shape
// so helpers can refer to other shapes without ordering constraints. (List
// of struct, struct containing list[Foo], etc.)
func (r *shapeRegistry) emitForwardDecls(b *strings.Builder) {
	// Sort struct/enum order by name for stability — declaration order from
	// the source already gives a stable order, but a canonical sort makes
	// the output independent of source-side reorderings.
	sort.Strings(r.structOrder)
	sort.Strings(r.enumOrder)
	sort.Strings(r.listOrder)
	sort.Strings(r.tupleOrder)

	if len(r.structOrder) > 0 || len(r.enumOrder) > 0 ||
		len(r.listOrder) > 0 || len(r.tupleOrder) > 0 {
		b.WriteString("/* Composite shape forward declarations. */\n")
	}
	for _, k := range r.structOrder {
		fmt.Fprintf(b, "typedef struct %s %s;\n", k, k)
	}
	for _, k := range r.tupleOrder {
		fmt.Fprintf(b, "typedef struct %s %s;\n", k, k)
	}
	for _, k := range r.listOrder {
		fmt.Fprintf(b, "typedef struct %s %s;\n", k, k)
	}
	for _, k := range r.enumOrder {
		// v0.4: enums are tag+union structs even when no variant carries a
		// payload — keeps the surface uniform between bare-only enums and
		// payload-carrying enums and means run-time `==` walks the same
		// shape regardless. Forward-declare the struct here; the typedef
		// body comes in emitTypedefs.
		fmt.Fprintf(b, "typedef struct %s %s;\n", k, k)
	}
	if len(r.structOrder) > 0 || len(r.enumOrder) > 0 ||
		len(r.listOrder) > 0 || len(r.tupleOrder) > 0 {
		b.WriteString("\n")
	}
}

// emitTypedefs writes the actual struct definitions for list / tuple / struct
// types, plus the variant-index macros for each enum.
//
// Order matters: a complete C struct definition needs the COMPLETE type of
// each composite field (a forward declaration is enough only behind a
// pointer). Strategy:
//
//   * List shape definitions first — every list field is a pointer-to-element,
//     so element types only need their forward decl (already emitted in
//     emitForwardDecls). Lists therefore have no shape-def dependency on
//     other shapes and can be emitted en bloc.
//   * Tuple, struct and enum shape definitions in a unified topological sort.
//     A tuple-of-struct needs the struct's full definition; a struct-of-
//     tuple needs the tuple's full definition; an enum variant payload
//     embeds its payload types by value, so a `Frame { Args(list[int]) }`
//     enum needs `zerg_list_int64_t`'s full definition (not just its forward
//     decl) before its own struct body can be emitted. Handling tuples,
//     structs and enums as one dependency graph respects whichever direction
//     the user wrote.
//
// typeck has rejected composite cycles so the fixed-point loop terminates.
func (r *shapeRegistry) emitTypedefs(b *strings.Builder) {
	if len(r.listOrder) > 0 {
		b.WriteString("/* List shape definitions. */\n")
	}
	for _, k := range r.listOrder {
		t := r.listShapes[k]
		elem := cTypeName(t.Element)
		// `cap` was dropped in v0.2 because lists were value-copied with
		// cap == len at every site. With `push` in play at v0.3, cap is
		// needed so the per-shape grow helper knows when to realloc.
		fmt.Fprintf(b, "struct %s { %s* data; size_t len; size_t cap; };\n", k, elem)
	}

	// Unified topo over tuple, struct and enum shapes. depsOf returns the
	// mangled names of OTHER composite shapes whose full definition is
	// needed before this one can be emitted. List deps resolve immediately
	// because lists are already fully defined above.
	depsOf := func(t *syntax.Type) []string {
		var out []string
		var fields []*syntax.Type
		switch t.Kind {
		case syntax.TypeTuple:
			fields = append(fields, t.Tuple...)
		case syntax.TypeStruct:
			for _, f := range t.Fields {
				fields = append(fields, f.Type)
			}
		case syntax.TypeEnum:
			for i := range t.Variants {
				fields = append(fields, variantPayload(t, i)...)
			}
		}
		for _, ft := range fields {
			if ft == nil {
				continue
			}
			switch ft.Kind {
			case syntax.TypeStruct, syntax.TypeTuple, syntax.TypeEnum, syntax.TypeList:
				out = append(out, mangleType(ft))
			}
		}
		return out
	}

	emittedTuple := map[string]bool{}
	emittedStruct := map[string]bool{}
	emittedEnum := map[string]bool{}
	depReady := func(deps []string) bool {
		for _, dep := range deps {
			if _, ok := r.tupleShapes[dep]; ok && !emittedTuple[dep] {
				return false
			}
			if _, ok := r.structShapes[dep]; ok && !emittedStruct[dep] {
				return false
			}
			if _, ok := r.enumShapes[dep]; ok && !emittedEnum[dep] {
				return false
			}
			// Lists are emitted en bloc above and are always ready.
		}
		return true
	}

	wroteTupleHeader := false
	wroteStructHeader := false
	wroteEnumHeader := false
	emitTuple := func(k string) {
		if !wroteTupleHeader {
			b.WriteString("\n/* Tuple shape definitions. */\n")
			wroteTupleHeader = true
		}
		t := r.tupleShapes[k]
		fmt.Fprintf(b, "struct %s {", k)
		for i, e := range t.Tuple {
			if i > 0 {
				b.WriteString(";")
			}
			fmt.Fprintf(b, " %s e%d", cTypeName(e), i)
		}
		b.WriteString("; };\n")
		emittedTuple[k] = true
	}
	emitStruct := func(k string) {
		if !wroteStructHeader {
			b.WriteString("\n/* Struct shape definitions. */\n")
			wroteStructHeader = true
		}
		t := r.structShapes[k]
		fmt.Fprintf(b, "struct %s {", k)
		for i, f := range t.Fields {
			if i > 0 {
				b.WriteString(";")
			}
			fmt.Fprintf(b, " %s %s", cTypeName(f.Type), mangleField(f.Name))
		}
		b.WriteString("; };\n")
		emittedStruct[k] = true
	}
	// v0.4 enum layout: `struct { int32_t tag; union { ... } payload; }`.
	// Each variant gets a payload sub-struct named pN where N is the variant
	// index; bare variants use a placeholder slot so the union is never empty.
	// Variant index macros are emitted alongside as `<Mangle>__<Variant>_TAG`
	// for use in match scrutinee tag tests.
	emitEnum := func(k string) {
		if !wroteEnumHeader {
			b.WriteString("\n/* Enum tag+union shape definitions. */\n")
			wroteEnumHeader = true
		}
		t := r.enumShapes[k]
		fmt.Fprintf(b, "struct %s {\n", k)
		fmt.Fprintf(b, "    int32_t tag;\n")
		fmt.Fprintf(b, "    union {\n")
		for i, v := range t.Variants {
			fmt.Fprintf(b, "        struct {")
			payload := variantPayload(t, i)
			if len(payload) == 0 {
				fmt.Fprintf(b, " char _empty;")
			} else {
				for j, pt := range payload {
					fmt.Fprintf(b, " %s a%d;", cTypeName(pt), j)
				}
			}
			fmt.Fprintf(b, " } p%d; /* %s */\n", i, v)
		}
		fmt.Fprintf(b, "    } payload;\n")
		fmt.Fprintf(b, "};\n")
		for i, v := range t.Variants {
			fmt.Fprintf(b, "#define %s__%s_TAG (%d)\n", k, v, i)
		}
		emittedEnum[k] = true
	}

	totalRemaining := len(r.tupleOrder) + len(r.structOrder) + len(r.enumOrder)
	for totalRemaining > 0 {
		progress := false
		for _, k := range r.tupleOrder {
			if emittedTuple[k] {
				continue
			}
			if !depReady(depsOf(r.tupleShapes[k])) {
				continue
			}
			emitTuple(k)
			progress = true
			totalRemaining--
		}
		for _, k := range r.structOrder {
			if emittedStruct[k] {
				continue
			}
			if !depReady(depsOf(r.structShapes[k])) {
				continue
			}
			emitStruct(k)
			progress = true
			totalRemaining--
		}
		for _, k := range r.enumOrder {
			if emittedEnum[k] {
				continue
			}
			if !depReady(depsOf(r.enumShapes[k])) {
				continue
			}
			emitEnum(k)
			progress = true
			totalRemaining--
		}
		if !progress {
			// Should not happen post-typeck cycle check; emit remaining
			// regardless rather than spin forever. The C compiler will
			// surface the underlying issue if any.
			for _, k := range r.tupleOrder {
				if !emittedTuple[k] {
					emitTuple(k)
				}
			}
			for _, k := range r.structOrder {
				if !emittedStruct[k] {
					emitStruct(k)
				}
			}
			for _, k := range r.enumOrder {
				if !emittedEnum[k] {
					emitEnum(k)
				}
			}
			break
		}
	}

	if len(r.listOrder)+len(r.tupleOrder)+len(r.structOrder)+len(r.enumOrder) > 0 {
		b.WriteString("\n")
	}
}

// emitHelpers writes per-shape copy / print / slice helpers. Every shape gets
// a copy helper even when the shape contains no lists (the helper is then a
// trivial pass-through that the C optimiser inlines), so call sites can be
// uniform.
func (r *shapeRegistry) emitHelpers(b *strings.Builder) {
	// Forward-declare all copy + print helpers first so they can reference
	// each other in any order (a list of struct copy calls the struct copy
	// which itself may call a list copy for an inner field).
	for _, k := range r.listOrder {
		t := r.listShapes[k]
		elem := cTypeName(t.Element)
		fmt.Fprintf(b, "static %s %s_copy(%s xs);\n", k, k, k)
		fmt.Fprintf(b, "static %s %s_slice(%s xs, int64_t lo, int64_t hi, const char* pos);\n", k, k, k)
		fmt.Fprintf(b, "static void %s_push(%s* xs, %s v);\n", k, k, elem)
		fmt.Fprintf(b, "static void zerg_print_%s(%s xs);\n", k, k)
	}
	for _, k := range r.tupleOrder {
		fmt.Fprintf(b, "static %s %s_copy(%s t);\n", k, k, k)
		fmt.Fprintf(b, "static void zerg_print_%s(%s t);\n", k, k)
	}
	for _, k := range r.structOrder {
		fmt.Fprintf(b, "static %s %s_copy(%s s);\n", k, k, k)
		fmt.Fprintf(b, "static void zerg_print_%s(%s s);\n", k, k)
	}
	for _, k := range r.enumOrder {
		fmt.Fprintf(b, "static %s %s_copy(%s e);\n", k, k, k)
		fmt.Fprintf(b, "static void zerg_print_%s(%s e);\n", k, k)
	}
	if len(r.listOrder)+len(r.tupleOrder)+len(r.structOrder)+len(r.enumOrder) > 0 {
		b.WriteString("\n")
	}

	// list helpers
	for _, k := range r.listOrder {
		t := r.listShapes[k]
		emitListHelpers(b, k, t)
		b.WriteString("\n")
	}
	// tuple helpers
	for _, k := range r.tupleOrder {
		t := r.tupleShapes[k]
		emitTupleHelpers(b, k, t)
		b.WriteString("\n")
	}
	// struct helpers
	for _, k := range r.structOrder {
		t := r.structShapes[k]
		emitStructHelpers(b, k, t)
		b.WriteString("\n")
	}
	// enum helpers (copy + print).
	for _, k := range r.enumOrder {
		t := r.enumShapes[k]
		emitEnumCopy(b, k, t)
		emitEnumPrint(b, k, t)
		b.WriteString("\n")
	}
}

// emitListHelpers writes copy, slice, push, and print for a list[T] shape.
func emitListHelpers(b *strings.Builder, mname string, t *syntax.Type) {
	elem := cTypeName(t.Element)
	// copy: malloc a fresh buffer (cap == len so subsequent pushes start
	// from a tight buffer), deep-copy each element via copyExpr.
	fmt.Fprintf(b, "static %s %s_copy(%s xs) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    %s out;\n", mname)
	fmt.Fprintf(b, "    out.len = xs.len;\n")
	fmt.Fprintf(b, "    out.cap = xs.len;\n")
	fmt.Fprintf(b, "    out.data = (%s*)malloc(out.len ? out.len * sizeof(%s) : 1);\n", elem, elem)
	fmt.Fprintf(b, "    for (size_t i = 0; i < out.len; i++) { out.data[i] = %s; }\n",
		copyExpr(t.Element, "xs.data[i]"))
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")

	// slice: bounds-check, malloc fresh buffer, deep-copy elements. The
	// resulting list owns its buffer with cap == len.
	fmt.Fprintf(b, "static %s %s_slice(%s xs, int64_t lo, int64_t hi, const char* pos) {\n",
		mname, mname, mname)
	fmt.Fprintf(b, "    if (lo < 0 || hi < lo || (size_t)hi > xs.len) {\n")
	fmt.Fprintf(b, "        fprintf(stderr, \"zerg: %%s: slice [%%lld..%%lld] out of range [0..%%zu]\\n\", pos, (long long)lo, (long long)hi, xs.len);\n")
	fmt.Fprintf(b, "        exit(1);\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    %s out;\n", mname)
	fmt.Fprintf(b, "    out.len = (size_t)(hi - lo);\n")
	fmt.Fprintf(b, "    out.cap = out.len;\n")
	fmt.Fprintf(b, "    out.data = (%s*)malloc(out.len ? out.len * sizeof(%s) : 1);\n", elem, elem)
	fmt.Fprintf(b, "    for (size_t i = 0; i < out.len; i++) { out.data[i] = %s; }\n",
		copyExpr(t.Element, "xs.data[lo + i]"))
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")

	// push: amortised-O(1) growth. Doubles cap when len catches up; first
	// growth from cap == 0 jumps to 4 to avoid the 0 → 1 → 2 → 4 ramp on
	// freshly-constructed empty lists. Takes a pointer so the caller's
	// (data, len, cap) header is updated in place.
	fmt.Fprintf(b, "static void %s_push(%s* xs, %s v) {\n", mname, mname, elem)
	fmt.Fprintf(b, "    if (xs->len == xs->cap) {\n")
	fmt.Fprintf(b, "        size_t newcap = xs->cap == 0 ? 4 : xs->cap * 2;\n")
	fmt.Fprintf(b, "        xs->data = (%s*)realloc(xs->data, newcap * sizeof(%s));\n", elem, elem)
	fmt.Fprintf(b, "        xs->cap = newcap;\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    xs->data[xs->len++] = v;\n")
	fmt.Fprintf(b, "}\n")

	// print: "[ e1, e2 ]" with space-comma-space; "[]" when empty.
	fmt.Fprintf(b, "static void zerg_print_%s(%s xs) {\n", mname, mname)
	fmt.Fprintf(b, "    if (xs.len == 0) { fputs(\"[]\", stdout); return; }\n")
	fmt.Fprintf(b, "    fputs(\"[ \", stdout);\n")
	fmt.Fprintf(b, "    for (size_t i = 0; i < xs.len; i++) {\n")
	fmt.Fprintf(b, "        if (i > 0) fputs(\", \", stdout);\n")
	fmt.Fprintf(b, "        %s;\n", printExpr(t.Element, "xs.data[i]"))
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    fputs(\" ]\", stdout);\n")
	fmt.Fprintf(b, "}\n")
}

// emitTupleHelpers writes copy and print for a tuple shape.
func emitTupleHelpers(b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static %s %s_copy(%s t) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    %s out;\n", mname)
	for i, e := range t.Tuple {
		fmt.Fprintf(b, "    out.e%d = %s;\n", i, copyExpr(e, fmt.Sprintf("t.e%d", i)))
	}
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")

	fmt.Fprintf(b, "static void zerg_print_%s(%s t) {\n", mname, mname)
	fmt.Fprintf(b, "    fputs(\"( \", stdout);\n")
	for i, e := range t.Tuple {
		if i > 0 {
			fmt.Fprintf(b, "    fputs(\", \", stdout);\n")
		}
		fmt.Fprintf(b, "    %s;\n", printExpr(e, fmt.Sprintf("t.e%d", i)))
	}
	fmt.Fprintf(b, "    fputs(\" )\", stdout);\n")
	fmt.Fprintf(b, "}\n")
}

// emitStructHelpers writes copy and print for a struct shape.
func emitStructHelpers(b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static %s %s_copy(%s s) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    %s out;\n", mname)
	for _, f := range t.Fields {
		fmt.Fprintf(b, "    out.%s = %s;\n",
			mangleField(f.Name),
			copyExpr(f.Type, "s."+mangleField(f.Name)))
	}
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")

	fmt.Fprintf(b, "static void zerg_print_%s(%s s) {\n", mname, mname)
	fmt.Fprintf(b, "    fputs(%q, stdout);\n", t.Name+" { ")
	for i, f := range t.Fields {
		if i > 0 {
			fmt.Fprintf(b, "    fputs(\", \", stdout);\n")
		}
		fmt.Fprintf(b, "    fputs(%q, stdout);\n", f.Name+": ")
		fmt.Fprintf(b, "    %s;\n", printExpr(f.Type, "s."+mangleField(f.Name)))
	}
	fmt.Fprintf(b, "    fputs(\" }\", stdout);\n")
	fmt.Fprintf(b, "}\n")
}

// emitEnumPrint writes print-helper for an enum: switch on the tag and emit
// either "Name.VariantName" (bare) or "Name.VariantName(payload, ...)" (with
// per-position recursive print of each payload value).
func emitEnumPrint(b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static void zerg_print_%s(%s e) {\n", mname, mname)
	fmt.Fprintf(b, "    switch (e.tag) {\n")
	for i, v := range t.Variants {
		payload := variantPayload(t, i)
		if len(payload) == 0 {
			fmt.Fprintf(b, "    case %d: fputs(%q, stdout); break;\n", i, t.Name+"."+v)
		} else {
			fmt.Fprintf(b, "    case %d: {\n", i)
			fmt.Fprintf(b, "        fputs(%q, stdout);\n", t.Name+"."+v+"(")
			for j, pt := range payload {
				if j > 0 {
					fmt.Fprintf(b, "        fputs(\", \", stdout);\n")
				}
				fmt.Fprintf(b, "        %s;\n", printExpr(pt, fmt.Sprintf("e.payload.p%d.a%d", i, j)))
			}
			fmt.Fprintf(b, "        fputs(\")\", stdout);\n")
			fmt.Fprintf(b, "        break;\n")
			fmt.Fprintf(b, "    }\n")
		}
	}
	fmt.Fprintf(b, "    default: fputs(\"<bad enum>\", stdout); break;\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "}\n")
}

// emitEnumCopy writes a deep-copy helper for an enum value. Copies primitive
// payloads by value and recurses through composite payloads via the per-shape
// _copy helpers. Bare variants copy the (single-byte) placeholder.
func emitEnumCopy(b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static %s %s_copy(%s e) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    %s out;\n", mname)
	fmt.Fprintf(b, "    out.tag = e.tag;\n")
	fmt.Fprintf(b, "    switch (e.tag) {\n")
	for i := range t.Variants {
		payload := variantPayload(t, i)
		fmt.Fprintf(b, "    case %d:\n", i)
		if len(payload) == 0 {
			fmt.Fprintf(b, "        out.payload.p%d._empty = 0;\n", i)
		} else {
			for j, pt := range payload {
				fmt.Fprintf(b, "        out.payload.p%d.a%d = %s;\n",
					i, j, copyExpr(pt, fmt.Sprintf("e.payload.p%d.a%d", i, j)))
			}
		}
		fmt.Fprintf(b, "        break;\n")
	}
	fmt.Fprintf(b, "    default: break;\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")
}

// variantPayload returns the per-position payload type slice for the i-th
// variant of t. Returns nil when the variant is bare or VariantPayloads is
// nil for that index (consistent with typeck's representation).
func variantPayload(t *syntax.Type, i int) []*syntax.Type {
	if t == nil || t.Kind != syntax.TypeEnum {
		return nil
	}
	if i < 0 || i >= len(t.VariantPayloads) {
		return nil
	}
	return t.VariantPayloads[i]
}

// variantIndex returns the index of variant `name` in enum t, or -1 when the
// variant is unknown (which should not happen post-typeck but we guard).
func variantIndex(t *syntax.Type, name string) int {
	if t == nil || t.Kind != syntax.TypeEnum {
		return -1
	}
	for i, v := range t.Variants {
		if v == name {
			return i
		}
	}
	return -1
}

// copyExpr returns a C expression that produces a deep-copy of expr (a C
// expression with type t). For primitives the copy is the expression itself
// (trivial copy via assignment); for composites we delegate to the per-shape
// _copy helper.
func copyExpr(t *syntax.Type, expr string) string {
	if t == nil {
		return expr
	}
	switch t.Kind {
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
		return fmt.Sprintf("%s_copy(%s)", mangleType(t), expr)
	}
	return expr
}

// printExpr returns a C *statement* (not an expression) that prints expr's
// value using the type-appropriate helper. Used inside list/tuple/struct
// print bodies where a single statement is the right level.
//
// NOTE: for primitives this is the inline `printf("%lld", ...)` form WITHOUT
// trailing newline — list/tuple/struct printing handles the surrounding
// punctuation. For top-level `print stmt` we use a different helper that
// adds the newline.
func printExpr(t *syntax.Type, expr string) string {
	if t == nil {
		return "(void)0"
	}
	switch t {
	case syntax.TInt():
		return fmt.Sprintf("printf(\"%%lld\", (long long)(%s))", expr)
	case syntax.TFloat():
		// %.17g matches Go's strconv.FormatFloat(x, 'g', 17, 64) — see
		// runtime.go zerg_print_float for the reasoning.
		return fmt.Sprintf("{ char __b[32]; snprintf(__b, sizeof __b, \"%%.17g\", (double)(%s)); fputs(__b, stdout); }", expr)
	case syntax.TBool():
		return fmt.Sprintf("fputs((%s) ? \"true\" : \"false\", stdout)", expr)
	case syntax.TStr():
		return fmt.Sprintf("zerg_str_write(%s)", expr)
	case syntax.TByte():
		return fmt.Sprintf("printf(\"%%hhu\", (uint8_t)(%s))", expr)
	case syntax.TRune():
		return fmt.Sprintf("printf(\"%%d\", (int32_t)(%s))", expr)
	}
	switch t.Kind {
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
		return fmt.Sprintf("zerg_print_%s(%s)", mangleType(t), expr)
	}
	return "(void)0"
}

// collectShapes walks the typed AST to register every composite shape with
// the registry. Walks types reachable from variable bindings, fn signatures,
// expression Type()s, struct/enum decls, and pattern types.
func (g *cgen) collectShapes(prog *syntax.Program) error {
	// First, register every top-level struct and enum DECLARED in the
	// program even if it isn't referenced from an expression — the typedef
	// is needed by anything that names the type.
	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *syntax.StructDecl:
			// Resolve the struct type by name via the field type refs.
			// Each struct's first field has a TypeRef.Resolved that points
			// at the field's type — but the struct itself we need to look
			// up via any field reference of struct kind. Simpler: we
			// reconstruct the struct *Type pointer by inspecting any
			// TypeRef that names this struct elsewhere. Easiest path: the
			// struct decl's fields all reference resolved types, but the
			// struct *Type itself is what we care about. We can find one
			// by using the first occurrence of a StructLit / FieldAccess
			// expression — but a never-used struct still needs a typedef.
			//
			// Workaround: attach the struct type to the decl by walking
			// every TypeRef looking for one whose Resolved.Name matches.
			// In practice every struct has at least one TypeRef in its
			// field-list pointing at a primitive or composite, but the
			// struct ITSELF is referred to via TypeRefNamed elsewhere.
			//
			// Cleanest: use the StructDecl name and fields to construct a
			// canonical *Type lookup is via typeck-internal table, but
			// that table is private. We sidestep by reconstructing from
			// the AST: build a *Type using NewStructType on the resolved
			// field types.
			fields := make([]syntax.NamedField, len(s.Fields))
			for i, f := range s.Fields {
				if f.Type == nil || f.Type.Resolved == nil {
					return fmt.Errorf("codegen: unresolved field type for %s.%s at %s", s.Name, f.Name, f.Pos)
				}
				fields[i] = syntax.NamedField{Name: f.Name, Type: f.Type.Resolved}
			}
			st := syntax.NewStructType(s.Name, fields)
			g.shapes.addType(st)
		case *syntax.EnumDecl:
			variants := make([]string, len(s.Variants))
			payloads := make([][]*syntax.Type, len(s.Variants))
			for i, v := range s.Variants {
				variants[i] = v.Name
				if len(v.Payload) > 0 {
					p := make([]*syntax.Type, len(v.Payload))
					for j, pr := range v.Payload {
						if pr != nil {
							p[j] = pr.Resolved
						}
					}
					payloads[i] = p
				}
			}
			en := syntax.NewEnumType(s.Name, variants)
			en.VariantPayloads = payloads
			g.shapes.addType(en)
		}
	}

	// Now walk all statements to pick up types reached via expressions and
	// type-refs. The walk is permissive — every visited type is added
	// (idempotent on the registry).
	for _, stmt := range prog.Statements {
		g.collectStmt(stmt)
	}
	return nil
}

func (g *cgen) collectStmt(stmt syntax.Stmt) {
	switch s := stmt.(type) {
	case *syntax.PrintStmt:
		g.collectExpr(s.Expr)
	case *syntax.LetStmt:
		if s.Type != nil && s.Type.Resolved != nil {
			g.shapes.addType(s.Type.Resolved)
			g.collectSpecsInType(s.Type.Resolved)
		}
		g.collectExpr(s.Value)
	case *syntax.MutStmt:
		if s.Type != nil && s.Type.Resolved != nil {
			g.shapes.addType(s.Type.Resolved)
			g.collectSpecsInType(s.Type.Resolved)
		}
		g.collectExpr(s.Value)
	case *syntax.ConstStmt:
		if s.Type != nil && s.Type.Resolved != nil {
			g.shapes.addType(s.Type.Resolved)
			g.collectSpecsInType(s.Type.Resolved)
		}
		g.collectExpr(s.Value)
	case *syntax.AssignStmt:
		g.collectExpr(s.Target)
		g.collectExpr(s.Value)
	case *syntax.ExprStmt:
		g.collectExpr(s.Expr)
	case *syntax.IfStmt:
		g.collectExpr(s.Cond)
		g.collectBlock(s.Then)
		for i := range s.Elifs {
			g.collectExpr(s.Elifs[i].Cond)
			g.collectBlock(s.Elifs[i].Body)
		}
		if s.Else != nil {
			g.collectBlock(s.Else)
		}
	case *syntax.ForStmt:
		switch s.Kind {
		case syntax.ForCond:
			g.collectExpr(s.Cond)
		case syntax.ForRange:
			g.collectExpr(s.Range.Start)
			g.collectExpr(s.Range.End)
		case syntax.ForIter:
			g.collectExpr(s.Iter)
		}
		g.collectBlock(s.Body)
	case *syntax.ReturnStmt:
		if s.Value != nil {
			g.collectExpr(s.Value)
		}
		if s.Guard != nil {
			g.collectExpr(s.Guard)
		}
	case *syntax.BreakStmt:
		if s.Guard != nil {
			g.collectExpr(s.Guard)
		}
	case *syntax.ContinueStmt:
		if s.Guard != nil {
			g.collectExpr(s.Guard)
		}
	case *syntax.FnDecl:
		for _, p := range s.Params {
			if p.Type != nil && p.Type.Resolved != nil {
				g.shapes.addType(p.Type.Resolved)
				g.collectSpecsInType(p.Type.Resolved)
			}
		}
		if s.Return != nil && s.Return.Resolved != nil {
			g.shapes.addType(s.Return.Resolved)
			g.collectSpecsInType(s.Return.Resolved)
		}
		g.collectBlock(s.Body)
	case *syntax.MatchStmt:
		g.collectExpr(s.Subject)
		for i := range s.Arms {
			arm := &s.Arms[i]
			g.collectPattern(arm.Pattern)
			if arm.Guard != nil {
				g.collectExpr(arm.Guard)
			}
			g.collectBlock(arm.Body)
		}
	case *syntax.SpecDecl:
		// Spec method default bodies may reference shapes the rest of the
		// program does not — walk them so the registry catches nested types.
		for _, m := range s.Methods {
			for _, p := range m.Params {
				if p.Type != nil && p.Type.Resolved != nil {
					g.shapes.addType(p.Type.Resolved)
				}
			}
			if m.Return != nil && m.Return.Resolved != nil {
				g.shapes.addType(m.Return.Resolved)
			}
			if m.Body != nil {
				g.collectBlock(m.Body)
			}
		}
	case *syntax.ImplDecl:
		for _, fn := range s.Methods {
			for _, p := range fn.Params {
				if p.Type != nil && p.Type.Resolved != nil {
					g.shapes.addType(p.Type.Resolved)
				}
			}
			if fn.Return != nil && fn.Return.Resolved != nil {
				g.shapes.addType(fn.Return.Resolved)
			}
			g.collectBlock(fn.Body)
		}
	}
}

func (g *cgen) collectBlock(b *syntax.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Statements {
		g.collectStmt(st)
	}
}

func (g *cgen) collectExpr(e syntax.Expr) {
	if e == nil {
		return
	}
	if t := e.Type(); t != nil {
		g.shapes.addType(t)
		g.collectSpecsInType(t)
	}
	switch x := e.(type) {
	case *syntax.UnaryExpr:
		g.collectExpr(x.Operand)
	case *syntax.BinaryExpr:
		g.collectExpr(x.Left)
		g.collectExpr(x.Right)
	case *syntax.ParenExpr:
		g.collectExpr(x.Inner)
	case *syntax.CallExpr:
		g.collectExpr(x.Callee)
		for _, a := range x.Args {
			g.collectExpr(a)
		}
	case *syntax.ListLit:
		for _, sub := range x.Elements {
			g.collectExpr(sub)
		}
	case *syntax.TupleLit:
		for _, sub := range x.Elements {
			g.collectExpr(sub)
		}
	case *syntax.StructLit:
		for _, f := range x.Fields {
			g.collectExpr(f.Value)
		}
	case *syntax.IndexExpr:
		g.collectExpr(x.Receiver)
		g.collectExpr(x.Index)
	case *syntax.SliceExpr:
		g.collectExpr(x.Receiver)
		if x.Low != nil {
			g.collectExpr(x.Low)
		}
		if x.High != nil {
			g.collectExpr(x.High)
		}
	case *syntax.FieldAccessExpr:
		g.collectExpr(x.Receiver)
		if x.Lowered != nil {
			g.collectExpr(x.Lowered)
		}
	case *syntax.MethodCallExpr:
		g.collectExpr(x.Receiver)
		for _, a := range x.Args {
			g.collectExpr(a)
		}
		if x.Lowered != nil {
			g.collectExpr(x.Lowered)
		}
		if x.LoweredCall != nil {
			g.collectExpr(x.LoweredCall)
		}
	case *syntax.ThisExpr:
		// nothing to collect; type is registered by the typed() walk above.
	case *syntax.EnumLit:
		for _, sub := range x.Payload {
			g.collectExpr(sub)
		}
	}
}

func (g *cgen) collectPattern(p syntax.Pattern) {
	switch x := p.(type) {
	case *syntax.LitPat:
		g.collectExpr(x.Lit)
	case *syntax.TuplePat:
		for _, sub := range x.Elements {
			g.collectPattern(sub)
		}
	case *syntax.StructPat:
		for _, f := range x.Fields {
			g.collectPattern(f.Pattern)
		}
	case *syntax.EnumPat:
		for _, sub := range x.Payload {
			g.collectPattern(sub)
		}
	}
}

// ---------------------------------------------------------------------------
// Statement emission.
// ---------------------------------------------------------------------------

func (g *cgen) emitStmt(stmt syntax.Stmt) error {
	switch s := stmt.(type) {
	case *syntax.NopStmt:
		g.writeIndent()
		g.b.WriteString("(void)0;\n")
		return nil
	case *syntax.PrintStmt:
		return g.emitPrint(s)
	case *syntax.LetStmt:
		if s.Tuple != nil {
			return g.emitTupleDestructure(s.Tuple, s.Value, false)
		}
		return g.emitDecl(s.Name, s.Type, s.Value, false)
	case *syntax.MutStmt:
		if s.Tuple != nil {
			return g.emitTupleDestructure(s.Tuple, s.Value, false)
		}
		return g.emitDecl(s.Name, s.Type, s.Value, false)
	case *syntax.ConstStmt:
		return g.emitDecl(s.Name, s.Type, s.Value, true)
	case *syntax.AssignStmt:
		return g.emitAssign(s)
	case *syntax.ExprStmt:
		expr, err := g.exprStr(s.Expr)
		if err != nil {
			return err
		}
		g.writeIndent()
		fmt.Fprintf(&g.b, "(void)(%s);\n", expr)
		return nil
	case *syntax.IfStmt:
		return g.emitIf(s)
	case *syntax.ForStmt:
		return g.emitFor(s)
	case *syntax.ReturnStmt:
		return g.emitReturn(s)
	case *syntax.BreakStmt:
		return g.emitFlow(s.Guard, "break")
	case *syntax.ContinueStmt:
		return g.emitFlow(s.Guard, "continue")
	case *syntax.FnDecl:
		return fmt.Errorf("internal: nested function %q at %s", s.Name, s.Pos)
	case *syntax.StructDecl, *syntax.EnumDecl:
		// Top-level type decls produce no executable code; their typedefs
		// were emitted by the runtime/shape pass. Reaching here at non-top
		// level is impossible — typeck rejects nested decls.
		return nil
	case *syntax.MatchStmt:
		return g.emitMatch(s)
	case *syntax.SpecDecl, *syntax.ImplDecl:
		// Spec / impl declarations produce no executable code at the
		// statement level — their per-method C functions and per-(Type,
		// Spec) vtable initialisers were emitted at file scope before
		// main(). Reaching here at non-top level is impossible because
		// typeck rejects nested decls.
		return nil
	}
	return fmt.Errorf("codegen: unhandled statement %T at %s", stmt, stmt.StmtPos())
}

// emitPrint dispatches on the static type. Every printable v0.2 shape has a
// dedicated helper; we add `\n` after the value-printer runs.
func (g *cgen) emitPrint(s *syntax.PrintStmt) error {
	expr, err := g.exprStr(s.Expr)
	if err != nil {
		return err
	}
	t := s.Expr.Type()
	if t == nil {
		return fmt.Errorf("codegen: missing type for print at %s", s.Pos)
	}
	g.writeIndent()
	switch t {
	case syntax.TInt():
		fmt.Fprintf(&g.b, "zerg_print_int(%s);\n", expr)
		return nil
	case syntax.TFloat():
		fmt.Fprintf(&g.b, "zerg_print_float(%s);\n", expr)
		return nil
	case syntax.TBool():
		fmt.Fprintf(&g.b, "zerg_print_bool(%s);\n", expr)
		return nil
	case syntax.TStr():
		fmt.Fprintf(&g.b, "zerg_print_str(%s);\n", expr)
		return nil
	case syntax.TByte():
		fmt.Fprintf(&g.b, "zerg_print_byte(%s);\n", expr)
		return nil
	case syntax.TRune():
		fmt.Fprintf(&g.b, "zerg_print_rune(%s);\n", expr)
		return nil
	}
	switch t.Kind {
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
		// The composite print helpers do NOT add a newline; we add one here
		// so the v0.1 contract (every print line ends with '\n') stays.
		fmt.Fprintf(&g.b, "zerg_print_%s(%s);\n", mangleType(t), expr)
		g.writeIndent()
		g.b.WriteString("putchar('\\n');\n")
		return nil
	}
	return fmt.Errorf("codegen: cannot print value of type %s at %s", t, s.Pos)
}

// emitDecl lowers let/mut/const into a C local declaration. At v0.3 we
// do NOT wrap composite RHS values in `_copy` — the borrow checker has
// invalidated the source binding at the move site, so sharing the
// underlying buffer/struct is safe. clone() is the explicit opt-in for
// the v0.2-style deep copy.
func (g *cgen) emitDecl(name string, ref *syntax.TypeRef, value syntax.Expr, isConst bool) error {
	t := value.Type()
	declT := t
	if ref != nil && ref.Resolved != nil {
		declT = ref.Resolved
	}
	if t == nil {
		t = declT
	}
	if declT == nil {
		return fmt.Errorf("codegen: missing type for %q", name)
	}
	exprS, err := g.exprStr(value)
	if err != nil {
		return err
	}
	// v0.4: a let/mut/const declared with a spec type widens the rhs into a
	// fat pointer at the bind site; nested specs (list[Spec], tuple[..., Spec])
	// recurse inside coerceCExpr.
	if shapeContainsSpec(declT) {
		exprS = g.coerceCExpr(exprS, t, declT)
	}
	g.writeIndent()
	if isConst {
		g.b.WriteString("const ")
	}
	fmt.Fprintf(&g.b, "%s %s = %s;\n", cTypeName(declT), mangle(name), exprS)
	return nil
}

// emitTupleDestructure lowers `let (a, b) := expr` into N variable decls
// reading from a fresh temp tuple. At v0.3 the elements are NOT deep-copied
// — the borrow checker invalidated the source pair at the destructure
// site so each name shares the underlying element value safely.
func (g *cgen) emitTupleDestructure(tb *syntax.TupleBinding, value syntax.Expr, isConst bool) error {
	t := value.Type()
	if t == nil || t.Kind != syntax.TypeTuple {
		return fmt.Errorf("codegen: tuple destructure rhs has non-tuple type at %s", tb.Pos)
	}
	exprS, err := g.exprStr(value)
	if err != nil {
		return err
	}
	tmp := g.freshTmp("tup")
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s %s = %s;\n", cTypeName(t), tmp, exprS)
	for i, name := range tb.Names {
		elemT := t.Tuple[i]
		g.writeIndent()
		if isConst {
			g.b.WriteString("const ")
		}
		fmt.Fprintf(&g.b, "%s %s = %s.e%d;\n",
			cTypeName(elemT), mangle(name), tmp, i)
	}
	return nil
}

// emitAssign lowers any assign-op to the C equivalent.
func (g *cgen) emitAssign(s *syntax.AssignStmt) error {
	// `xs[i] = v` lowers to a bounds-checked write through the list's data
	// pointer. Other LHS shapes are typeck/borrow-check rejected before they
	// reach codegen; only bare IdentExpr targets remain.
	if idx, ok := s.Target.(*syntax.IndexExpr); ok {
		return g.emitIndexAssign(s, idx)
	}
	target, ok := s.Target.(*syntax.IdentExpr)
	if !ok {
		return fmt.Errorf("codegen: unsupported assignment target %T at %s", s.Target, s.Pos)
	}
	rhs, err := g.exprStr(s.Value)
	if err != nil {
		return err
	}
	targetName := mangle(target.Name)
	g.writeIndent()
	switch s.Op {
	case syntax.AssignSet:
		// At v0.3 plain `x = v` is only meaningful for primitive targets
		// (the borrow checker rejects composite rebind via `=` because
		// composite mut bindings reach the new value via `:=` rebinding
		// or via `xs[i] = v` indexing). No implicit deep-copy.
		fmt.Fprintf(&g.b, "%s = %s;\n", targetName, rhs)
	case syntax.AssignAdd:
		if target.Type() == syntax.TStr() {
			fmt.Fprintf(&g.b, "%s = zerg_str_concat(%s, %s);\n", targetName, targetName, rhs)
			return nil
		}
		fmt.Fprintf(&g.b, "%s += %s;\n", targetName, rhs)
	case syntax.AssignSub:
		fmt.Fprintf(&g.b, "%s -= %s;\n", targetName, rhs)
	case syntax.AssignMul:
		fmt.Fprintf(&g.b, "%s *= %s;\n", targetName, rhs)
	case syntax.AssignDiv:
		fmt.Fprintf(&g.b, "%s /= %s;\n", targetName, rhs)
	case syntax.AssignMod:
		if target.Type() == syntax.TFloat() {
			fmt.Fprintf(&g.b, "%s = fmod(%s, %s);\n", targetName, targetName, rhs)
			return nil
		}
		fmt.Fprintf(&g.b, "%s %%= %s;\n", targetName, rhs)
	case syntax.AssignAnd:
		fmt.Fprintf(&g.b, "%s &= %s;\n", targetName, rhs)
	case syntax.AssignOr:
		fmt.Fprintf(&g.b, "%s |= %s;\n", targetName, rhs)
	case syntax.AssignXor:
		fmt.Fprintf(&g.b, "%s ^= %s;\n", targetName, rhs)
	case syntax.AssignShl:
		fmt.Fprintf(&g.b, "%s <<= %s;\n", targetName, rhs)
	case syntax.AssignShr:
		fmt.Fprintf(&g.b, "%s >>= %s;\n", targetName, rhs)
	default:
		return fmt.Errorf("codegen: unknown assign op %s at %s", s.Op, s.Pos)
	}
	return nil
}

// emitIndexAssign lowers `xs[i] = v`. The receiver must be a bare named list
// (typeck and borrow check have enforced this); we look up its mangled name,
// bounds-check the index, and assign through the data pointer with a deep
// copy of the rhs so the source rhs binding stays independent of the slot.
//
// Only AssignSet is admitted on a list element; compound assigns through a
// list element are out of scope at v0.3.
func (g *cgen) emitIndexAssign(s *syntax.AssignStmt, idx *syntax.IndexExpr) error {
	id, ok := idx.Receiver.(*syntax.IdentExpr)
	if !ok {
		return fmt.Errorf("codegen: list-element assignment requires a named list at %s", s.Pos)
	}
	if s.Op != syntax.AssignSet {
		return fmt.Errorf("codegen: list-element compound assign %s not supported at %s", s.Op, s.Pos)
	}
	listT := idx.Receiver.Type()
	if listT == nil || listT.Kind != syntax.TypeList {
		return fmt.Errorf("codegen: list-element assign target is not a list at %s", s.Pos)
	}
	is, err := g.exprStr(idx.Index)
	if err != nil {
		return err
	}
	rhs, err := g.exprStr(s.Value)
	if err != nil {
		return err
	}
	posStr := fmt.Sprintf("%d:%d", s.Pos.Line, s.Pos.Column)
	nameS := mangle(id.Name)
	g.writeIndent()
	// Bounds-check then write through the slice header. We compute the index
	// once into a local so the bounds check sees the same value the write
	// uses, and so a side-effecting index expression is evaluated once.
	fmt.Fprintf(&g.b,
		"{ int64_t __i = %s; zerg_index_check(__i, %s.len, %q); %s.data[__i] = %s; }\n",
		is, nameS, posStr, nameS, rhs)
	return nil
}

// emitIf walks the if-elif-else chain.
func (g *cgen) emitIf(s *syntax.IfStmt) error {
	cond, err := g.exprStr(s.Cond)
	if err != nil {
		return err
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "if (%s) {\n", cond)
	if err := g.emitBlockBody(s.Then); err != nil {
		return err
	}
	for i := range s.Elifs {
		ec := &s.Elifs[i]
		c, err := g.exprStr(ec.Cond)
		if err != nil {
			return err
		}
		g.writeIndent()
		fmt.Fprintf(&g.b, "} else if (%s) {\n", c)
		if err := g.emitBlockBody(ec.Body); err != nil {
			return err
		}
	}
	if s.Else != nil {
		g.writeIndent()
		g.b.WriteString("} else {\n")
		if err := g.emitBlockBody(s.Else); err != nil {
			return err
		}
	}
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// emitBlockBody emits the statements between `{` and `}` with one extra level
// of indent. The braces themselves are emitted by the caller.
func (g *cgen) emitBlockBody(b *syntax.Block) error {
	g.indent++
	defer func() { g.indent-- }()
	for _, st := range b.Statements {
		if err := g.emitStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// emitFor lowers all four for shapes. ForIter (list iteration) is the v0.2
// addition: a C-level `for` walking xs.data[0..xs.len) with a per-iteration
// fresh deep-copy bound to the loop variable.
func (g *cgen) emitFor(s *syntax.ForStmt) error {
	switch s.Kind {
	case syntax.ForInfinite:
		g.writeIndent()
		g.b.WriteString("while (1) {\n")
		if err := g.emitBlockBody(s.Body); err != nil {
			return err
		}
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	case syntax.ForCond:
		cond, err := g.exprStr(s.Cond)
		if err != nil {
			return err
		}
		g.writeIndent()
		fmt.Fprintf(&g.b, "while (%s) {\n", cond)
		if err := g.emitBlockBody(s.Body); err != nil {
			return err
		}
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	case syntax.ForRange:
		start, err := g.exprStr(s.Range.Start)
		if err != nil {
			return err
		}
		end, err := g.exprStr(s.Range.End)
		if err != nil {
			return err
		}
		cmp := "<"
		if s.Range.Inclusive {
			cmp = "<="
		}
		v := mangle(s.Var)
		g.writeIndent()
		fmt.Fprintf(&g.b, "for (int64_t %s = %s; %s %s %s; ++%s) {\n", v, start, v, cmp, end, v)
		if err := g.emitBlockBody(s.Body); err != nil {
			return err
		}
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	case syntax.ForIter:
		// Wrap in a brace-block so the temp + loop variable are scoped.
		iterT := s.Iter.Type()
		if iterT == nil || iterT.Kind != syntax.TypeList {
			return fmt.Errorf("codegen: for-in iter has non-list type at %s", s.Pos)
		}
		iterS, err := g.exprStr(s.Iter)
		if err != nil {
			return err
		}
		// Snapshot the iterable into a temp so a fn-call iterable is only
		// evaluated once. At v0.3 the borrow checker has BorrowedShared
		// the iterable for the body's duration and rejects in-body
		// mutation of it, so we don't need a deep-copy snapshot — a
		// shallow snapshot of the (data, len, cap) header suffices.
		listMangle := mangleType(iterT)
		tmp := g.freshTmp("iter")
		idx := g.freshTmp("i")
		v := mangle(s.Var)
		elemT := iterT.Element

		g.writeIndent()
		g.b.WriteString("{\n")
		g.indent++
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s %s = %s;\n", listMangle, tmp, iterS)
		g.writeIndent()
		fmt.Fprintf(&g.b, "for (size_t %s = 0; %s < %s.len; %s++) {\n", idx, idx, tmp, idx)
		g.indent++
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s %s = %s.data[%s];\n", cTypeName(elemT), v, tmp, idx)
		// Body statements (without the extra brace, but with indent already
		// raised once). Walk statements one-by-one without using
		// emitBlockBody to keep the indent layered correctly.
		for _, st := range s.Body.Statements {
			if err := g.emitStmt(st); err != nil {
				return err
			}
		}
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	}
	return fmt.Errorf("codegen: unknown for kind at %s", s.Pos)
}

// emitReturn handles bare return, return-with-value, and the guard form.
// At v0.3 we do NOT wrap composite return values in `_copy` — the borrow
// checker has invalidated the local binding at the return site, so the
// caller can take ownership of the underlying buffer/struct directly.
func (g *cgen) emitReturn(s *syntax.ReturnStmt) error {
	body := "return;"
	if s.Value != nil {
		v, err := g.exprStr(s.Value)
		if err != nil {
			return err
		}
		// v0.4: coerce to the declared fn return type if spec-typed.
		if g.currentFnRet != nil && shapeContainsSpec(g.currentFnRet) {
			v = g.coerceCExpr(v, s.Value.Type(), g.currentFnRet)
		}
		body = fmt.Sprintf("return %s;", v)
	}
	if s.Guard == nil {
		g.writeIndent()
		g.b.WriteString(body)
		g.b.WriteString("\n")
		return nil
	}
	guard, err := g.exprStr(s.Guard)
	if err != nil {
		return err
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "if (%s) { %s }\n", guard, body)
	return nil
}

// emitFlow handles break/continue with optional guard.
func (g *cgen) emitFlow(guard syntax.Expr, kw string) error {
	if guard == nil {
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s;\n", kw)
		return nil
	}
	c, err := g.exprStr(guard)
	if err != nil {
		return err
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "if (%s) %s;\n", c, kw)
	return nil
}

// emitFn writes a complete static function definition.
func (g *cgen) emitFn(fn *syntax.FnDecl) error {
	writeFnSig(&g.b, fn)
	g.b.WriteString(" {\n")
	prevRet := g.currentFnRet
	if fn.Return != nil {
		g.currentFnRet = fn.Return.Resolved
	} else {
		g.currentFnRet = nil
	}
	defer func() { g.currentFnRet = prevRet }()
	if err := g.emitBlockBody(fn.Body); err != nil {
		return err
	}
	g.b.WriteString("}\n")
	return nil
}

// writeFnSig renders the C signature (no trailing punctuation).
func writeFnSig(b *strings.Builder, fn *syntax.FnDecl) {
	ret := "void"
	if fn.Return != nil && fn.Return.Resolved != nil && fn.Return.Resolved != syntax.TVoid() {
		ret = cTypeName(fn.Return.Resolved)
	}
	b.WriteString("static ")
	b.WriteString(ret)
	b.WriteByte(' ')
	b.WriteString(mangle(fn.Name))
	b.WriteByte('(')
	if len(fn.Params) == 0 {
		b.WriteString("void")
	} else {
		for i, p := range fn.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(cTypeName(p.Type.Resolved))
			b.WriteByte(' ')
			b.WriteString(mangle(p.Name))
		}
	}
	b.WriteByte(')')
}

// ---------------------------------------------------------------------------
// Match.
// ---------------------------------------------------------------------------

// emitMatch lowers a match statement to a labelled brace-block:
//
//	{
//	    <SubjT> __zg_subj_<n> = <subj>;
//	    {  /* arm 1 */
//	        if (!<test for arm 1>) goto matcharm_<n>_1;
//	        <bind> ...
//	        if (guard) {
//	            if (!<guard>) goto matcharm_<n>_1;
//	        }
//	        <body>
//	        goto matchend_<n>;
//	    }
//	    matcharm_<n>_1:;
//	    {  /* arm 2 */ ... goto matcharm_<n>_2; ... }
//	    matcharm_<n>_2:;
//	    ...
//	    zerg_match_panic(<pos>);
//	    matchend_<n>: ;
//	}
//
// We use `goto` to jump to the next arm rather than `break`/do-while(0)
// because the body may itself contain `break` / `continue` that should
// affect an enclosing loop, not the match. (do-while(0) would swallow
// `break`.) `goto` is the simplest portable lowering that preserves
// statement transparency.
func (g *cgen) emitMatch(s *syntax.MatchStmt) error {
	g.matchCounter++
	id := g.matchCounter
	subjT := s.Subject.Type()
	if subjT == nil {
		return fmt.Errorf("codegen: missing type for match subject at %s", s.Pos)
	}
	subjStr, err := g.exprStr(s.Subject)
	if err != nil {
		return err
	}
	subjVar := fmt.Sprintf("__zg_subj_%d", id)
	endLabel := fmt.Sprintf("matchend_%d", id)
	posStr := fmt.Sprintf("%d:%d", s.Pos.Line, s.Pos.Column)

	g.writeIndent()
	g.b.WriteString("{\n")
	g.indent++
	// Snapshot the subject into a local so binding patterns and tests
	// reference a stable value without re-evaluating the subject (which
	// may have side effects). At v0.3 we don't deep-copy — the snapshot
	// shares the underlying buffer/struct with the subject, and the
	// borrow checker has BorrowedShared the subject for the duration.
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s %s = %s;\n",
		cTypeName(subjT), subjVar, subjStr)

	for i := range s.Arms {
		arm := &s.Arms[i]
		nextLabel := fmt.Sprintf("matcharm_%d_%d", id, i+1)

		g.writeIndent()
		g.b.WriteString("{\n")
		g.indent++

		// Pattern test: jump to the next-arm label on failure. Wildcard /
		// bind always pass, so we skip emitting the test entirely when it
		// would be a no-op.
		test := g.patternTest(arm.Pattern, subjVar, subjT)
		if test != "1" {
			g.writeIndent()
			fmt.Fprintf(&g.b, "if (!(%s)) goto %s;\n", test, nextLabel)
		}
		// Pattern bindings: declare locals from the matched parts.
		if err := g.emitPatternBindings(arm.Pattern, subjVar, subjT); err != nil {
			return err
		}
		// Guard: same fallthrough on false.
		if arm.Guard != nil {
			gs, err := g.exprStr(arm.Guard)
			if err != nil {
				return err
			}
			g.writeIndent()
			fmt.Fprintf(&g.b, "if (!(%s)) goto %s;\n", gs, nextLabel)
		}
		// Body:
		for _, st := range arm.Body.Statements {
			if err := g.emitStmt(st); err != nil {
				return err
			}
		}
		// Successful arm — skip remaining arms.
		g.writeIndent()
		fmt.Fprintf(&g.b, "goto %s;\n", endLabel)

		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
		// next-arm label, even on the last arm (cheaper than book-keeping
		// to skip the trailing label, and the C compiler folds away the
		// unreachable label).
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s: ;\n", nextLabel)
	}

	// Fall-through: no arm matched ⇒ panic.
	g.writeIndent()
	fmt.Fprintf(&g.b, "zerg_match_panic(%q);\n", posStr)

	// End label. Wrap in `;` so the label always has a statement after it
	// even when the surrounding block ends here.
	g.indent--
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s: ;\n", endLabel)
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// patternTest returns a C boolean expression that's true iff pat matches the
// scrutinee at C expression `scrut`. Returns "1" for wildcard/bind patterns
// (which always match).
func (g *cgen) patternTest(pat syntax.Pattern, scrut string, scrutT *syntax.Type) string {
	switch p := pat.(type) {
	case *syntax.WildcardPat, *syntax.BindPat:
		_ = p
		return "1"
	case *syntax.LitPat:
		// Lit is a primitive literal (optionally negated); emit a == compare.
		// Strings need zerg_str_eq.
		litS, err := g.exprStr(p.Lit)
		if err != nil {
			// Should not happen post-typeck; emit a guard that always fails
			// so the arm is skipped rather than miscompiled.
			return "0"
		}
		t := p.Lit.Type()
		if t == syntax.TStr() {
			return fmt.Sprintf("zerg_str_eq(%s, %s)", scrut, litS)
		}
		return fmt.Sprintf("(%s == %s)", scrut, litS)
	case *syntax.TuplePat:
		var parts []string
		for i, sub := range p.Elements {
			if scrutT == nil || scrutT.Kind != syntax.TypeTuple {
				return "0"
			}
			parts = append(parts, g.patternTest(sub,
				fmt.Sprintf("%s.e%d", scrut, i), scrutT.Tuple[i]))
		}
		return joinAnd(parts)
	case *syntax.StructPat:
		var parts []string
		for _, f := range p.Fields {
			fieldT := lookupFieldType(scrutT, f.Name)
			parts = append(parts, g.patternTest(f.Pattern,
				fmt.Sprintf("%s.%s", scrut, mangleField(f.Name)), fieldT))
		}
		return joinAnd(parts)
	case *syntax.EnumPat:
		// Variant tag test plus per-position payload pattern tests.
		idx := variantIndex(scrutT, p.VariantName)
		head := fmt.Sprintf("(%s.tag == %d)", scrut, idx)
		if len(p.Payload) == 0 {
			return head
		}
		payloadTypes := variantPayload(scrutT, idx)
		parts := []string{head}
		for i, sub := range p.Payload {
			var pt *syntax.Type
			if i < len(payloadTypes) {
				pt = payloadTypes[i]
			}
			access := fmt.Sprintf("%s.payload.p%d.a%d", scrut, idx, i)
			parts = append(parts, g.patternTest(sub, access, pt))
		}
		return joinAnd(parts)
	}
	return "0"
}

// joinAnd returns the C expression "(p1) && (p2) && ...". Returns "1" for the
// empty list (pattern with no sub-tests, e.g. `Point { .. }`).
func joinAnd(parts []string) string {
	out := []string{}
	for _, p := range parts {
		if p == "1" {
			continue
		}
		out = append(out, "("+p+")")
	}
	if len(out) == 0 {
		return "1"
	}
	return strings.Join(out, " && ")
}

// emitPatternBindings emits local-variable declarations for every BindPat
// nested in pat. The bound name receives a deep copy of the corresponding
// piece of scrut.
func (g *cgen) emitPatternBindings(pat syntax.Pattern, scrut string, scrutT *syntax.Type) error {
	switch p := pat.(type) {
	case *syntax.WildcardPat, *syntax.LitPat:
		_ = p
		return nil
	case *syntax.EnumPat:
		// Recurse into per-position payload sub-patterns so a BindPat at any
		// payload slot creates a local binding to that payload value.
		if len(p.Payload) == 0 {
			return nil
		}
		idx := variantIndex(scrutT, p.VariantName)
		payloadTypes := variantPayload(scrutT, idx)
		for i, sub := range p.Payload {
			var pt *syntax.Type
			if i < len(payloadTypes) {
				pt = payloadTypes[i]
			}
			access := fmt.Sprintf("%s.payload.p%d.a%d", scrut, idx, i)
			if err := g.emitPatternBindings(sub, access, pt); err != nil {
				return err
			}
		}
		return nil
	case *syntax.BindPat:
		// At v0.3 the bound name shares the matched value (no deep copy).
		// The borrow checker has flagged the scrutinee as Moved at exit
		// for BindPat arms, so the user can't observe aliasing.
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s %s = %s;\n",
			cTypeName(scrutT), mangle(p.Name), scrut)
		return nil
	case *syntax.TuplePat:
		if scrutT == nil || scrutT.Kind != syntax.TypeTuple {
			return fmt.Errorf("codegen: tuple pattern against non-tuple type")
		}
		for i, sub := range p.Elements {
			if err := g.emitPatternBindings(sub,
				fmt.Sprintf("%s.e%d", scrut, i), scrutT.Tuple[i]); err != nil {
				return err
			}
		}
		return nil
	case *syntax.StructPat:
		for _, f := range p.Fields {
			fieldT := lookupFieldType(scrutT, f.Name)
			if err := g.emitPatternBindings(f.Pattern,
				fmt.Sprintf("%s.%s", scrut, mangleField(f.Name)), fieldT); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// lookupFieldType returns the field type of t.Name. Returns nil if not found
// (which should not happen post-typeck).
func lookupFieldType(t *syntax.Type, fieldName string) *syntax.Type {
	if t == nil || t.Kind != syntax.TypeStruct {
		return nil
	}
	for _, f := range t.Fields {
		if f.Name == fieldName {
			return f.Type
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Expression rendering.
// ---------------------------------------------------------------------------

func (g *cgen) exprStr(expr syntax.Expr) (string, error) {
	switch e := expr.(type) {
	case *syntax.IntLit:
		return fmt.Sprintf("INT64_C(%d)", e.Int), nil
	case *syntax.FloatLit:
		return strconv.FormatFloat(e.Float, 'g', 17, 64), nil
	case *syntax.StringLit:
		return fmt.Sprintf("zerg_str_lit(%s, %d)", cQuote(e.Value), len(e.Value)), nil
	case *syntax.BoolLit:
		if e.Value {
			return "(_Bool)1", nil
		}
		return "(_Bool)0", nil
	case *syntax.IdentExpr:
		return mangle(e.Name), nil
	case *syntax.ParenExpr:
		inner, err := g.exprStr(e.Inner)
		if err != nil {
			return "", err
		}
		return "(" + inner + ")", nil
	case *syntax.UnaryExpr:
		return g.unaryStr(e)
	case *syntax.BinaryExpr:
		return g.binaryStr(e)
	case *syntax.RuneLit:
		// Rune literal → integer constant. typeck classified it as byte
		// (codepoint < 128) or rune; cType picks the right C int width.
		if e.Type() == syntax.TByte() {
			return fmt.Sprintf("((uint8_t)%d)", e.Value), nil
		}
		return fmt.Sprintf("((int32_t)%d)", e.Value), nil
	case *syntax.ListLit:
		return g.listLitStr(e)
	case *syntax.TupleLit:
		return g.tupleLitStr(e)
	case *syntax.StructLit:
		return g.structLitStr(e)
	case *syntax.IndexExpr:
		return g.indexStr(e)
	case *syntax.SliceExpr:
		return g.sliceStr(e)
	case *syntax.FieldAccessExpr:
		return g.fieldAccessStr(e)
	case *syntax.CallExpr:
		return g.callStr(e)
	case *syntax.MethodCallExpr:
		return g.methodCallStr(e)
	case *syntax.ThisExpr:
		// `this` is the implicit receiver parameter inside a method body;
		// we emit it as a C identifier `z_this` (mangled like any local).
		return mangle("this"), nil
	case *syntax.EnumLit:
		return g.enumLitStr(e)
	}
	return "", fmt.Errorf("codegen: unhandled expression %T at %s", expr, expr.ExprPos())
}

// listLitStr emits a list literal as a C statement-expression that allocates
// a backing buffer, fills it with element values (each deep-copied), and
// returns a list-shape struct value.
//
// We use GCC/Clang's `({ ... })` statement-expression extension because
// constructing a list value inline in an arbitrary expression position
// otherwise requires a helper macro per shape. The PLAN's portability
// requirement is "compile under cc -fwrapv -O2 -lm" — both gcc and clang
// (the only two `cc` shipped today) support statement expressions.
func (g *cgen) listLitStr(e *syntax.ListLit) (string, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeList {
		return "", fmt.Errorf("codegen: list literal has non-list type at %s", e.Pos)
	}
	mname := mangleType(t)
	elem := cTypeName(t.Element)
	var b strings.Builder
	// cap == len at construction; the per-shape push helper doubles cap
	// when len catches up. Element values are NOT deep-copied at v0.3 —
	// the borrow checker has invalidated source bindings at any move
	// site, so sharing the underlying value is safe.
	fmt.Fprintf(&b, "({ %s __l; __l.len = %d; __l.cap = %d; ", mname, len(e.Elements), len(e.Elements))
	if len(e.Elements) == 0 {
		fmt.Fprintf(&b, "__l.data = (%s*)malloc(1); ", elem)
	} else {
		fmt.Fprintf(&b, "__l.data = (%s*)malloc(%d * sizeof(%s)); ", elem, len(e.Elements), elem)
		for i, sub := range e.Elements {
			s, err := g.exprStr(sub)
			if err != nil {
				return "", err
			}
			// v0.4: when the list element type is a spec, each concrete
			// element coerces to a fat pointer at the construction site.
			s = g.coerceCExpr(s, sub.Type(), t.Element)
			fmt.Fprintf(&b, "__l.data[%d] = %s; ", i, s)
		}
	}
	fmt.Fprintf(&b, "__l; })")
	return b.String(), nil
}

// tupleLitStr emits a tuple literal as a `(zerg_tuple_<...>){.e0 = ..., .e1 =
// ...}` compound literal. C99 designated initialisers handle the rest. At
// v0.3 we do NOT deep-copy composite elements — the borrow checker has
// invalidated source bindings at the move site.
func (g *cgen) tupleLitStr(e *syntax.TupleLit) (string, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeTuple {
		return "", fmt.Errorf("codegen: tuple literal has non-tuple type at %s", e.Pos)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "((%s){", mangleType(t))
	for i, sub := range e.Elements {
		if i > 0 {
			b.WriteString(", ")
		}
		s, err := g.exprStr(sub)
		if err != nil {
			return "", err
		}
		// v0.4: per-position spec coercion for tuple element types.
		if i < len(t.Tuple) {
			s = g.coerceCExpr(s, sub.Type(), t.Tuple[i])
		}
		fmt.Fprintf(&b, ".e%d = %s", i, s)
	}
	b.WriteString("})")
	return b.String(), nil
}

// structLitStr emits a struct literal via designated initialisers; field
// order follows declaration order so the C compiler's "missing field"
// warning would catch any drift.
func (g *cgen) structLitStr(e *syntax.StructLit) (string, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeStruct {
		return "", fmt.Errorf("codegen: struct literal has non-struct type at %s", e.Pos)
	}
	// Index user inits by name for lookup in declaration order.
	byName := map[string]syntax.Expr{}
	for _, init := range e.Fields {
		byName[init.Name] = init.Value
	}
	var b strings.Builder
	fmt.Fprintf(&b, "((%s){", mangleType(t))
	for i, f := range t.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		val, ok := byName[f.Name]
		if !ok {
			return "", fmt.Errorf("codegen: struct %q literal missing field %q at %s",
				t.Name, f.Name, e.Pos)
		}
		s, err := g.exprStr(val)
		if err != nil {
			return "", err
		}
		// v0.4: per-field spec coercion when the declared field type is a
		// spec (or contains one).
		s = g.coerceCExpr(s, val.Type(), f.Type)
		fmt.Fprintf(&b, ".%s = %s", mangleField(f.Name), s)
	}
	b.WriteString("})")
	return b.String(), nil
}

// indexStr emits `xs[i]` access. For lists it bounds-checks via a helper and
// returns a deep-copy of the element. For str it walks UTF-8 and returns the
// rune at codepoint position i.
func (g *cgen) indexStr(e *syntax.IndexExpr) (string, error) {
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	is, err := g.exprStr(e.Index)
	if err != nil {
		return "", err
	}
	rt := e.Receiver.Type()
	posStr := fmt.Sprintf("%d:%d", e.Pos.Line, e.Pos.Column)
	switch {
	case rt != nil && rt.Kind == syntax.TypeList:
		// Bounds-check via a statement-expression; the result aliases the
		// element in the underlying buffer (no implicit copy at v0.3).
		mname := mangleType(rt)
		return fmt.Sprintf(
			"({ %s __r = %s; int64_t __i = %s; zerg_index_check(__i, __r.len, %q); __r.data[__i]; })",
			mname, rs, is, posStr), nil
	case rt == syntax.TStr():
		return fmt.Sprintf("zerg_str_rune_at(%s, %s, %q)", rs, is, posStr), nil
	}
	return "", fmt.Errorf("codegen: cannot index %s at %s", rt, e.Pos)
}

// sliceStr lowers a SliceExpr to a per-shape slice helper call, taking care
// of open ends and inclusive bounds via small adjustments at the call site.
func (g *cgen) sliceStr(e *syntax.SliceExpr) (string, error) {
	rt := e.Receiver.Type()
	if rt == nil || rt.Kind != syntax.TypeList {
		return "", fmt.Errorf("codegen: cannot slice %s at %s", rt, e.Pos)
	}
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	// Build lo / hi expressions. We need to evaluate the receiver once to
	// reach .len for omitted high bounds, so wrap in a statement-expression.
	mname := mangleType(rt)
	posStr := fmt.Sprintf("%d:%d", e.Pos.Line, e.Pos.Column)
	var lo, hi string
	if e.Low != nil {
		s, err := g.exprStr(e.Low)
		if err != nil {
			return "", err
		}
		lo = s
	} else {
		lo = "INT64_C(0)"
	}
	if e.High != nil {
		s, err := g.exprStr(e.High)
		if err != nil {
			return "", err
		}
		if e.Inclusive {
			hi = fmt.Sprintf("(%s + INT64_C(1))", s)
		} else {
			hi = s
		}
	} else {
		// Use the receiver temp's .len; fold via statement-expression below.
		hi = "(int64_t)__rcv.len"
	}
	return fmt.Sprintf("({ %s __rcv = %s; %s_slice(__rcv, %s, %s, %q); })",
		mname, rs, mname, lo, hi, posStr), nil
}

// fieldAccessStr emits struct field access OR enum variant access. typeck
// has already disambiguated: a FieldAccessExpr whose receiver is a bare
// IdentExpr that resolves to an enum type is the variant form.
func (g *cgen) fieldAccessStr(e *syntax.FieldAccessExpr) (string, error) {
	// v0.4: if typeck lowered this to an EnumLit (bare-variant construction),
	// route through the EnumLit emitter so the tag+union struct shape is
	// produced uniformly with the payloadful form.
	if e.Lowered != nil {
		return g.enumLitStr(e.Lowered)
	}
	if id, ok := e.Receiver.(*syntax.IdentExpr); ok {
		if rt := id.Type(); rt != nil && rt.Kind == syntax.TypeEnum {
			// Bare variant access without a Lowered EnumLit (e.g. inside a
			// match scrutinee or a context where typeck didn't lower).
			// Construct the compound literal directly.
			idx := variantIndex(rt, e.FieldName)
			return fmt.Sprintf("((%s){.tag = %d})", mangleType(rt), idx), nil
		}
	}
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	rt := e.Receiver.Type()
	if rt == nil || rt.Kind != syntax.TypeStruct {
		return "", fmt.Errorf("codegen: cannot access field on %s at %s", rt, e.Pos)
	}
	// Validate the field exists; the access itself is a direct member
	// reference (no implicit deep copy at v0.3).
	found := false
	for _, f := range rt.Fields {
		if f.Name == e.FieldName {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("codegen: struct %s has no field %q at %s", rt.Name, e.FieldName, e.Pos)
	}
	access := fmt.Sprintf("(%s).%s", rs, mangleField(e.FieldName))
	return access, nil
}

// callStr handles user-fn calls and the `len` / `clone` / `push` built-ins.
// At v0.3 fn-call composite args are implicit shared borrows — NO implicit
// deep copy at the call site. `clone(xs)` is the explicit opt-in for the
// v0.2-style deep copy; it remains the only call-site of the per-shape
// `_copy` helper.
func (g *cgen) callStr(e *syntax.CallExpr) (string, error) {
	ident, ok := e.Callee.(*syntax.IdentExpr)
	if !ok {
		return "", fmt.Errorf("codegen: non-ident callee at %s", e.Pos)
	}
	if ident.Name == "len" {
		if len(e.Args) != 1 {
			return "", fmt.Errorf("codegen: len expects 1 arg at %s", e.Pos)
		}
		argT := e.Args[0].Type()
		argS, err := g.exprStr(e.Args[0])
		if err != nil {
			return "", err
		}
		if argT == syntax.TStr() {
			return fmt.Sprintf("zerg_str_runelen(%s)", argS), nil
		}
		// list — len is an int64 view of size_t.
		return fmt.Sprintf("((int64_t)((%s).len))", argS), nil
	}
	if ident.Name == "clone" {
		// clone(x) returns a fresh deep copy of its composite argument.
		// typeck rejects primitives; the borrow checker has confirmed the
		// receiver is observed (not consumed). The emit is the existing
		// per-shape _copy helper — same path the v0.2 implicit-bind copy
		// took, just exposed under the user-visible builtin name.
		if len(e.Args) != 1 {
			return "", fmt.Errorf("codegen: clone expects 1 arg at %s", e.Pos)
		}
		argS, err := g.exprStr(e.Args[0])
		if err != nil {
			return "", err
		}
		return copyExpr(e.Args[0].Type(), argS), nil
	}
	if ident.Name == "push" {
		// push(xs, v) appends v to xs in place via the per-shape grow
		// helper, which doubles cap when len catches up. typeck has
		// required xs to be a top-level mut-bound list ident; the borrow
		// checker has validated state.
		if len(e.Args) != 2 {
			return "", fmt.Errorf("codegen: push expects 2 args at %s", e.Pos)
		}
		id, ok := e.Args[0].(*syntax.IdentExpr)
		if !ok {
			return "", fmt.Errorf("codegen: push first arg must be ident at %s", e.Pos)
		}
		valS, err := g.exprStr(e.Args[1])
		if err != nil {
			return "", err
		}
		listT := e.Args[0].Type()
		if listT == nil || listT.Kind != syntax.TypeList {
			return "", fmt.Errorf("codegen: push first arg must be list at %s", e.Pos)
		}
		nameS := mangle(id.Name)
		// `_push` returns void; we emit it as an expression so the caller
		// (an ExprStmt-wrapped call) compiles. Wrap in `(<call>, 0)` so
		// the comma expression has a non-void value, matching how the
		// previous lowering shaped the expression position.
		expr := fmt.Sprintf("(%s_push(&%s, %s), 0)", mangleType(listT), nameS, valS)
		return expr, nil
	}
	var paramTypes []*syntax.Type
	if fn, ok := g.fnTable[ident.Name]; ok {
		for _, p := range fn.Params {
			if p.Type != nil {
				paramTypes = append(paramTypes, p.Type.Resolved)
			} else {
				paramTypes = append(paramTypes, nil)
			}
		}
	}
	args, err := g.coerceArgs(e.Args, paramTypes)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(mangle(ident.Name))
	sb.WriteByte('(')
	for i, a := range args {
		if i > 0 {
			sb.WriteString(", ")
		}
		// At v0.3 fn-call composite args are implicit shared borrows —
		// no deep copy. The borrow checker has confirmed the caller's
		// binding remains valid for the call duration. v0.4 adds spec
		// coercion via coerceArgs above.
		sb.WriteString(a)
	}
	sb.WriteByte(')')
	return sb.String(), nil
}

// unaryStr lowers -, ~, not.
func (g *cgen) unaryStr(e *syntax.UnaryExpr) (string, error) {
	inner, err := g.exprStr(e.Operand)
	if err != nil {
		return "", err
	}
	switch e.Op {
	case syntax.UnaryNeg:
		return fmt.Sprintf("(-%s)", inner), nil
	case syntax.UnaryBitNot:
		return fmt.Sprintf("(~%s)", inner), nil
	case syntax.UnaryNot:
		return fmt.Sprintf("(!%s)", inner), nil
	}
	return "", fmt.Errorf("codegen: unknown unary op %s at %s", e.Op, e.Pos)
}

// binaryStr lowers each binary operator. Identical to v0.1 with the addition
// that byte/rune comparisons fall through to integer compares (already true
// for `==` because TByte/TRune are tracked as primitives in typeck).
func (g *cgen) binaryStr(e *syntax.BinaryExpr) (string, error) {
	left, err := g.exprStr(e.Left)
	if err != nil {
		return "", err
	}
	right, err := g.exprStr(e.Right)
	if err != nil {
		return "", err
	}
	lt := e.Left.Type()
	switch e.Op {
	case syntax.BinAdd:
		if lt == syntax.TStr() {
			return fmt.Sprintf("zerg_str_concat(%s, %s)", left, right), nil
		}
		return infix(left, "+", right), nil
	case syntax.BinSub:
		return infix(left, "-", right), nil
	case syntax.BinMul:
		return infix(left, "*", right), nil
	case syntax.BinDiv:
		return infix(left, "/", right), nil
	case syntax.BinFloorDiv:
		if lt == syntax.TFloat() {
			return fmt.Sprintf("floor((%s) / (%s))", left, right), nil
		}
		return infix(left, "/", right), nil
	case syntax.BinMod:
		if lt == syntax.TFloat() {
			return fmt.Sprintf("fmod((%s), (%s))", left, right), nil
		}
		return infix(left, "%", right), nil
	case syntax.BinBitAnd:
		return infix(left, "&", right), nil
	case syntax.BinBitOr:
		return infix(left, "|", right), nil
	case syntax.BinBitXor:
		return infix(left, "^", right), nil
	case syntax.BinShl:
		return infix(left, "<<", right), nil
	case syntax.BinShr:
		return infix(left, ">>", right), nil
	case syntax.BinEq:
		if lt == syntax.TStr() {
			return fmt.Sprintf("zerg_str_eq(%s, %s)", left, right), nil
		}
		if lt != nil {
			switch lt.Kind {
			case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
				return fmt.Sprintf("%s_eq(%s, %s)", mangleType(lt), left, right), nil
			}
		}
		return infix(left, "==", right), nil
	case syntax.BinNE:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(!zerg_str_eq(%s, %s))", left, right), nil
		}
		if lt != nil {
			switch lt.Kind {
			case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
				return fmt.Sprintf("(!%s_eq(%s, %s))", mangleType(lt), left, right), nil
			}
		}
		return infix(left, "!=", right), nil
	case syntax.BinLT:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) < 0)", left, right), nil
		}
		return infix(left, "<", right), nil
	case syntax.BinGT:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) > 0)", left, right), nil
		}
		return infix(left, ">", right), nil
	case syntax.BinLE:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) <= 0)", left, right), nil
		}
		return infix(left, "<=", right), nil
	case syntax.BinGE:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) >= 0)", left, right), nil
		}
		return infix(left, ">=", right), nil
	case syntax.BinAnd:
		return infix(left, "&&", right), nil
	case syntax.BinOr:
		return infix(left, "||", right), nil
	case syntax.BinXor:
		return fmt.Sprintf("((_Bool)(%s) ^ (_Bool)(%s))", left, right), nil
	}
	return "", fmt.Errorf("codegen: unknown binary op %s at %s", e.Op, e.Pos)
}

func infix(left, op, right string) string {
	return "(" + left + " " + op + " " + right + ")"
}

// ---------------------------------------------------------------------------
// Type-name and identifier mangling.
// ---------------------------------------------------------------------------

// cTypeName maps a Zerg type to its C representation.
//
//   - int   → int64_t
//   - float → double
//   - bool  → _Bool
//   - str   → zerg_str
//   - byte  → uint8_t
//   - rune  → int32_t
//   - list[T] / tuple[...] / struct Name / enum Name → mangled per-shape name
func cTypeName(t *syntax.Type) string {
	if t == nil {
		return "void"
	}
	switch t {
	case syntax.TInt():
		return "int64_t"
	case syntax.TFloat():
		return "double"
	case syntax.TBool():
		return "_Bool"
	case syntax.TStr():
		return "zerg_str"
	case syntax.TByte():
		return "uint8_t"
	case syntax.TRune():
		return "int32_t"
	case syntax.TVoid():
		return "void"
	}
	switch t.Kind {
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum, syntax.TypeSpec:
		return mangleType(t)
	}
	return "void"
}

// mangleType returns a stable C identifier for any Zerg type, suitable for
// use as a typedef name and as a suffix on per-shape helpers.
//
// Mangling rules:
//
//   - int   → "int64_t"
//   - float → "double"
//   - bool  → "_Bool" — used inline only; not a valid typedef leaf, but composite
//     wrappers don't use it as the final component.
//   - str   → "zerg_str"
//   - byte  → "uint8_t"
//   - rune  → "int32_t"
//   - list[T] → "zerg_list_<mangle(T)>"
//   - tuple[T1,T2] → "zerg_tuple_<mangle(T1)>_<mangle(T2)>"
//   - struct Name → "zerg_struct_<Name>"
//   - enum Name   → "zerg_enum_<Name>"
//
// Mangling is purely structural for list/tuple — two `list[int]` constructed
// at different sites produce identical names. Struct/enum mangle by name
// (typeck guarantees one canonical type per name).
func mangleType(t *syntax.Type) string {
	if t == nil {
		return "void"
	}
	switch t {
	case syntax.TInt():
		return "int64_t"
	case syntax.TFloat():
		return "double"
	case syntax.TBool():
		return "bool" // _Bool is not a valid identifier suffix
	case syntax.TStr():
		return "zerg_str"
	case syntax.TByte():
		return "uint8_t"
	case syntax.TRune():
		return "int32_t"
	}
	switch t.Kind {
	case syntax.TypeList:
		return "zerg_list_" + mangleType(t.Element)
	case syntax.TypeTuple:
		var parts []string
		for _, e := range t.Tuple {
			parts = append(parts, mangleType(e))
		}
		return "zerg_tuple_" + strings.Join(parts, "_")
	case syntax.TypeStruct:
		return "zerg_struct_" + t.Name
	case syntax.TypeEnum:
		return "zerg_enum_" + t.Name
	case syntax.TypeSpec:
		return "zerg_dyn_" + t.Name
	}
	return "void"
}

// mangle prefixes Zerg variable / function names with `z_` so they cannot
// clash with C keywords or runtime helpers.
func mangle(name string) string {
	return "z_" + name
}

// mangleField prefixes struct field names with `f_` so a field named `for`
// or `int` does not collide with a C keyword.
func mangleField(name string) string {
	return "f_" + name
}

// cQuote returns a C string literal whose runtime value equals s. Non-printable
// bytes are emitted as octal escapes so the output is portable across compilers.
func cQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ---------------------------------------------------------------------------
// v0.4 — specs, impls, vtables, method calls, fat pointers, NotImplemented.
//
// Layout:
//
//   * Per spec: a `zerg_dyn_<Spec>` struct typedef carrying (data, vt) plus
//     a `struct zerg_vtable_<Spec>` listing one function pointer per method
//     in declaration order (typeck pinned the order for determinism).
//
//   * Per (Type, Spec) pair an impl block exists for: a static const
//     `zerg_vt_<Type>_<Spec>` initialised with each method's resolution —
//     impl override > spec default specialised to this Type > NotImplemented
//     stub specialised to this Type / method / spec / pos.
//
//   * Per inherent impl method: a static C fn `zerg_struct_<T>__<method>` (or
//     `zerg_enum_...`) taking `<MangledType> z_this` as the first parameter.
//
//   * Per spec impl method: a static C fn
//     `zerg_struct_<T>__<Spec>__<method>` likewise. The fn pointer in the
//     vtable wraps this through a `void* this` adapter so the vtable type is
//     spec-uniform regardless of which Type provides the method.
//
//   * Spec coercion (let/arg/return/list-elem/tuple-pos/struct-field/
//     enum-payload of spec type) wraps the concrete value in a fat pointer.
//     The wrapped value is heap-boxed so the fat pointer can outlive the
//     stack frame the source value lives in (mirrors run.go's specVal which
//     holds *Value).
// ---------------------------------------------------------------------------

// collectSpecsImpls walks top-level statements once to populate the
// spec-decl, inherent-impl, and spec-impl tables that every later v0.4 emit
// pass consults. Mirrors the interpreter's two-pass collection in newInterp.
func (g *cgen) collectSpecsImpls(prog *syntax.Program) {
	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *syntax.SpecDecl:
			if _, dup := g.specs[s.Name]; !dup {
				g.specOrder = append(g.specOrder, s.Name)
			}
			g.specs[s.Name] = s
		case *syntax.ImplDecl:
			// Resolve the receiver type by name. Any impl reaching codegen
			// has been validated by typeck, so the type resolution lookup
			// below is best-effort — if it fails we fall back to the methods
			// alone, which still emit the C fn but with no vtable wrapper.
			receiverT := g.lookupReceiverType(prog, s.Type)
			if receiverT != nil {
				g.receiverTypes[s.Type] = receiverT
			}
			if s.Spec == "" {
				if _, ok := g.inherent[s.Type]; !ok {
					g.inherentTypeOrder = append(g.inherentTypeOrder, s.Type)
				}
				g.inherent[s.Type] = append(g.inherent[s.Type], s.Methods...)
			} else {
				key := implKey{typeName: s.Type, specName: s.Spec}
				if _, ok := g.specImpls[key]; !ok {
					g.specImplKeys = append(g.specImplKeys, key)
				}
				g.specImpls[key] = s
				// Mark the spec as used so the fat-pointer typedef + vtable
				// struct emit even if the program never binds a spec-typed
				// value directly.
				g.specsUsed[s.Spec] = true
			}
		}
	}
}

// lookupReceiverType resolves a type name to its canonical *Type by walking
// the program's struct/enum decls. Returns nil if not found.
func (g *cgen) lookupReceiverType(prog *syntax.Program, name string) *syntax.Type {
	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *syntax.StructDecl:
			if s.Name == name {
				fields := make([]syntax.NamedField, len(s.Fields))
				for i, f := range s.Fields {
					if f.Type != nil && f.Type.Resolved != nil {
						fields[i] = syntax.NamedField{Name: f.Name, Type: f.Type.Resolved}
					}
				}
				return syntax.NewStructType(s.Name, fields)
			}
		case *syntax.EnumDecl:
			if s.Name == name {
				vs := make([]string, len(s.Variants))
				for i, v := range s.Variants {
					vs[i] = v.Name
				}
				return syntax.NewEnumType(s.Name, vs)
			}
		}
	}
	return nil
}

// collectSpecsInType records every spec-typed leaf reachable from t into
// g.specsUsed. Walks list/tuple/struct/enum-payload composites recursively
// so a `list[Printable]` registers the Printable spec for fat-pointer +
// vtable emission.
func (g *cgen) collectSpecsInType(t *syntax.Type) {
	if t == nil {
		return
	}
	switch t.Kind {
	case syntax.TypeSpec:
		g.specsUsed[t.Name] = true
	case syntax.TypeList:
		g.collectSpecsInType(t.Element)
	case syntax.TypeTuple:
		for _, e := range t.Tuple {
			g.collectSpecsInType(e)
		}
	case syntax.TypeStruct:
		for _, f := range t.Fields {
			g.collectSpecsInType(f.Type)
		}
	case syntax.TypeEnum:
		for _, payload := range t.VariantPayloads {
			for _, pt := range payload {
				g.collectSpecsInType(pt)
			}
		}
	}
}

// emitSpecForwardDecls writes the fat-pointer typedef and vtable struct
// for every spec used by the program (declared by `spec ...`, referenced by
// `let x: Spec = ...`, exposed by an `impl Type for Spec`, etc.). Method
// signatures inside the vtable struct take `void* this` so the vtable type
// is spec-uniform regardless of which Type provides the implementation.
func (g *cgen) emitSpecForwardDecls() {
	// Union the declared specs and the used specs into a single declaration-
	// order list; specs declared but never used still emit (the vtable struct
	// is harmless when no impl blocks reference it).
	seen := map[string]bool{}
	var order []string
	for _, n := range g.specOrder {
		if !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	for _, n := range g.specOrder {
		if g.specsUsed[n] && !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	if len(order) == 0 {
		return
	}
	g.b.WriteString("/* Spec fat-pointer typedefs and vtable struct definitions (v0.4). */\n")
	for _, name := range order {
		s := g.specs[name]
		// Vtable struct: one function pointer per spec method in declaration
		// order; each takes `void* this` plus the declared param types.
		fmt.Fprintf(&g.b, "struct zerg_vtable_%s {\n", name)
		if s == nil || len(s.Methods) == 0 {
			// An empty spec still gets a struct so sizeof(zerg_dyn_<Spec>)
			// is well-defined; emit a placeholder field.
			fmt.Fprintf(&g.b, "    char _empty;\n")
		} else {
			for _, m := range s.Methods {
				ret := "void"
				if m.Return != nil && m.Return.Resolved != nil && m.Return.Resolved != syntax.TVoid() {
					ret = cTypeName(m.Return.Resolved)
				}
				fmt.Fprintf(&g.b, "    %s (*%s)(void* z_this", ret, m.Name)
				for _, p := range m.Params {
					if p.Type != nil && p.Type.Resolved != nil {
						fmt.Fprintf(&g.b, ", %s %s", cTypeName(p.Type.Resolved), mangle(p.Name))
					}
				}
				fmt.Fprintf(&g.b, ");\n")
			}
		}
		fmt.Fprintf(&g.b, "};\n")
		// Fat pointer: data + vtable.
		fmt.Fprintf(&g.b, "typedef struct { void* data; const struct zerg_vtable_%s* vt; } zerg_dyn_%s;\n",
			name, name)
	}
	g.b.WriteString("\n")
}

// emitSpecVtablesAndMethods is the v0.4 file-scope emit pass. Order:
//
//  1. Forward-declare every emitted method C function so they can reference
//     each other in any order.
//  2. Emit each method body — both inherent and per-(Type, Spec) override.
//     A spec impl method gets one C fn whose receiver is the concrete
//     mangled type, plus a small `void*` adapter wrapper that downcasts to
//     the concrete and forwards. The adapter is what the vtable points at.
//  3. Emit the per-(Type, Spec) static const vtable initialiser populated
//     with adapter pointers for impl overrides, type-specialised default
//     adapters for spec defaults, and NotImplemented stubs for the
//     remainder.
func (g *cgen) emitSpecVtablesAndMethods(prog *syntax.Program) {
	if len(g.inherentTypeOrder) == 0 && len(g.specImplKeys) == 0 && len(g.specOrder) == 0 {
		return
	}
	// Stable ordering: inherent first by declaration order of types, then
	// (Type, Spec) impls by declaration order.
	g.b.WriteString("/* v0.4 method functions, vtable adapters, and per-(Type, Spec) vtables. */\n")

	// Forward decls.
	for _, typeName := range g.inherentTypeOrder {
		recv := g.receiverTypes[typeName]
		if recv == nil {
			continue
		}
		for _, fn := range g.inherent[typeName] {
			g.writeMethodSig(recv, "", fn)
			g.b.WriteString(";\n")
		}
	}
	for _, key := range g.specImplKeys {
		recv := g.receiverTypes[key.typeName]
		if recv == nil {
			continue
		}
		decl := g.specImpls[key]
		spec := g.specs[key.specName]
		for _, fn := range decl.Methods {
			g.writeMethodSig(recv, key.specName, fn)
			g.b.WriteString(";\n")
			// Adapter forward decl.
			fmt.Fprintf(&g.b, "static %s zerg_adapter_%s__%s__%s(void* z_this",
				returnCType(fn.Return), mangleType(recv), key.specName, fn.Name)
			for _, p := range fn.Params {
				if p.Type != nil && p.Type.Resolved != nil {
					fmt.Fprintf(&g.b, ", %s %s", cTypeName(p.Type.Resolved), mangle(p.Name))
				}
			}
			g.b.WriteString(");\n")
		}
		// Default-method adapters: for any spec method NOT overridden, we
		// either need a Type-specialised default adapter (if the spec
		// supplies a default body) or a NotImplemented stub.
		if spec != nil {
			for _, sm := range spec.Methods {
				overridden := false
				for _, fn := range decl.Methods {
					if fn.Name == sm.Name {
						overridden = true
						break
					}
				}
				if overridden {
					continue
				}
				if sm.Body != nil {
					// Type-specialised default-body adapter.
					fmt.Fprintf(&g.b, "static %s zerg_default_%s__%s__%s(void* z_this",
						returnCType(sm.Return), mangleType(recv), key.specName, sm.Name)
					for _, p := range sm.Params {
						if p.Type != nil && p.Type.Resolved != nil {
							fmt.Fprintf(&g.b, ", %s %s", cTypeName(p.Type.Resolved), mangle(p.Name))
						}
					}
					g.b.WriteString(");\n")
				} else {
					// NotImplemented stub.
					fmt.Fprintf(&g.b, "static %s zerg_not_impl_%s__%s__%s(void* z_this",
						returnCType(sm.Return), mangleType(recv), key.specName, sm.Name)
					for _, p := range sm.Params {
						if p.Type != nil && p.Type.Resolved != nil {
							fmt.Fprintf(&g.b, ", %s %s", cTypeName(p.Type.Resolved), mangle(p.Name))
						}
					}
					g.b.WriteString(");\n")
				}
			}
		}
	}
	g.b.WriteString("\n")

	// Method bodies — inherent.
	for _, typeName := range g.inherentTypeOrder {
		recv := g.receiverTypes[typeName]
		if recv == nil {
			continue
		}
		for _, fn := range g.inherent[typeName] {
			if err := g.emitMethodFn(recv, "", fn); err != nil {
				// Should not happen post-typeck; emit a comment so the C
				// compiler error points at the right method.
				fmt.Fprintf(&g.b, "/* codegen error in %s::%s: %v */\n", typeName, fn.Name, err)
			}
			g.b.WriteString("\n")
		}
	}
	// Method bodies — spec impls + adapters.
	for _, key := range g.specImplKeys {
		recv := g.receiverTypes[key.typeName]
		if recv == nil {
			continue
		}
		decl := g.specImpls[key]
		spec := g.specs[key.specName]
		for _, fn := range decl.Methods {
			if err := g.emitMethodFn(recv, key.specName, fn); err != nil {
				fmt.Fprintf(&g.b, "/* codegen error in %s::%s::%s: %v */\n",
					key.typeName, key.specName, fn.Name, err)
			}
			g.b.WriteString("\n")
			g.emitSpecAdapter(recv, key.specName, fn)
			g.b.WriteString("\n")
		}
		// Default-body adapters / NotImplemented stubs for unfilled methods.
		if spec != nil {
			for _, sm := range spec.Methods {
				overridden := false
				for _, fn := range decl.Methods {
					if fn.Name == sm.Name {
						overridden = true
						break
					}
				}
				if overridden {
					continue
				}
				if sm.Body != nil {
					g.emitSpecDefaultAdapter(recv, key.specName, sm)
					g.b.WriteString("\n")
				} else {
					g.emitNotImplementedStub(recv, key.specName, sm)
					g.b.WriteString("\n")
				}
			}
		}
	}

	// Per-(Type, Spec) static vtables.
	for _, key := range g.specImplKeys {
		recv := g.receiverTypes[key.typeName]
		if recv == nil {
			continue
		}
		spec := g.specs[key.specName]
		decl := g.specImpls[key]
		fmt.Fprintf(&g.b, "static const struct zerg_vtable_%s zerg_vt_%s_%s = {\n",
			key.specName, mangleType(recv), key.specName)
		if spec == nil || len(spec.Methods) == 0 {
			g.b.WriteString("    ._empty = 0,\n")
		} else {
			for _, sm := range spec.Methods {
				// Pick adapter target.
				overridden := false
				for _, fn := range decl.Methods {
					if fn.Name == sm.Name {
						overridden = true
						break
					}
				}
				switch {
				case overridden:
					fmt.Fprintf(&g.b, "    .%s = zerg_adapter_%s__%s__%s,\n",
						sm.Name, mangleType(recv), key.specName, sm.Name)
				case sm.Body != nil:
					fmt.Fprintf(&g.b, "    .%s = zerg_default_%s__%s__%s,\n",
						sm.Name, mangleType(recv), key.specName, sm.Name)
				default:
					fmt.Fprintf(&g.b, "    .%s = zerg_not_impl_%s__%s__%s,\n",
						sm.Name, mangleType(recv), key.specName, sm.Name)
				}
			}
		}
		g.b.WriteString("};\n")
	}
	g.b.WriteString("\n")
}

// writeMethodSig writes the C signature of a method fn (no trailing punct).
// receiver is the resolved Type; specName is "" for inherent, otherwise the
// spec name (used for mangling).
func (g *cgen) writeMethodSig(receiver *syntax.Type, specName string, fn *syntax.FnDecl) {
	g.b.WriteString("static ")
	g.b.WriteString(returnCType(fn.Return))
	g.b.WriteByte(' ')
	g.b.WriteString(methodMangle(receiver, specName, fn.Name))
	g.b.WriteByte('(')
	fmt.Fprintf(&g.b, "%s %s", cTypeName(receiver), mangle("this"))
	for _, p := range fn.Params {
		fmt.Fprintf(&g.b, ", %s %s", cTypeName(p.Type.Resolved), mangle(p.Name))
	}
	g.b.WriteByte(')')
}

// methodMangle returns the C identifier for a method function on receiver.
// Inherent: <MangledType>__<method>. Spec impl: <MangledType>__<Spec>__<method>.
func methodMangle(receiver *syntax.Type, specName, method string) string {
	if specName == "" {
		return mangleType(receiver) + "__" + method
	}
	return mangleType(receiver) + "__" + specName + "__" + method
}

// returnCType returns the C return-type string for a method, mapping a nil
// or void TypeRef to "void".
func returnCType(ref *syntax.TypeRef) string {
	if ref == nil || ref.Resolved == nil || ref.Resolved == syntax.TVoid() {
		return "void"
	}
	return cTypeName(ref.Resolved)
}

// emitMethodFn emits the body of an impl method. The receiver becomes the
// implicit first parameter `z_this`; the rest of the body lowers like a
// normal fn body via emitBlockBody.
func (g *cgen) emitMethodFn(receiver *syntax.Type, specName string, fn *syntax.FnDecl) error {
	g.writeMethodSig(receiver, specName, fn)
	g.b.WriteString(" {\n")
	prevRet := g.currentFnRet
	if fn.Return != nil {
		g.currentFnRet = fn.Return.Resolved
	} else {
		g.currentFnRet = nil
	}
	prevIndent := g.indent
	g.indent = 1
	defer func() {
		g.currentFnRet = prevRet
		g.indent = prevIndent
	}()
	if err := g.emitBlockBody(fn.Body); err != nil {
		return err
	}
	g.b.WriteString("}\n")
	return nil
}

// emitSpecAdapter emits the void* → concrete adapter that the vtable points
// at. The adapter casts the void pointer back to the concrete Type, derefs,
// and forwards to the real method fn.
func (g *cgen) emitSpecAdapter(receiver *syntax.Type, specName string, fn *syntax.FnDecl) {
	fmt.Fprintf(&g.b, "static %s zerg_adapter_%s__%s__%s(void* z_this",
		returnCType(fn.Return), mangleType(receiver), specName, fn.Name)
	for _, p := range fn.Params {
		fmt.Fprintf(&g.b, ", %s %s", cTypeName(p.Type.Resolved), mangle(p.Name))
	}
	g.b.WriteString(") {\n")
	hasReturn := fn.Return != nil && fn.Return.Resolved != nil && fn.Return.Resolved != syntax.TVoid()
	if hasReturn {
		g.b.WriteString("    return ")
	} else {
		g.b.WriteString("    ")
	}
	fmt.Fprintf(&g.b, "%s(*((%s*)z_this)", methodMangle(receiver, specName, fn.Name), cTypeName(receiver))
	for _, p := range fn.Params {
		fmt.Fprintf(&g.b, ", %s", mangle(p.Name))
	}
	g.b.WriteString(");\n")
	g.b.WriteString("}\n")
}

// emitSpecDefaultAdapter emits a Type-specialised version of a spec default
// method body. The default body refers to `this` as the implementing type;
// each (Type, Spec) pair that inherits the default produces its own copy of
// the lowered C function so `this` resolves to the right concrete type.
func (g *cgen) emitSpecDefaultAdapter(receiver *syntax.Type, specName string, sm *syntax.SpecMethod) {
	ret := returnCType(sm.Return)
	fmt.Fprintf(&g.b, "static %s zerg_default_%s__%s__%s(void* __zg_this_raw",
		ret, mangleType(receiver), specName, sm.Name)
	for _, p := range sm.Params {
		fmt.Fprintf(&g.b, ", %s %s", cTypeName(p.Type.Resolved), mangle(p.Name))
	}
	g.b.WriteString(") {\n")
	// Bind `this` to the concrete value so the body's `this` reference walks
	// the same path as inherent / impl method bodies.
	fmt.Fprintf(&g.b, "    %s %s = *((%s*)__zg_this_raw);\n", cTypeName(receiver), mangle("this"), cTypeName(receiver))
	prevRet := g.currentFnRet
	prevIndent := g.indent
	if sm.Return != nil {
		g.currentFnRet = sm.Return.Resolved
	} else {
		g.currentFnRet = nil
	}
	g.indent = 1
	defer func() {
		g.currentFnRet = prevRet
		g.indent = prevIndent
	}()
	if err := g.emitBlockBody(sm.Body); err != nil {
		fmt.Fprintf(&g.b, "    /* codegen error in default %s::%s::%s: %v */\n",
			receiver.Name, specName, sm.Name, err)
	}
	g.b.WriteString("}\n")
}

// emitNotImplementedStub emits a `__attribute__((noreturn))` C function for
// a spec method that has no impl override and no spec default. The function
// signature matches the vtable slot so it can be installed there directly.
// The diagnostic format is byte-identical to run.go's NotImplemented panic.
func (g *cgen) emitNotImplementedStub(receiver *syntax.Type, specName string, sm *syntax.SpecMethod) {
	ret := returnCType(sm.Return)
	fmt.Fprintf(&g.b, "static %s zerg_not_impl_%s__%s__%s(void* z_this",
		ret, mangleType(receiver), specName, sm.Name)
	for _, p := range sm.Params {
		fmt.Fprintf(&g.b, ", %s %s", cTypeName(p.Type.Resolved), mangle(p.Name))
	}
	g.b.WriteString(") {\n")
	g.b.WriteString("    (void)z_this;\n")
	for _, p := range sm.Params {
		fmt.Fprintf(&g.b, "    (void)%s;\n", mangle(p.Name))
	}
	posStr := fmt.Sprintf("%d:%d", sm.Pos.Line, sm.Pos.Column)
	fmt.Fprintf(&g.b, "    zerg_not_implemented(%q, %q, %q, %q);\n",
		receiver.Name, sm.Name, specName, posStr)
	// noreturn helper exits — but the C compiler still wants a path that
	// produces the declared return value. Add a defensive trap:
	if ret != "void" {
		fmt.Fprintf(&g.b, "    return (%s){0};\n", ret)
	}
	g.b.WriteString("}\n")
}

// ---------------------------------------------------------------------------
// Method-call expression lowering.
// ---------------------------------------------------------------------------

// methodCallStr lowers a MethodCallExpr. Routing precedence mirrors run.go:
//
//   1. typeck-lowered EnumLit (`Token.Ident("foo")`) → enumLitStr.
//   2. typeck-lowered builtin call (`xs.push(v)`) → existing callStr path.
//   3. Spec-typed receiver → fat-pointer vtable dispatch.
//   4. Concrete receiver → resolve to inherent or unique spec impl method,
//      emit a direct C fn call.
func (g *cgen) methodCallStr(e *syntax.MethodCallExpr) (string, error) {
	if e.Lowered != nil {
		return g.enumLitStr(e.Lowered)
	}
	if e.LoweredCall != nil {
		return g.callStr(e.LoweredCall)
	}
	rt := e.Receiver.Type()
	if rt == nil {
		return "", fmt.Errorf("codegen: method-call receiver has nil type at %s", e.Pos)
	}
	if rt.Kind == syntax.TypeSpec {
		return g.dispatchSpec(e, rt)
	}
	return g.dispatchConcrete(e, rt)
}

// dispatchSpec emits a fat-pointer vtable dispatch:
//
//	({ zerg_dyn_<Spec> __r = <recv>; __r.vt-><method>(__r.data, args...); })
//
// The receiver is snapshotted into a temp so a side-effecting receiver
// expression evaluates exactly once. typeck has validated that <method> is
// declared by the spec; codegen does not re-check.
func (g *cgen) dispatchSpec(e *syntax.MethodCallExpr, rt *syntax.Type) (string, error) {
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	specName := rt.Name
	spec := g.specs[specName]
	// Coerce each arg to the declared param type so spec-typed params widen.
	var paramTypes []*syntax.Type
	if spec != nil {
		for _, m := range spec.Methods {
			if m.Name == e.Method {
				for _, p := range m.Params {
					if p.Type != nil {
						paramTypes = append(paramTypes, p.Type.Resolved)
					} else {
						paramTypes = append(paramTypes, nil)
					}
				}
				break
			}
		}
	}
	args, err := g.coerceArgs(e.Args, paramTypes)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "({ %s __r = %s; __r.vt->%s(__r.data", mangleType(rt), rs, e.Method)
	for _, a := range args {
		sb.WriteString(", ")
		sb.WriteString(a)
	}
	sb.WriteString("); })")
	return sb.String(), nil
}

// dispatchConcrete emits a direct C method-fn call with the receiver as the
// first argument. Resolution: inherent methods first, then unique spec-impl
// method by name. typeck has rejected ambiguity so the first match suffices.
//
// For spec-impl-via-concrete dispatch we must also handle the spec-default
// and NotImplemented cases: we route via the same Type-specialised default
// adapter / NotImplemented stub the vtable uses.
func (g *cgen) dispatchConcrete(e *syntax.MethodCallExpr, rt *syntax.Type) (string, error) {
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	typeName := rt.Name
	// 1. Inherent.
	if methods, ok := g.inherent[typeName]; ok {
		for _, fn := range methods {
			if fn.Name == e.Method {
				return g.emitConcreteMethodCall(rt, "", fn, rs, e.Args)
			}
		}
	}
	// 2. Spec impl methods. Walk the spec impls for this type and find the
	// first one exposing the method (override or via default).
	for _, key := range g.specImplKeys {
		if key.typeName != typeName {
			continue
		}
		decl := g.specImpls[key]
		for _, fn := range decl.Methods {
			if fn.Name == e.Method {
				return g.emitConcreteMethodCall(rt, key.specName, fn, rs, e.Args)
			}
		}
		// No override — try default.
		spec := g.specs[key.specName]
		if spec != nil {
			for _, sm := range spec.Methods {
				if sm.Name != e.Method {
					continue
				}
				if sm.Body != nil {
					// Default body — call the type-specialised default
					// adapter.
					return g.emitConcreteSpecDefault(rt, key.specName, sm, rs, e.Args)
				}
				// Signature only — NotImplemented stub.
				return g.emitConcreteNotImpl(rt, key.specName, sm, rs, e.Args)
			}
		}
	}
	return "", fmt.Errorf("codegen: method %q on %s not resolvable at %s", e.Method, typeName, e.MethodPos)
}

// emitConcreteMethodCall renders a direct call to either an inherent or
// spec-impl method fn, passing the receiver value (NOT a pointer; cgen's
// method functions take the receiver by value, matching the v0.3 fn-call
// composite-arg convention).
func (g *cgen) emitConcreteMethodCall(rt *syntax.Type, specName string, fn *syntax.FnDecl, rs string, callArgs []syntax.Expr) (string, error) {
	var paramTypes []*syntax.Type
	for _, p := range fn.Params {
		if p.Type != nil {
			paramTypes = append(paramTypes, p.Type.Resolved)
		} else {
			paramTypes = append(paramTypes, nil)
		}
	}
	args, err := g.coerceArgs(callArgs, paramTypes)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(methodMangle(rt, specName, fn.Name))
	sb.WriteByte('(')
	sb.WriteString(rs)
	for _, a := range args {
		sb.WriteString(", ")
		sb.WriteString(a)
	}
	sb.WriteByte(')')
	return sb.String(), nil
}

// emitConcreteSpecDefault routes a concrete-receiver call to the per-(Type,
// Spec) default adapter — the same one the vtable points at when the impl
// inherits the spec's default. The adapter takes void* so we wrap the
// receiver in a temp and pass its address.
func (g *cgen) emitConcreteSpecDefault(rt *syntax.Type, specName string, sm *syntax.SpecMethod, rs string, callArgs []syntax.Expr) (string, error) {
	var paramTypes []*syntax.Type
	for _, p := range sm.Params {
		if p.Type != nil {
			paramTypes = append(paramTypes, p.Type.Resolved)
		} else {
			paramTypes = append(paramTypes, nil)
		}
	}
	args, err := g.coerceArgs(callArgs, paramTypes)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "({ %s __t = %s; zerg_default_%s__%s__%s(&__t",
		cTypeName(rt), rs, mangleType(rt), specName, sm.Name)
	for _, a := range args {
		sb.WriteString(", ")
		sb.WriteString(a)
	}
	sb.WriteString("); })")
	return sb.String(), nil
}

// emitConcreteNotImpl routes a concrete-receiver call to the per-(Type,
// Spec, method) NotImplemented stub. Same shape as the default adapter
// path but the call traps before returning.
func (g *cgen) emitConcreteNotImpl(rt *syntax.Type, specName string, sm *syntax.SpecMethod, rs string, callArgs []syntax.Expr) (string, error) {
	var paramTypes []*syntax.Type
	for _, p := range sm.Params {
		if p.Type != nil {
			paramTypes = append(paramTypes, p.Type.Resolved)
		} else {
			paramTypes = append(paramTypes, nil)
		}
	}
	args, err := g.coerceArgs(callArgs, paramTypes)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "({ %s __t = %s; zerg_not_impl_%s__%s__%s(&__t",
		cTypeName(rt), rs, mangleType(rt), specName, sm.Name)
	for _, a := range args {
		sb.WriteString(", ")
		sb.WriteString(a)
	}
	sb.WriteString("); })")
	return sb.String(), nil
}

// coerceArgs evaluates a slice of argument expressions and applies spec
// coercion to each one based on the matching declared param type. Used for
// both method-call and ordinary fn-call arg lowering when the callee carries
// spec-typed parameters.
func (g *cgen) coerceArgs(args []syntax.Expr, paramTypes []*syntax.Type) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		s, err := g.exprStr(a)
		if err != nil {
			return nil, err
		}
		var pt *syntax.Type
		if i < len(paramTypes) {
			pt = paramTypes[i]
		}
		out[i] = g.coerceCExpr(s, a.Type(), pt)
	}
	return out, nil
}

// coerceCExpr returns a C expression that produces a value of type `target`
// from a C expression of type `srcT`. The function is the codegen analogue
// of run.go's coerceToType: when srcT is a concrete struct/enum and target
// is a spec, we wrap the value in a fat pointer pointing at a heap-boxed
// copy of the source. Composite shapes (list/tuple/struct/enum-payload)
// recurse element-wise.
//
// Lifetime — at v0.4 the underlying value is heap-boxed so the fat pointer
// can outlive any local frame. malloc is leaked (consistent with the rest
// of the runtime which leaks list buffers and string concats too); a v0.5+
// arena will reclaim.
func (g *cgen) coerceCExpr(expr string, srcT, target *syntax.Type) string {
	if target == nil || srcT == nil {
		return expr
	}
	// If the source already has the same fully-resolved shape as the target
	// (including spec elements), no coercion is needed — every element
	// position has already been wrapped at the source-construction site
	// (list-lit / tuple-lit / struct-lit emit per-element coerces).
	if srcT.Equals(target) {
		return expr
	}
	if target.Kind == srcT.Kind && target.Kind != syntax.TypeSpec {
		// Same outer shape — only descend when recursion can hit a spec
		// inside (list[Spec], tuple[..., Spec], etc.).
		if !shapeContainsSpec(target) {
			return expr
		}
	}
	switch target.Kind {
	case syntax.TypeSpec:
		if srcT.Kind == syntax.TypeSpec {
			// Already wrapped — typeck rejects spec-to-different-spec at
			// v0.4 so just pass through.
			return expr
		}
		// Heap-box the concrete value, then wrap.
		concreteC := cTypeName(srcT)
		return fmt.Sprintf(
			"({ %s* __p = (%s*)malloc(sizeof(%s)); *__p = (%s); (zerg_dyn_%s){.data = __p, .vt = &zerg_vt_%s_%s}; })",
			concreteC, concreteC, concreteC, expr, target.Name, mangleType(srcT), target.Name)
	case syntax.TypeList:
		if srcT.Kind != syntax.TypeList {
			return expr
		}
		if !shapeContainsSpec(target) {
			return expr
		}
		// Build a fresh list, copying each element with element-coerce.
		mname := mangleType(target)
		elemC := cTypeName(target.Element)
		tmp := g.freshTmp("co")
		coerced := g.coerceCExpr(fmt.Sprintf("__src.data[__i]"), srcT.Element, target.Element)
		return fmt.Sprintf(
			"({ %s __src = (%s); %s %s; %s.len = __src.len; %s.cap = __src.len; %s.data = (%s*)malloc(%s.len ? %s.len * sizeof(%s) : 1); for (size_t __i = 0; __i < __src.len; __i++) { %s.data[__i] = %s; } %s; })",
			cTypeName(srcT), expr, mname, tmp,
			tmp, tmp, tmp, elemC, tmp, tmp, elemC,
			tmp, coerced, tmp)
	case syntax.TypeTuple:
		if srcT.Kind != syntax.TypeTuple || len(target.Tuple) != len(srcT.Tuple) {
			return expr
		}
		if !shapeContainsSpec(target) {
			return expr
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "({ %s __src = (%s); ((%s){", cTypeName(srcT), expr, mangleType(target))
		for i := range target.Tuple {
			if i > 0 {
				sb.WriteString(", ")
			}
			coerced := g.coerceCExpr(fmt.Sprintf("__src.e%d", i), srcT.Tuple[i], target.Tuple[i])
			fmt.Fprintf(&sb, ".e%d = %s", i, coerced)
		}
		sb.WriteString("}); })")
		return sb.String()
	}
	return expr
}

// shapeContainsSpec returns true if t (or any composite leaf reached via
// list/tuple/struct/enum-payload recursion) has Kind TypeSpec. Drives the
// per-shape coercion descent decision.
func shapeContainsSpec(t *syntax.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case syntax.TypeSpec:
		return true
	case syntax.TypeList:
		return shapeContainsSpec(t.Element)
	case syntax.TypeTuple:
		for _, e := range t.Tuple {
			if shapeContainsSpec(e) {
				return true
			}
		}
	case syntax.TypeStruct:
		for _, f := range t.Fields {
			if shapeContainsSpec(f.Type) {
				return true
			}
		}
	case syntax.TypeEnum:
		for _, payload := range t.VariantPayloads {
			for _, pt := range payload {
				if shapeContainsSpec(pt) {
					return true
				}
			}
		}
	}
	return false
}

// enumLitStr lowers an EnumLit to a compound literal of the per-shape enum
// struct: `((zerg_enum_<Name>){.tag = idx, .payload.pN = {.aJ = ...}})`.
// Bare variants omit the per-variant payload init (the union slot stays
// zero-initialised).
func (g *cgen) enumLitStr(e *syntax.EnumLit) (string, error) {
	en := e.Type()
	if en == nil || en.Kind != syntax.TypeEnum {
		return "", fmt.Errorf("codegen: enum literal has non-enum type at %s", e.Pos)
	}
	idx := variantIndex(en, e.Variant)
	if idx < 0 {
		return "", fmt.Errorf("codegen: enum %s has no variant %s at %s", en.Name, e.Variant, e.VariantPos)
	}
	mname := mangleType(en)
	if len(e.Payload) == 0 {
		return fmt.Sprintf("((%s){.tag = %d})", mname, idx), nil
	}
	payloadTypes := variantPayload(en, idx)
	var sb strings.Builder
	fmt.Fprintf(&sb, "((%s){.tag = %d, .payload.p%d = {", mname, idx, idx)
	for i, sub := range e.Payload {
		if i > 0 {
			sb.WriteString(", ")
		}
		s, err := g.exprStr(sub)
		if err != nil {
			return "", err
		}
		// Coerce each payload element to the declared variant payload type
		// so a payload position declared as a spec widens the concrete arg.
		var pt *syntax.Type
		if i < len(payloadTypes) {
			pt = payloadTypes[i]
		}
		s = g.coerceCExpr(s, sub.Type(), pt)
		fmt.Fprintf(&sb, ".a%d = %s", i, s)
	}
	sb.WriteString("}})")
	return sb.String(), nil
}

// emitEqHelpers emits a per-shape `_eq` helper for every composite shape in
// the program. typeck has validated that == on composites only invokes
// these helpers when the operand types match exactly. Recursion bottoms out
// at primitives (== / zerg_str_eq) and at nested composites (which have
// their own _eq helper emitted alongside).
//
// Spec-typed bindings reject == at typeck per PLAN.md, so this function
// never needs an _eq path for TypeSpec.
func (g *cgen) emitEqHelpers() {
	r := g.shapes
	if len(r.listOrder)+len(r.tupleOrder)+len(r.structOrder)+len(r.enumOrder) == 0 {
		return
	}
	// Skip shapes that transitively reach a spec-typed leaf — typeck rejects
	// composite == on those at v0.4 so the helper would never be called and
	// emitting one fails to compile (no eq for spec types).
	listKeys := filterEqShapes(r.listOrder, r.listShapes)
	tupleKeys := filterEqShapes(r.tupleOrder, r.tupleShapes)
	structKeys := filterEqShapes(r.structOrder, r.structShapes)
	enumKeys := filterEqShapes(r.enumOrder, r.enumShapes)
	if len(listKeys)+len(tupleKeys)+len(structKeys)+len(enumKeys) == 0 {
		return
	}
	g.b.WriteString("/* Per-shape composite == helpers (v0.4). */\n")
	// Forward decls so helpers can mutually reference (list-of-struct calls
	// struct_eq which may itself call list_eq for an inner list field).
	for _, k := range listKeys {
		fmt.Fprintf(&g.b, "static _Bool %s_eq(%s a, %s b);\n", k, k, k)
	}
	for _, k := range tupleKeys {
		fmt.Fprintf(&g.b, "static _Bool %s_eq(%s a, %s b);\n", k, k, k)
	}
	for _, k := range structKeys {
		fmt.Fprintf(&g.b, "static _Bool %s_eq(%s a, %s b);\n", k, k, k)
	}
	for _, k := range enumKeys {
		fmt.Fprintf(&g.b, "static _Bool %s_eq(%s a, %s b);\n", k, k, k)
	}
	g.b.WriteString("\n")

	for _, k := range listKeys {
		t := r.listShapes[k]
		emitListEq(&g.b, k, t)
		g.b.WriteString("\n")
	}
	for _, k := range tupleKeys {
		t := r.tupleShapes[k]
		emitTupleEq(&g.b, k, t)
		g.b.WriteString("\n")
	}
	for _, k := range structKeys {
		t := r.structShapes[k]
		emitStructEq(&g.b, k, t)
		g.b.WriteString("\n")
	}
	for _, k := range enumKeys {
		t := r.enumShapes[k]
		emitEnumEq(&g.b, k, t)
		g.b.WriteString("\n")
	}
}

// filterEqShapes returns the keys whose shape does NOT transitively contain a
// spec-typed leaf. Spec shapes can't participate in == per typeck rules.
func filterEqShapes(order []string, shapes map[string]*syntax.Type) []string {
	out := make([]string, 0, len(order))
	for _, k := range order {
		if !shapeContainsSpec(shapes[k]) {
			out = append(out, k)
		}
	}
	return out
}

// eqExpr returns a C boolean expression that compares two operands of type
// t. Routes to the per-shape _eq helper for composite types and to the
// existing primitive == / zerg_str_eq for primitives.
func eqExpr(t *syntax.Type, a, b string) string {
	if t == nil {
		return fmt.Sprintf("(%s == %s)", a, b)
	}
	if t == syntax.TStr() {
		return fmt.Sprintf("zerg_str_eq(%s, %s)", a, b)
	}
	switch t.Kind {
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
		return fmt.Sprintf("%s_eq(%s, %s)", mangleType(t), a, b)
	}
	return fmt.Sprintf("(%s == %s)", a, b)
}

func emitListEq(b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static _Bool %s_eq(%s a, %s b) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    if (a.len != b.len) return 0;\n")
	fmt.Fprintf(b, "    for (size_t i = 0; i < a.len; i++) {\n")
	fmt.Fprintf(b, "        if (!(%s)) return 0;\n", eqExpr(t.Element, "a.data[i]", "b.data[i]"))
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    return 1;\n")
	fmt.Fprintf(b, "}\n")
}

func emitTupleEq(b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static _Bool %s_eq(%s a, %s b) {\n", mname, mname, mname)
	for i, e := range t.Tuple {
		fmt.Fprintf(b, "    if (!(%s)) return 0;\n",
			eqExpr(e, fmt.Sprintf("a.e%d", i), fmt.Sprintf("b.e%d", i)))
	}
	fmt.Fprintf(b, "    return 1;\n")
	fmt.Fprintf(b, "}\n")
}

func emitStructEq(b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static _Bool %s_eq(%s a, %s b) {\n", mname, mname, mname)
	for _, f := range t.Fields {
		fname := mangleField(f.Name)
		fmt.Fprintf(b, "    if (!(%s)) return 0;\n",
			eqExpr(f.Type, "a."+fname, "b."+fname))
	}
	fmt.Fprintf(b, "    return 1;\n")
	fmt.Fprintf(b, "}\n")
}

func emitEnumEq(b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static _Bool %s_eq(%s a, %s b) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    if (a.tag != b.tag) return 0;\n")
	fmt.Fprintf(b, "    switch (a.tag) {\n")
	for i := range t.Variants {
		payload := variantPayload(t, i)
		fmt.Fprintf(b, "    case %d:\n", i)
		if len(payload) == 0 {
			fmt.Fprintf(b, "        return 1;\n")
			continue
		}
		for j, pt := range payload {
			access := fmt.Sprintf("payload.p%d.a%d", i, j)
			fmt.Fprintf(b, "        if (!(%s)) return 0;\n",
				eqExpr(pt, "a."+access, "b."+access))
		}
		fmt.Fprintf(b, "        return 1;\n")
	}
	fmt.Fprintf(b, "    default: return 0;\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "}\n")
}
