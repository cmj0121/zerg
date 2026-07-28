/*
 * thread_win32.c - the Windows backend of the zrt_thread shim.
 *
 * CreateThread, CRITICAL_SECTION and CONDITION_VARIABLE are OS interfaces, the same
 * floor pthreads is on POSIX. A CRITICAL_SECTION rather than a Win32 mutex object
 * because it is an in-process lock with no kernel transition on the uncontended
 * path, which is what every lock here is: the scheduler's run queue and a channel's
 * wait queues.
 *
 * CONDITION_VARIABLE needs Vista or later. That is the same floor SRWLOCK and the
 * rest of the modern Win32 synchronisation set sits on, and older targets are served
 * by thread_none.c, which is a correct build rather than a broken one.
 */
#include "zergrt.h"

#include <windows.h>

#if defined(__STDC_VERSION__) && __STDC_VERSION__ >= 201112L
_Static_assert(sizeof(HANDLE) <= sizeof(zrt_thread), "HANDLE does not fit zrt_thread");
_Static_assert(sizeof(CRITICAL_SECTION) <= sizeof(zrt_mutex), "CRITICAL_SECTION does not fit zrt_mutex");
_Static_assert(sizeof(CONDITION_VARIABLE) <= sizeof(zrt_cond), "CONDITION_VARIABLE does not fit zrt_cond");
#endif

static HANDLE *as_thread(zrt_thread *t) { return (HANDLE *)(void *)t->slots; }
static CRITICAL_SECTION *as_mutex(zrt_mutex *m) { return (CRITICAL_SECTION *)(void *)m->slots; }
static CONDITION_VARIABLE *as_cond(zrt_cond *c) { return (CONDITION_VARIABLE *)(void *)c->slots; }

bool zrt_thread_supported(void) { return true; }

size_t zrt_cpu_count(void) {
	SYSTEM_INFO si;
	GetSystemInfo(&si);
	return (si.dwNumberOfProcessors > 0) ? (size_t)si.dwNumberOfProcessors : 1;
}

typedef struct {
	void (*fn)(void *arg);
	void *arg;
} start_env;

static DWORD WINAPI thread_trampoline(LPVOID p) {
	start_env *e = (start_env *)p;
	void (*fn)(void *) = e->fn;
	void *arg = e->arg;
	zrt_free(e);
	fn(arg);
	return 0;
}

bool zrt_thread_start(zrt_thread *t, void (*fn)(void *arg), void *arg) {
	start_env *e = (start_env *)zrt_alloc(sizeof(start_env));
	e->fn = fn;
	e->arg = arg;
	HANDLE h = CreateThread(NULL, 0, thread_trampoline, e, 0, NULL);
	if (h == NULL) {
		zrt_free(e);
		return false;
	}
	*as_thread(t) = h;
	return true;
}

void zrt_thread_join(zrt_thread *t) {
	WaitForSingleObject(*as_thread(t), INFINITE);
	CloseHandle(*as_thread(t));
}

void zrt_mutex_init(zrt_mutex *m) { InitializeCriticalSection(as_mutex(m)); }
void zrt_mutex_destroy(zrt_mutex *m) { DeleteCriticalSection(as_mutex(m)); }
void zrt_mutex_lock(zrt_mutex *m) { EnterCriticalSection(as_mutex(m)); }
void zrt_mutex_unlock(zrt_mutex *m) { LeaveCriticalSection(as_mutex(m)); }

void zrt_cond_init(zrt_cond *c) { InitializeConditionVariable(as_cond(c)); }
void zrt_cond_destroy(zrt_cond *c) { (void)c; /* no destroy in the Win32 API */ }
void zrt_cond_wait(zrt_cond *c, zrt_mutex *m) { SleepConditionVariableCS(as_cond(c), as_mutex(m), INFINITE); }
void zrt_cond_signal(zrt_cond *c) { WakeConditionVariable(as_cond(c)); }
void zrt_cond_broadcast(zrt_cond *c) { WakeAllConditionVariable(as_cond(c)); }
