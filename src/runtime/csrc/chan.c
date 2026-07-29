/*
 * chan.c - the Zerg runtime's channels (Phase 1e slice C2).
 *
 * Linked only when a program's Manifest reports Concurrency and it uses a channel.
 * A channel is a coroutine-shared, refcounted object with an optional ring buffer
 * and two independent counts (Fork-D):
 *
 *   - rc      : the holder count. The last holder frees the object (as a 1d Ref).
 *   - senders : the count of send-capable handles. When it reaches zero the channel
 *               AUTO-CLOSES (there is no explicit close in the language) and every
 *               parked receiver wakes with the Right of Result[T].
 *
 * Splitting the two lets a channel close (senders -> 0) while receivers still drain
 * its buffer, and lets the object outlive its close until the last holder leaves.
 *
 * Send and recv are the two blocking points. They PARK the running coroutine on the
 * channel's send/recv queue and are woken (zrt_sched_wake) by the counterparty's
 * hand-off or by close. A parked coroutine's waiter node still lives on its own
 * (suspended) stack, so no queue node is heap-allocated — a suspended stack does not
 * move, and that holds with any number of workers.
 *
 * What does NOT hold under M:N is the old "single-threaded, so no lock" (Fork-E). Every
 * field below is now guarded by the channel's own `lock`, and a coroutine that must
 * block hands that lock to the scheduler (zrt_sched_park_unlock) instead of releasing
 * it first: releasing it before the switch completes would let a counterparty on
 * another worker wake a coroutine that is still running, and run it twice.
 *
 * LOCK ORDER is channel first, scheduler second — zrt_sched_wake takes the scheduler
 * lock while this file holds a channel lock, and nothing ever takes them the other way
 * round.
 */
#include "zergrt.h"

#include <stdlib.h>
#include <string.h>

/* zrt_waiter is one parked coroutine on a send or recv queue. It lives on the parked
 * coroutine's own stack (valid while it is suspended): `val` points at the value to
 * send (a sender) or the receive target (a receiver), and `done` is set by the
 * counterparty when a direct hand-off completes, so the woken coroutine can tell a
 * rendezvous from a close.
 *
 * `claimed` supports select's park-on-many. A select parks one waiter on EACH watched
 * channel, all sharing one bool: `claimed` points at it (NULL for a plain send/recv
 * waiter). A counterparty hands off through wq_take, which claims the shared bool so the
 * first hand-off wins and any later hand-off to another of that select's waiters is
 * skipped — a select fires exactly one arm even while its other waiters still sit in
 * other queues. */
typedef struct zrt_waiter {
	zrt_coro          *co;
	void              *val;
	bool               done;
	struct zrt_waiter *next;
	bool              *claimed; /* select: shared "already fired" flag; NULL if plain */
} zrt_waiter;

/* zrt_chan is the channel object (Fork-D layout). The ring buffer is present only for
 * a buffered channel (cap > 0); an unbuffered channel (cap == 0) hands off directly
 * between a sender and a receiver. */
struct zrt_chan {
	zrt_mutex      lock;    /* guards every field below, and both wait queues */
	size_t         rc;      /* holder count; last holder frees */
	size_t         senders; /* send-capable handles; zero -> auto-close */
	bool           closed;  /* set once senders reaches zero */
	const char    *err;     /* close reason: NULL = StopIteration, else a crash Err */
	size_t         elemsz;  /* element size in bytes (memcpy unit) */
	size_t         cap;     /* ring capacity; 0 = unbuffered rendezvous */
	size_t         head;    /* ring read cursor */
	size_t         tail;    /* ring write cursor */
	size_t         len;     /* live elements in the ring */
	unsigned char *buf;     /* cap*elemsz bytes; NULL when cap == 0 */
	zrt_waiter    *sendq_head, *sendq_tail; /* coroutines parked in send */
	zrt_waiter    *recvq_head, *recvq_tail; /* coroutines parked in recv */
};

/* --- wait queues (FIFO, intrusive via waiter->next) -------------------------- */

static void wq_push(zrt_waiter **head, zrt_waiter **tail, zrt_waiter *w) {
	w->next = NULL;
	if (*tail != NULL) {
		(*tail)->next = w;
	} else {
		*head = w;
	}
	*tail = w;
}

static zrt_waiter *wq_pop(zrt_waiter **head, zrt_waiter **tail) {
	zrt_waiter *w = *head;
	if (w != NULL) {
		*head = w->next;
		if (*head == NULL) {
			*tail = NULL;
		}
		w->next = NULL;
	}
	return w;
}

