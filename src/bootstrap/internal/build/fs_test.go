package build

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// RUN-based tests for the stdlib `fs` module (src/stdlib/fs.zg) and io.write_file — the
// filesystem-write surface, pure Zerg over thin runtime leaves.

// TestFSRuns covers exists/remove: a file is present after io.write_file, absent after
// fs.remove, and a bogus path is absent. Output stays on the buffered `print` path so it
// does not interleave with unbuffered io writes.
func TestFSRuns(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	src := fmt.Sprintf("import \"io\"\nimport \"fs\"\n"+
		"fn main() -> Result[nil] {\n"+
		"\tio.write_file(%q, list[byte](\"x\"))\n"+
		"\tprint fs.exists(%q)\n"+ // true
		"\tfs.remove(%q)\n"+
		"\tprint fs.exists(%q)\n"+ // false
		"\tprint fs.exists(\"/no/such/zerg/path\")\n"+ // false
		"\treturn nil\n}\n", p, p, p, p)
	got := runProgramRT(t, src)
	if want := "true\nfalse\nfalse\n"; got != want {
		t.Fatalf("fs: got %q, want %q", got, want)
	}
}

// TestWriteFileRoundTrip writes bytes with io.write_file and reads them back, asserting
// both the in-program round-trip and the actual bytes on disk.
func TestWriteFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.txt")
	src := fmt.Sprintf("import \"io\"\n"+
		"fn main() -> Result[nil] {\n"+
		"\tio.write_file(%q, list[byte](\"hello zerg\"))\n"+ // no trailing newline
		"\tprint str(io.read_file(%q))\n"+ // print adds the newline
		"\treturn nil\n}\n", p, p)
	if got := runProgramRT(t, src); got != "hello zerg\n" {
		t.Fatalf("write_file round-trip: got %q, want %q", got, "hello zerg\n")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back the written file: %v", err)
	}
	if string(data) != "hello zerg" {
		t.Fatalf("on-disk content: got %q, want %q", data, "hello zerg")
	}
}
