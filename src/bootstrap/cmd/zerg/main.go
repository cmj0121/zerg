// Command zerg is the Zerg toolchain driver. Phase 1a ships its first
// subcommand, 'zerg fmt': parse a source file and reprint it in canonical form
// (see internal/fmt), to stdout by default or rewriting the file with --write.
// Later phases hang further subcommands (build, lint, test) off this driver;
// the Phase 0 'zergc' build path is untouched.
//
// Usage:
//
//	zerg fmt [--write] <file.zg>
package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	zfmt "github.com/cmj0121/zerg/src/bootstrap/internal/fmt"
)

// CLI is the zerg command line, parsed by kong.
type CLI struct {
	Fmt     FmtCmd `cmd:"" name:"fmt" help:"Format a Zerg source file to canonical form."`
	Verbose int    `short:"v" type:"counter" help:"increase log verbosity (-v info, -vv debug)"`
}

// FmtCmd formats one source file.
type FmtCmd struct {
	File  string `arg:"" name:"file" help:"the Zerg source file to format" type:"existingfile"`
	Write bool   `short:"w" name:"write" help:"rewrite the file in place instead of printing to stdout"`
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("zerg"),
		kong.Description("The Zerg toolchain driver (Phase 1a): format source to canonical form."),
		kong.UsageOnError(),
	)
	setupLog(cli.Verbose)
	switch ctx.Command() {
	case "fmt <file>":
		os.Exit(runFmt(&cli.Fmt))
	default:
		ctx.Fatalf("unknown command %q", ctx.Command())
	}
}

// runFmt formats one file and returns the process exit code.
func runFmt(cmd *FmtCmd) int {
	src, err := os.ReadFile(cmd.File)
	if err != nil {
		log.Error().Err(err).Msg("cannot read source")
		return 1
	}

	log.Debug().Str("file", cmd.File).Msg("formatting")
	out, diags := zfmt.Format(string(src))
	if len(diags) > 0 {
		reportDiags(cmd.File, diags)
		return 1
	}

	if !cmd.Write {
		fmt.Print(out)
		return 0
	}
	if out == string(src) {
		log.Debug().Msg("already canonical")
		return 0
	}
	if err := os.WriteFile(cmd.File, []byte(out), 0o644); err != nil { //nolint:gosec // formatted source, not a secret
		log.Error().Err(err).Msg("cannot rewrite source")
		return 1
	}
	log.Info().Str("file", cmd.File).Msg("formatted")
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
