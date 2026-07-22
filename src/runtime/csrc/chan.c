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
 * Send and recv are the two blocking points. Under the N:1 cooperative scheduler
 * they PARK the running coroutine (zrt_sched_park) on the channel's send/recv queue
 * and are woken (zrt_sched_wake) by the counterparty's hand-off or by close. A parked
 * coroutine's waiter node lives on its own (suspended) stack, so no queue node is
 * heap-allocated and, single-threaded, no queue operation needs a lock (Fork-E).
 */
#include "zergrt.h"

#include <stdlib.h>
#include <string.h>

/* zrt_waiter is one parked coroutine on a send or recv queue. It lives on the parked
 * coroutine's own stack (valid while it is suspended): `val` points at the value to
 * send (a sender) or the receive target (a receiver), and `done` is set by the
 * counterparty when a direct hand-off completes, so the woken coroutine can tell a
 * rendezvous from a close. */
typedef struct zrt_waiter {
	zrt_coro          *co;
	void              *val;
	bool               done;
	struct zrt_waiter *next;
} zrt_waiter;

/* zrt_chan is the channel object (Fork-D layout). The ring buffer is present only for
 * a buffered channel (cap > 0); an unbuffered channel (cap == 0) hands off directly
 * between a sender and a receiver. */
struct zrt_chan {
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
	ch->rc++;
	return ch;
}

zrt_chan *zrt_chan_sender_copy(zrt_chan *ch) {
	ch->rc++;
	ch->senders++;
	return ch;
}

void zrt_chan_release(zrt_chan *ch) {
	if (--ch->rc == 0) {
		chan_free(ch);
	}
}

void zrt_chan_sender_release(zrt_chan *ch) {
	if (--ch->senders == 0) {
		/* the last sender left: auto-close. A crashing sender (Fork-C) carries a crash
		 * Err so a receiver observes Right(Err) rather than the ordinary StopIteration. */
		chan_close(ch, zrt_crash_active() ? "coroutine crashed" : NULL);
	}
	if (--ch->rc == 0) {
		chan_free(ch);
	}
}

/* --- send / recv ------------------------------------------------------------- */

void zrt_chan_send(zrt_chan *ch, const void *val) {
	if (ch->closed) {
		zrt_abort("send on a closed channel");
	}
	/* a waiting receiver takes the value directly (rendezvous / buffered hand-off). */
	zrt_waiter *r = wq_pop(&ch->recvq_head, &ch->recvq_tail);
	if (r != NULL) {
		memcpy(r->val, val, ch->elemsz);
		r->done = true;
		zrt_sched_wake(r->co);
		return;
	}
	/* buffered with room: enqueue and return without blocking. */
	if (ch->len < ch->cap) {
		ring_put(ch, val);
		return;
	}
	/* full (or unbuffered with no receiver): park until a receiver takes the value. */
	zrt_waiter w = {zrt_sched_current(), (void *)val, false, NULL};
	wq_push(&ch->sendq_head, &ch->sendq_tail, &w);
	zrt_sched_park();
	if (!w.done) {
		/* woken without a taker: the channel closed under us. */
		zrt_abort("send on a closed channel");
	}
}

int zrt_chan_recv(zrt_chan *ch, void *out) {
	for (;;) {
		/* a buffered value is available: take it, then let a parked sender fill the
		 * freed slot so a full buffer keeps flowing. */
		if (ch->len > 0) {
			ring_get(ch, out);
			zrt_waiter *s = wq_pop(&ch->sendq_head, &ch->sendq_tail);
			if (s != NULL) {
				ring_put(ch, s->val);
				s->done = true;
				zrt_sched_wake(s->co);
			}
			return 0;
		}
		/* no buffered value but a parked sender: take its value directly (unbuffered
		 * rendezvous, or a buffered channel whose sender parked on a full buffer). */
		zrt_waiter *s = wq_pop(&ch->sendq_head, &ch->sendq_tail);
		if (s != NULL) {
			memcpy(out, s->val, ch->elemsz);
			s->done = true;
			zrt_sched_wake(s->co);
			return 0;
		}
		/* empty and closed: the Right of Result[T] (StopIteration or a crash Err). */
		if (ch->closed) {
			return 1;
		}
		/* empty and open: park until a sender hands off or the channel closes. */
		zrt_waiter w = {zrt_sched_current(), out, false, NULL};
		wq_push(&ch->recvq_head, &ch->recvq_tail, &w);
		zrt_sched_park();
		if (w.done) {
			return 0; /* a sender rendezvoused straight into *out. */
		}
		/* woken by close (or spuriously): loop to re-check buffer/senders/closed. */
	}
}

const char *zrt_chan_err(zrt_chan *ch) {
	return ch->err;
}
