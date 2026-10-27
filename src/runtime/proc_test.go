package runtime

import (
	"strings"
	"testing"
	"time"
)

// TestChildProcessIsBounded drives the child a tool can bound (#196): where its streams go, a
// limit that kills what it started, a stop signal passed on to it, and a directory of its own.
// Each line the driver prints is one claim; the test wants every one of them `ok`, and a
// count, so a driver that stopped early cannot pass by printing fewer.
func TestChildProcessIsBounded(t *testing.T) {
	bin := buildCore(t, "proc", procC)
	out, code := runBounded(t, bin, "", 30*time.Second)
	if code != 0 {
		t.Fatalf("proc driver exited %d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	bad := 0
	for _, ln := range lines {
		if !strings.HasSuffix(ln, "=ok") {
			t.Errorf("%s", ln)
			bad++
		}
	}
	if bad == 0 && len(lines) != procClaims {
		t.Fatalf("proc driver printed %d claims, want %d:\n%s", len(lines), procClaims, out)
	}
}

// procClaims is how many `say` lines procC prints.
const procClaims = 14

const procC = `
#define _POSIX_C_SOURCE 200809L
#include "zergrt.h"
#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

static char dir[512];

static void say(const char *what, int ok, const char *got) {
	printf("%s=%s%s%s\n", what, ok ? "ok" : "FAIL", ok ? "" : " got ", ok ? "" : got);
}

static zrt_list sh(const char *script) {
	static const char *a[3];
	a[0] = "sh";
	a[1] = "-c";
	a[2] = script;
	zrt_list l;
	zrt_list_init(&l, sizeof(char *), NULL);
	for (int i = 0; i < 3; i++) {
		zrt_list_push(&l, &a[i]);
	}
	return l;
}

static const char *at(const char *name) {
	static char buf[4][600];
	static int  k = 0;
	k = (k + 1) % 4;
	snprintf(buf[k], sizeof(buf[k]), "%s/%s", dir, name);
	return buf[k];
}

static const char *slurp(const char *path) {
	static char buf[256];
	FILE *f = fopen(path, "r");
	if (f == NULL) {
		return "(missing)";
	}
	size_t n = fread(buf, 1, sizeof(buf) - 1, f);
	fclose(f);
	buf[n] = '\0';
	return buf;
}

static long run(const char *script, const char *out, const char *err, long ms) {
	int64_t pid = zrt_proc_start(sh(script), out, err);
	if (pid < 0) {
		return -100;
	}
	return (long)zrt_proc_wait_for(pid, ms);
}

/* dead is whether a process this driver did not start has ended: gone, or a zombie nobody has
 * reaped yet — which is what a container whose pid 1 reaps nothing leaves of it. */
static int dead(pid_t pid) {
	if (kill(pid, 0) != 0 && errno == ESRCH) {
		return 1;
	}
	char path[64], line[256] = "";
	snprintf(path, sizeof(path), "/proc/%ld/stat", (long)pid);
	FILE *f = fopen(path, "r");
	if (f != NULL) { /* Linux: the state is the field after the parenthesised name */
		char *close = fgets(line, sizeof(line), f) != NULL ? strrchr(line, ')') : NULL;
		fclose(f);
		return close != NULL && close[1] == ' ' && close[2] == 'Z';
	}
	snprintf(path, sizeof(path), "ps -o stat= -p %ld", (long)pid);
	FILE *p = popen(path, "r");
	if (p == NULL) {
		return 0;
	}
	if (fgets(line, sizeof(line), p) == NULL) {
		line[0] = '\0';
	}
	pclose(p);
	return line[strspn(line, " ")] == 'Z';
}

static double now(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ts.tv_sec + ts.tv_nsec / 1e9;
}

int main(void) {
	const char *base = getenv("TMPDIR");
	char prefix[400];
	snprintf(prefix, sizeof(prefix), "%s/zrt-proc.", base != NULL && base[0] != '\0' ? base : "/tmp");
	const char *d1 = zrt_mkdtemp(prefix);
	const char *d2 = zrt_mkdtemp(prefix);
	struct stat st;
	say("mkdtemp", d1[0] != '\0' && d2[0] != '\0' && strcmp(d1, d2) != 0 && stat(d1, &st) == 0 && S_ISDIR(st.st_mode), d1);
	snprintf(dir, sizeof(dir), "%s", d1);

	/* joined: one stream, in the order written */
	long rc = run("echo a; echo b >&2; echo c", at("j"), at("j"), -1);
	say("joined", rc == 0 && strcmp(slurp(at("j")), "a\nb\nc\n") == 0, slurp(at("j")));

	/* apart: each stream its own file */
	rc = run("echo o; echo e >&2", at("o"), at("e"), -1);
	say("apart", rc == 0 && strcmp(slurp(at("o")), "o\n") == 0 && strcmp(slurp(at("e")), "e\n") == 0, slurp(at("e")));

	/* discarded: stderr to /dev/null leaves stdout alone */
	rc = run("echo kept; echo gone >&2", at("d"), "/dev/null", -1);
	say("discarded", rc == 0 && strcmp(slurp(at("d")), "kept\n") == 0, slurp(at("d")));

	/* the input is empty, so a reader of it ends at once */
	rc = run("cat; echo done", at("i"), at("i"), 5000);
	say("stdin-empty", rc == 0 && strcmp(slurp(at("i")), "done\n") == 0, slurp(at("i")));

	/* an exit status comes back as it is */
	rc = run("exit 3", "", "", 5000);
	say("status", rc == 3, "");

	/* a file that cannot be opened is a child never started */
	rc = run("true", "/nonexistent-zrt-dir/out", "", -1);
	say("unopenable", rc == -100, "");

	/* THE LIMIT kills the child's whole group, a grandchild included */
	double t0 = now();
	char script[700];
	snprintf(script, sizeof(script), "sleep 31 & echo $! >%s; wait", at("g"));
	rc = run(script, "", "", 300);
	double took = now() - t0;
	long gpid = atol(slurp(at("g")));
	say("late", rc == ZRT_PROC_LATE && took < 5.0, "");
	int gone = 0;
	for (int i = 0; i < 200 && !gone; i++) {
		gone = gpid > 0 && dead((pid_t)gpid);
		if (!gone) {
			struct timespec ts = {0, 10000000L};
			nanosleep(&ts, NULL);
		}
	}
	say("late-group-killed", gone, slurp(at("g")));

	/* a signal sent to the child */
	int64_t pid = zrt_proc_start(sh("sleep 32"), "", "");
	say("kill", zrt_proc_kill(pid, SIGTERM) && zrt_proc_wait_for(pid, 5000) == 128 + SIGTERM, "");

	/* an interrupt to THIS process while it waits stops the child, and is not fatal here */
	rc = run("kill -INT $PPID; sleep 33", "", "", 10000);
	say("stopped", rc == ZRT_PROC_STOPPED, "");
	struct sigaction old;
	sigaction(SIGINT, NULL, &old);
	say("handler-restored", old.sa_handler == SIG_DFL, "");

	/* an empty directory is removed, a full one is not */
	say("remove-dir-full", !zrt_remove_dir(d1), "");
	unlink(at("j"));
	unlink(at("o"));
	unlink(at("e"));
	unlink(at("d"));
	unlink(at("i"));
	unlink(at("g"));
	say("remove-dir", zrt_remove_dir(d1) && zrt_remove_dir(d2) && stat(d1, &st) != 0, "");
	return 0;
}
`
