package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/loader"
)

// printStdlib is the formatter; exercise it directly to lock the
// output shape without paying for the binary-build overhead of an
// end-to-end run.
func TestPrintStdlibFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := printStdlib(&buf); err != nil {
		t.Fatalf("printStdlib: %v", err)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("output is empty")
	}
	// Every catalog entry must appear, with its description on the same
	// line. The tabwriter pads but does not reorder.
	for _, e := range loader.Catalog() {
		var found bool
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, e.Path) && strings.Contains(line, e.Description) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("catalog entry %s missing or mis-rendered in output:\n%s", e.Path, out)
		}
	}
	// One line per entry, no trailing blank, no extra preamble.
	gotLines := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1
	if gotLines != len(loader.Catalog()) {
		t.Errorf("got %d output lines, want %d (one per entry)", gotLines, len(loader.Catalog()))
	}
}

// End-to-end check via the built CLI binary — pins the kong wiring
// (the subcommand is registered, the dispatch returns exit 0, stdout
// carries the catalog).
func TestStdlibSubcommand(t *testing.T) {
	bin := buildBin(t)
	out, code := runBin(t, bin, "stdlib")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, out.stderr)
	}
	// Bare-name std/* entry (no prefix) AND explicit sys/* entry both
	// appear — pins the dual display convention end-to-end.
	if !strings.Contains(out.stdout, "io ") {
		t.Errorf("stdout missing bare 'io' entry; got:\n%s", out.stdout)
	}
	if strings.Contains(out.stdout, "std/io") {
		t.Errorf("stdout leaked the std/ prefix on a std/* entry; got:\n%s", out.stdout)
	}
	if !strings.Contains(out.stdout, "sys/path") {
		t.Errorf("stdout missing sys/path entry; got:\n%s", out.stdout)
	}
	if out.stderr != "" {
		t.Errorf("stderr should be empty for successful stdlib call; got:\n%s", out.stderr)
	}
}
