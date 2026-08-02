/*
 * sys.c - the Zerg runtime's minimal system surface.
 *
 * Abort-message output plus the Phase 1f byte-level stream primitives the stdlib
 * `io` module lowers onto. The `print` keyword still emits inline libc printf and
 * does not route through here (so a non-io program is byte-identical). Keeping this
 * isolated marks the one spot a freestanding backend swaps libc read/write for a
 * platform console.
 */

/* Ask for the POSIX.1-2008 surface BEFORE any header: under a strict `-std=c11` glibc
 * hides POSIX declarations (clock_gettime, the CLOCK_* macros, open/read/…) unless a
 * feature-test macro is set. macOS exposes them regardless; this keeps Linux in step. */
#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif

#include "zergrt.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

void zrt_report(const char *msg) {
	if (msg != NULL) {
		fputs(msg, stderr);
		fputc('\n', stderr);
	}
}

long zrt_write(int fd, const uint8_t *buf, size_t n) {
	size_t off = 0;
	while (off < n) {
		ssize_t w = write(fd, buf + off, n - off);
		if (w < 0) {
			return -1;
		}
		off += (size_t)w;
	}
	return (long)off;
}

long zrt_read(int fd, uint8_t *buf, size_t n) {
	ssize_t r = read(fd, buf, n);
	return (long)r;
}

long zrt_write_str(int fd, const char *s) {
	if (s == NULL) {
		return 0;
	}
	return zrt_write(fd, (const uint8_t *)s, strlen(s));
}

long zrt_write_int(int fd, int64_t v) {
	char buf[32];
	int len = snprintf(buf, sizeof(buf), "%lld", (long long)v);
	if (len < 0) {
		return -1;
	}
	return zrt_write(fd, (const uint8_t *)buf, (size_t)len);
}

/*
 * Atomic[int] cell operations (Phase 1f U2). The stdlib `atomic` module lowers a
 * shared Atomic[int] onto a Ref[int] box and calls these on its payload pointer,
 * so every copy of the box (including one sent across `spawn`) names one cell.
 * Sequential-consistency ordering keeps the API correct beyond the current N:1
 * cooperative scheduler. The __atomic_* builtins operate on a plain int64_t*, so
 * the payload needs no _Atomic storage qualifier.
 */
int64_t zrt_atomic_load(int64_t *p) {
	return __atomic_load_n(p, __ATOMIC_SEQ_CST);
}

int64_t zrt_atomic_store(int64_t *p, int64_t v) {
	__atomic_store_n(p, v, __ATOMIC_SEQ_CST);
	return v;
}

int64_t zrt_atomic_swap(int64_t *p, int64_t v) {
	return __atomic_exchange_n(p, v, __ATOMIC_SEQ_CST);
}

int64_t zrt_atomic_add(int64_t *p, int64_t n) {
	return __atomic_fetch_add(p, n, __ATOMIC_SEQ_CST);
}

bool zrt_atomic_cas(int64_t *p, int64_t expect, int64_t desired) {
	return __atomic_compare_exchange_n(p, &expect, desired, false, __ATOMIC_SEQ_CST,
	                                   __ATOMIC_SEQ_CST);
}

/* zrt_os_args builds a `list[str]` of the command-line arguments — the value a
 * `fn main(args: list[str])` receives. The program name (argv[0]) is skipped: `args`
 * is the program's own interface, so `myprog build a.zg` yields ["build", "a.zg"]
 * (docs/runtime/package.md). Each element is a `const char*` copied BY VALUE (a str is a
 * pointer); the argv strings the C runtime owns outlive the program, so nothing is
 * duplicated. The one heap allocation is the list's own buffer, which main frees as
 * its by-value parameter at scope exit. */
zrt_list zrt_os_args(int argc, char **argv) {
	zrt_list l;
	/* Each argv string is copied into an rc=1 string CELL (S2), and the list carries the
	 * str element vtable (retain on copy, release on drop). This gives a str-managed program
	 * a real cell to retain/release — a raw argv pointer would crash header recovery — while
	 * keeping the list alloc/free BALANCED: the list's own drop releases each cell. The one
	 * shape serves managed and unmanaged programs alike. */
	zrt_list_init(&l, sizeof(const char *), &zrt_str_elem_vt);
	for (int i = 1; i < argc; i++) {
		size_t      n = strlen(argv[i]);
		char       *p = (char *)zrt_ref_payload(zrt_ref_alloc(n + 1, NULL));
		memcpy(p, argv[i], n + 1);
		const char *s = p; /* rc=1, owned by the list */
		zrt_list_push(&l, &s);
	}
	return l;
}

