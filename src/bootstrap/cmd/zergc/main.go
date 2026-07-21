// Command zergc is the bootstrap Zerg compiler driver. For Phase 0 it proves the
// pipeline lex -> parse -> sema -> emit C -> cc: it reads a .zg file, reports any
// diagnostics against the source, emits C, and links it with a C compiler.
//
// Usage:
//
//	zergc [flags] <file.zg>
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
	"github.com/cmj0121/zerg/src/bootstrap/internal/lexer"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// CLI is the zergc command line, parsed by kong.
type CLI struct {
	File    string `arg:"" name:"file" help:"the Zerg source file to compile" type:"existingfile"`
	Output  string `short:"o" name:"output" default:"a.out" help:"output binary path"`
	Emit    string `name:"emit" enum:"tokens,ast,c,bin" default:"bin" help:"stop after a stage: tokens, ast, c; or bin to link a binary"`
	CC      string `name:"cc" default:"cc" help:"C compiler used to link the emitted C"`
	KeepC   bool   `name:"keep-c" help:"keep the generated .c file next to the output"`
	Verbose int    `short:"v" type:"counter" help:"increase log verbosity (-v info, -vv debug)"`
}

func main() {
	var cli CLI
	kong.Parse(&cli,
		kong.Name("zergc"),
		kong.Description("The bootstrap Zerg compiler (Phase 0): lex, parse, check, emit C, and link."),
		kong.UsageOnError(),
	)
	os.Exit(run(&cli))
}

// run executes the driver for an already-parsed command line and returns the
// process exit code.
func run(cli *CLI) int {
	setupLog(cli.Verbose)

	src, err := os.ReadFile(cli.File)
	if err != nil {
		log.Error().Err(err).Msg("cannot read source")
		return 1
	}

	if cli.Emit == "tokens" {
		return dumpTokens(string(src))
	}
	if cli.Emit == "ast" {
		return dumpAST(cli.File, string(src))
	}

	log.Debug().Str("file", cli.File).Msg("compiling")
	code, diags := build.Compile(string(src))
	if len(diags) > 0 {
		reportDiags(cli.File, diags)
		return 1
	}
	log.Debug().Msg("front-end and C emission ok")

	if cli.Emit == "c" {
		fmt.Print(code)
		return 0
	}
	return link(cli, code)
}

// link writes the C source and compiles it into cli.Output with cli.CC.
func link(cli *CLI, code string) int {
	cpath, cleanup, err := writeCSource(cli, code)
	if err != nil {
		log.Error().Err(err).Msg("cannot write C source")
		return 1
	}
	defer cleanup()

	log.Debug().Str("cc", cli.CC).Str("c", cpath).Str("out", cli.Output).Msg("linking")
	out, err := exec.Command(cli.CC, "-std=c11", "-o", cli.Output, cpath).CombinedOutput() //nolint:gosec // cc + generated file are trusted inputs
	if err != nil {
		log.Error().Err(err).Msg("cc failed")
		_, _ = os.Stderr.Write(out)
		return 1
	}
	log.Info().Str("output", cli.Output).Msg("built")
	return 0
}

// writeCSource writes the emitted C to a sidecar file (with --keep-c) or a temp
// file, returning its path and a cleanup function.
func writeCSource(cli *CLI, code string) (string, func(), error) {
	if cli.KeepC {
		path := cli.Output + ".c"
		if err := os.WriteFile(path, []byte(code), 0o644); err != nil { //nolint:gosec // generated source, not a secret
			return "", func() {}, err
		}
		return path, func() {}, nil
	}
	f, err := os.CreateTemp("", "zergc-*.c")
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

// reportDiags prints each diagnostic as 'file:line:col: message' to stderr.
func reportDiags(file string, diags []diag.Diagnostic) {
	for _, d := range diags {
		fmt.Fprintln(os.Stderr, d.WithFile(file))
	}
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
