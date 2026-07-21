package diag

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

func at(offset int) token.Span {
	return token.Span{Start: token.Pos{Offset: offset, Line: offset, Col: 1}}
}

func TestListLifecycle(t *testing.T) {
	var l List
	if !l.Empty() || l.Len() != 0 {
		t.Fatalf("new list should be empty")
	}
	l.Add(at(5), "second at %d", 5)
	l.Add(at(1), "first")
	l.Append(Diagnostic{Span: at(3), Msg: "middle"})

	if l.Empty() || l.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", l.Len())
	}
	// Items are sorted by source offset regardless of insertion order.
	items := l.Items()
	want := []string{"first", "middle", "second at 5"}
	for i, w := range want {
		if items[i].Msg != w {
			t.Fatalf("items[%d].Msg = %q, want %q", i, items[i].Msg, w)
		}
	}
}

func TestDiagnosticRender(t *testing.T) {
	d := Diagnostic{Span: token.Span{Start: token.Pos{Line: 2, Col: 4}}, Msg: "boom"}
	if got := d.Error(); got != "2:4: boom" {
		t.Errorf("Error() = %q, want 2:4: boom", got)
	}
	if got := d.WithFile("a.zg"); got != "a.zg:2:4: boom" {
		t.Errorf("WithFile() = %q, want a.zg:2:4: boom", got)
	}
}