/*
 * Whole-file read floor (Phase 1, pure-Zerg io). These are the irreducible syscall
 * leaves the stdlib `io.read_file` lowers onto — thin 1:1 wrappers, no loop of their
 * own: the open/read-chunk/close ORCHESTRATION lives in pure Zerg (src/stdlib/io.zg),
 * per the zero-external-dependency principle (the runtime is the self syscall layer,
 * like Go's). An fd crosses to Zerg as a plain int.
 */

/* zrt_open opens path read-only and returns its fd, aborting IOError when the file
 * cannot be opened (a value-failure that `guard { io.read_file(p) }` demotes). */
int64_t zrt_open(const char *path) {
	int fd = open(path, O_RDONLY);
	if (fd < 0) {
		zrt_abort_kind(ZRT_ERR_IO, "IOError: cannot open file");
	}
	return (int64_t)fd;
}

/* zrt_read_fd reads up to 4096 bytes from fd into a fresh list[byte] chunk — one
 * iteration of io.read_file's pure-Zerg loop. An empty chunk marks end of input; a
 * read error aborts IOError. */
zrt_list zrt_read_fd(int64_t fd) {
	zrt_list l;
	zrt_list_init(&l, sizeof(uint8_t), NULL);
	uint8_t buf[4096];
	ssize_t n = read((int)fd, buf, sizeof(buf));
	if (n < 0) {
		zrt_abort_kind(ZRT_ERR_IO, "IOError: read failed");
	}
	for (ssize_t i = 0; i < n; i++) {
		zrt_list_push(&l, &buf[i]);
	}
	return l;
}

/* zrt_close closes fd, returning close(2)'s status; io.read_file calls it once the
 * loop hits end of input. */
int64_t zrt_close(int64_t fd) {
	return (int64_t)close((int)fd);
}

/*
 * Clock leaves the stdlib `time` module lowers onto. Thin wrappers over clock_gettime;
 * all higher-level logic (durations, formatting) is pure Zerg above them.
 */

/* zrt_time_unix returns the wall-clock time in whole seconds since the Unix epoch. `ts`
 * is zero-initialised so a (near-impossible) clock_gettime failure yields 0, not garbage. */
int64_t zrt_time_unix(void) {
	struct timespec ts = {0};
	clock_gettime(CLOCK_REALTIME, &ts);
	return (int64_t)ts.tv_sec;
}

/* zrt_time_mono returns a monotonic clock reading in nanoseconds — meaningful only as a
 * difference between two readings (measuring elapsed time), not as an absolute date. */
int64_t zrt_time_mono(void) {
	struct timespec ts = {0};
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (int64_t)ts.tv_sec * 1000000000 + (int64_t)ts.tv_nsec;
}

/*
 * Process & platform leaves the stdlib `os` module lowers onto. A str returned to Zerg
 * must be a MANAGED cell (a str-managed program releases it, so a static/foreign pointer
 * would crash header recovery) — sys_str_cell copies a C string into a fresh rc=1 cell,
 * the same shape zrt_os_args builds.
 */

/* sys_str_cell copies the NUL-terminated s (NULL treated as "") into a fresh str cell. */
static const char *sys_str_cell(const char *s) {
	if (s == NULL) {
		s = "";
	}
	size_t n = strlen(s);
	char  *p = (char *)zrt_ref_payload(zrt_ref_alloc(n + 1, NULL));
	memcpy(p, s, n + 1);
	return p;
}

/* zrt_platform / zrt_arch return the TARGET OS / architecture as a str. They resolve at
 * C-compile time (#ifdef), so the value is exactly the platform `cc` built for. */
const char *zrt_platform(void) {
#if defined(__APPLE__)
	return sys_str_cell("darwin");
#elif defined(__linux__)
	return sys_str_cell("linux");
#elif defined(_WIN32)
	return sys_str_cell("windows");
#elif defined(__FreeBSD__)
	return sys_str_cell("freebsd");
#else
	return sys_str_cell("unknown");
#endif
}

const char *zrt_arch(void) {
#if defined(__aarch64__) || defined(__arm64__)
	return sys_str_cell("arm64");
#elif defined(__x86_64__) || defined(_M_X64)
	return sys_str_cell("x86_64");
#elif defined(__i386__)
	return sys_str_cell("x86");
#elif defined(__riscv) && __riscv_xlen == 64
	return sys_str_cell("riscv64");
#else
	return sys_str_cell("unknown");
#endif
}

