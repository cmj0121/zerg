package build

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/loader"
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

// EmitSource lexes, parses, and type-checks the Zerg source at srcPath and
// writes the generated C to w. It does not invoke the C compiler — use Build
// for the full compile-to-binary pipeline.
func EmitSource(srcPath string, w io.Writer) error {
	prog, err := parseSource(srcPath)
	if err != nil {
		return err
	}
	return Emit(prog, w)
}

// parseSource reads, lexes, parses (via the v0.5 module loader), and
// type-checks srcPath, wrapping each failure with a `zerg build:` prefix
// so callers can return errors verbatim. Type-checking happens here (not
// just inside Build) so EmitSource — used by `--emit-c` — also rejects
// ill-typed programs before generating C that the codegen would otherwise
// accept and produce nonsense for.
//
// v0.5 Unit 3: typeck runs across the whole Bundle so cross-module name
// resolution, pub gating, and the orphan rule fire. Codegen still
// consumes only the entry module's typed AST until Unit 6 wires per-
// module mangling.
func parseSource(srcPath string) (*syntax.Program, error) {
	bundle, err := loader.Load(srcPath)
	if err != nil {
		return nil, fmt.Errorf("zerg build: %w", err)
	}
	if err := syntax.CheckBundle(bundle); err != nil {
		return nil, fmt.Errorf("zerg build: %s: %w", srcPath, err)
	}
	return bundle.Entry.Program, nil
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

	// -fwrapv: pin signed integer overflow to two's-complement wrap so build
	// matches Go's natural int64 wrap (PLAN.md "Numeric semantics (pinned)").
	// -lm at the end: libm is the home of floor / fmod called from generated
	// expressions; gcc and clang both accept the link flag last.
	cmd := exec.Command(ccPath, "-fwrapv", "-O2", "-o", outPath, cPath, "-lm")
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
