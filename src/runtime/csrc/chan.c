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
	zrt_err        err;     /* why it closed; see chan_close. Valid from construction. */
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

/* chan_stop_iteration is the Err a CLEAN close carries: the end-of-stream sentinel the
 * spec names StopIteration, which is an ordinary ending and not a failure. Giving it a
 * kind of its own is what lets a receiver tell a clean close from a crash without
 * matching on a message — and the taxonomy has no way to raise StopIteration from
 * Zerg, so no crash Err can wear this kind by accident. */
static zrt_err chan_stop_iteration(void) {
	return zrt_err_new_kind(ZRT_ERR_STOP_ITERATION, "channel closed");
}

zrt_chan *zrt_chan_new(size_t elemsz, size_t cap) {
	zrt_chan *ch = (zrt_chan *)zrt_alloc(sizeof(*ch));
	zrt_mutex_init(&ch->lock);
	ch->rc = 1;
	ch->senders = 1; /* the new bidirectional handle is a sender */
	ch->closed = false;
	ch->err = chan_stop_iteration(); /* zrt_alloc is malloc: never leave the reason garbage */
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

/* chan_close flips the channel to closed carrying the Err a receiver will read back as
 * the Right of Result[T], and wakes every parked receiver. It only flips a flag and
 * wakes — freeing stays with rc. When senders has reached zero no sender can be parked,
 * so sendq is empty here.
 *
 * The Err is stored BY VALUE, and the value is all the channel owns: `msg` and `cause`
 * are pointers into storage the crashing coroutine never owned either — a message is a
 * literal or an interned string and a cause is a heap box, both leaked for the MVP like
 * every other Err. So the copy outlives the stack it came from, which it must: the
 * coroutine that crashed is unwinding as this runs, and by the time a receiver reads
 * the Err that coroutine is gone. */
static void chan_close(zrt_chan *ch, zrt_err err) {
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
		/* the last sender left: auto-close. A crashing sender (Fork-C) hands the channel
		 * its OWN in-flight Err — message, cause and kind intact — so the receiver reads
		 * back what actually went wrong rather than a stand-in string; a crash reason a
		 * receiver cannot read is a crash reason lost. chan_close wakes the parked
		 * receivers, so it runs with the lock held — waking takes the scheduler lock,
		 * which is the allowed order. */
		chan_close(ch, zrt_crash_active() ? zrt_taken_err() : chan_stop_iteration());
	}
	bool last = (--ch->rc == 0);
	zrt_mutex_unlock(&ch->lock);
	if (last) {
		chan_free(ch);
	}
}

/* --- send / recv ------------------------------------------------------------- */

/* chan_send_closed is the one diagnostic a dead letter gets, in the one place its kind
 * is written down. A send on a closed channel is a program error, not a value: the
 * counterparty is gone and there is nothing to hand the value to. */
static _Noreturn void chan_send_closed(void) {
	zrt_abort_kind(ZRT_ERR_SEND_ON_CLOSED, "SendOnClosedError: send on a closed channel");
}