/* zrt_getenv returns a COPY of environment variable key's value (an empty str when the
 * variable is unset — pair with zrt_has_env to tell the two apart). */
const char *zrt_getenv(const char *key) {
	return sys_str_cell(getenv(key));
}

/* zrt_has_env reports whether environment variable key is set. */
bool zrt_has_env(const char *key) {
	return getenv(key) != NULL;
}

/* zrt_exit terminates the process with the given status code; it does not return. */
void zrt_exit(int64_t code) {
	exit((int)code);
}

/*
 * Filesystem-write leaves the stdlib `io.write_file` and `fs` modules lower onto. Thin
 * wrappers; the open/write/close orchestration of io.write_file is pure Zerg.
 */

/* zrt_open_write opens path for writing, creating or truncating it (mode 0644), and
 * returns its fd — aborting IOError when it cannot be opened. */
int64_t zrt_open_write(const char *path) {
	int fd = open(path, O_WRONLY | O_CREAT | O_TRUNC, 0644);
	if (fd < 0) {
		zrt_abort_kind(ZRT_ERR_IO, "IOError: cannot open file for writing");
	}
	return (int64_t)fd;
}

/* zrt_write_bytes writes all of a list[byte] to fd, aborting IOError on failure. It only
 * READS the list (the caller keeps ownership and drops it as usual). */
void zrt_write_bytes(int64_t fd, zrt_list bytes) {
	if (bytes.len > 0 && zrt_write((int)fd, bytes.data, bytes.len) < 0) {
		zrt_abort_kind(ZRT_ERR_IO, "IOError: write failed");
	}
}

/* zrt_exists reports whether a file or directory exists at path. */
bool zrt_exists(const char *path) {
	return access(path, F_OK) == 0;
}

/* zrt_listdir returns the entry NAMES directly under path (no "." or "..", no recursion,
 * not path-prefixed) as a list[str], or an empty list when path is not a readable
 * directory — a missing directory is an answer here, not an abort, since the caller is
 * probing a search path. The names are sorted, because a compiler that reads a directory
 * must not let the filesystem's order decide its output: the emitted C has to be
 * reproducible for the self-host fixpoint to mean anything. */
zrt_list zrt_listdir(const char *path) {
	zrt_list l;
	zrt_list_init(&l, sizeof(const char *), &zrt_str_elem_vt);
	DIR *d = opendir(path);
	if (d == NULL) {
		return l;
	}
	struct dirent *e;
	while ((e = readdir(d)) != NULL) {
		if (strcmp(e->d_name, ".") == 0 || strcmp(e->d_name, "..") == 0) {
			continue;
		}
		size_t      n = strlen(e->d_name);
		char       *p = (char *)zrt_ref_payload(zrt_ref_alloc(n + 1, NULL));
		memcpy(p, e->d_name, n + 1);
		const char *s = p; /* rc=1, owned by the list */
		/* insertion sort into the list: a directory read is small (a module's files), and
		 * this keeps the ordering guarantee in one place instead of a second pass. */
		size_t i = zrt_list_len(&l);
		zrt_list_push(&l, &s);
		while (i > 0) {
			const char **prev = (const char **)zrt_list_at(&l, i - 1);
			const char **cur  = (const char **)zrt_list_at(&l, i);
			if (strcmp(*prev, *cur) <= 0) {
				break;
			}
			const char *tmp = *prev;
			*prev           = *cur;
			*cur            = tmp;
			i--;
		}
	}
	closedir(d);
	return l;
}

/* zrt_mkdir creates a directory, including any missing parents, and says nothing when it
 * already exists — the `mkdir -p` shape, because every caller wants "make sure this path
 * is there" rather than "create exactly this one". It returns true when the directory
 * exists afterwards, so a caller that cannot write its cache can carry on without one
 * instead of dying over a convenience. */
bool zrt_mkdir(const char *path) {
	char   buf[1024];
	size_t n = strlen(path);
	if (n == 0 || n >= sizeof(buf)) {
		return false;
	}
	memcpy(buf, path, n + 1);
	for (size_t i = 1; i <= n; i++) {
		if (buf[i] != '/' && buf[i] != '\0') {
			continue;
		}
		char saved = buf[i];
		buf[i]     = '\0';
		if (mkdir(buf, 0755) != 0 && errno != EEXIST) {
			return false;
		}
		buf[i] = saved;
	}
	return true;
}