/* wq_remove unlinks a specific waiter from a queue if present, a no-op otherwise. A
 * select removes its remaining waiters from every queue once it fires, before its stack
 * (which the waiters live on) is reused. */
static void wq_remove(zrt_waiter **head, zrt_waiter **tail, zrt_waiter *w) {
	zrt_waiter *prev = NULL;
	for (zrt_waiter *cur = *head; cur != NULL; prev = cur, cur = cur->next) {
		if (cur != w) {
			continue;
		}
		if (prev == NULL) {
			*head = cur->next;
		} else {
			prev->next = cur->next;
		}
		if (*tail == cur) {
			*tail = prev;
		}
		cur->next = NULL;
		return;
	}
}

/* wq_take pops the next LIVE waiter for a hand-off, discarding stale select waiters
 * whose select has already fired elsewhere, and claiming a fresh select waiter so it
 * cannot also fire on another channel. A plain waiter (claimed == NULL) is always live.
 * Used wherever a counterparty consumes a waiter (send-to-receiver, recv-from-sender). */
static zrt_waiter *wq_take(zrt_waiter **head, zrt_waiter **tail) {
	for (;;) {
		zrt_waiter *w = wq_pop(head, tail);
		if (w == NULL) {
			return NULL;
		}
		if (w->claimed != NULL && !zrt_atomic_claim(w->claimed)) {
			continue; /* stale: this select already fired on another channel */
		}
		return w;
	}
}

/* --- ring buffer ------------------------------------------------------------- */

static void ring_put(zrt_chan *ch, const void *val) {
	memcpy(ch->buf + ch->tail * ch->elemsz, val, ch->elemsz);
	ch->tail = (ch->tail + 1) % ch->cap;
	ch->len++;
}

static void ring_get(zrt_chan *ch, void *out) {
	memcpy(out, ch->buf + ch->head * ch->elemsz, ch->elemsz);
	ch->head = (ch->head + 1) % ch->cap;
	ch->len--;
}

/* --- construction / lifetime ------------------------------------------------- */

zrt_chan *zrt_chan_new(size_t elemsz, size_t cap) {
	zrt_chan *ch = (zrt_chan *)zrt_alloc(sizeof(*ch));
	zrt_mutex_init(&ch->lock);
	ch->rc = 1;
	ch->senders = 1; /* the new bidirectional handle is a sender */
	ch->closed = false;
	ch->err = NULL;
	ch->elemsz = elemsz;
	ch->cap = cap;
	ch->head = ch->tail = ch->len = 0;
	ch->buf = (cap > 0) ? (unsigned char *)zrt_alloc(cap * elemsz) : NULL;
	ch->sendq_head = ch->sendq_tail = NULL;
	ch->recvq_head = ch->recvq_tail = NULL;
	return ch;
}

static void chan_free(zrt_chan *ch) {
	zrt_mutex_destroy(&ch->lock);
	if (ch->buf != NULL) {
		zrt_free(ch->buf);
	}
	zrt_free(ch);
}

/* chan_close flips the channel to closed with the given reason (NULL = StopIteration,
 * else a crash Err) and wakes every parked receiver, each of which re-checks and takes
 * the Right of Result[T]. It only flips a flag and wakes — freeing stays with rc. When
 * senders has reached zero no sender can be parked, so sendq is empty here. */
static void chan_close(zrt_chan *ch, const char *err) {
	if (ch->closed) {
		return;
	}
	ch->closed = true;
	ch->err = err;
	for (zrt_waiter *w = ch->recvq_head; w != NULL; w = w->next) {
		zrt_sched_wake(w->co);
	}
	ch->recvq_head = ch->recvq_tail = NULL;
}

zrt_chan *zrt_chan_copy(zrt_chan *ch) {
	zrt_mutex_lock(&ch->lock);
	ch->rc++;
	zrt_mutex_unlock(&ch->lock);
	return ch;
}

zrt_chan *zrt_chan_sender_copy(zrt_chan *ch) {
	zrt_mutex_lock(&ch->lock);
	ch->rc++;
	ch->senders++;
	zrt_mutex_unlock(&ch->lock);
	return ch;
}

