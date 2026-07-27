// Command zerg is the minimal Zerg bootstrap seed (M6): a single 'build'
// subcommand that drives the compile pipeline (lex -> parse -> sema -> emit C ->
// cc -> binary) — the one capability needed to build the self-hosting Zerg
// compiler. The fmt / lint / test subcommands were dropped once the compiler
// self-hosts; the Zerg-written toolchain reimplements those.
//
// Usage:
//
//	zerg build [flags] <file.zg>
//
// See --help for the flags (output path, --emit stage, --cc, verbosity).
package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/alecthomas/kong"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/cmj0121/zerg/src/bootstrap/internal/build"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/emit"
	"github.com/cmj0121/zerg/src/bootstrap/internal/lexer"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	runtime "github.com/cmj0121/zerg/src/runtime"
)

// CLI is the zerg command line, parsed by kong.
type CLI struct {
	Build   BuildCmd `cmd:"" name:"build" help:"Compile a Zerg source file to a binary."`
	Verbose int      `short:"v" type:"counter" help:"increase log verbosity (-v info, -vv debug)"`
}

// BuildCmd compiles one source file through the Phase 0 pipeline.
type BuildCmd struct {
	File   string `arg:"" name:"file" help:"the Zerg source file to compile" type:"existingfile"`
	Output string `short:"o" name:"output" default:"a.out" help:"output binary path"`
	Emit   string `name:"emit" enum:"tokens,ast,c,bin" default:"bin" help:"stop after a stage: tokens, ast, c; or bin to link a binary"`
	CC     string `name:"cc" default:"cc" help:"C compiler used to link the emitted C"`
	KeepC  bool   `name:"keep-c" help:"keep the generated .c file next to the output"`
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("zerg"),
		kong.Description("The minimal Zerg bootstrap seed: compile a .zg source with 'build'."),
		kong.UsageOnError(),
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
	src, err := os.ReadFile(cmd.File)
	if err != nil {
		log.Error().Err(err).Msg("cannot read source")
		return 1
	}

	if cmd.Emit == "tokens" {
		return dumpTokens(string(src))
	}
	if cmd.Emit == "ast" {
		return dumpAST(cmd.File, string(src))
	}

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

// link writes the C source and compiles it into cmd.Output with cmd.CC. When the
// manifest reports no runtime is needed (every value-only program, including all
// the examples), it links exactly as Phase 0 did — a single 'cc -std=c11 -o out
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

	args := []string{"-std=c11", "-o", cmd.Output, cpath}
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
		return "", func() {}, fmt.Errorf("write temp C: %w", cmpErr(werr, cerr))
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// dumpTokens prints the token stream for debugging the lexer.
func dumpTokens(src string) int {
	toks, diags := lexer.Tokenize(src)
	for _, t := range toks {
		if t.Kind == token.EOF {
			break
		}
		fmt.Printf("%s\t%s\n", t.Span.Start, t)
	}
	for _, d := range diags {
		fmt.Fprintln(os.Stderr, d.Error())
	}
	if len(diags) > 0 {
		return 1
	}
	return 0
}

// dumpAST parses and reports the number of top-level items.
func dumpAST(file, src string) int {
	f, diags := parser.Parse(src)
	if len(diags) > 0 {
		reportDiags(file, diags)
		return 1
	}
	fmt.Printf("%d top-level item(s) parsed\n", len(f.Items))
	return 0
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
