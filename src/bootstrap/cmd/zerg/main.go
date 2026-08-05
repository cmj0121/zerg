// Command zerg0 is the minimal Zerg bootstrap seed (M6): a single 'build'
// subcommand that drives the compile pipeline (lex -> parse -> sema -> emit C ->
// cc -> binary) — the one capability needed to build the self-hosting Zerg
// compiler. The fmt / lint / test subcommands were dropped once the compiler
// self-hosts; the Zerg-written toolchain reimplements those.
//
// It is named zerg0, not zerg: `zerg` is the compiler this one exists to build.
//
// Usage:
//
//	zerg0 build [flags] <file.zg>
//
// See --help for the flags (output path, --emit stage, --cc, verbosity).
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/alecthomas/kong"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/cmj0121/zerg/src/bootstrap/internal/build"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/emit"
	runtime "github.com/cmj0121/zerg/src/runtime"
)

// CLI is the zerg command line, parsed by kong.
//
// Version is a kong.VersionFlag, which prints kong's "version" variable and exits 0 before
// anything else is parsed. Handling it by hand would mean a second path through argument
// parsing that has to agree with the first about what a flag is; letting kong own it means
// `zerg0 --version` and `zerg0 build --version` answer the same way, and the flag appears in
// --help without being written there twice.
type CLI struct {
	Build   BuildCmd         `cmd:"" name:"build" help:"Compile a Zerg source file to a binary."`
	Version kong.VersionFlag `short:"V" name:"version" help:"show the version and exit"`
	Verbose int              `short:"v" type:"counter" help:"increase log verbosity (-v info, -vv debug)"`
}

// BuildCmd compiles one source file through the Phase 0 pipeline.
type BuildCmd struct {
	File   string `arg:"" name:"file" help:"the Zerg source file to compile" type:"existingfile"`
	Output string `short:"o" name:"output" default:"a.out" help:"output binary path"`
	Emit   string `name:"emit" enum:"c,bin" default:"bin" help:"stop after emitting C (c), or bin to link a binary"`
	CC     string `name:"cc" default:"cc" help:"C compiler used to link the emitted C"`
	KeepC  bool   `name:"keep-c" help:"keep the generated .c file next to the output"`
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("zerg0"),
		kong.Description("The minimal Zerg bootstrap seed: compile a .zg source with 'build'."),
		kong.UsageOnError(),
		// The banner names the SEED, so nobody reading a build log has to work out which of
		// the two compilers answered. Both carry the same number, which is the whole point of
		// stamping it in from one file; the words around it are where they differ.
		kong.Vars{"version": "zerg0 " + version + " (seed)"},
	)
	setupLog(cli.Verbose)
	switch ctx.Command() {
	case "build <file>":
		os.Exit(runBuild(&cli.Build))
	default:
		ctx.Fatalf("unknown command %q", ctx.Command())
	}
}

// runBuild executes the compile pipeline for an already-parsed command line and
// returns the process exit code.
func runBuild(cmd *BuildCmd) int {
	log.Debug().Str("file", cmd.File).Msg("compiling")
	// Compile as a whole program: `import "a/b"` roots in the entry file's
	// directory tree, falling back to the embedded stdlib. A single-file entry with
	// no import flattens to exactly itself, so its C stays byte-identical.
	code, manifest, diags := build.CompileProgram(cmd.File)
	if len(diags) > 0 {
		reportDiags(cmd.File, diags)
		return 1
	}
	log.Debug().Msg("front-end and C emission ok")

	if cmd.Emit == "c" {
		fmt.Print(code)
		return 0
	}
	return link(cmd, code, manifest)
}

// cStd is the C dialect the emitted code is compiled with: c17 by default, and
// whatever $ZERG_CSTD names when a build asks for another one. The seed reads the
// same variable as the compiler it builds, because the two are one chain — a job
// that sets it to c99 and gets a c17 stage1 out of the seed has measured half of
// what it set out to.
func cStd() string {
	if v := os.Getenv("ZERG_CSTD"); v != "" {
		return "-std=" + v
	}
	return "-std=c17"
}

