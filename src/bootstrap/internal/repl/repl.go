// Package repl is the interactive read-eval-print loop for v0.0.
//
// The REPL accepts one statement per line. Parse and runtime errors are
// printed to the writer passed to Start and the loop continues — you should
// never have to restart the REPL because of a typo.
package repl

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/run"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

const banner = "Zerg REPL v0.0 — accepts: nop, print \"...\"\n" +
	"Type :exit to quit.\n"

const helpText = `Supported v0.0 syntax:
  nop                    — no-op
  print "..."            — print a string literal
Commands:
  :help                  — show this help
  :exit                  — quit (Ctrl-D also works)
`

// Start runs the REPL using in for input and out for normal program output.
// It returns when the user issues :exit or input reaches EOF.
func Start(in io.Reader, out io.Writer) error {
	if _, err := io.WriteString(out, banner); err != nil {
		return err
	}
	scanner := bufio.NewScanner(in)
	// Allow long-ish lines without surprising the user.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		if _, err := io.WriteString(out, "zerg> "); err != nil {
			return err
		}
		if !scanner.Scan() {
			// Trailing newline so the next shell prompt isn't glued to
			// `zerg> `.
			fmt.Fprintln(out)
			return scanner.Err()
		}
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case trimmed == ":exit":
			return nil
		case trimmed == ":help":
			if _, err := io.WriteString(out, helpText); err != nil {
				return err
			}
			continue
		}
		if err := evalLine(line, out); err != nil {
			fmt.Fprintln(out, err)
		}
	}
}

func evalLine(line string, out io.Writer) error {
	tokens, err := syntax.Lex([]byte(line))
	if err != nil {
		return err
	}
	stmt, err := syntax.ParseStatement(tokens)
	if err != nil {
		return err
	}
	if stmt == nil {
		return nil
	}
	prog := &syntax.Program{Statements: []syntax.Stmt{stmt}}
	return run.Run(prog, out)
}
