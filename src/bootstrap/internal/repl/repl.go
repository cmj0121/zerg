// Package repl is the interactive read-eval-print loop.
//
// Input handling uses the *try-parse* strategy. Lines are accumulated into a
// buffer; after each line we attempt to parse the buffer. A successful parse
// runs the program and clears the buffer. A parse error flagged Incomplete
// — the parser ran out of tokens mid-construct — keeps the buffer and asks
// for more input. Any other parse error or runtime error prints to the user
// and clears the buffer so the next prompt starts fresh.
//
// Persistent state across prompts is achieved by re-parsing and re-running
// the entire accumulated program from scratch each turn, discarding output
// produced by previously executed statements. This is wasteful but trivially
// correct, and REPL sessions are short. Switching to an incremental
// interpreter is a future concern.
package repl

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/run"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

const banner = "Zerg REPL v0.2 — accepts the v0.2 procedural core plus composite data\n" +
	"Type :exit to quit, :help for syntax\n"

const helpText = "Statements: let/mut/const, fn, struct/enum, if/elif/else, for, match, return/break/continue, print. Run :exit to quit.\n"

const (
	primaryPrompt      = "zerg> "
	continuationPrompt = "... "
)

// Start runs the REPL using in for input and out for normal program output.
// It returns when the user issues :exit or input reaches EOF.
func Start(in io.Reader, out io.Writer) error {
	if _, err := io.WriteString(out, banner); err != nil {
		return err
	}
	scanner := bufio.NewScanner(in)
	// Allow long-ish lines without surprising the user.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// committedSrc is the source of every previously-executed statement,
	// concatenated. accumBuf is the in-progress buffer for the current
	// multi-line input. When accumBuf parses cleanly it is appended to
	// committedSrc and executed.
	var committedSrc strings.Builder
	var accumBuf strings.Builder

	// priorBytes is the byte count produced by running committedSrc alone.
	// We use it to skip already-emitted output when re-running the combined
	// program — see runWithSuppression.
	priorBytes := 0

	for {
		// Continuation prompt while we are waiting on more lines for an
		// in-progress statement; primary prompt at the start of input.
		prompt := primaryPrompt
		if accumBuf.Len() > 0 {
			prompt = continuationPrompt
		}
		if _, err := io.WriteString(out, prompt); err != nil {
			return err
		}
		if !scanner.Scan() {
			// Trailing newline so the next shell prompt isn't glued to
			// the REPL prompt.
			fmt.Fprintln(out)
			return scanner.Err()
		}
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Meta-commands only fire at the primary prompt — once the user
		// is mid-statement, `:exit` is just text inside their program.
		if accumBuf.Len() == 0 {
			switch trimmed {
			case "":
				continue
			case ":exit":
				return nil
			case ":help":
				if _, err := io.WriteString(out, helpText); err != nil {
					return err
				}
				continue
			}
		}

		// Append the line and try to parse the combined committed + accum
		// program. Trailing newline ensures statement termination on lines
		// like `let x := 5` that are otherwise complete.
		accumBuf.WriteString(line)
		accumBuf.WriteByte('\n')

		combined := committedSrc.String() + accumBuf.String()
		tokens, err := syntax.Lex([]byte(combined))
		if err != nil {
			// Lex errors are never "incomplete" at v0.1 — we don't have
			// multi-line strings or any other lexer-level continuations.
			fmt.Fprintln(out, err)
			accumBuf.Reset()
			continue
		}

		_, err = syntax.Parse(tokens)
		if err != nil {
			var pe *syntax.ParseError
			if errors.As(err, &pe) && pe.IsIncomplete() {
				// Keep the buffer; ask for the next line.
				continue
			}
			// A real parse error: report and discard the in-progress buffer
			// so the user can retry from a clean slate.
			fmt.Fprintln(out, err)
			accumBuf.Reset()
			continue
		}

		// Parse succeeded for the *combined* source. Run it under a writer
		// that suppresses output already produced by committedSrc, so each
		// statement's print only fires once across the session.
		newPriorBytes, runErr := runWithSuppression(combined, out, priorBytes)
		if runErr != nil {
			// Type or runtime error: drop the in-progress buffer and the
			// new statements stay uncommitted. Previously-committed source
			// is unaffected so prior bindings remain usable.
			fmt.Fprintln(out, runErr)
			accumBuf.Reset()
			continue
		}

		// Promote the accumulated buffer to committed history.
		committedSrc.WriteString(accumBuf.String())
		accumBuf.Reset()
		priorBytes = newPriorBytes
	}
}

// runWithSuppression executes src and returns the total number of output
// bytes the program emits (so the next call can suppress them again). The
// first `skipBytes` bytes of program output are discarded; the rest are
// forwarded to out. This is how persistent state is implemented: we re-run
// the whole accumulated program every turn but only let new output through.
func runWithSuppression(src string, out io.Writer, skipBytes int) (int, error) {
	tokens, err := syntax.Lex([]byte(src))
	if err != nil {
		return 0, err
	}
	prog, err := syntax.Parse(tokens)
	if err != nil {
		return 0, err
	}
	w := &skipWriter{dst: out, skip: skipBytes}
	if err := run.Run(prog, w); err != nil {
		return 0, err
	}
	return skipBytes + w.forwarded, nil
}

// skipWriter discards the first `skip` bytes written to it and forwards the
// rest to dst. It records how many bytes it forwarded so the caller can
// compute the program's total output for the next round.
//
// We rely on program output being deterministic: re-running the same source
// always emits the same bytes in the same order, so skipping by byte-count
// is exact rather than approximate.
type skipWriter struct {
	dst       io.Writer
	skip      int
	forwarded int
}

func (s *skipWriter) Write(p []byte) (int, error) {
	if s.skip >= len(p) {
		s.skip -= len(p)
		return len(p), nil
	}
	start := s.skip
	s.skip = 0
	n, err := s.dst.Write(p[start:])
	s.forwarded += n
	// Report the full input length as written so the caller doesn't see a
	// short-write — we actually consumed all of p (some of it via discard).
	if err != nil {
		return start + n, err
	}
	return len(p), nil
}