/* zrt_remove deletes the file at path, aborting IOError on failure — e.g. it is missing,
 * or it is a directory (unlink removes files only, never a directory). */
void zrt_remove(const char *path) {
	if (unlink(path) != 0) {
		zrt_abort_kind(ZRT_ERR_IO, "IOError: cannot remove file");
	}
}

/* zrt_exec runs argv[0] with arguments argv (PATH-searched), waits for the child, and
 * returns its exit status — 128+signal if it died on a signal, 127 if exec failed, -1 if
 * it could not fork. The process-spawn floor the stdlib `os.run` and command literals
 * lower onto, over the POSIX fork / exec / wait syscalls only (zero third-party
 * dependency). argv holds `const char *` elements; the child image is a shallow view of
 * them, valid until exec replaces the address space. */
int64_t zrt_exec(zrt_list argv) {
	int64_t pid = zrt_proc_spawn(argv);
	if (pid < 0) {
		return -1;
	}
	return zrt_proc_wait(pid);
}

/* zrt_proc_spawn starts argv[0] with arguments argv (PATH-searched) and returns immediately
 * with the child's pid, or -1. It is the half of exec that lets a caller have SEVERAL
 * children running at once — a build compiling its units in parallel is the reason it
 * exists — and it pairs with zrt_wait, which collects one. */
int64_t zrt_proc_spawn(zrt_list argv) {
	size_t n  = zrt_list_len(&argv);
	char **av = (char **)malloc((n + 1) * sizeof(char *));
	if (av == NULL) {
		return -1;
	}
	for (size_t i = 0; i < n; i++) {
		av[i] = *(char **)zrt_list_at_ref(&argv, i);
	}
	av[n] = NULL;
	pid_t pid = fork();
	if (pid < 0) {
		free(av);
		return -1;
	}
	if (pid == 0) {
		execvp(av[0], av);
		_exit(127); /* exec failed */
	}
	free(av);
	return (int64_t)pid;
}

/* zrt_proc_wait blocks until the given child exits and returns its status the way a shell
 * reports one: the exit code, or 128+signal when it was killed. */
int64_t zrt_proc_wait(int64_t pid) {
	int status = 0;
	if (waitpid((pid_t)pid, &status, 0) < 0) {
		return -1;
	}
	if (WIFEXITED(status)) {
		return (int64_t)WEXITSTATUS(status);
	}
	if (WIFSIGNALED(status)) {
		return (int64_t)(128 + WTERMSIG(status));
	}
	return -1;
}

#ifdef ZRT_TRACE
/* zrt_trace_on reads ZRG_TRACE once and remembers it. Once, because the answer cannot
 * change during a run and because getenv is not something to call from inside the
 * interleaving under study. Not atomic: two threads racing here both compute the same
 * value from the same environment. */
bool zrt_trace_on(void) {
	static int on = -1;
	if (on < 0) {
		const char *v = getenv("ZRG_TRACE");
		on = (v != NULL && v[0] != '\0' && v[0] != '0') ? 1 : 0;
	}
	return on == 1;
}
#endif

#ifdef ZRT_TRACE
/* --- the live-waiter registry (ZRT_TRACE only) --------------------------------
 *
 * A `zrt_waiter` lives on the stack of the coroutine that parked, so a queue still
 * pointing at one whose coroutine has been freed is a hand-off into unmapped memory. That
 * is a SEGV in wq_pop, and it happens a long way from the mistake and one run in several
 * hundred — the shape that costs days to find by waiting for it.
 *
 * So it is turned into an INVARIANT that every run can check: every waiter that is on a
 * queue is registered here, and freeing a coroutine's stack asserts that none of them
 * lies inside it. A run that would have crashed later fails HERE instead, on the run that
 * made the mistake rather than the run that tripped over it — and a run that never
 * crashes still proves the property.
 *
 * Under -DZRT_TRACE only: it takes a lock the shipped runtime does not have, and it holds
 * a global the shipped runtime does not have either. */
#define ZRT_TRACE_DEAD_STACKS 64
static void  *g_ds_lo[ZRT_TRACE_DEAD_STACKS];
static size_t g_ds_len[ZRT_TRACE_DEAD_STACKS];
static void  *g_ds_co[ZRT_TRACE_DEAD_STACKS];
static int    g_ds_at;