// link writes the C source and compiles it into cmd.Output with cmd.CC. When the
// manifest reports no runtime is needed (every value-only program, including all
// the examples), it links exactly as Phase 0 did — a single 'cc -std=… -o out
// file.c' — so the built binary is byte-identical. When the runtime is needed it
// materializes the embedded src/runtime tree next to the C and compiles it in
// with the include path.
func link(cmd *BuildCmd, code string, manifest emit.Manifest) int {
	cpath, cleanup, err := writeCSource(cmd, code)
	if err != nil {
		log.Error().Err(err).Msg("cannot write C source")
		return 1
	}
	defer cleanup()

	args := []string{cStd(), "-o", cmd.Output, cpath}
	if manifest.NeedsRuntime {
		rtdir, err := os.MkdirTemp("", "zerg-rt-")
		if err != nil {
			log.Error().Err(err).Msg("cannot stage runtime")
			return 1
		}
		defer func() { _ = os.RemoveAll(rtdir) }()
		cfiles, err := runtime.Materialize(rtdir)
		if err != nil {
			log.Error().Err(err).Msg("cannot write runtime sources")
			return 1
		}
		// A concurrent program (spawn/channel) additionally links the scheduler and the
		// context switch selected by the host arch; a program that touches neither links
		// exactly the set it always did, so its command line is unchanged.
		if manifest.Concurrency {
			cfiles = append(cfiles, runtime.ConcurrencyCUnits(rtdir, runtime.HostArch())...)
		}
		args = append([]string{"-std=c11", "-I", rtdir, "-o", cmd.Output, cpath}, cfiles...)
	}

	log.Debug().Str("cc", cmd.CC).Str("c", cpath).Str("out", cmd.Output).Bool("runtime", manifest.NeedsRuntime).Msg("linking")
	out, err := exec.Command(cmd.CC, args...).CombinedOutput() //nolint:gosec // cc + generated files are trusted inputs
	if err != nil {
		log.Error().Err(err).Msg("cc failed")
		_, _ = os.Stderr.Write(out)
		return 1
	}
	log.Info().Str("output", cmd.Output).Msg("built")
	return 0
}

// writeCSource writes the emitted C to a sidecar file (with --keep-c) or a temp
// file, returning its path and a cleanup function.
func writeCSource(cmd *BuildCmd, code string) (string, func(), error) {
	if cmd.KeepC {
		path := cmd.Output + ".c"
		if err := os.WriteFile(path, []byte(code), 0o644); err != nil { //nolint:gosec // generated source, not a secret
			return "", func() {}, err
		}
		return path, func() {}, nil
	}
	f, err := os.CreateTemp("", "zerg-*.c")
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	_, werr := f.WriteString(code)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(path)
		return "", func() {}, fmt.Errorf("write temp C: %w", errors.Join(werr, cerr))
	}
	return path, func() { _ = os.Remove(path) }, nil
}

// reportDiags prints each diagnostic as 'file:line:col: message' to stderr.
func reportDiags(file string, diags []diag.Diagnostic) {
	for _, d := range diags {
		fmt.Fprintln(os.Stderr, d.WithFile(file))
	}
}

// setupLog configures zerolog to write human-readable logs to stderr; verbosity
// raises the level from warn (default) to info (-v) or debug (-vv).
func setupLog(verbose int) {
	level := zerolog.WarnLevel
	switch {
	case verbose >= 2:
		level = zerolog.DebugLevel
	case verbose == 1:
		level = zerolog.InfoLevel
	}
	writer := zerolog.ConsoleWriter{Out: os.Stderr, PartsExclude: []string{zerolog.TimestampFieldName}}
	log.Logger = zerolog.New(writer).Level(level)
}
