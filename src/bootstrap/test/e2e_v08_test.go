// v0.8 parity corpus harness — stdlib edition.
//
// Mirrors `e2e_v07_test.go` for the deterministic and reject halves; the
// only structural addition is a per-program `manifest.txt` parser that
// provisions environment variables and fixture files inside a per-test
// tempdir before invoking either half.
//
// Layout:
//
//   test/v0_8/<NN_name>/
//       main.zg           — entry file
//       expected.txt      — golden stdout for both interpret and build
//       manifest.txt      — optional manifest (env, fixture, etc.)
//
//   test/v0_8/rejects/<NN_name>/
//       main.zg           — entry file
//       error.txt         — diagnostic substring expected on stderr
//
// Manifest grammar (one rule per line, blank/`#` lines ignored):
//
//   env: KEY=VALUE        — set env var on both halves; `$TMPDIR` in VALUE
//                           is substituted with the per-test tempdir.
//   fixture: name = body  — write `body` to `<tempdir>/name` before running.
//   stderr_contains: STR  — substring expected to appear in stderr.
//   exit_code: N          — expected exit code (default 0).
//
// The per-test tempdir is always exposed to the program via the
// ZERG_TEST_TEMPDIR environment variable so corpus programs can read
// fixture files portably across macOS/Linux/CI.
package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/build"
)

// v08CorpusDir resolves to src/bootstrap/test/v0_8/.
func v08CorpusDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testDir(t), "v0_8")
}

// v08RejectsDir resolves to src/bootstrap/test/v0_8/rejects/.
func v08RejectsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(v08CorpusDir(t), "rejects")
}

// listV08Programs returns absolute paths to every deterministic program
// directory under v0_8/, excluding the rejects/ subdirectory. Each must
// contain main.zg + expected.txt; manifest.txt is optional.
func listV08Programs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read corpus root %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("unexpected non-directory entry in %s: %q", root, e.Name())
		}
		if e.Name() == "rejects" {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "main.zg")); err != nil {
			t.Fatalf("program %s missing main.zg: %v", dir, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "expected.txt")); err != nil {
			t.Fatalf("program %s missing expected.txt: %v", dir, err)
		}
		out = append(out, dir)
	}
	if len(out) == 0 {
		t.Fatalf("no program directories found under %s", root)
	}
	return out
}

// listV08Rejects mirrors listV08Programs for the rejects sub-corpus.
func listV08Rejects(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read rejects root %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("unexpected non-directory entry in %s: %q", root, e.Name())
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "main.zg")); err != nil {
			t.Fatalf("reject %s missing main.zg: %v", dir, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "error.txt")); err != nil {
			t.Fatalf("reject %s missing error.txt: %v", dir, err)
		}
		out = append(out, dir)
	}
	if len(out) == 0 {
		t.Fatalf("no reject directories found under %s", root)
	}
	return out
}

// v08Manifest is the parsed shape of a corpus program's manifest.txt.
type v08Manifest struct {
	envs            [][2]string // ordered KEY/VALUE pairs
	fixtures        [][2]string // ordered name/body pairs
	stderrContains  string
	exitCode        int
	hasExitOverride bool
}

// parseV08Manifest reads <dir>/manifest.txt if present and returns the
// parsed manifest. If the file does not exist, an empty manifest is
// returned with exitCode = 0 and no fixtures or env vars.
func parseV08Manifest(t *testing.T, dir, tempDir string) v08Manifest {
	t.Helper()
	path := filepath.Join(dir, "manifest.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return v08Manifest{}
		}
		t.Fatalf("read manifest %s: %v", path, err)
	}
	var m v08Manifest
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			t.Fatalf("%s:%d: missing ':' in rule %q", path, i+1, line)
		}
		kind := strings.TrimSpace(line[:colon])
		rest := strings.TrimLeft(line[colon+1:], " \t")
		switch kind {
		case "env":
			eq := strings.IndexByte(rest, '=')
			if eq < 0 {
				t.Fatalf("%s:%d: env rule missing '=' in %q", path, i+1, rest)
			}
			k := strings.TrimSpace(rest[:eq])
			v := rest[eq+1:]
			v = strings.ReplaceAll(v, "$TMPDIR", tempDir)
			m.envs = append(m.envs, [2]string{k, v})
		case "fixture":
			eq := strings.IndexByte(rest, '=')
			if eq < 0 {
				t.Fatalf("%s:%d: fixture rule missing '=' in %q", path, i+1, rest)
			}
			name := strings.TrimSpace(rest[:eq])
			body := rest[eq+1:]
			if strings.HasPrefix(body, " ") {
				body = body[1:]
			}
			m.fixtures = append(m.fixtures, [2]string{name, body})
		case "stderr_contains":
			m.stderrContains = rest
		case "exit_code":
			n, perr := atoiStrict(rest)
			if perr != nil {
				t.Fatalf("%s:%d: invalid exit_code %q: %v", path, i+1, rest, perr)
			}
			m.exitCode = n
			m.hasExitOverride = true
		default:
			t.Fatalf("%s:%d: unknown manifest rule %q", path, i+1, kind)
		}
	}
	return m
}

