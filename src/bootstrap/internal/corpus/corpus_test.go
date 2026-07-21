package corpus

import (
	"path/filepath"
	"testing"
)

func TestPathNotFound(t *testing.T) {
	if dir, ok := Path("no-such-corpus-subdir-xyz"); ok {
		t.Fatalf("Path(nonexistent) = (%q, true), want not found", dir)
	}
}

func TestPathFound(t *testing.T) {
	// The test-data submodule ships a lexer/ corpus; when it is checked out,
	// Path locates it by walking up from the package directory.
	dir, ok := Path("lexer")
	if !ok {
		t.Skip("test-data submodule not initialized")
	}
	if filepath.Base(dir) != "lexer" || filepath.Base(filepath.Dir(dir)) != "test-data" {
		t.Fatalf("Path(\"lexer\") = %q, want .../test-data/lexer", dir)
	}
}
