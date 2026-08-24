package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// ScopeKind tags what a scope belongs to. It shapes lookups (a module surface is
// forward-visible; a function or block scope is not) and lets diagnostics name
// the enclosing construct.
type ScopeKind int

const (
	// ScopeModule is a file's top level (the module surface).
	ScopeModule ScopeKind = iota
	// ScopeFunc is a function's parameter scope.
	ScopeFunc
	// ScopeBlock is a '{ }' block, a match arm, or a for/if head binding scope.
	ScopeBlock
	// ScopeImpl is an impl/spec body ('This' and the associated types are visible).
	ScopeImpl
)

// SymKind tags what a symbol denotes, which decides the value-vs-type namespace
// and how a name in an ambiguous position (a bracket base, a bare pattern name)
// resolves.
type SymKind int

const (
	// SymVar is a value binding: ':=', 'mut :=', 'x: T = e', a parameter, or a
	// loop/pattern binding.
	SymVar SymKind = iota
	// SymConst is a 'const' binding: immutable and shadow-proof in both directions.
	SymConst
	// SymFunc is a function declaration (possibly generic); its type is a Fn.
	SymFunc
	// SymType is a struct/enum/type-alias/spec or a built-in type name (the type
	// namespace).
	SymType
	// SymVariant is an enum variant: a callable in the value namespace and a name
	// usable in pattern position.
	SymVariant
	// SymTypeParam is a generic type parameter (an abstract, bound-constrained type).
	SymTypeParam
	// SymValueParam is a value-generic parameter '[N: int]' (a compile-time value).
	SymValueParam
	// SymNamespace is a namespace bound by an import.
	SymNamespace
	// SymBuiltin is a name that is always in scope (e.g. 'print').
	SymBuiltin
)

// Symbol is one declared name. Value symbols carry a value type in Type; a type
// symbol carries its declaration structure in TypeDef and a variant in Variant.
type Symbol struct {
	Name    string
	Kind    SymKind
	Type    types.Type // the value type for a value symbol; nil for a pure type symbol
	Mutable bool       // a 'mut' binding
	ByRef   bool       // a 'mut &x' parameter
	Const   bool       // a shadow-proof 'const' binding
	Pub     bool       // 'pub' visibility
	Span    token.Span // the declaration point (diagnostics & redeclaration)
	Decl    ast.Node   // back-reference to the declaring node

	// Module is the canonical C-mangle tag of the module a SymNamespace refers to
	// (the module loader's tag, e.g. "util_text"). It keys cross-module member
	// mangling; empty for every non-namespace symbol and for a namespace bound
	// without a loader pass (member resolution then falls back to the local name).
	Module string

	// Reexports holds, for a SymNamespace, the canonical tags of the modules the
	// namespace's module re-exports through its own `import pub "…"` (one level, S2).
	// A member the module does not expose directly resolves onto a re-exported
	// module's public surface. Empty for a plain import.
	Reexports []string

	// SiblingTags are the tags of every OTHER module that bound a sibling namespace of this
	// same local name. This seed keeps one namespace scope for the whole program while #57
	// makes the file the unit, so two modules' `import "./fmt"` are two bindings that land on
	// one symbol; resolution tries each tag rather than refusing the second declaration.
	//
	// It is a SEED GAP and named as one: the shipping compiler binds per file and needs no
	// such list. What this buys is that the seed can still READ a tree written to #57.
	SiblingTags []string

	TypeDef *types.TypeDef    // set when Kind == SymType
	Variant *types.VariantDef // set when Kind == SymVariant
}

// Scope is one lexical scope in a retained scope tree. The tree is kept (not
// popped) after the resolve pass so a later pass can revisit a scope, and so the
// 'const' shadow rule can see every visible outer binding.
type Scope struct {
	Kind    ScopeKind
	Parent  *Scope
	symbols map[string]*Symbol
	order   []*Symbol // declaration order, for stable diagnostics
}

// newScope opens a child scope of the given kind under parent (nil for the root
// module scope).
func newScope(kind ScopeKind, parent *Scope) *Scope {
	return &Scope{Kind: kind, Parent: parent, symbols: map[string]*Symbol{}}
}

// declareLocal inserts sym into this scope. It returns the existing symbol when
// the name is already declared in this same scope (a redeclaration), leaving the
// scope unchanged; it returns nil on success.
func (s *Scope) declareLocal(sym *Symbol) *Symbol {
	if prev, dup := s.symbols[sym.Name]; dup {
		return prev
	}
	s.symbols[sym.Name] = sym
	s.order = append(s.order, sym)
	return nil
}

// local returns the symbol declared directly in this scope, or nil.
func (s *Scope) local(name string) *Symbol { return s.symbols[name] }

// lookup finds a name from this scope outward through the parent chain, returning
// the first (innermost) match, or nil.
func (s *Scope) lookup(name string) *Symbol {
	for sc := s; sc != nil; sc = sc.Parent {
		if sym, ok := sc.symbols[name]; ok {
			return sym
		}
	}
	return nil
}

// lookupVisible finds a name strictly outside this scope (starting at the
// parent), used by the 'const' shadow rule to spot a shadowed or shadowing outer
// binding.
func lookupVisible(parent *Scope, name string) *Symbol {
	if parent == nil {
		return nil
	}
	return parent.lookup(name)
}