#define ZRT_TRACE_MAX_WAITERS 512
static void        *g_tw[ZRT_TRACE_MAX_WAITERS];
static void        *g_tw_co[ZRT_TRACE_MAX_WAITERS];
static zrt_mutex    g_tw_lock;
static bool         g_tw_init;

/* zrt_trace_init is called ONCE from sched_init, before any worker exists.
 *
 * It used to be a lazy `if (!inited) init()` at every entry point, which is a data race on
 * the guard: several workers reach it at once, more than one calls zrt_mutex_init, and the
 * rest take a lock that is being initialised under them. The registry then answered
 * nonsense and the range check aborted every MULTI-WORKER run of every case — the seeded
 * single-worker half stayed green, which is exactly the shape that says "the bug is in the
 * thing doing the measuring". */
void zrt_trace_init(void) {
	if (!g_tw_init) {
		zrt_mutex_init(&g_tw_lock);
		g_tw_init = true;
	}
}

static void tw_ensure(void) {
}

/* A waiter's ADDRESS repeats: a coroutine stack is unmapped and the next one is mapped
 * where it was, so the same `zrt_waiter *` names a different waiter minutes apart. The
 * registry therefore holds one ENTRY per push, never a set keyed on the address — and
 * removing one entry may not remove the others. Getting that wrong is what made the first
 * version of this silent: `off` cleared every slot matching the address, so a live waiter
 * at a recycled address vanished from the invariant along with the dead one it shared a
 * number with. */
void zrt_trace_waiter_on(void *w, void *co) {
	tw_ensure();
	zrt_mutex_lock(&g_tw_lock);
	for (int i = 0; i < ZRT_TRACE_MAX_WAITERS; i++) {
		if (g_tw[i] == NULL) {
			g_tw[i] = w;
			g_tw_co[i] = co;
			zrt_mutex_unlock(&g_tw_lock);
			return;
		}
	}
	zrt_mutex_unlock(&g_tw_lock);
	/* dropping one would make the invariant quietly weaker, which is worse than failing */
	fprintf(stderr, "[zrt] TRACE REGISTRY FULL at %d live waiters — raise ZRT_TRACE_MAX_WAITERS\n",
	        ZRT_TRACE_MAX_WAITERS);
	fflush(stderr);
	abort();
}

void zrt_trace_waiter_off(void *w) {
	tw_ensure();
	zrt_mutex_lock(&g_tw_lock);
	for (int i = 0; i < ZRT_TRACE_MAX_WAITERS; i++) {
		if (g_tw[i] == w) {
			g_tw[i] = NULL;
			g_tw_co[i] = NULL;
			break; /* ONE entry, not every slot sharing this address */
		}
	}
	zrt_mutex_unlock(&g_tw_lock);
}

/* zrt_trace_waiter_live reports whether this pointer is on some queue. A queue head that
 * is not is a STALE head — the pointer was popped or removed already, and dereferencing it
 * is the SEGV. Checking here names it one instruction before it happens. */
bool zrt_trace_waiter_live(void *w) {
	tw_ensure();
	zrt_mutex_lock(&g_tw_lock);
	bool live = false;
	for (int i = 0; i < ZRT_TRACE_MAX_WAITERS; i++) {
		if (g_tw[i] == w) {
			live = true;
			break;
		}
	}
	zrt_mutex_unlock(&g_tw_lock);
	return live;
}

/* --- live coroutine stacks (ZRT_TRACE only) -----------------------------------
 *
 * The waiter registry cannot answer "is this pointer still alive?", and it never could: a
 * `zrt_waiter *` is a STACK address, coroutine stacks are unmapped and remapped where they
 * were, so a dead pointer aliases a live registration and every address-keyed test says
 * yes. That is why two rounds of this instrument stayed silent while CI kept crashing.
 *
 * A stack RANGE does not alias: it is live exactly between its mmap and its munmap. So the
 * question a queue can actually answer is "does this waiter lie inside a stack that is
 * still mapped?" — and the answer is no precisely when dereferencing it would fault. */
#define ZRT_TRACE_MAX_STACKS 128
static void      *g_ts_lo[ZRT_TRACE_MAX_STACKS];
static size_t     g_ts_len[ZRT_TRACE_MAX_STACKS];

