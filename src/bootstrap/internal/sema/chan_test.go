package sema

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
)

// TestChannelDirectionNarrowing checks the slice-C2 directional rules (GRAMMAR group
// 9): a send on a receive-only channel and a receive from a send-only channel are each
// rejected, while both operations are allowed on a bidirectional channel.
func TestChannelDirectionNarrowing(t *testing.T) {
	reject := []struct {
		name   string
		src    string
		substr string
	}{
		{
			name:   "send on a receive-only channel",
			src:    "fn f(ch: <-chan[int]) {\n  ch <- 1\n}",
			substr: "cannot send on a receive-only channel",
		},
		{
			name:   "receive from a send-only channel",
			src:    "fn f(ch: chan[int]<-) -> int {\n  x := <-ch\n  return 0\n}",
			substr: "cannot receive from a send-only channel",
		},
	}
	for _, tc := range reject {
		t.Run(tc.name, func(t *testing.T) {
			file, pdiags := parser.Parse(tc.src)
			if len(pdiags) != 0 {
				t.Fatalf("unexpected parse errors: %v", pdiags)
			}
			_, diags := Check(file)
			found := false
			for _, d := range diags {
				if strings.Contains(d.Msg, tc.substr) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want a diagnostic containing %q, got %v", tc.substr, diags)
			}
		})
	}

	// a bidirectional channel accepts both a send and a receive with no diagnostic.
	t.Run("bidirectional allows send and receive", func(t *testing.T) {
		const src = "fn f(ch: chan[int]) -> int {\n  ch <- 1\n  x := <-ch\n  return 0\n}"
		file, pdiags := parser.Parse(src)
		if len(pdiags) != 0 {
			t.Fatalf("unexpected parse errors: %v", pdiags)
		}
		if _, diags := Check(file); len(diags) != 0 {
			t.Fatalf("bidirectional channel send/recv must be clean, got %v", diags)
		}
	})
}