void zrt_chan_send(zrt_chan *ch, const void *val) {
	zrt_mutex_lock(&ch->lock);
	if (ch->closed) {
		zrt_mutex_unlock(&ch->lock);
		chan_send_closed();
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
	if (zrt_sched_park_unlock(&ch->lock)) {
		/* the deadlock detector resumed us to carry the raise. `w` lives on the stack the
		 * abort is about to unwind, and the send queue still points at it, so it has to
		 * come back out before we leave — a dangling waiter is a hand-off into freed
		 * memory the moment anything touches this channel again. */
		zrt_mutex_lock(&ch->lock);
		wq_remove(&ch->sendq_head, &ch->sendq_tail, &w);
		zrt_mutex_unlock(&ch->lock);
		zrt_sched_deadlock();
	}
	if (!w.done) {
		/* woken without a taker: the channel closed under us. */
		chan_send_closed();
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
		if (zrt_sched_park_unlock(&ch->lock)) {
			/* the deadlock detector resumed us to carry the raise; unlink `w` before the
			 * abort unwinds the stack it lives on. */
			zrt_mutex_lock(&ch->lock);
			wq_remove(&ch->recvq_head, &ch->recvq_tail, &w);
			zrt_mutex_unlock(&ch->lock);
			zrt_sched_deadlock();
		}
		if (w.done) {
			return 0; /* a sender rendezvoused straight into *out. */
		}
		/* Woken by close, or spuriously. Re-take the lock and re-check everything: with
		 * several workers the state that woke us may already have been taken by another
		 * receiver, which is exactly why this is a loop and not an if. */
		zrt_mutex_lock(&ch->lock);
	}
}

zrt_err zrt_chan_close_err(zrt_chan *ch) {
	zrt_mutex_lock(&ch->lock);
	zrt_err e = ch->err;
	zrt_mutex_unlock(&ch->lock);
	return e;
}

/* zrt_chan_close is the EXPLICIT close behind Zerg's `close(ch)`: it marks the CHANNEL
 * no-longer-sendable. It is a property of the channel and not of a holder, so it touches
 * neither rc nor senders — every handle keeps its reference and stays readable, and a
 * receive drains what is buffered before answering the Right, exactly as an auto-close
 * does.
 *
 * It is idempotent: chan_close returns early on an already-closed channel, so FIRST CLOSE
 * WINS for the recorded reason. That is a real trade — a producer that crashes after an
 * explicit close does not overwrite the clean StopIteration with its own Err, so its
 * receiver reads an ordinary end. The crash still reports on stderr through the crashing
 * coroutine's own handler, so the failure is not lost, only its path to that receiver.
 *
 * Explicit close is the one that can happen while senders REMAIN, which is why it wakes
 * the send queue and the auto-close path does not: a sender parked waiting for a taker
 * would otherwise sleep forever. Each wakes with `done` false and aborts with
 * SendOnClosedError, and the queue is emptied here — a woken sender's waiter lives on the
 * stack it is about to unwind, so leaving it linked would be a hand-off into freed
 * memory. */
void zrt_chan_close(zrt_chan *ch) {
	zrt_mutex_lock(&ch->lock);
	if (!ch->closed) {
		chan_close(ch, chan_stop_iteration());
		for (zrt_waiter *w = ch->sendq_head; w != NULL; w = w->next) {
			zrt_sched_wake(w->co);
		}
		ch->sendq_head = ch->sendq_tail = NULL;
	}
	zrt_mutex_unlock(&ch->lock);
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

/* sel_send_closed answers whether a send arm's channel has closed, which is the arm
 * aborting rather than the arm being ready (DESIGN-1e §4.2: a closed send case selected
 * aborts). It is a separate question from readiness because of WHERE the abort has to
 * happen: an abort longjmps out, so raising it from inside sel_try_send would leave both
 * the channel's lock and select's own lock held for the rest of the program, and every
 * later select would hang on a lock whose owner is gone. The caller asks first, unwinds
 * its locks, and only then aborts. */
static bool sel_send_closed(zrt_chan *ch) {
	zrt_mutex_lock(&ch->lock);
	bool closed = ch->closed;
	zrt_mutex_unlock(&ch->lock);
	return closed;
}

/* sel_try_send performs a send on ch if it can proceed without blocking (a parked live
 * receiver, or buffer room), returning 1; it returns 0 when the channel would block. It
 * mirrors zrt_chan_send's ready path. */
static int sel_try_send_locked(zrt_chan *ch, const void *val) {
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

/* sel_shut_scan reads, in one pass, everything this select needs to know about closure:
 * which recv arms are FINISHED (closed AND drained, recorded into shut[]), whether they
 * all are, the first one in fair order, and the first whose close carried a crash. Every
 * closure question the resolution below asks is answered from this one scan.
 *
 * Closed alone is not finished, and the difference is values. This holds one channel's
 * lock at a time, so a sender can fill a channel already passed and then close it before
 * the last is looked at — and `done` would end the select over a buffer that still has
 * values in it. A closed channel keeps delivering what it already holds; only an empty
 * one is over.
 *
 * The reads must be under the lock even though only a flag is wanted: `closed` is
 * written by whichever worker released the last sender, and losing it is not a wrong
 * answer but a HANG — the select sees an open channel, parks, and the close that would
 * have woken it already happened. */
typedef struct {
	bool all;   /* every watched recv arm is finished (vacuously true with none) */
	int  first; /* first finished recv arm in fair order, or -1 */
	int  crash; /* first finished recv arm whose close carried a crash Err, or -1 */
} sel_shut;

static sel_shut sel_shut_scan(const zrt_sel_case *cases, bool *shut, size_t n, size_t start) {
	sel_shut r = {true, -1, -1};
	for (size_t k = 0; k < n; k++) {
		size_t i = (start + k) % n;
		shut[i] = false;
		if (cases[i].op != ZRT_SEL_RECV) {
			continue;
		}
		zrt_mutex_lock(&cases[i].ch->lock);
		bool over = cases[i].ch->closed && cases[i].ch->len == 0 && cases[i].ch->sendq_head == NULL;
		bool crashed = over && cases[i].ch->err.kind != ZRT_ERR_STOP_ITERATION;
		zrt_mutex_unlock(&cases[i].ch->lock);
		shut[i] = over;
		if (!over) {
			r.all = false;
			continue;
		}
		if (r.first < 0) {
			r.first = (int)i;
		}
		if (crashed && r.crash < 0) {
			r.crash = (int)i;
		}
	}
	return r;
}

/* sel_ready_locked answers whether a scan of this one case would find something to do,
 * without doing it. Read-only, and the caller must hold the channel's lock.
 *
 * It is deliberately generous: a wait queue holding nothing but another select's stale
 * waiter reads as ready here, because telling that apart means consuming it. The cost of
 * saying yes wrongly is one extra scan before parking again; the cost of saying no
 * wrongly would be a hang, so the error is taken in the harmless direction. */
static bool sel_ready_locked(const zrt_sel_case *c, bool was_shut) {
	const zrt_chan *ch = c->ch;
	if (c->op == ZRT_SEL_RECV) {
		/* a buffered value, a sender to take one from, or a close this select has not
		 * already accounted for. `was_shut` is what keeps the generosity from becoming a
		 * livelock: a channel that was ALREADY finished when the resolution above ran is
		 * not news, and calling it ready would send this select round the loop forever
		 * without ever parking — which, with one worker, also means the coroutines that
		 * would have unstuck it never get to run. A close that arrived since is news, and
		 * that is exactly the race the re-scan exists for. */
		return ch->len > 0 || ch->sendq_head != NULL || (ch->closed && !was_shut);
	}
	/* room in the buffer, a receiver to hand to, or a close (which aborts, but is
	 * something this select must proceed to rather than sleep through) */
	return ch->len < ch->cap || ch->recvq_head != NULL || ch->closed;
}

/* sel_unlink ends a select's park. It claims the shared flag first, so that from here on
 * no counterparty can hand off to any of these waiters, then takes every one of them back
 * out of its queue before the stack frame they live on is reused. It answers the arm a
 * counterparty did hand off to, or -1 if none did — at most one, because the claim inside
 * wq_take is atomic and only one taker can win it. */
static int sel_unlink(zrt_sel_case *cases, zrt_waiter *ws, size_t n, bool *claimed) {
	zrt_atomic_claim(claimed);
	int fired = -1;
	for (size_t i = 0; i < n; i++) {
		zrt_mutex_lock(&cases[i].ch->lock);
		/* `done` and the value beside it are written by the counterparty under THIS lock,
		 * so reading them under it is what orders the two — the select lock the caller
		 * holds is a different lock and orders nothing against them. An unsynchronised
		 * read here can see a stale false and lose a value already counted as sent. */
		if (ws[i].done) {
			fired = (int)i;
		}
		if (cases[i].op == ZRT_SEL_RECV) {
			wq_remove(&cases[i].ch->recvq_head, &cases[i].ch->recvq_tail, &ws[i]);
		} else {
			wq_remove(&cases[i].ch->sendq_head, &cases[i].ch->sendq_tail, &ws[i]);
		}
		zrt_mutex_unlock(&cases[i].ch->lock);
	}
	return fired;
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
			} else if (sel_send_closed(c->ch)) {
				zrt_mutex_unlock(&g_sel_lock); /* the abort never comes back for it */
				chan_send_closed();
			} else if (sel_try_send(c->ch, c->val)) {
				zrt_mutex_unlock(&g_sel_lock);
				return (int)i;
			}
		}
		/* Nothing value-ready, so closure decides. One scan answers every question that
		 * follows, and shut[] is kept for the re-scan after the park. */
		bool shut[n];
		sel_shut sh = sel_shut_scan(cases, shut, n, start);
		if (has_done && sh.all) {
			zrt_mutex_unlock(&g_sel_lock);
			return ZRT_SEL_DONE; /* the select is exhausted: every producer has finished */
		}
		if (!has_done) {
			if (sh.crash >= 0) {
				/* a crash close is news, not an ending: it outranks both the clean closes
				 * beside it and the deadlock below, because the one thing this select must
				 * never do is swallow the error its producer died of. */
				cases[sh.crash].closed = 1;
				zrt_mutex_unlock(&g_sel_lock);
				return sh.crash;
			}
			if (sh.all && sh.first >= 0 && !has_default) {
				/* every channel this select watches has closed cleanly, and the select has
				 * neither a `done` to end on nor a `_` to fall through to — so it is waiting
				 * for something that can no longer happen. Raising is the safety net for a
				 * forgotten shutdown arm; the alternative is a select that runs an arm over
				 * a value nobody sent, or one that sleeps for good. */
				zrt_mutex_unlock(&g_sel_lock);
				zrt_sched_deadlock();
			}
			if (sh.first >= 0) {
				/* some arms are still open, so this is not the end of the select — but with
				 * no `done` to route it to, closure is reported per arm as the Right. */
				cases[sh.first].closed = 1;
				zrt_mutex_unlock(&g_sel_lock);
				return sh.first;
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
		/* RE-SCAN, now that the waiters are enqueued, and this is not an optimisation —
		 * without it the select can sleep on a value that is already sitting in a buffer.
		 * The scan above released each channel's lock to move to the next, and a sender
		 * arriving in that gap finds an empty recvq, drops its value into the ring, and
		 * returns: correct, and it wakes nobody, because at that instant there is nobody
		 * to wake. The waiter goes up afterwards, against a channel that will never be
		 * touched again.
		 *
		 * Enqueueing first and re-reading second closes it, whichever order the two
		 * threads take: a sender that arrives after the push finds the waiter and hands
		 * off, and one that arrived before it left its value where this read, under the
		 * same channel lock, is bound to see it. */
		bool ready = false;
		for (size_t i = 0; i < n && !ready; i++) {
			zrt_mutex_lock(&cases[i].ch->lock);
			ready = sel_ready_locked(&cases[i], shut[i]);
			zrt_mutex_unlock(&cases[i].ch->lock);
		}
		if (ready) {
			/* Take the waiters back down and go round again rather than parking. If a
			 * counterparty got in first, its hand-off is the answer and is returned here;
			 * otherwise the top of the loop consumes whatever this read saw.
			 *
			 * A wake may already be pending against this coroutine from that hand-off. It
			 * is left standing: the token is one-shot, and the park it is spent on simply
			 * returns early to a scan that finds nothing and parks again. */
			int early = sel_unlink(cases, ws, n, &claimed);
			if (early >= 0) {
				cases[early].closed = 0; /* a hand-off delivered a real value (Left) */
				zrt_mutex_unlock(&g_sel_lock);
				return early;
			}
			continue;
		}
		/* the select lock goes to the scheduler, released once this coroutine is off the
		 * CPU — the same hand-off a plain recv makes with the channel lock. */
		bool deadlocked = zrt_sched_park_unlock(&g_sel_lock);
		zrt_mutex_lock(&g_sel_lock);
		int fired = sel_unlink(cases, ws, n, &claimed);
		if (fired >= 0) {
			cases[fired].closed = 0; /* a hand-off delivered a real value (Left) */
			zrt_mutex_unlock(&g_sel_lock);
			return fired;
		}
		if (deadlocked) {
			/* the deadlock detector resumed us to carry the raise, and sel_unlink has
			 * already taken every waiter back off every channel — which a select must do
			 * on this path more than any other, since `ws` lives on the stack the abort is
			 * about to unwind and it was on as many queues as the select had arms. */
			zrt_mutex_unlock(&g_sel_lock);
			zrt_sched_deadlock();
		}
		/* woken by a close (no hand-off): loop and re-scan — a closed recv now routes to
		 * `done` or fires as Right per the resolution order above. */
	}
}