void zrt_trace_stack_on(void *lo, size_t len) {
	tw_ensure();
	zrt_mutex_lock(&g_tw_lock);
	/* A RELEASED range is only dead until its addresses are mapped again, and mmap hands
	 * them back constantly — so a dead entry that outlives its addresses turns the next
	 * coroutine's perfectly good waiter into a finding. Pruned by OVERLAP, not by equal
	 * base: a fresh mapping need not start where the old one did, and matching the base
	 * alone left overlapping remaps in the dead list — which fired on a live select waiter
	 * and read, for half a day, as the leak this instrument exists to find. */
	for (int d = 0; d < ZRT_TRACE_DEAD_STACKS; d++) {
		char *dlo = (char *)g_ds_lo[d];
		if (dlo != NULL && dlo < (char *)lo + len && (char *)lo < dlo + g_ds_len[d]) {
			g_ds_lo[d] = NULL;
			g_ds_len[d] = 0;
			g_ds_co[d] = NULL;
		}
	}
	for (int i = 0; i < ZRT_TRACE_MAX_STACKS; i++) {
		if (g_ts_lo[i] == NULL) {
			g_ts_lo[i] = lo;
			g_ts_len[i] = len;
			zrt_mutex_unlock(&g_tw_lock);
			return;
		}
	}
	zrt_mutex_unlock(&g_tw_lock);
	/* the last place in this instrument that could still lose a fact quietly: a dropped
	 * range makes every waiter on that stack look dead, which is exactly the false positive
	 * this whole hunt has been trying to tell apart from a finding */
	fprintf(stderr, "[zrt] TRACE STACK TABLE FULL at %d — raise ZRT_TRACE_MAX_STACKS\n",
	        ZRT_TRACE_MAX_STACKS);
	fflush(stderr);
	abort();
}

static void ts_off(void *lo) {
	for (int i = 0; i < ZRT_TRACE_MAX_STACKS; i++) {
		if (g_ts_lo[i] == lo) {
			g_ts_lo[i] = NULL;
			g_ts_len[i] = 0;
			return;
		}
	}
}

/* --- a queue's recent history (ZRT_TRACE only) --------------------------------
 *
 * The head holds a pointer that is in no coroutine stack: not a stale waiter, a WILD value.
 * A pointer like that was written by somebody, and the only writers are the four queue
 * operations — so the question is which of them ran last on this queue, and with what.
 *
 * A fixed ring, keyed on nothing: it records every operation on every queue and the report
 * filters by the queue that went bad. Cheap enough to leave on for a whole run, and it does
 * not have to be exact under a race — the last few entries for one queue are the story. */
#define ZRT_TRACE_HIST 4096
static void       *g_h_q[ZRT_TRACE_HIST];
static void       *g_h_w[ZRT_TRACE_HIST];
static const char *g_h_op[ZRT_TRACE_HIST];
static int         g_h_at;

static void hist_dump(void *q);
static void hist_dump_w(void *w);

/* --- recently FREED stacks (ZRT_TRACE only) -----------------------------------
 *
 * The live-range test could not answer the question; this one can. A stack that has just
 * been unmapped is remembered with the coroutine it belonged to, so a queue head landing
 * inside one is a complete diagnosis: this waiter belongs to that coroutine, whose stack
 * was released at that point. It does not depend on the live set being complete, which is
 * where the earlier attempt went wrong. */
void zrt_trace_stack_dead(void *lo, size_t len, void *co) {
	zrt_mutex_lock(&g_tw_lock);
	int i = g_ds_at % ZRT_TRACE_DEAD_STACKS;
	g_ds_lo[i] = lo;
	g_ds_len[i] = len;
	g_ds_co[i] = co;
	g_ds_at++;
	zrt_mutex_unlock(&g_tw_lock);
}

/* --- freed coroutines (ZRT_TRACE only) ----------------------------------------
 *
 * Keyed on the COROUTINE, not on its stack, and that is the whole point. A stack address is
 * handed straight back by mmap, so "is this waiter in a released stack?" has to forget a
 * range the moment it is reused — and forgets the true positive along with the false ones.
 * A `zrt_coro *` is a heap object that ASan's quarantine does not hand back nearly as fast,
 * and the registry already records which coroutine pushed each waiter, so the question can
 * be asked WITHOUT dereferencing the waiter — which is the very read that faults.
 *
 * That matters twice over: `chan_close` wakes `w->co` for every waiter it pops, so a waiter
 * outliving its coroutine is also a `zrt_sched_wake` on a freed coroutine, and the
 * scheduler then swaps into a stack that is not there. The SEGVs in `zrt_handler_pop` and
 * in generated user code are that second face of it. */