// atoiStrict parses a non-negative decimal int. Used only for exit_code.
func atoiStrict(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, &parseErr{"empty"}
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, &parseErr{"non-digit"}
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// applyManifestFixtures writes every fixture file declared in the
// manifest into the given tempdir. Called once per test, before either
// half is invoked.
func applyManifestFixtures(t *testing.T, tempDir string, m v08Manifest) {
	t.Helper()
	for _, kv := range m.fixtures {
		name, body := kv[0], kv[1]
		full := filepath.Join(tempDir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for fixture %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
}

// envForManifest assembles the env slice (os.Environ + manifest envs +
// ZERG_TEST_TEMPDIR) for invoking a half.
func envForManifest(tempDir string, m v08Manifest) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "ZERG_TEST_TEMPDIR="+tempDir)
	for _, kv := range m.envs {
		env = append(env, kv[0]+"="+kv[1])
	}
	return env
}

// captureCmdEnv runs the command with extra env entries and returns
// stdout, stderr, exit code, and any execution error.
func captureCmdEnv(name string, args []string, dir string, env []string) (stdout, stderr []byte, code int, err error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	runErr := cmd.Run()
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			return so.Bytes(), se.Bytes(), ee.ExitCode(), nil
		}
		return so.Bytes(), se.Bytes(), -1, runErr
	}
	return so.Bytes(), se.Bytes(), 0, nil
}

// TestE2EV08Corpus runs every deterministic v0.8 program through both
// `zerg run` and `zerg build`-then-exec and checks parity against
// expected.txt. Programs may carry a manifest.txt that provisions env
// vars and fixture files for both halves.
func TestE2EV08Corpus(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v08CorpusDir(t)
	programs := listV08Programs(t, corpus)

	ccAvailable := true
	if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
		ccAvailable = false
	}

	for _, prog := range programs {
		prog := prog
		name := filepath.Base(prog)
		t.Run(name, func(t *testing.T) {
			entry := filepath.Join(prog, "main.zg")
			goldenPath := filepath.Join(prog, "expected.txt")
			golden, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v", goldenPath, err)
			}

			runDir := t.TempDir()
			runManifest := parseV08Manifest(t, prog, runDir)
			applyManifestFixtures(t, runDir, runManifest)
			runEnv := envForManifest(runDir, runManifest)

			runOut, runErr, runCode, err := captureCmdEnv(binPath, []string{"run", entry}, runDir, runEnv)
			if err != nil {
				t.Fatalf("zerg run: %v", err)
			}
			wantCode := 0
			if runManifest.hasExitOverride {
				wantCode = runManifest.exitCode
			}
			if runCode != wantCode {
				t.Fatalf("zerg run exit code = %d, want %d\nstdout: %s\nstderr: %s", runCode, wantCode, runOut, runErr)
			}
			if runManifest.stderrContains != "" && !strings.Contains(string(runErr), runManifest.stderrContains) {
				t.Errorf("zerg run stderr missing substring %q\nstderr: %s", runManifest.stderrContains, runErr)
			}
			if !bytes.Equal(runOut, golden) {
				t.Errorf("run stdout vs golden mismatch\nrun:    %q\ngolden: %q", runOut, golden)
			}

			if !ccAvailable {
				t.Skip("cc not available; build half skipped")
			}

			buildDir := t.TempDir()
			buildManifest := parseV08Manifest(t, prog, buildDir)
			applyManifestFixtures(t, buildDir, buildManifest)
			buildEnv := envForManifest(buildDir, buildManifest)

			_, _, buildCode, err := captureCmdEnv(binPath, []string{"build", entry}, buildDir, buildEnv)
			if err != nil {
				t.Fatalf("zerg build: %v", err)
			}
			if buildCode != 0 {
				t.Fatalf("zerg build exit code = %d, want 0", buildCode)
			}
			outBin := filepath.Join(buildDir, "main")
			if _, err := os.Stat(outBin); err != nil {
				t.Fatalf("expected binary at %s: %v", outBin, err)
			}
			binOut, binErr, binCode, err := captureCmdEnv(outBin, nil, buildDir, buildEnv)
			if err != nil {
				t.Fatalf("execute %s: %v", outBin, err)
			}
			if binCode != wantCode {
				t.Fatalf("compiled binary exit code = %d, want %d\nstderr: %s", binCode, wantCode, binErr)
			}
			if buildManifest.stderrContains != "" && !strings.Contains(string(binErr), buildManifest.stderrContains) {
				t.Errorf("build binary stderr missing substring %q\nstderr: %s", buildManifest.stderrContains, binErr)
			}
			if !bytes.Equal(binOut, golden) {
				t.Errorf("build stdout vs golden mismatch\nbuild:  %q\ngolden: %q", binOut, golden)
			}
			if !bytes.Equal(runOut, binOut) {
				t.Errorf("run vs build stdout mismatch\nrun:   %q\nbuild: %q", runOut, binOut)
			}
		})
	}
}

