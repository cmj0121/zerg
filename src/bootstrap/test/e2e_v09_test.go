// v0.9 parity corpus harness — process-surface edition.
//
// Mirrors `e2e_v08_test.go` for the deterministic and reject halves; the
// structural addition is a new `argv:` manifest directive that pipes a
// list of argv elements (after argv[0]) into both halves:
//
//   - Interpreter half: passed to `zerg run main.zg` as positional args
//     after the source path. cmd/zerg constructs Argv as
//     [main.zg, args...] and forwards via RunBundleWithOptions(Argv).
//   - Cgen half: appended to the compiled binary's exec.Cmd.Args. The
//     kernel sets argv[0] to the binary path, so corpus programs MUST NOT
//     print argv[0] — only argv[1:]. Per PLAN.md §"argv[0] parity rule".
//
// $TMPDIR substitution applies to argv values too (so a corpus test can
// hand the program a path inside its per-test tempdir).
//
// Layout:
//
//   test/v0_9/<NN_name>/
//       main.zg           — entry file
//       expected.txt      — golden stdout for both interpret and build
//       manifest.txt      — optional manifest (env, fixture, argv, etc.)
//
//   test/v0_9/rejects/<NN_name>/
//       main.zg           — entry file
//       error.txt         — diagnostic substring expected on stderr
//
// Manifest grammar additions on top of v0.8:
//
//   argv: a b c           — argv elements after argv[0]; whitespace-split.
//                           $TMPDIR in any element is substituted with the
//                           per-test tempdir.
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

// v09CorpusDir resolves to src/bootstrap/test/v0_9/.
func v09CorpusDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testDir(t), "v0_9")
}

// v09RejectsDir resolves to src/bootstrap/test/v0_9/rejects/.
func v09RejectsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(v09CorpusDir(t), "rejects")
}

// listV09Programs returns absolute paths to every deterministic program
// directory under v0_9/, excluding the rejects/ subdirectory. Each must
// contain main.zg + expected.txt; manifest.txt is optional.
func listV09Programs(t *testing.T, root string) []string {
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

// listV09Rejects mirrors listV09Programs for the rejects sub-corpus.
func listV09Rejects(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		t.Skipf("no rejects root at %s (v0.9 rejects corpus retired with `never`)", root)
	}
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
		t.Skipf("no reject directories under %s (v0.9 rejects corpus is currently empty)", root)
	}
	return out
}

// v09Manifest extends v08Manifest with an argv slice. Reuse of the v08
// fields keeps env / fixture / stderr_contains / exit_code grammar
// byte-identical across milestones.
type v09Manifest struct {
	envs            [][2]string
	fixtures        [][2]string
	stderrContains  string
	exitCode        int
	hasExitOverride bool
	argv            []string
}

// parseV09Manifest reads <dir>/manifest.txt and returns the parsed
// manifest. Adds the v0.9 `argv:` rule on top of the v0.8 grammar.
func parseV09Manifest(t *testing.T, dir, tempDir string) v09Manifest {
	t.Helper()
	path := filepath.Join(dir, "manifest.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return v09Manifest{}
		}
		t.Fatalf("read manifest %s: %v", path, err)
	}
	var m v09Manifest
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
		case "argv":
			for _, tok := range strings.Fields(rest) {
				m.argv = append(m.argv, strings.ReplaceAll(tok, "$TMPDIR", tempDir))
			}
		default:
			t.Fatalf("%s:%d: unknown manifest rule %q", path, i+1, kind)
		}
	}
	return m
}

// applyV09Fixtures writes every fixture file declared in the manifest
// into the given tempdir. Identical to v0.8's applyManifestFixtures
// modulo the v09Manifest type.
func applyV09Fixtures(t *testing.T, tempDir string, m v09Manifest) {
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

// envForV09Manifest assembles the env slice for invoking a half.
func envForV09Manifest(tempDir string, m v09Manifest) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "ZERG_TEST_TEMPDIR="+tempDir)
	for _, kv := range m.envs {
		env = append(env, kv[0]+"="+kv[1])
	}
	return env
}

// lintV09NoArgvZero rejects a corpus program that prints argv[0]. The
// kernel-set argv[0] differs between halves (interpreter sees the .zg
// path; cgen sees the binary path), so corpus programs MUST limit
// themselves to argv[1:]. Catch obvious offenders so the parity rule is
// noticed at corpus-add time, not at flake-debug time. The literal-form
// check (`argv[0]`, `a[0]` after `a := os.argv()`, etc.) is a heuristic
// — if a future program needs to print the value at index zero for a
// legitimate reason, this lint can be relaxed.
func lintV09NoArgvZero(t *testing.T, programDir string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(programDir, "main.zg"))
	if err != nil {
		t.Fatalf("lint read %s: %v", programDir, err)
	}
	if bytes.Contains(src, []byte("argv()[0]")) || bytes.Contains(src, []byte("argv[0]")) {
		t.Fatalf("%s: prints argv[0]; corpus programs must use argv[1:] only (see PLAN.md §argv[0] parity rule)",
			programDir)
	}
}

// TestE2EV09Corpus runs every deterministic v0.9 program through both
// `zerg run` and `zerg build`-then-exec and checks parity against
// expected.txt. Programs may carry a manifest.txt that provisions env
// vars, fixture files, argv, exit_code, and stderr_contains.
func TestE2EV09Corpus(t *testing.T) {
	binPath := buildToolchain(t)
	corpus := v09CorpusDir(t)
	programs := listV09Programs(t, corpus)

	ccAvailable := true
	if _, lookErr := exec.LookPath(build.DefaultCC()); lookErr != nil {
		ccAvailable = false
	}

	for _, prog := range programs {
		prog := prog
		name := filepath.Base(prog)
		t.Run(name, func(t *testing.T) {
			lintV09NoArgvZero(t, prog)

			entry := filepath.Join(prog, "main.zg")
			goldenPath := filepath.Join(prog, "expected.txt")
			golden, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v", goldenPath, err)
			}

			runDir := t.TempDir()
			runManifest := parseV09Manifest(t, prog, runDir)
			applyV09Fixtures(t, runDir, runManifest)
			runEnv := envForV09Manifest(runDir, runManifest)

			runArgs := append([]string{"run", entry}, runManifest.argv...)
			runOut, runErr, runCode, err := captureCmdEnv(binPath, runArgs, runDir, runEnv)
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
			buildManifest := parseV09Manifest(t, prog, buildDir)
			applyV09Fixtures(t, buildDir, buildManifest)
			buildEnv := envForV09Manifest(buildDir, buildManifest)

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
			binOut, binErr, binCode, err := captureCmdEnv(outBin, buildManifest.argv, buildDir, buildEnv)
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

// TestE2EV09Rejects runs every reject program through both halves and
// asserts non-zero exit + stderr substring match.
func TestE2EV09Rejects(t *testing.T) {
	binPath := buildToolchain(t)
	dir := v09RejectsDir(t)
	rejects := listV09Rejects(t, dir)

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