#define ZRT_TRACE_DEAD_CORO 128
static void *g_dc[ZRT_TRACE_DEAD_CORO];
static int   g_dc_at;

void zrt_trace_coro_dead(void *co) {
	zrt_mutex_lock(&g_tw_lock);
	g_dc[g_dc_at % ZRT_TRACE_DEAD_CORO] = co;
	g_dc_at++;
	zrt_mutex_unlock(&g_tw_lock);
}

/* zrt_trace_check_head aborts when a queue head is not a waiter any queue is holding.
 *
 * Address aliasing makes this test miss things — a recycled address answers "registered"
 * for the wrong waiter — but it cannot make it fire wrongly: a head that matches NOTHING
 * was never pushed, or was already popped, and either way `w->next` is not a read to make.
 * Everything else has passed while the read still faults, which leaves this.
 */
void zrt_trace_check_head(void *q, void *w) {
	if (w == NULL) {
		return;
	}
	zrt_mutex_lock(&g_tw_lock);
	for (int i = 0; i < ZRT_TRACE_MAX_WAITERS; i++) {
		if (g_tw[i] == w) {
			zrt_mutex_unlock(&g_tw_lock);
			return;
		}
	}
	fprintf(stderr, "[zrt] STALE QUEUE HEAD q=%p w=%p — no queue is holding this waiter\n", q, w);
	fprintf(stderr, "[zrt]   operations on this queue, newest first:\n");
	hist_dump(q);
	fprintf(stderr, "[zrt]   operations on this waiter, newest first:\n");
	hist_dump_w(w);
	zrt_mutex_unlock(&g_tw_lock);
	fflush(stderr);
	abort();
}

/* zrt_trace_check_coro aborts when this coroutine has been freed. It is the one check
 * upstream of every fault signature seen: a waiter left on a queue is woken by chan_close
 * through `zrt_sched_wake(w->co)`, that reads `co->state` out of freed memory, and if the
 * bytes happen to read as BLOCKED the coroutine is put back on the run queue — after which
 * a worker swaps into a context whose stack is unmapped. Whatever it touches next faults:
 * its own `zrt_frame` in `zrt_handler_pop`, a local in generated code, or the next waiter
 * in `wq_pop`. One cause, three places to notice it. */
void zrt_trace_check_coro(void *co, const char *where) {
	if (co == NULL) {
		return;
	}
	zrt_mutex_lock(&g_tw_lock);
	for (int i = 0; i < ZRT_TRACE_DEAD_CORO; i++) {
		if (g_dc[i] != co) {
			continue;
		}
		fprintf(stderr, "[zrt] FREED COROUTINE TOUCHED at %s co=%p\n", where, co);
		zrt_mutex_unlock(&g_tw_lock);
		fflush(stderr);
		abort();
	}
	zrt_mutex_unlock(&g_tw_lock);
}

/* zrt_trace_check_owner aborts when the waiter at this address was pushed by a coroutine
 * that has since been freed. The waiter is never dereferenced: the owner comes from the
 * registry entry made at the push. */
void zrt_trace_check_owner(void *q, void *w) {
	if (w == NULL) {
		return;
	}
	zrt_mutex_lock(&g_tw_lock);
	void *co = NULL;
	for (int i = 0; i < ZRT_TRACE_MAX_WAITERS; i++) {
		if (g_tw[i] == w) {
			co = g_tw_co[i];
			break;
		}
	}
	if (co != NULL) {
		for (int i = 0; i < ZRT_TRACE_DEAD_CORO; i++) {
			if (g_dc[i] != co) {
				continue;
			}
			fprintf(stderr, "[zrt] WAITER OUTLIVED ITS COROUTINE q=%p w=%p co=%p — freed, and still queued\n",
			        q, w, co);
			fprintf(stderr, "[zrt]   operations on this waiter, newest first:\n");
			hist_dump_w(w);
			zrt_mutex_unlock(&g_tw_lock);
			fflush(stderr);
			abort();
		}
	}
	zrt_mutex_unlock(&g_tw_lock);
}

