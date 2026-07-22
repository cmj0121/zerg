// The embedded base gives every AST node a uniform home for its source span and
// its node-attached trivia (DESIGN-1a §1–2): the span anchors diagnostics, the
// lead/trail trivia carry the comments and blank lines 'zerg fmt' reprints
// around the node. The fields are unexported so a node's span and trivia can
// only move through the setters the parser calls.
package ast

import "github.com/cmj0121/zerg/src/bootstrap/internal/token"

// base is embedded by every node: the node's source span plus the leading and
// trailing trivia attached to it.
type base struct {
	span  token.Span
	lead  []token.Trivia // comments/doc/blank lines before the node
	trail []token.Trivia // same-line comment(s) after the node
}

// Span returns the node's source span.
func (b *base) Span() token.Span { return b.span }

// Lead returns the trivia printed on the lines before the node.
func (b *base) Lead() []token.Trivia { return b.lead }

// Trail returns the trivia printed on the node's own line, after it.
func (b *base) Trail() []token.Trivia { return b.trail }

// SetSpan records the node's source span; the parser stamps every node.
func (b *base) SetSpan(s token.Span) { b.span = s }

// SetLead attaches the node's leading trivia.
func (b *base) SetLead(tr []token.Trivia) { b.lead = tr }

// SetTrail attaches the node's trailing trivia.
func (b *base) SetTrail(tr []token.Trivia) { b.trail = tr }
