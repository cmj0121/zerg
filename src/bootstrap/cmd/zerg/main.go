// Command zerg is the v0.0 toolchain entry point. CLI dispatch is handled
// by kong; diagnostic logging goes to zerolog with a -v / -vv verbosity
// dial. See PLAN.md for the design.
package main

import (
	"errors"
	"os"

	"github.com/alecthomas/kong"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/cmj/zerg/src/bootstrap/internal/build"
	"github.com/cmj/zerg/src/bootstrap/internal/repl"
	"github.com/cmj/zerg/src/bootstrap/internal/run"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

const version = "0.2.0"

type cli struct {
	Verbose int              `short:"v" type:"counter" help:"Enable diagnostic logging (-v info, -vv debug, -vvv trace)."`
	Version kong.VersionFlag `name:"version" help:"Print version and exit."`

	Run   runCmd   `cmd:"" help:"Interpret a .zg source file."`
	Build buildCmd `cmd:"" help:"Compile a .zg source file to a native binary in CWD."`
	Repl  replCmd  `cmd:"" help:"Start the interactive REPL."`
}

type runCmd struct {
	File string `arg:"" type:"existingfile" help:".zg source file."`
}

func (c *runCmd) Run() error {
	if err := checkRequiresFile(c.File); err != nil {
		return err
	}
	src, err := os.ReadFile(c.File)
	if err != nil {
		return err
	}
	tokens, err := syntax.Lex(src)
	if err != nil {
		return err
	}
	log.Debug().Int("tokens", len(tokens)).Str("file", c.File).Msg("lexed")

	prog, err := syntax.Parse(tokens)
	if err != nil {
		return err
	}
	log.Debug().Int("statements", len(prog.Statements)).Msg("parsed")

	return run.Run(prog, os.Stdout)
}

type buildCmd struct {
	File  string `arg:"" type:"existingfile" help:".zg source file."`
	EmitC bool   `name:"emit-c" help:"Print generated C source to stdout instead of compiling."`
}

func (c *buildCmd) Run() error {
	if err := checkRequiresFile(c.File); err != nil {
		return err
	}
	if c.EmitC {
		return build.EmitSource(c.File, os.Stdout)
	}
	log.Info().Str("file", c.File).Str("cc", build.DefaultCC()).Msg("building")
	return build.Build(c.File)
}

type replCmd struct{}

func (c *replCmd) Run() error {
	return repl.Start(os.Stdin, os.Stdout)
}

func main() {
	app := &cli{}
	ctx := kong.Parse(app,
		kong.Name("zerg"),
		kong.Description("Zerg toolchain (v0.2)."),
		kong.Vars{"version": "zerg " + version},
		kong.UsageOnError(),
	)
	configureLogger(app.Verbose)

	if err := ctx.Run(); err != nil {
		// errRequiresFutureVersion has already written its own user-facing
		// stderr line. Don't double-log via zerolog — it would duplicate the
		// message and add a timestamp the test harness has to filter out.
		if !errors.Is(err, errRequiresFutureVersion) {
			log.Error().Err(err).Send()
		}
		os.Exit(1)
	}
}

// configureLogger writes to stderr so diagnostic output does not pollute
// the program's stdout — the surface checked for run/build parity.
func configureLogger(vCount int) {
	level := zerolog.WarnLevel
	switch {
	case vCount >= 3:
		level = zerolog.TraceLevel
	case vCount >= 2:
		level = zerolog.DebugLevel
	case vCount >= 1:
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "15:04:05",
	}).With().Timestamp().Logger()
}
