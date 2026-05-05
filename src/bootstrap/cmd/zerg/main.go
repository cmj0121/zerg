// Command zerg is the toolchain entry point. CLI dispatch is handled by
// kong; diagnostic logging goes to zerolog with a -v / -vv verbosity dial.
package main

import (
	"errors"
	"os"

	"github.com/alecthomas/kong"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/cmj/zerg/src/bootstrap/internal/build"
	"github.com/cmj/zerg/src/bootstrap/internal/loader"
	"github.com/cmj/zerg/src/bootstrap/internal/repl"
	"github.com/cmj/zerg/src/bootstrap/internal/run"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// cliVersion is the user-facing string `zerg --version` prints. The
// toolchain's (major, minor) gate lives in internal/version (see
// version.Major / version.Minor) — `cliVersion` may lag behind the gate
// during a v0.X release in progress and is bumped to canonical form by
// the twain unit at the end of each version's work.
const cliVersion = "0.10.0"

type cli struct {
	Verbose int              `short:"v" type:"counter" help:"Enable diagnostic logging (-v info, -vv debug, -vvv trace)."`
	Version kong.VersionFlag `name:"version" help:"Print version and exit."`

	Run   runCmd   `cmd:"" help:"Interpret a .zg source file."`
	Build buildCmd `cmd:"" help:"Compile a .zg source file to a native binary in CWD."`
	Fmt   fmtCmd   `cmd:"" help:"Format .zg source files (canonical style)."`
	Repl  replCmd  `cmd:"" help:"Start the interactive REPL."`
}

type runCmd struct {
	File string   `arg:"" type:"existingfile" help:".zg source file."`
	Args []string `arg:"" optional:"" help:"Args passed to the program as os.argv[1:]."`
}

func (c *runCmd) Run() error {
	if err := checkRequiresFile(c.File); err != nil {
		return err
	}
	bundle, err := loader.Load(c.File)
	if err != nil {
		return err
	}
	log.Debug().
		Int("modules", len(bundle.Modules)).
		Str("entry", bundle.Entry.Path).
		Msg("loaded")
	if err := syntax.CheckBundle(bundle); err != nil {
		return err
	}
	argv := append([]string{c.File}, c.Args...)
	code, exited, err := run.RunBundleWithOptions(bundle, os.Stdout, run.Options{Argv: argv})
	if err != nil {
		return err
	}
	if exited && code != 0 {
		os.Exit(code)
	}
	return nil
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
		kong.Description("Zerg toolchain (v0.10)."),
		kong.Vars{"version": "zerg " + cliVersion},
		kong.UsageOnError(),
	)
	configureLogger(app.Verbose)

	if err := ctx.Run(); err != nil {
		// `zerg fmt` carries its own exit code — it already wrote the
		// user-facing stderr line (or a parse-error envelope), so just
		// exit with the requested code without re-logging.
		var fxe *fmtExitError
		if errors.As(err, &fxe) {
			os.Exit(fxe.code)
		}
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
