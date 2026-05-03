package build

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// DefaultCC returns the C compiler the builder will invoke: $CC if set,
// otherwise "cc". Exposed so tests and tooling can resolve the same name
// the build path uses.
func DefaultCC() string {
	if v := os.Getenv("CC"); v != "" {
		return v
	}
	return "cc"
}

// EmitSource lexes and parses the Zerg source at srcPath and writes the
// generated C to w. It does not invoke the C compiler — use Build for the
// full compile-to-binary pipeline.
func EmitSource(srcPath string, w io.Writer) error {
	prog, err := parseSource(srcPath)
	if err != nil {
		return err
	}
	return Emit(prog, w)
}

// parseSource reads, lexes, and parses srcPath, wrapping each failure with
// a `zerg build:` prefix so callers can return errors verbatim.
func parseSource(srcPath string) (*syntax.Program, error) {
	src, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf("zerg build: %w", err)
	}
	tokens, err := syntax.Lex(src)
	if err != nil {
		return nil, fmt.Errorf("zerg build: %s: %w", srcPath, err)
	}
	prog, err := syntax.Parse(tokens)
	if err != nil {
		return nil, fmt.Errorf("zerg build: %s: %w", srcPath, err)
	}
	return prog, nil
}

// Build compiles the Zerg source at srcPath into a native binary placed in
// the current working directory. The output basename is the source basename
// minus the `.zg` suffix.
//
// We resolve the C compiler before lexing so users get a fast, clear failure
// when the toolchain is missing. On compiler failure we leave the generated
// .c file in place so the user can inspect what we fed to cc.
func Build(srcPath string) error {
	cc := DefaultCC()
	ccPath, err := exec.LookPath(cc)
	if err != nil {
		return fmt.Errorf("zerg build: %q not found in PATH; set $CC or install gcc/clang", cc)
	}

	prog, err := parseSource(srcPath)
	if err != nil {
		return err
	}

	base := strings.TrimSuffix(filepath.Base(srcPath), ".zg")
	if base == "" || base == filepath.Base(srcPath) {
		// Either the file had no `.zg` suffix at all, or trimming left an
		// empty name. Either way we don't have a sensible output name.
		return fmt.Errorf("zerg build: source path %q must end in .zg", srcPath)
	}

	tmpDir, err := os.MkdirTemp("", "zerg-build-")
	if err != nil {
		return fmt.Errorf("zerg build: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(tmpDir)
		}
	}()

	cPath := filepath.Join(tmpDir, base+".c")
	cFile, err := os.Create(cPath)
	if err != nil {
		return fmt.Errorf("zerg build: %w", err)
	}
	if err := Emit(prog, cFile); err != nil {
		cFile.Close()
		return fmt.Errorf("zerg build: %w", err)
	}
	if err := cFile.Close(); err != nil {
		return fmt.Errorf("zerg build: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("zerg build: %w", err)
	}
	outPath := filepath.Join(cwd, base)

	cmd := exec.Command(ccPath, "-O2", "-o", outPath, cPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Keep the .c file so the user can inspect what we generated.
		cleanup = false
		fmt.Fprintf(os.Stderr, "zerg build: C compiler failed; generated source kept at %s\n", cPath)
		return fmt.Errorf("zerg build: %s: %w", cc, err)
	}

	return nil
}
