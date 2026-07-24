/*
 * sys.c - the Zerg runtime's minimal system surface.
 *
 * Abort-message output plus the Phase 1f byte-level stream primitives the stdlib
 * `io` module lowers onto. The `print` keyword still emits inline libc printf and
 * does not route through here (so a non-io program is byte-identical). Keeping this
 * isolated marks the one spot a freestanding backend swaps libc read/write for a
 * platform console.
 */
#include "zergrt.h"

#include <stdio.h>
#include <string.h>
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
 * (docs/package.md). Each element is a `const char*` copied BY VALUE (a str is a
 * pointer); the argv strings the C runtime owns outlive the program, so nothing is
 * duplicated. The one heap allocation is the list's own buffer, which main frees as
 * its by-value parameter at scope exit. */
zrt_list zrt_os_args(int argc, char **argv) {
	zrt_list l;
	zrt_list_init(&l, sizeof(const char *), NULL);
	for (int i = 1; i < argc; i++) {
		const char *s = argv[i];
		zrt_list_push(&l, &s);
	}
	return l;
}
