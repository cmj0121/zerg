// Package diag holds the diagnostic type shared by every compiler stage. A
// diagnostic anchors a message to a source span so the driver can render a
// `file:line:col: message` line the way GRAMMAR's error-UX workstream asks for.
package diag

import (
	"fmt"
	"sort"

	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// Diagnostic is a single error anchored at a source span.
type Diagnostic struct {
	Span token.Span
	Msg  string
}

// Error renders the diagnostic without a file name (line:col: message).
func (d Diagnostic) Error() string {
	return fmt.Sprintf("%s: %s", d.Span.Start, d.Msg)
}

// WithFile renders the diagnostic prefixed with the source file name.
func (d Diagnostic) WithFile(file string) string {
	return fmt.Sprintf("%s:%s: %s", file, d.Span.Start, d.Msg)
}

// List is an ordered collection of diagnostics gathered by a stage.
type List struct {
	items []Diagnostic
}

// Add appends a diagnostic spanning the given range.
func (l *List) Add(span token.Span, format string, args ...any) {
	l.items = append(l.items, Diagnostic{Span: span, Msg: fmt.Sprintf(format, args...)})
}

// Append adds an already-built diagnostic (e.g. one carried over from an earlier
// stage).
func (l *List) Append(d Diagnostic) { l.items = append(l.items, d) }

// Len reports how many diagnostics were gathered.
func (l *List) Len() int { return len(l.items) }

// Empty reports whether no diagnostics were gathered.
func (l *List) Empty() bool { return len(l.items) == 0 }

// Items returns the diagnostics sorted by source position.
func (l *List) Items() []Diagnostic {
	out := make([]Diagnostic, len(l.items))
	copy(out, l.items)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Span.Start.Offset < out[j].Span.Start.Offset
	})
	return out
}
