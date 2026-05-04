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
	g := &cgen{indent: 1}
	g.shapes = newShapeRegistry()

	// Walk the program (typed AST) to collect every concrete composite shape
	// that needs a per-shape typedef and helpers (list, tuple, struct, enum).
	if err := g.collectShapes(prog); err != nil {
		return err
	}

	// 1. Runtime header.
	g.b.WriteString(runtimeC)
	g.b.WriteString("\n")

	// 2. Composite-shape typedefs and helpers (forward decls then bodies).
	g.shapes.emitForwardDecls(&g.b)
	g.shapes.emitTypedefs(&g.b)
	g.shapes.emitHelpers(&g.b)

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
		}
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
		fmt.Fprintf(b, "typedef int32_t %s;\n", k)
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
// each field, not just a forward declaration. Order from no-deps to
// most-dependent: enum macros (depend on nothing), then struct definitions
// in reverse-topological order over struct→struct edges (typeck has rejected
// struct cycles so a topo sort exists), then tuples (may contain structs),
// then lists (may contain structs and tuples). Enums are emitted as
// `int32_t` typedefs in emitForwardDecls so they're already complete.
func (r *shapeRegistry) emitTypedefs(b *strings.Builder) {
	if len(r.enumOrder) > 0 {
		b.WriteString("/* Enum variant indices. */\n")
	}
	for _, k := range r.enumOrder {
		t := r.enumShapes[k]
		for i, v := range t.Variants {
			fmt.Fprintf(b, "#define %s_%s ((%s)%d)\n", k, v, k, i)
		}
	}

	if len(r.structOrder) > 0 {
		b.WriteString("\n/* Struct shape definitions. */\n")
	}
	// Topo-sort: emit a struct only after every struct it transitively
	// references via a field has been emitted. typeck has rejected cycles so
	// a fixed-point loop terminates in O(N) passes.
	emittedStruct := map[string]bool{}
	for len(emittedStruct) < len(r.structOrder) {
		progress := false
		for _, k := range r.structOrder {
			if emittedStruct[k] {
				continue
			}
			t := r.structShapes[k]
			ready := true
			for _, f := range t.Fields {
				if f.Type != nil && f.Type.Kind == syntax.TypeStruct {
					depKey := mangleType(f.Type)
					if _, hasDep := r.structShapes[depKey]; hasDep && !emittedStruct[depKey] {
						ready = false
						break
					}
				}
			}
			if !ready {
				continue
			}
			fmt.Fprintf(b, "struct %s {", k)
			for i, f := range t.Fields {
				if i > 0 {
					b.WriteString(";")
				}
				fmt.Fprintf(b, " %s %s", cTypeName(f.Type), mangleField(f.Name))
			}
			b.WriteString("; };\n")
			emittedStruct[k] = true
			progress = true
		}
		if !progress {
			// Should not happen post-typeck cycle check; emit remaining
			// regardless rather than spin forever.
			for _, k := range r.structOrder {
				if !emittedStruct[k] {
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
			}
			break
		}
	}

	if len(r.tupleOrder) > 0 {
		b.WriteString("\n/* Tuple shape definitions. */\n")
	}
	for _, k := range r.tupleOrder {
		t := r.tupleShapes[k]
		fmt.Fprintf(b, "struct %s {", k)
		for i, e := range t.Tuple {
			if i > 0 {
				b.WriteString(";")
			}
			fmt.Fprintf(b, " %s e%d", cTypeName(e), i)
		}
		b.WriteString("; };\n")
	}

	if len(r.listOrder) > 0 {
		b.WriteString("\n/* List shape definitions. */\n")
	}
	for _, k := range r.listOrder {
		t := r.listShapes[k]
		elem := cTypeName(t.Element)
		fmt.Fprintf(b, "struct %s { %s* data; size_t len; };\n", k, elem)
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
		fmt.Fprintf(b, "static %s %s_copy(%s xs);\n", k, k, k)
		fmt.Fprintf(b, "static %s %s_slice(%s xs, int64_t lo, int64_t hi, const char* pos);\n", k, k, k)
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
	// enum helpers (print only)
	for _, k := range r.enumOrder {
		t := r.enumShapes[k]
		emitEnumPrint(b, k, t)
		b.WriteString("\n")
	}
}

// emitListHelpers writes copy, slice, and print for a list[T] shape.
func emitListHelpers(b *strings.Builder, mname string, t *syntax.Type) {
	elem := cTypeName(t.Element)
	// copy: malloc a fresh buffer, deep-copy each element via copyExpr.
	fmt.Fprintf(b, "static %s %s_copy(%s xs) {\n", mname, mname, mname)
	fmt.Fprintf(b, "    %s out;\n", mname)
	fmt.Fprintf(b, "    out.len = xs.len;\n")
	fmt.Fprintf(b, "    out.data = (%s*)malloc(out.len ? out.len * sizeof(%s) : 1);\n", elem, elem)
	fmt.Fprintf(b, "    for (size_t i = 0; i < out.len; i++) { out.data[i] = %s; }\n",
		copyExpr(t.Element, "xs.data[i]"))
	fmt.Fprintf(b, "    return out;\n")
	fmt.Fprintf(b, "}\n")

	// slice: bounds-check, malloc fresh buffer, deep-copy elements.
	fmt.Fprintf(b, "static %s %s_slice(%s xs, int64_t lo, int64_t hi, const char* pos) {\n",
		mname, mname, mname)
	fmt.Fprintf(b, "    if (lo < 0 || hi < lo || (size_t)hi > xs.len) {\n")
	fmt.Fprintf(b, "        fprintf(stderr, \"zerg: %%s: slice [%%lld..%%lld] out of range [0..%%zu]\\n\", pos, (long long)lo, (long long)hi, xs.len);\n")
	fmt.Fprintf(b, "        exit(1);\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    %s out;\n", mname)
	fmt.Fprintf(b, "    out.len = (size_t)(hi - lo);\n")
	fmt.Fprintf(b, "    out.data = (%s*)malloc(out.len ? out.len * sizeof(%s) : 1);\n", elem, elem)
	fmt.Fprintf(b, "    for (size_t i = 0; i < out.len; i++) { out.data[i] = %s; }\n",
		copyExpr(t.Element, "xs.data[lo + i]"))
	fmt.Fprintf(b, "    return out;\n")
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

// emitEnumPrint writes print-helper for an enum: switch on the int and emit
// "Name.VariantName".
func emitEnumPrint(b *strings.Builder, mname string, t *syntax.Type) {
	fmt.Fprintf(b, "static void zerg_print_%s(%s e) {\n", mname, mname)
	fmt.Fprintf(b, "    switch ((int)e) {\n")
	for i, v := range t.Variants {
		fmt.Fprintf(b, "    case %d: fputs(%q, stdout); break;\n", i, t.Name+"."+v)
	}
	fmt.Fprintf(b, "    default: fputs(\"<bad enum>\", stdout); break;\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "}\n")
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
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct:
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
			for i, v := range s.Variants {
				variants[i] = v.Name
			}
			en := syntax.NewEnumType(s.Name, variants)
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
		}
		g.collectExpr(s.Value)
	case *syntax.MutStmt:
		if s.Type != nil && s.Type.Resolved != nil {
			g.shapes.addType(s.Type.Resolved)
		}
		g.collectExpr(s.Value)
	case *syntax.ConstStmt:
		if s.Type != nil && s.Type.Resolved != nil {
			g.shapes.addType(s.Type.Resolved)
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
			}
		}
		if s.Return != nil && s.Return.Resolved != nil {
			g.shapes.addType(s.Return.Resolved)
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

// emitDecl lowers let/mut/const into a C local declaration. The annotated
// type ref (when present) and the inferred type from the rhs are equal by
// typeck — but we prefer the rhs's type because LetStmt/MutStmt may have a
// nil Type ref (the := walrus form). The rhs is wrapped in copyExpr for
// composites — that's the v0.2 value-semantics rule.
func (g *cgen) emitDecl(name string, ref *syntax.TypeRef, value syntax.Expr, isConst bool) error {
	t := value.Type()
	if t == nil && ref != nil {
		t = ref.Resolved
	}
	if t == nil {
		return fmt.Errorf("codegen: missing type for %q", name)
	}
	exprS, err := g.exprStr(value)
	if err != nil {
		return err
	}
	exprS = copyExpr(t, exprS)
	g.writeIndent()
	if isConst {
		g.b.WriteString("const ")
	}
	fmt.Fprintf(&g.b, "%s %s = %s;\n", cTypeName(t), mangle(name), exprS)
	return nil
}

// emitTupleDestructure lowers `let (a, b) := expr` into N variable decls
// reading from a fresh temp tuple. Each name gets a deep-copy of the matching
// element.
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
		fmt.Fprintf(&g.b, "%s %s = %s;\n",
			cTypeName(elemT), mangle(name),
			copyExpr(elemT, fmt.Sprintf("%s.e%d", tmp, i)))
	}
	return nil
}

// emitAssign lowers any assign-op to the C equivalent.
func (g *cgen) emitAssign(s *syntax.AssignStmt) error {
	// v0.3 Unit 1 admits IndexExpr LHS at parse time, but typeck rejects it
	// until Unit 3 lands. Codegen therefore only sees IdentExpr targets in
	// well-formed programs; the IndexExpr arm here is a defensive stub so a
	// stray AST doesn't surface as a nil deref.
	target, ok := s.Target.(*syntax.IdentExpr)
	if !ok {
		return fmt.Errorf("v0.3 work in progress: list-element assignment is not yet emitted by the C backend (at %s)", s.Pos)
	}
	rhs, err := g.exprStr(s.Value)
	if err != nil {
		return err
	}
	targetName := mangle(target.Name)
	g.writeIndent()
	switch s.Op {
	case syntax.AssignSet:
		// For composite targets we deep-copy the rhs so the assignment is a
		// fresh value (matches the interpreter's copyValue on assign).
		t := target.Type()
		fmt.Fprintf(&g.b, "%s = %s;\n", targetName, copyExpr(t, rhs))
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
		// evaluated once. The snapshot is itself a deep copy so a body
		// holding a reference into the original is fine.
		listMangle := mangleType(iterT)
		tmp := g.freshTmp("iter")
		idx := g.freshTmp("i")
		v := mangle(s.Var)
		elemT := iterT.Element

		g.writeIndent()
		g.b.WriteString("{\n")
		g.indent++
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s %s = %s_copy(%s);\n", listMangle, tmp, listMangle, iterS)
		g.writeIndent()
		fmt.Fprintf(&g.b, "for (size_t %s = 0; %s < %s.len; %s++) {\n", idx, idx, tmp, idx)
		g.indent++
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s %s = %s;\n", cTypeName(elemT), v,
			copyExpr(elemT, fmt.Sprintf("%s.data[%s]", tmp, idx)))
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
func (g *cgen) emitReturn(s *syntax.ReturnStmt) error {
	body := "return;"
	if s.Value != nil {
		v, err := g.exprStr(s.Value)
		if err != nil {
			return err
		}
		// Deep-copy on return for composites so the caller receives an
		// independent value (consistent with let/mut/arg-pass).
		v = copyExpr(s.Value.Type(), v)
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
	// Take a deep-copy snapshot so binding patterns can re-copy from a stable
	// value without re-evaluating the subject expression (which may have
	// side effects). The copy is a no-op for primitives.
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s %s = %s;\n",
		cTypeName(subjT), subjVar, copyExpr(subjT, subjStr))

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
		// Variant access: the int value matches the variant's macro.
		return fmt.Sprintf("(%s == %s_%s)", scrut, mangleType(scrutT), p.VariantName)
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
	case *syntax.WildcardPat, *syntax.LitPat, *syntax.EnumPat:
		_ = p
		return nil
	case *syntax.BindPat:
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s %s = %s;\n",
			cTypeName(scrutT), mangle(p.Name),
			copyExpr(scrutT, scrut))
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
	fmt.Fprintf(&b, "({ %s __l; __l.len = %d; ", mname, len(e.Elements))
	if len(e.Elements) == 0 {
		fmt.Fprintf(&b, "__l.data = (%s*)malloc(1); ", elem)
	} else {
		fmt.Fprintf(&b, "__l.data = (%s*)malloc(%d * sizeof(%s)); ", elem, len(e.Elements), elem)
		for i, sub := range e.Elements {
			s, err := g.exprStr(sub)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "__l.data[%d] = %s; ", i, copyExpr(t.Element, s))
		}
	}
	fmt.Fprintf(&b, "__l; })")
	return b.String(), nil
}

