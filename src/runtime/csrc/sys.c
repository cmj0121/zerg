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

#include <fcntl.h>
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

/* zrt_read_file reads the whole file at `path` into a list[byte] — the MVP source-input
 * leaf the stdlib `io` module lowers onto until the FFI binder lands (like the write
 * intrinsics above). A missing or unreadable file raises IOError, which `guard` can
 * demote to a Result. */
zrt_list zrt_read_file(const char *path) {
	zrt_list l;
	zrt_list_init(&l, sizeof(uint8_t), NULL);
	int fd = open(path, O_RDONLY);
	if (fd < 0) {
		zrt_abort_kind(ZRT_ERR_IO, "IOError: cannot open file");
	}
	uint8_t buf[4096];
	for (;;) {
		ssize_t n = read(fd, buf, sizeof(buf));
		if (n < 0) {
			close(fd);
			zrt_abort_kind(ZRT_ERR_IO, "IOError: read failed");
		}
		if (n == 0) {
			break;
		}
		for (ssize_t i = 0; i < n; i++) {
			zrt_list_push(&l, &buf[i]);
		}
	}
	close(fd);
	return l;
}
