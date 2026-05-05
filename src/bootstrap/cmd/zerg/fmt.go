package main

import (
	"bytes"
	stdfmt "fmt"
	"os"
	"path/filepath"

	zfmt "github.com/cmj/zerg/src/bootstrap/internal/fmt"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// fmtCmd implements `zerg fmt` — the canonical source formatter.
//
// Three modes, picked by flag precedence (Check > Write > stdout):
//
//   - default: read each file, parse with comments, emit canonical text to
//     stdout. Multiple files concatenate with no separator (one file per
//     invocation is the typical use; multi-file is provided for shell loops).
//   - `-w`: rewrite each file in place with the canonical text. Comment
//     preservation (Unit 1) makes this safe for files with header licences
//     and `# requires:` markers.
//   - `--check`: exit 0 if every file is already canonical; exit 1 if any
//     file differs (path written to stderr); exit 2 on parse error.
//
// Stdin / pipe input is NOT supported at v0.10.
type fmtCmd struct {
	Files []string `arg:"" name:"file" type:"existingfile" help:".zg source file(s)."`
	Write bool     `short:"w" help:"Rewrite each file in place with the canonical text."`
	Check bool     `name:"check" help:"Exit 0 if all files are canonical; 1 if any differ; 2 on parse error."`
}

// Run dispatches to the active mode and returns nil on success / a sentinel
// error on a non-zero exit. Sentinel errors carry the desired exit code via
// the fmtExitError type so main() can map them to os.Exit codes.
func (c *fmtCmd) Run() error {
	if c.Check {
		return c.runCheck()
	}
	if c.Write {
		return c.runWrite()
	}
	return c.runStdout()
}

// fmtExitError carries an explicit exit code out of fmtCmd.Run so main()
// can call os.Exit with the right value. The error message is empty —
// fmtCmd has already written user-facing text to stderr.
type fmtExitError struct{ code int }

func (e *fmtExitError) Error() string { return stdfmt.Sprintf("fmt exit %d", e.code) }

// runStdout formats each file and writes the canonical text to stdout. A
// parse error on any file aborts with exit 2; a read error aborts with
// exit 1.
func (c *fmtCmd) runStdout() error {
	for _, path := range c.Files {
		out, err := formatFile(path)
		if err != nil {
			stdfmt.Fprintln(os.Stderr, err)
			if isParseLikeError(err) {
				return &fmtExitError{code: 2}
			}
			return &fmtExitError{code: 1}
		}
		os.Stdout.Write(out)
	}
	return nil
}

// runWrite rewrites each file in place. Parse errors abort with exit 2 and
// no files are mutated past the failing one.
func (c *fmtCmd) runWrite() error {
	for _, path := range c.Files {
		src, err := os.ReadFile(path)
		if err != nil {
			stdfmt.Fprintln(os.Stderr, err)
			return &fmtExitError{code: 1}
		}
		out, err := formatBytes(path, src)
		if err != nil {
			stdfmt.Fprintln(os.Stderr, err)
			if isParseLikeError(err) {
				return &fmtExitError{code: 2}
			}
			return &fmtExitError{code: 1}
		}
		if err := writeCanonical(path, src, out); err != nil {
			stdfmt.Fprintln(os.Stderr, err)
			return &fmtExitError{code: 1}
		}
	}
	return nil
}

// writeCanonical replaces path's contents with out, atomically and only when
// the bytes actually differ. Skipping the write when src == out preserves
// mtime so downstream build systems do not see a spurious change. The atomic
// path is tempfile-in-same-dir + rename: a SIGINT between Close and Rename
// leaves the source intact (the tempfile is orphaned, never the truth).
// Permission bits are inherited from the source file's lstat — symlinks are
// rejected up front so `fmt -w` can never silently rewrite the link target.
func writeCanonical(path string, src, out []byte) error {
	if bytes.Equal(out, src) {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return stdfmt.Errorf("zerg fmt: refusing to follow symlink: %s", path)
	}
	perm := info.Mode().Perm()
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".fmt.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// runCheck reports the first non-canonical file (and every subsequent one)
// to stderr and exits 1. Parse errors abort with exit 2. Exit 0 means every
// file matched its canonical form byte-for-byte.
func (c *fmtCmd) runCheck() error {
	anyDiff := false
	for _, path := range c.Files {
		src, err := os.ReadFile(path)
		if err != nil {
			stdfmt.Fprintln(os.Stderr, err)
			return &fmtExitError{code: 1}
		}
		out, err := formatBytes(path, src)
		if err != nil {
			stdfmt.Fprintln(os.Stderr, err)
			return &fmtExitError{code: 2}
		}
		if !bytes.Equal(out, src) {
			stdfmt.Fprintln(os.Stderr, path)
			anyDiff = true
		}
	}
	if anyDiff {
		return &fmtExitError{code: 1}
	}
	return nil
}

// formatFile is the read+format helper shared by stdout / write modes.
func formatFile(path string) ([]byte, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return formatBytes(path, src)
}

// formatBytes runs src through Lex + Parse + Format. Lex / parse errors are
// returned with the standard `file:line:col: message` envelope so the
// surface matches `zerg run` / `zerg build`. Empty source produces empty
// output (no header injection). A file containing only comments produces
// just those comments verbatim.
func formatBytes(path string, src []byte) ([]byte, error) {
	tokens, comments, err := syntax.LexWithComments(src)
	if err != nil {
		return nil, &fmtParseError{path: path, err: err}
	}
	prog, err := syntax.ParseWithComments(tokens, comments)
	if err != nil {
		return nil, &fmtParseError{path: path, err: err}
	}
	return zfmt.Format(prog), nil
}

// fmtParseError wraps a lex/parse error with the source path so the user
// sees `file.zg:line:col: message` even for errors raised pre-tokeniser.
type fmtParseError struct {
	path string
	err  error
}

func (e *fmtParseError) Error() string {
	switch x := e.err.(type) {
	case *syntax.LexError:
		return stdfmt.Sprintf("%s:%d:%d: %s", e.path, x.Pos.Line, x.Pos.Column, x.Message)
	case *syntax.ParseError:
		return stdfmt.Sprintf("%s:%d:%d: %s", e.path, x.Pos.Line, x.Pos.Column, x.Message)
	}
	return stdfmt.Sprintf("%s: %s", e.path, e.err)
}

func (e *fmtParseError) Unwrap() error { return e.err }

// isParseLikeError reports whether err originated from lex/parse — i.e. the
// caller should map to exit code 2 instead of 1.
func isParseLikeError(err error) bool {
	_, ok := err.(*fmtParseError)
	return ok
}