void zrt_chan_release(zrt_chan *ch) {
	/* the count must drop under the lock, but chan_free must NOT run under it — it
	 * destroys the lock it would be holding. So the decision is made inside and acted
	 * on outside, which is also why the last holder is the only one that can free: no
	 * other thread can still be looking at a channel whose count it just took to zero. */
	zrt_mutex_lock(&ch->lock);
	bool last = (--ch->rc == 0);
	zrt_mutex_unlock(&ch->lock);
	if (last) {
		chan_free(ch);
	}
}

void zrt_chan_sender_release(zrt_chan *ch) {
	zrt_mutex_lock(&ch->lock);
	if (--ch->senders == 0) {
		/* the last sender left: auto-close. A crashing sender (Fork-C) carries a crash
		 * Err so a receiver observes Right(Err) rather than the ordinary StopIteration.
		 * chan_close wakes the parked receivers, so it runs with the lock held — waking
		 * takes the scheduler lock, which is the allowed order. */
		chan_close(ch, zrt_crash_active() ? "coroutine crashed" : NULL);
	}
	bool last = (--ch->rc == 0);
	zrt_mutex_unlock(&ch->lock);
	if (last) {
		chan_free(ch);
	}
}

/* --- send / recv ------------------------------------------------------------- */

void zrt_chan_send(zrt_chan *ch, const void *val) {
	zrt_mutex_lock(&ch->lock);
	if (ch->closed) {
		zrt_mutex_unlock(&ch->lock);
		zrt_abort("send on a closed channel");
	}
	/* a waiting receiver takes the value directly (rendezvous / buffered hand-off). */
	zrt_waiter *r = wq_take(&ch->recvq_head, &ch->recvq_tail);
	if (r != NULL) {
		memcpy(r->val, val, ch->elemsz);
		r->done = true;
		zrt_sched_wake(r->co); /* channel lock held, scheduler lock taken inside */
		zrt_mutex_unlock(&ch->lock);
		return;
	}
	/* buffered with room: enqueue and return without blocking. */
	if (ch->len < ch->cap) {
		ring_put(ch, val);
		zrt_mutex_unlock(&ch->lock);
		return;
	}
	/* full (or unbuffered with no receiver): park until a receiver takes the value. The
	 * lock goes to the scheduler, which releases it once this coroutine is off the CPU —
	 * a receiver must not be able to find this waiter before then. */
	zrt_waiter w = {zrt_sched_current(), (void *)val, false, NULL, NULL};
	wq_push(&ch->sendq_head, &ch->sendq_tail, &w);
	zrt_sched_park_unlock(&ch->lock);
	if (!w.done) {
		/* woken without a taker: the channel closed under us. */
		zrt_abort("send on a closed channel");
	}
}

int zrt_chan_recv(zrt_chan *ch, void *out) {
	zrt_mutex_lock(&ch->lock);
	for (;;) {
		/* a buffered value is available: take it, then let a parked sender fill the
		 * freed slot so a full buffer keeps flowing. */
		if (ch->len > 0) {
			ring_get(ch, out);
			zrt_waiter *s = wq_take(&ch->sendq_head, &ch->sendq_tail);
			if (s != NULL) {
				ring_put(ch, s->val);
				s->done = true;
				zrt_sched_wake(s->co);
			}
			zrt_mutex_unlock(&ch->lock);
			return 0;
		}
		/* no buffered value but a parked sender: take its value directly (unbuffered
		 * rendezvous, or a buffered channel whose sender parked on a full buffer). */
		zrt_waiter *s = wq_take(&ch->sendq_head, &ch->sendq_tail);
		if (s != NULL) {
			memcpy(out, s->val, ch->elemsz);
			s->done = true;
			zrt_sched_wake(s->co);
			zrt_mutex_unlock(&ch->lock);
			return 0;
		}
		/* empty and closed: the Right of Result[T] (StopIteration or a crash Err). */
		if (ch->closed) {
			zrt_mutex_unlock(&ch->lock);
			return 1;
		}
		/* empty and open: park until a sender hands off or the channel closes. */
		zrt_waiter w = {zrt_sched_current(), out, false, NULL, NULL};
		wq_push(&ch->recvq_head, &ch->recvq_tail, &w);
		zrt_sched_park_unlock(&ch->lock);
		if (w.done) {
			return 0; /* a sender rendezvoused straight into *out. */
		}
		/* Woken by close, or spuriously. Re-take the lock and re-check everything: with
		 * several workers the state that woke us may already have been taken by another
		 * receiver, which is exactly why this is a loop and not an if. */
		zrt_mutex_lock(&ch->lock);
	}
}

