/*
 * lsp-heap.c — the bytes malloc still has outstanding, sampled inside one process.
 *
 * scripts/lsp-check.sh loads this into the `zerg lsp` it is measuring and asks, between
 * checks, over the socket named by ZERG_HEAP_SOCK. The answer is the allocator's own
 * count of bytes handed out and not returned — macOS `malloc_zone_statistics`, glibc
 * `mallinfo2` — which a machine under load does not move. The kernel's footprint does,
 * and that is the reading this replaced.
 *
 * It does not interpose malloc. A counter on every allocation would tax the check in
 * order to watch it, and the allocator already keeps this total.
 */
#if defined(__APPLE__)
#define _DARWIN_C_SOURCE
#include <malloc/malloc.h>
#elif defined(__linux__)
#define _GNU_SOURCE
#include <malloc.h>
#else
#error "lsp-heap.c samples malloc on macOS and Linux"
#endif

#include <errno.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

static unsigned long long live_bytes(void) {
#if defined(__APPLE__)
	malloc_statistics_t s;
	malloc_zone_statistics(malloc_default_zone(), &s);
	return (unsigned long long)s.size_in_use;
#else
	struct mallinfo2 info = mallinfo2();
	/* uordblks leaves out the chunks glibc maps on their own (hblkhd); a leak of large buffers is there. */
	return (unsigned long long)(info.uordblks + info.hblkhd);
#endif
}

/* One question, one line. The gate sends a byte and reads until the newline. */
static void *serve(void *arg) {
	int fd = *(int *)arg;
	free(arg);
	char req;
	for (;;) {
		if (read(fd, &req, 1) <= 0) {
			break;
		}
		char line[64];
		int len = snprintf(line, sizeof line, "%llu\n", live_bytes());
		if (len > 0) {
			(void)write(fd, line, (size_t)len);
		}
	}
	close(fd);
	return NULL;
}

static int connect_to(const char *path) {
	for (int attempt = 0; attempt < 50; attempt++) {
		int fd = socket(AF_UNIX, SOCK_STREAM, 0);
		if (fd < 0) {
			return -1;
		}
		struct sockaddr_un addr;
		memset(&addr, 0, sizeof addr);
		addr.sun_family = AF_UNIX;
		size_t n = strlen(path);
		if (n >= sizeof addr.sun_path) {
			fprintf(stderr, "lsp-heap: socket path is too long\n");
			close(fd);
			return -1;
		}
		memcpy(addr.sun_path, path, n + 1);
		if (connect(fd, (struct sockaddr *)&addr, sizeof addr) == 0) {
			return fd;
		}
		close(fd);
		usleep(20000);
	}
	fprintf(stderr, "lsp-heap: connect %s: %s\n", path, strerror(errno));
	return -1;
}

__attribute__((constructor)) static void lsp_heap_init(void) {
	const char *path = getenv("ZERG_HEAP_SOCK");
	if (path == NULL || path[0] == '\0') {
		return;
	}
	int fd = connect_to(path);
	if (fd < 0) {
		return;
	}
	int *held = malloc(sizeof(int));
	if (held == NULL) {
		close(fd);
		return;
	}
	*held = fd;
	pthread_t th;
	if (pthread_create(&th, NULL, serve, held) != 0) {
		free(held);
		close(fd);
		return;
	}
	pthread_detach(th);
}
