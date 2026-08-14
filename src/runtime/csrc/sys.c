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

/* pthread_getattr_np — the only way glibc names the calling thread's stack bounds,
 * which zrt_fault_init needs for main's — is a GNU extension a strict `-std=c11`
 * glibc hides unless _GNU_SOURCE is set. And on macOS, defining _POSIX_C_SOURCE
 * above NARROWS the default surface, hiding the Darwin `_np` pair that answers the
 * same question there; _DARWIN_C_SOURCE puts it back. Both must precede every
 * #include, feature-test macros being read at the first one. */
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif

#include "zergrt.h"

#if defined(__APPLE__)
#include <mach-o/dyld.h> /* _NSGetExecutablePath, for zrt_exe_path */
#endif

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <signal.h>
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

/* --- stack-overflow naming (zrt_fault_*) --------------------------------------
 *
 * The one fault the abort contract cannot route through zrt_abort: by the time a
 * stack overflow is observable, the faulting stack is exhausted — nothing on it can
 * run, so there is no unwind and the pending `defer`s are skipped, which is the half
 * of the docs/conformance.md deviation that stays. What is fixed here is the NAME
 * and the STATUS: the overflow now dies as `StackOverflowError: stack overflow` on
 * stderr with exit status 1, the shape every other abort has, instead of as a bare
 * signal the shell reports.
 *
 * Overflow-or-not is decided by the fault address, and the two windows are as NARROW
 * as the two stacks allow, because everything they over-claim is a genuine memory bug
 * wearing this error's name. A coroutine's stack begins with a real PROT_NONE guard
 * page, so its window is that page EXACTLY — an overflow's si_addr always lands inside
 * it, and no slack below it is needed or honest (measured on macOS/arm64 and
 * Linux/aarch64, with frames from 512B to 1MB: both compilers stack-probe, so not one
 * of them stepped over the page). Main's native stack has no mapping this process
 * made, only the OS guard region under the low bound pthread reports, so its window is
 * the ONE page below that bound — measured to be enough, where zero was not.
 *
 * A fault in neither window is passed on rather than reported: the handler puts back
 * whatever action was installed before this one — a sanitizer's, or the default
 * disposition — and returns, so the faulting instruction re-fires and the fault dies
 * as itself, with the diagnostic that handler gives. That chaining is the difference
 * between an ASan build printing `SEGV on unknown address 0x10` with a stack trace and
 * printing nothing at all; restoring SIG_DFL instead threw ASan's report away.
 *
 * Everything the handler does after deciding is async-signal-safe by construction:
 * write(2), sigaction(2) and _exit(2) only — all three on POSIX's list — no printf, no
 * malloc, no unwind. It runs on its own sigaltstack because the normal stack being
 * unusable is the very case it exists for; without SA_ONSTACK the handler could not
 * even be entered.
 *
 * The state the handler reads is `volatile`: C11 7.14.1.1p5 promises nothing about any
 * other object a handler touches. */

/* the guard PAGE of the coroutine stack THIS thread is running user code on right now;
 * 0 while it runs on its own native stack. Set by the scheduler around every switch. */
static ZRT_THREAD_LOCAL volatile uintptr_t t_guard_lo;
static ZRT_THREAD_LOCAL volatile uintptr_t t_guard_hi;

/* main's native stack low bound, recorded at entry; 0 = unknown (a platform with no
 * stack query — an overflow there dies by signal, as before). THREAD-LOCAL, so it is
 * consulted only on the thread it describes: a worker whose fault arrives while it is
 * between coroutines has no bounds to compare against, and comparing it against MAIN's
 * would be a cross-thread guess that could only widen the mislabel. */
static ZRT_THREAD_LOCAL volatile uintptr_t t_main_stack_lo;

/* the host page size, read once at init — sysconf is not async-signal-safe, so the
 * handler may not ask. */
static volatile uintptr_t g_page;

static bool fault_is_overflow(uintptr_t addr) {
	if (t_guard_lo != 0) {
		/* on a coroutine stack: the guard page is the whole tripwire, exactly */
		return addr >= t_guard_lo && addr < t_guard_hi;
	}
	if (t_main_stack_lo != 0) {
		/* main's native stack: an overflow faults in the page below the low bound */
		return addr >= t_main_stack_lo - g_page && addr < t_main_stack_lo;
	}
	return false;
}

/* the actions installed before this runtime's, so a fault this handler does not claim
 * goes back to whoever had the signal first. Written once, under g_fault_installed. */
static struct sigaction g_old_segv;
static struct sigaction g_old_bus;
static bool g_fault_installed;

