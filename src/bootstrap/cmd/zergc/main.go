// Command zergc is the bootstrap Zerg compiler driver. For Phase 0 it proves the
// pipeline lex -> parse -> sema -> emit C -> cc; this entry point wires the stages
// together and reports diagnostics against the source file.
//
// Usage:
//
//	zergc [flags] <file.zg>
//
// Flags:
//
//	-o <path>     output binary path (default "a.out")
//	-emit <what>  stop early and print an intermediate form: "tokens" or "ast"
//
// Stages beyond the front-end are added as their packages land (sema, emit).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cmj0121/zerg/src/bootstrap/internal/lexer"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("zergc", flag.ContinueOnError)
	out := fs.String("o", "a.out", "output binary path")
	emit := fs.String("emit", "", "stop early and print an intermediate form: tokens|ast")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: zergc [flags] <file.zg>")
		return 2
	}
	path := fs.Arg(0)

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "zergc: %v\n", err)
		return 1
	}

	if *emit == "tokens" {
		return dumpTokens(string(src))
	}

	file, diags := parser.Parse(string(src))
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d.WithFile(path))
		}
		return 1
	}

	if *emit == "ast" {
		fmt.Printf("%d declaration(s) parsed\n", len(file.Decls))
		return 0
	}

	// TODO(phase0): sema type-check, emit C, and invoke cc to produce *out.
	_ = out
	fmt.Fprintln(os.Stderr, "zergc: front-end only (sema and C emission are not wired yet)")
	return 0
}

// dumpTokens prints the token stream, one per line, for debugging the lexer.
func dumpTokens(src string) int {
	toks, diags := lexer.Tokenize(src)
	for _, t := range toks {
		if t.Kind == token.EOF {
			break
		}
		fmt.Printf("%s\t%s\n", t.Span.Start, t)
	}
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d.Error())
		}
		return 1
	}
	return 0
}