/* zrt_trace_check_dead aborts when w lies in a stack that was released. */
void zrt_trace_check_dead(void *q, void *w) {
	if (w == NULL) {
		return;
	}
	zrt_mutex_lock(&g_tw_lock);
	/* the LIVE table is authoritative: with ASan's fake stack off, a waiter is on some
	 * coroutine's real stack, so an address inside a live range is a live waiter whatever
	 * the dead list still remembers about those addresses */
	for (int i = 0; i < ZRT_TRACE_MAX_STACKS; i++) {
		char *lo = (char *)g_ts_lo[i];
		if (lo != NULL && (char *)w >= lo && (char *)w < lo + g_ts_len[i]) {
			zrt_mutex_unlock(&g_tw_lock);
			return;
		}
	}
	for (int i = 0; i < ZRT_TRACE_DEAD_STACKS; i++) {
		char *lo = (char *)g_ds_lo[i];
		if (lo == NULL || (char *)w < lo || (char *)w >= lo + g_ds_len[i]) {
			continue;
		}
		fprintf(stderr, "[zrt] WAITER ON A RELEASED STACK q=%p w=%p — co %p, stack [%p,%p)\n",
		        q, w, g_ds_co[i], lo, (void *)(lo + g_ds_len[i]));
		fprintf(stderr, "[zrt]   operations on this queue, newest first:\n");
		hist_dump(q);
		fprintf(stderr, "[zrt]   operations on THIS WAITER, newest first:\n");
		hist_dump_w(w);
		zrt_mutex_unlock(&g_tw_lock);
		fflush(stderr);
		abort();
	}
	zrt_mutex_unlock(&g_tw_lock);
}

void zrt_trace_qop(void *q, void *w, const char *op) {
	zrt_mutex_lock(&g_tw_lock);
	int i = g_h_at % ZRT_TRACE_HIST;
	g_h_q[i] = q;
	g_h_w[i] = w;
	g_h_op[i] = op;
	g_h_at++;
	zrt_mutex_unlock(&g_tw_lock);
}

/* hist_dump_w is hist_dump keyed on the WAITER instead of the queue: it shows every queue
 * this waiter was put on and taken off, which is what says who leaked it. */
static void hist_dump_w(void *w) {
	int n = g_h_at < ZRT_TRACE_HIST ? g_h_at : ZRT_TRACE_HIST;
	int shown = 0;
	for (int k = 0; k < n && shown < 16; k++) {
		int i = (g_h_at - 1 - k) % ZRT_TRACE_HIST;
		if (i < 0) {
			i += ZRT_TRACE_HIST;
		}
		if (g_h_w[i] == w) {
			fprintf(stderr, "[zrt]   ... %-9s q=%p\n", g_h_op[i], g_h_q[i]);
			shown++;
		}
	}
	if (shown == 0) {
		fprintf(stderr, "[zrt]   ... nothing recorded for this waiter\n");
	}
}

/* hist_dump prints the last operations recorded for one queue, oldest first. */
static void hist_dump(void *q) {
	int n = g_h_at < ZRT_TRACE_HIST ? g_h_at : ZRT_TRACE_HIST;
	int shown = 0;
	for (int k = n - 1; k >= 0 && shown < 12; k--) {
		int i = (g_h_at - 1 - (n - 1 - k)) % ZRT_TRACE_HIST;
		if (i < 0) {
			i += ZRT_TRACE_HIST;
		}
		if (g_h_q[i] == q) {
			fprintf(stderr, "[zrt]   ... %-9s w=%p\n", g_h_op[i], g_h_w[i]);
			shown++;
		}
	}
	if (shown == 0) {
		fprintf(stderr, "[zrt]   ... NO operation was ever recorded on this queue\n");
	}
}

void zrt_trace_stale(void *q, void *w) {
	fprintf(stderr, "[zrt] STALE QUEUE HEAD q=%p w=%p — the head names a waiter no queue holds\n", q, w);
	fflush(stderr);
	abort();
}

/* zrt_trace_stack_free is the assertion: nothing still queued may live in this stack. */
void zrt_trace_stack_free(void *lo, size_t len) {
	tw_ensure();
	zrt_mutex_lock(&g_tw_lock);
	ts_off(lo);
	for (int i = 0; i < ZRT_TRACE_MAX_WAITERS; i++) {
		char *w = (char *)g_tw[i];
		if (w != NULL && w >= (char *)lo && w < (char *)lo + len) {
			fprintf(stderr, "[zrt] LEAKED WAITER %p (co %p) is still on a queue, and its stack [%p,%p) is being freed\n",
			        (void *)w, g_tw_co[i], lo, (void *)((char *)lo + len));
			fflush(stderr);
			abort();
		}
	}
	zrt_mutex_unlock(&g_tw_lock);
}
#endif