// tupleLitStr emits a tuple literal as a `(zerg_tuple_<...>){.e0 = ..., .e1 =
// ...}` compound literal. C99 designated initialisers handle the rest.
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
		fmt.Fprintf(&b, ".e%d = %s", i, copyExpr(t.Tuple[i], s))
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
		fmt.Fprintf(&b, ".%s = %s", mangleField(f.Name), copyExpr(f.Type, s))
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
		elemT := rt.Element
		// Use a statement-expression to bounds-check, then return a deep copy.
		mname := mangleType(rt)
		copyForm := copyExpr(elemT,
			fmt.Sprintf("__r.data[__i]"))
		return fmt.Sprintf(
			"({ %s __r = %s; int64_t __i = %s; zerg_index_check(__i, __r.len, %q); %s; })",
			mname, rs, is, posStr, copyForm), nil
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
	if id, ok := e.Receiver.(*syntax.IdentExpr); ok {
		if rt := id.Type(); rt != nil && rt.Kind == syntax.TypeEnum {
			return fmt.Sprintf("%s_%s", mangleType(rt), e.FieldName), nil
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
	// Find the field type for the deep-copy decision.
	var fieldT *syntax.Type
	for _, f := range rt.Fields {
		if f.Name == e.FieldName {
			fieldT = f.Type
			break
		}
	}
	if fieldT == nil {
		return "", fmt.Errorf("codegen: struct %s has no field %q at %s", rt.Name, e.FieldName, e.Pos)
	}
	access := fmt.Sprintf("(%s).%s", rs, mangleField(e.FieldName))
	return copyExpr(fieldT, access), nil
}

// callStr handles user-fn calls and the `len` built-in. Each composite
// argument is deep-copied at the call site (matches the interpreter rule).
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
	var sb strings.Builder
	sb.WriteString(mangle(ident.Name))
	sb.WriteByte('(')
	for i, a := range e.Args {
		if i > 0 {
			sb.WriteString(", ")
		}
		s, err := g.exprStr(a)
		if err != nil {
			return "", err
		}
		// Deep-copy composite args at the call boundary.
		s = copyExpr(a.Type(), s)
		sb.WriteString(s)
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
		return infix(left, "==", right), nil
	case syntax.BinNE:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(!zerg_str_eq(%s, %s))", left, right), nil
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
	case syntax.TypeList, syntax.TypeTuple, syntax.TypeStruct, syntax.TypeEnum:
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