// TestE2EV08Rejects runs every reject program through both halves and
// asserts non-zero exit + stderr substring match. Build-time rejections
// surface via build's own stderr; runtime panics (e.g. split with empty
// separator) compile cleanly and surface at exec time.
func TestE2EV08Rejects(t *testing.T) {
	binPath := buildToolchain(t)
	dir := v08RejectsDir(t)
	rejects := listV08Rejects(t, dir)

	ccAvailable := true
	if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
		ccAvailable = false
	}

	for _, prog := range rejects {
		prog := prog
		name := filepath.Base(prog)
		t.Run(name, func(t *testing.T) {
			entry := filepath.Join(prog, "main.zg")
			errPath := filepath.Join(prog, "error.txt")
			wantBytes, err := os.ReadFile(errPath)
			if err != nil {
				t.Fatalf("read error.txt %s: %v", errPath, err)
			}
			want := strings.TrimRight(string(wantBytes), "\n")
			if want == "" {
				t.Fatalf("error.txt %s is empty", errPath)
			}

			_, stderr, code, err := captureCmdBoth(binPath, []string{"run", entry}, t.TempDir())
			if err != nil {
				t.Fatalf("zerg run: %v", err)
			}
			if code == 0 {
				t.Fatalf("zerg run exit code = 0, want non-zero\nstderr: %s", stderr)
			}
			if !strings.Contains(string(stderr), want) {
				t.Fatalf("zerg run stderr missing substring %q\nstderr: %s", want, stderr)
			}

			if !ccAvailable {
				t.Skip("cc not available; build half skipped")
			}
			buildDir := t.TempDir()
			_, buildStderr, buildCode, err := captureCmdBoth(binPath, []string{"build", entry}, buildDir)
			if err != nil {
				t.Fatalf("zerg build: %v", err)
			}
			if buildCode != 0 {
				if !strings.Contains(string(buildStderr), want) {
					t.Fatalf("zerg build stderr missing substring %q\nstderr: %s", want, buildStderr)
				}
				return
			}
			outBin := filepath.Join(buildDir, "main")
			if _, err := os.Stat(outBin); err != nil {
				t.Fatalf("expected binary at %s: %v", outBin, err)
			}
			_, execStderr, execCode, err := captureCmdBoth(outBin, nil, buildDir)
			if err != nil {
				t.Fatalf("execute %s: %v", outBin, err)
			}
			if execCode == 0 {
				t.Fatalf("compiled binary exit code = 0, want non-zero\nstderr: %s", execStderr)
			}
			if !strings.Contains(string(execStderr), want) {
				t.Fatalf("binary stderr missing substring %q\nstderr: %s", want, execStderr)
			}
		})
	}
}
