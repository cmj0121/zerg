/*
 * thread_none.c - the floor of the zrt_thread shim: a host with no threads at all.
 *
 * This is not a stub, it is the SEMANTICS. A freestanding target, a wasm build, or
 * any host whose threading the project does not speak still runs every Zerg program
 * — the scheduler simply runs all coroutines on the calling thread, which is exactly
 * what it did before M:N existed. A program's OUTPUT never depends on which backend
 * it was built with; only whether two coroutines can occupy two CPUs at once.
 *
 * Because there is only ever one thread, the mutex and condition variable have
 * nothing to protect against and nothing to wait for:
 *
 *   - lock/unlock are no-ops. There is no second thread to contend with, and the
 *     scheduler never re-enters a lock it holds.
 *   - cond_wait would be a deadlock if it could be reached: the only thread that
 *     could signal is the one calling it. The scheduler never calls it with M = 1
 *     (it has no idle workers to park), so reaching it means a caller assumed
 *     threads exist, and saying so beats hanging.
 */
#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif

#include "zergrt.h"

#include <time.h>

bool zrt_thread_supported(void) { return false; }

size_t zrt_cpu_count(void) { return 1; }

bool zrt_thread_start(zrt_thread *t, void (*fn)(void *arg), void *arg) {
	(void)t;
	(void)fn;
	(void)arg;
	return false; /* the caller runs the work itself */
}

void zrt_thread_join(zrt_thread *t) { (void)t; }

void zrt_mutex_init(zrt_mutex *m) { (void)m; }
void zrt_mutex_destroy(zrt_mutex *m) { (void)m; }
void zrt_mutex_lock(zrt_mutex *m) { (void)m; }
void zrt_mutex_unlock(zrt_mutex *m) { (void)m; }

void zrt_cond_init(zrt_cond *c) { (void)c; }
void zrt_cond_destroy(zrt_cond *c) { (void)c; }

void zrt_cond_wait(zrt_cond *c, zrt_mutex *m) {
	(void)c;
	(void)m;
	zrt_report("zrt_cond_wait with no thread support (would never wake)");
}

void zrt_cond_timedwait(zrt_cond *c, zrt_mutex *m, int64_t ns) {
	(void)c;
	(void)m;
	/* The one wait this backend CAN honour, and the reason timers work here at all: with
	 * a single thread nothing can signal, so a bounded wait is simply a sleep, and the
	 * scheduler's idle worker wakes at the deadline exactly as it would with M workers.
	 * The mutex is not released because on this backend it locks nothing. */
	if (ns > 0) {
		struct timespec ts;
		ts.tv_sec = (time_t)(ns / 1000000000);
		ts.tv_nsec = (long)(ns % 1000000000);
		nanosleep(&ts, NULL);
	}
}

void zrt_cond_signal(zrt_cond *c) { (void)c; }
void zrt_cond_broadcast(zrt_cond *c) { (void)c; }

bool zrt_atomic_claim(bool *flag) {
	/* one thread: nothing can interleave between the read and the write */
	if (*flag) {
		return false;
	}
	*flag = true;
	return true;
}