static void fault_handler(int sig, siginfo_t *si, void *ctx) {
	(void)ctx;
	if (si != NULL && fault_is_overflow((uintptr_t)si->si_addr)) {
		static const char msg[] = "StackOverflowError: stack overflow\n";
		size_t off = 0;
		while (off < sizeof(msg) - 1) {
			ssize_t w = write(2, msg + off, sizeof(msg) - 1 - off);
			if (w <= 0) {
				break;
			}
			off += (size_t)w;
		}
		_exit(1);
	}
	/* not a stack overflow: hand the signal back to the action that was there before
	 * and return, so the faulting instruction re-fires into it. */
	{
		const struct sigaction *old = (sig == SIGBUS) ? &g_old_bus : &g_old_segv;
		(void)sigaction(sig, old, NULL);
	}
}

void zrt_fault_thread_init(void) {
#ifdef ZRT_ASAN
	/* ASan already gave every thread an alternate signal stack, and it UNMAPS
	 * whatever sigaltstack answers at thread exit, assuming its own mapping is
	 * still installed. Replacing it with this TLS array made that munmap fail and
	 * ASan abort the teardown — so under ASan, keep ASan's altstack: the handler
	 * above is SA_ONSTACK and runs on it just the same. */
#else
	/* the normal stack is exactly what is exhausted when this handler matters, so
	 * the handler needs its own ground. Static and thread-local: no allocation,
	 * one per thread, alive as long as the thread — nothing to free, ever. 32KB is
	 * the conventional SIGSTKSZ ceiling, spelled as a constant because glibc's
	 * SIGSTKSZ has been a sysconf call since 2.34 and cannot size an array. This
	 * handler's own needs are a few hundred bytes. */
	static ZRT_THREAD_LOCAL char altstack[32 * 1024];
	stack_t ss;
	ss.ss_sp = altstack;
	ss.ss_size = sizeof(altstack);
	ss.ss_flags = 0;
	(void)sigaltstack(&ss, NULL);
#endif
}

void zrt_fault_init(void) {
	long pg = sysconf(_SC_PAGESIZE);
	g_page = (pg > 0) ? (uintptr_t)pg : 4096;
	zrt_fault_thread_init();
#if defined(__APPLE__)
	{
		pthread_t self = pthread_self();
		uintptr_t hi = (uintptr_t)pthread_get_stackaddr_np(self);
		t_main_stack_lo = hi - (uintptr_t)pthread_get_stacksize_np(self);
	}
#elif defined(__linux__)
	{
		pthread_attr_t attr;
		if (pthread_getattr_np(pthread_self(), &attr) == 0) {
			void *lo = NULL;
			size_t size = 0;
			if (pthread_attr_getstack(&attr, &lo, &size) == 0) {
				t_main_stack_lo = (uintptr_t)lo;
			}
			(void)pthread_attr_destroy(&attr);
		}
	}
#endif
	/* ONCE, and the guard is not a nicety: there are seven callers (six entry.c
	 * shims and sched_init), and a second install would save THIS handler as the
	 * old one — after which an unclaimed fault hands the signal to itself and
	 * ping-pongs forever instead of dying. */
	if (!g_fault_installed) {
		struct sigaction sa;
		memset(&sa, 0, sizeof(sa));
		sa.sa_sigaction = fault_handler;
		sa.sa_flags = SA_SIGINFO | SA_ONSTACK;
		sigemptyset(&sa.sa_mask);
		g_fault_installed = true;
		(void)sigaction(SIGSEGV, &sa, &g_old_segv);
		(void)sigaction(SIGBUS, &sa, &g_old_bus);
	}
}

void zrt_fault_stack_set(void *base, size_t guard_len) {
	/* hi BEFORE lo: lo != 0 is what arms the coroutine window, so writing it last
	 * means the handler never reads a live lo against a stale hi. */
	t_guard_hi = (uintptr_t)base + guard_len;
	t_guard_lo = (uintptr_t)base;
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

/* zrt_exe_path answers the path of the RUNNING executable, or "" when the host will not
 * say. It is how a toolchain finds the files it was installed beside — its runtime C
 * sources and its standard library — without being told where it lives by an environment
 * variable or by the current directory, neither of which the user set.
 *
 * argv[0] is not enough and is deliberately not used: it is whatever the caller passed,
 * which is a bare name when the binary came off PATH. Each host has one call that answers
 * the real thing, and a host with none answers "" — the caller falls back rather than
 * guessing from a name.
 */
const char *zrt_exe_path(void) {
#if defined(__APPLE__)
	char     buf[4096];
	uint32_t n = (uint32_t)sizeof(buf);
	if (_NSGetExecutablePath(buf, &n) != 0) {
		return sys_str_cell("");
	}
	char real[4096];
	if (realpath(buf, real) == NULL) {
		return sys_str_cell(buf);
	}
	return sys_str_cell(real);
#elif defined(__linux__)
	char    buf[4096];
	ssize_t n = readlink("/proc/self/exe", buf, sizeof(buf) - 1);
	if (n <= 0) {
		return sys_str_cell("");
	}
	buf[n] = '\0';
	return sys_str_cell(buf);
#else
	return sys_str_cell("");
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
