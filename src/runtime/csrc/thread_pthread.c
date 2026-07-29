/*
 * thread_pthread.c - the POSIX backend of the zrt_thread shim.
 *
 * pthreads is an OS-provided interface, not a third-party library, so it sits on the
 * same floor as posix_spawn and open/read/write: the zero-dependency rule is about
 * what the project links against, not about refusing to use the operating system.
 *
 * Every type is stored INSIDE the opaque slot array the header declares rather than
 * behind a pointer, so a mutex can be embedded in a channel and a thread handle in
 * the scheduler's worker table with no allocation. The static_asserts below are what
 * keeps that honest: if a host's pthread_mutex_t outgrows the slots, the build stops
 * here rather than corrupting memory at run time.
 */
#include "zergrt.h"

#include <errno.h>
#include <pthread.h>
#include <sys/time.h>
#include <unistd.h>

#if defined(__STDC_VERSION__) && __STDC_VERSION__ >= 201112L
_Static_assert(sizeof(pthread_t) <= sizeof(zrt_thread), "pthread_t does not fit zrt_thread");
_Static_assert(sizeof(pthread_mutex_t) <= sizeof(zrt_mutex), "pthread_mutex_t does not fit zrt_mutex");
_Static_assert(sizeof(pthread_cond_t) <= sizeof(zrt_cond), "pthread_cond_t does not fit zrt_cond");
#endif

static pthread_t *as_thread(zrt_thread *t) { return (pthread_t *)(void *)t->slots; }
static pthread_mutex_t *as_mutex(zrt_mutex *m) { return (pthread_mutex_t *)(void *)m->slots; }
static pthread_cond_t *as_cond(zrt_cond *c) { return (pthread_cond_t *)(void *)c->slots; }

bool zrt_thread_supported(void) { return true; }

size_t zrt_cpu_count(void) {
#if defined(_SC_NPROCESSORS_ONLN)
	long n = sysconf(_SC_NPROCESSORS_ONLN);
	return (n > 0) ? (size_t)n : 1;
#else
	return 1;
#endif
}

/* the trampoline: pthreads wants void *(*)(void *) and the shim promises void (*)(void *) */
typedef struct {
	void (*fn)(void *arg);
	void *arg;
} start_env;

static void *thread_trampoline(void *p) {
	start_env *e = (start_env *)p;
	void (*fn)(void *) = e->fn;
	void *arg = e->arg;
	zrt_free(e);
	fn(arg);
	return NULL;
}

bool zrt_thread_start(zrt_thread *t, void (*fn)(void *arg), void *arg) {
	start_env *e = (start_env *)zrt_alloc(sizeof(start_env));
	e->fn = fn;
	e->arg = arg;
	if (pthread_create(as_thread(t), NULL, thread_trampoline, e) != 0) {
		zrt_free(e);
		return false;
	}
	return true;
}

void zrt_thread_join(zrt_thread *t) { pthread_join(*as_thread(t), NULL); }

void zrt_mutex_init(zrt_mutex *m) { pthread_mutex_init(as_mutex(m), NULL); }
void zrt_mutex_destroy(zrt_mutex *m) { pthread_mutex_destroy(as_mutex(m)); }
void zrt_mutex_lock(zrt_mutex *m) { pthread_mutex_lock(as_mutex(m)); }
void zrt_mutex_unlock(zrt_mutex *m) { pthread_mutex_unlock(as_mutex(m)); }

void zrt_cond_init(zrt_cond *c) { pthread_cond_init(as_cond(c), NULL); }
void zrt_cond_destroy(zrt_cond *c) { pthread_cond_destroy(as_cond(c)); }
void zrt_cond_wait(zrt_cond *c, zrt_mutex *m) { pthread_cond_wait(as_cond(c), as_mutex(m)); }
void zrt_cond_signal(zrt_cond *c) { pthread_cond_signal(as_cond(c)); }
void zrt_cond_broadcast(zrt_cond *c) { pthread_cond_broadcast(as_cond(c)); }

void zrt_cond_timedwait(zrt_cond *c, zrt_mutex *m, int64_t ns) {
	/* pthread_cond_timedwait wants an ABSOLUTE deadline on the condvar's clock, which is
	 * CLOCK_REALTIME by default — so the relative span the scheduler computed from the
	 * monotonic clock is converted here rather than at the call site. A wall clock that
	 * jumps can make this wait long or short; the caller re-reads the monotonic clock and
	 * loops, so a jump costs an extra turn and never a missed deadline.
	 *
	 * The reading comes from gettimeofday rather than clock_gettime, which would be the
	 * better clock but is hidden by a strict `-std=c11` glibc unless _POSIX_C_SOURCE is
	 * defined before the headers — and asking for strict POSIX here also hides
	 * _SC_NPROCESSORS_ONLN on macOS, which would silently drop this scheduler to one
	 * worker. Microseconds are ample for a wait the Win32 backend rounds to whole
	 * milliseconds anyway. */
	if (ns < 0) {
		ns = 0;
	}
	struct timeval tv;
	gettimeofday(&tv, NULL);
	struct timespec ts;
	ts.tv_sec = tv.tv_sec + (time_t)(ns / 1000000000);
	ts.tv_nsec = tv.tv_usec * 1000 + (long)(ns % 1000000000);
	if (ts.tv_nsec >= 1000000000) {
		ts.tv_sec += 1;
		ts.tv_nsec -= 1000000000;
	}
	pthread_cond_timedwait(as_cond(c), as_mutex(m), &ts);
}

bool zrt_atomic_claim(bool *flag) {
	return __atomic_exchange_n(flag, true, __ATOMIC_ACQ_REL) == false;
}