const char *zrt_chan_err(zrt_chan *ch) {
	zrt_mutex_lock(&ch->lock);
	const char *e = ch->err;
	zrt_mutex_unlock(&ch->lock);
	return e;
}

/* --- select ------------------------------------------------------------------ */

/* g_sel_rot rotates the fair ready-scan's start index so that, when several arms are
 * ready at once, the winner rotates rather than always being the first (front) arm —
 * enough fairness to keep a back arm from starving under the N:1 scheduler. */
static size_t g_sel_rot;

/* g_sel_lock serialises whole selects. A select touches SEVERAL channels' queues, so it
 * cannot be ordered by any one channel's lock; one lock above them all is what keeps the
 * multi-channel park atomic against another select doing the same. Lock order is
 * select -> channel -> scheduler, and nothing takes them the other way round: a plain
 * send or recv never touches this lock, so it cannot be behind a select that is waiting
 * on a channel it holds.
 *
 * It serialises selects against each other, not against sends and receives — those keep
 * running in parallel on their own channel locks, which is where the throughput is. */
static zrt_mutex g_sel_lock;
static bool g_sel_ready;

/* zrt_chan_select_init prepares that lock. sched_init calls it, which is the one place
 * that runs before any coroutine and after the runtime exists. */
void zrt_chan_select_init(void) {
	if (!g_sel_ready) {
		zrt_mutex_init(&g_sel_lock);
		g_sel_ready = true;
	}
}

/* sel_try_recv performs a recv on ch if it can proceed WITHOUT blocking on a real value
 * (a buffered element, or a parked live sender), returning 1 and delivering into *out;
 * it returns 0 when the channel has no value to give right now (including a closed,
 * drained channel — closure is resolved by the caller via `done` / Right). It mirrors
 * zrt_chan_recv's ready path and uses wq_take, so a stale select sender is skipped. */
static int sel_try_recv_locked(zrt_chan *ch, void *out) {
	if (ch->len > 0) {
		ring_get(ch, out);
		zrt_waiter *s = wq_take(&ch->sendq_head, &ch->sendq_tail);
		if (s != NULL) {
			ring_put(ch, s->val);
			s->done = true;
			zrt_sched_wake(s->co);
		}
		return 1;
	}
	zrt_waiter *s = wq_take(&ch->sendq_head, &ch->sendq_tail);
	if (s != NULL) {
		memcpy(out, s->val, ch->elemsz);
		s->done = true;
		zrt_sched_wake(s->co);
		return 1;
	}
	return 0;
}

static int sel_try_recv(zrt_chan *ch, void *out) {
	zrt_mutex_lock(&ch->lock);
	int r = sel_try_recv_locked(ch, out);
	zrt_mutex_unlock(&ch->lock);
	return r;
}

/* sel_try_send performs a send on ch if it can proceed without blocking (a parked live
 * receiver, or buffer room), returning 1; it returns 0 when the channel would block.
 * Sending on a closed channel is a program error and aborts (DESIGN-1e §4.2: a closed
 * send case selected aborts). It mirrors zrt_chan_send's ready path. */
static int sel_try_send_locked(zrt_chan *ch, const void *val) {
	if (ch->closed) {
		zrt_abort("send on a closed channel");
	}
	zrt_waiter *r = wq_take(&ch->recvq_head, &ch->recvq_tail);
	if (r != NULL) {
		memcpy(r->val, val, ch->elemsz);
		r->done = true;
		zrt_sched_wake(r->co);
		return 1;
	}
	if (ch->len < ch->cap) {
		ring_put(ch, val);
		return 1;
	}
	return 0;
}

static int sel_try_send(zrt_chan *ch, const void *val) {
	zrt_mutex_lock(&ch->lock);
	int r = sel_try_send_locked(ch, val);
	zrt_mutex_unlock(&ch->lock);
	return r;
}

/* sel_all_recv_closed reports whether every watched recv case's channel has closed (its
 * buffer is drained by the time this runs, since a buffered value would have been
 * value-ready). With no recv case it is vacuously true. Drives the `done` arm. */
static bool sel_all_recv_closed(const zrt_sel_case *cases, size_t n) {
	for (size_t i = 0; i < n; i++) {
		if (cases[i].op != ZRT_SEL_RECV) {
			continue;
		}
		/* `closed` is written by whichever worker released the last sender, so reading it
		 * bare is a race — and losing it is not a wrong answer but a HANG: the select
		 * sees an open channel, parks, and the close that would have woken it already
		 * happened. */
		zrt_mutex_lock(&cases[i].ch->lock);
		bool c = cases[i].ch->closed;
		zrt_mutex_unlock(&cases[i].ch->lock);
		if (!c) {
			return false;
		}
	}
	return true;
}

