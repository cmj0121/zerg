// Package corpus locates the shared test-data submodule so the golden tests can
// read their fixtures. The submodule lives at the repo-root 'test-data/'
// directory (a git submodule of the zerg-testdata repository, organized as
// lexer/, parser/, codegen/). Tests skip gracefully when it is not initialized.
package corpus

import (
	"os"
	"path/filepath"
)

// Path returns the absolute path of a subdirectory of the test-data submodule,
// found by walking up from the current working directory. found is false when the
// submodule is absent (not checked out), which callers treat as a skip.
func Path(sub string) (dir string, found bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(cwd, "test-data", sub)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			return "", false
		}
		cwd = parent
	}
}
