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

const banner = "Zerg REPL v0.9 — accepts the v0.9 surface (procedural core, composite data, borrow checking, polymorphism, modules, generics, null-safety, concurrency, stdlib, process surface, time)\n" +
	"Type :exit to quit, :help for syntax\n"

const helpText = "Statements: let/mut/const, fn, struct/enum/spec/impl, if/elif/else, for, match, return/break/continue, print, spawn, defer, select. Generics: [T: A + B] on fn/struct/enum/spec/impl. Null-safety: T?, nil, ?, ??, ?.. Concurrency: chan[T], <-, close, for v in ch, anon fn, wait_group. Run :exit to quit.\n"

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

		prog, err := syntax.Parse(tokens)
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

		// PLAN-mandated v0.5 guard: imports are not admitted at the REPL.
		// We detect any *ImportDecl in the parsed Program's top-level
		// statements and emit the dedicated diagnostic, dropping the
		// in-progress buffer so the user can keep typing.
		if hasImport(prog) {
			fmt.Fprintln(out, "import not supported at REPL")
			accumBuf.Reset()
			continue
		}

		// Parse succeeded for the *combined* source. Run it under a writer
		// that suppresses output already produced by committedSrc, so each
		// statement's print only fires once across the session.
		newPriorBytes, exitCode, exited, runErr := runWithSuppression(combined, out, priorBytes)
		if runErr != nil {
			// Type or runtime error: drop the in-progress buffer and the
			// new statements stay uncommitted. Previously-committed source
			// is unaffected so prior bindings remain usable.
			fmt.Fprintln(out, runErr)
			accumBuf.Reset()
			continue
		}
		if exited {
			fmt.Fprintf(out, "process exited with code %d\n", exitCode)
			// Drop the exit-statement so subsequent prompts can keep
			// running. The committed history retains everything BEFORE
			// the exit; the exit-statement itself stays uncommitted.
			accumBuf.Reset()
			continue
		}

		// Promote the accumulated buffer to committed history.
		committedSrc.WriteString(accumBuf.String())
		accumBuf.Reset()
		priorBytes = newPriorBytes
	}
}

// hasImport reports whether the parsed Program contains an *ImportDecl at
// the top level. Used to enforce the v0.5 PLAN's "import not supported at
// REPL" rule before any later phase (loader/typeck) would silently succeed.
func hasImport(prog *syntax.Program) bool {
	if prog == nil {
		return false
	}
	for _, st := range prog.Statements {
		if _, ok := st.(*syntax.ImportDecl); ok {
			return true
		}
	}
	return false
}

// runWithSuppression executes src and returns the total number of output
// bytes the program emits (so the next call can suppress them again). The
// first `skipBytes` bytes of program output are discarded; the rest are
// forwarded to out. This is how persistent state is implemented: we re-run
// the whole accumulated program every turn but only let new output through.
//
// v0.9: REPL hard-codes argv to ["<repl>"] per PLAN.md §"argv[0] parity
// rule". An os.exit(N) call from REPL code surfaces "process exited with
// code N" via the returned exitCode/exited and does NOT terminate the
// host Go process — the caller decides what to do.
func runWithSuppression(src string, out io.Writer, skipBytes int) (int, int, bool, error) {
	tokens, err := syntax.Lex([]byte(src))
	if err != nil {
		return 0, 0, false, err
	}
	prog, err := syntax.Parse(tokens)
	if err != nil {
		return 0, 0, false, err
	}
	if err := syntax.Check(prog); err != nil {
		return 0, 0, false, err
	}
	w := &skipWriter{dst: out, skip: skipBytes}
	bundle := singleProgramBundle{prog: prog}
	code, exited, err := run.RunBundleWithOptions(bundle, w, run.Options{Argv: []string{"<repl>"}})
	if err != nil {
		return 0, 0, false, err
	}
	return skipBytes + w.forwarded, code, exited, nil
}

// singleProgramBundle wraps a *Program in the run.BundleView interface
// so the REPL can route through RunBundleWithOptions exactly like the
// CLI driver. Mirrors run.singleProgramBundleAdapter (unexported).
type singleProgramBundle struct {
	prog *syntax.Program
}

func (b singleProgramBundle) BundleEntry() syntax.ModuleView {
	return singleProgramModule{prog: b.prog}
}

func (b singleProgramBundle) BundleModules() []syntax.ModuleView {
	return []syntax.ModuleView{singleProgramModule{prog: b.prog}}
}

type singleProgramModule struct {
	prog *syntax.Program
}

func (m singleProgramModule) ModuleName() string             { return "main" }
func (m singleProgramModule) ModuleProgram() *syntax.Program { return m.prog }
func (m singleProgramModule) ModuleImports() []syntax.ImportView {
	return nil
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