int zrt_select(zrt_sel_case *cases, size_t n, bool has_default, bool has_done) {
	zrt_mutex_lock(&g_sel_lock);
	for (;;) {
		size_t start = g_sel_rot++;
		/* fair scan for a value-ready case: perform the first that can proceed. */
		for (size_t k = 0; k < n; k++) {
			size_t i = (start + k) % n;
			zrt_sel_case *c = &cases[i];
			if (c->op == ZRT_SEL_RECV) {
				if (sel_try_recv(c->ch, c->val)) {
					c->closed = 0;
					zrt_mutex_unlock(&g_sel_lock);
					return (int)i;
				}
			} else if (sel_try_send(c->ch, c->val)) {
				zrt_mutex_unlock(&g_sel_lock);
				return (int)i;
			}
		}
		/* nothing value-ready. `done` fires once every watched recv channel has closed. */
		if (has_done && sel_all_recv_closed(cases, n)) {
			zrt_mutex_unlock(&g_sel_lock);
			return ZRT_SEL_DONE;
		}
		/* with no `done` arm to absorb closure, a closed recv channel fires as Right. */
		if (!has_done) {
			for (size_t k = 0; k < n; k++) {
				size_t i = (start + k) % n;
				zrt_sel_case *c = &cases[i];
				if (c->op != ZRT_SEL_RECV) {
					continue;
				}
				zrt_mutex_lock(&c->ch->lock);
				bool shut = c->ch->closed;
				zrt_mutex_unlock(&c->ch->lock);
				if (shut) {
					c->closed = 1;
					zrt_mutex_unlock(&g_sel_lock);
					return (int)i;
				}
			}
		}
		/* the non-blocking `_`: nothing ready, so run its arm without parking. */
		if (has_default) {
			zrt_mutex_unlock(&g_sel_lock);
			return ZRT_SEL_DEFAULT;
		}
		/* park on EVERY case's channel at once; wake when any becomes ready. The waiters
		 * share one `claimed` flag so at most one hand-off fires this select. */
		bool claimed = false;
		zrt_waiter ws[n];
		zrt_coro *self = zrt_sched_current();
		for (size_t i = 0; i < n; i++) {
			ws[i].co = self;
			ws[i].val = cases[i].val;
			ws[i].done = false;
			ws[i].next = NULL;
			ws[i].claimed = &claimed;
			zrt_mutex_lock(&cases[i].ch->lock);
			if (cases[i].op == ZRT_SEL_RECV) {
				wq_push(&cases[i].ch->recvq_head, &cases[i].ch->recvq_tail, &ws[i]);
			} else {
				wq_push(&cases[i].ch->sendq_head, &cases[i].ch->sendq_tail, &ws[i]);
			}
			zrt_mutex_unlock(&cases[i].ch->lock);
		}
		/* the select lock goes to the scheduler, released once this coroutine is off the
		 * CPU — the same hand-off a plain recv makes with the channel lock. */
		zrt_sched_park_unlock(&g_sel_lock);
		zrt_mutex_lock(&g_sel_lock);
		/* woken. Claim ourselves so no late hand-off consumes another waiter, then unlink
		 * every waiter before this stack frame (which they live on) is reused. */
		zrt_atomic_claim(&claimed); /* no late hand-off may consume another waiter */
		int fired = -1;
		for (size_t i = 0; i < n; i++) {
			if (ws[i].done) {
				fired = (int)i;
			}
			zrt_mutex_lock(&cases[i].ch->lock);
			if (cases[i].op == ZRT_SEL_RECV) {
				wq_remove(&cases[i].ch->recvq_head, &cases[i].ch->recvq_tail, &ws[i]);
			} else {
				wq_remove(&cases[i].ch->sendq_head, &cases[i].ch->sendq_tail, &ws[i]);
			}
			zrt_mutex_unlock(&cases[i].ch->lock);
		}
		if (fired >= 0) {
			cases[fired].closed = 0; /* a hand-off delivered a real value (Left) */
			zrt_mutex_unlock(&g_sel_lock);
			return fired;
		}
		/* woken by a close (no hand-off): loop and re-scan — a closed recv now routes to
		 * `done` or fires as Right per the resolution order above. */
	}
}
