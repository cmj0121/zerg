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
