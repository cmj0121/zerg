package build

// v0.12 Unit 3 — channel runtime on park/unpark.
//
// Replaces the v0.7 chan runtime (pthread mutex + cv_send / cv_recv condvar
// pair) with a coroutine-aware variant: blocking ops park the calling
// coroutine on the channel's wait queue, freeing the underlying OS worker
// to run other coroutines. The chan struct keeps the same buffer + counters
// shape as v0.7's emit so the U6 cgen swap is a body-only change.
//
// The U3 source below is a concrete int64_t specialisation; the U6 cgen
// templates per-element-type (`zerg_chan_str`, `zerg_chan_<MyStruct>`, …)
// using the same algorithm but with the right element type / copy fn.
//
// Wait-queue protocol:
//
//   - Each waiting coroutine pushes a zerg_chan_wait_node_t holding its
//     own coroutine pointer, a pointer to the value slot (sender's source
//     OR receiver's destination), and a `closed_flag` that close()
//     stamps on every parked node before unparking.
//
//   - Send: with the chan mutex held, hand the value directly to a waiting
//     receiver if one exists; else push to the buffer if there's room;
//     else stage a wait_node and call zerg_coro_park(&ch->mu) — the
//     deferred-unlock mechanism in zerg_coro_park guarantees the chan
//     mutex is released only AFTER the parker's ucontext is fully saved,
//     so an unparker that subsequently acquires the mutex never queues
//     the coro before its ctx is ready.
//
//   - Recv: pop from buffer if non-empty (and pull a parked sender into
//     the buffer to maintain progress), else hand off directly from a
//     parked sender, else return None on closed, else park.
//
//   - Close: with the mutex held, drain both wait queues — set
//     closed_flag on every parked sender (they panic on resume) and on
//     every parked receiver (they re-check, see closed + empty, return
//     None) and unpark each.
//
// The patterns above mirror Go's channel runtime; the only Zerg-specific
// twist is the v0.12 deferred-unlock (because the cooperative scheduler
// cannot Go-style park-via-mcall and instead piggybacks the unlock on the
// worker's swap-back).

const chanRuntimeC = `
/* ---------------- channel — int64_t specialisation ----------------------
   U6 generates one of these per chan element type via the cgen template
   (matching the v0.7 emit pattern). U3's hardcoded int64_t version
   below stays for the targeted unit test that compiles a stand-alone
   driver against the runtime; cgen_v07's chan emitter produces
   semantically-equivalent helpers per element type. The wait-node
   primitive (zerg_chan_wait_node_t / push / pop) lives in
   schedRuntimeC so the per-element-type emitter and the wait_group
   runtime can share it. */
typedef struct zerg_chan_int64_t {
    pthread_mutex_t mu;
    int64_t *buf;
    int64_t cap;
    int64_t count;
    int64_t head;
    int64_t tail;
    int     closed;
    zerg_chan_wait_node_t *send_head, *send_tail;
    zerg_chan_wait_node_t *recv_head, *recv_tail;
} zerg_chan_int64_t;

static zerg_chan_int64_t *zerg_chan_int64_t_make(int64_t cap) {
    zerg_chan_int64_t *ch =
        (zerg_chan_int64_t *)calloc(1, sizeof(*ch));
    pthread_mutex_init(&ch->mu, 0);
    int64_t slots = cap > 0 ? cap : 0;
    if (slots > 0) ch->buf = (int64_t *)malloc((size_t)slots * sizeof(int64_t));
    ch->cap = cap;
    return ch;
}

static void zerg_chan_int64_t_send(zerg_chan_int64_t *ch, int64_t v) {
    pthread_mutex_lock(&ch->mu);
    if (ch->closed) {
        pthread_mutex_unlock(&ch->mu);
        fprintf(stderr, "zerg: runtime: send on closed channel\n");
        exit(1);
    }
    /* Direct hand-off to a waiting receiver. */
    zerg_chan_wait_node_t *r = zerg_chan_wait_pop(&ch->recv_head, &ch->recv_tail);
    if (r) {
        *(int64_t *)r->value_ptr = v;
        zerg_coro_t *target = r->coro;
        pthread_mutex_unlock(&ch->mu);
        zerg_coro_unpark(target);
        return;
    }
    /* Buffer push. */
    if (ch->count < ch->cap) {
        ch->buf[ch->tail] = v;
        ch->tail = (ch->tail + 1) % ch->cap;
        ch->count++;
        pthread_mutex_unlock(&ch->mu);
        return;
    }
    /* Buffer full / unbuffered with no receiver: park. */
    zerg_chan_wait_node_t node;
    node.coro = zerg_current_coro;
    node.value_ptr = &v;
    node.closed_flag = 0;
    zerg_chan_wait_push(&ch->send_head, &ch->send_tail, &node);
    /* Park hands the mutex unlock to the worker post-swap so an unparker
       that subsequently acquires the mutex never queues us before our
       ucontext is fully saved. */
    zerg_coro_park(&ch->mu);
    /* Resumed: either a receiver consumed v (success) or close drained
       us (panic). */
    if (node.closed_flag) {
        fprintf(stderr, "zerg: runtime: send on closed channel\n");
        exit(1);
    }
}

/* zerg_opt_int64_t mirrors the v0.6 Option[int] enum the cgen emits for
   recv. tag=0 Some, tag=1 None; payload.p0.a0 holds the int64_t for
   Some. U3 hard-codes the layout; U6's generic emit reuses whatever
   Option[T] tag layout the cgen already produces for the chan element
   type — receiver helpers take the option-typedef's mangled name. */
typedef struct {
    int tag;
    union {
        struct { int64_t a0; } p0;
    } payload;
} zerg_opt_int64_t;

static zerg_opt_int64_t zerg_chan_int64_t_recv(zerg_chan_int64_t *ch) {
    pthread_mutex_lock(&ch->mu);
    /* Buffer pop with possible parked-sender handoff to keep the queue
       full as long as senders are waiting. */
    if (ch->count > 0) {
        int64_t v = ch->buf[ch->head];
        ch->head = (ch->head + 1) % ch->cap;
        ch->count--;
        zerg_chan_wait_node_t *s = zerg_chan_wait_pop(&ch->send_head, &ch->send_tail);
        if (s) {
            ch->buf[ch->tail] = *(int64_t *)s->value_ptr;
            ch->tail = (ch->tail + 1) % ch->cap;
            ch->count++;
            zerg_coro_t *target = s->coro;
            pthread_mutex_unlock(&ch->mu);
            zerg_coro_unpark(target);
        } else {
            pthread_mutex_unlock(&ch->mu);
        }
        zerg_opt_int64_t out;
        out.tag = 0;
        out.payload.p0.a0 = v;
        return out;
    }
    /* Empty buffer: direct handoff from a parked sender (only fires for
       unbuffered channels — buffered channels with parked senders would
       have count > 0). */
    zerg_chan_wait_node_t *s = zerg_chan_wait_pop(&ch->send_head, &ch->send_tail);
    if (s) {
        int64_t v = *(int64_t *)s->value_ptr;
        zerg_coro_t *target = s->coro;
        pthread_mutex_unlock(&ch->mu);
        zerg_coro_unpark(target);
        zerg_opt_int64_t out;
        out.tag = 0;
        out.payload.p0.a0 = v;
        return out;
    }
    /* Closed: drained. */
    if (ch->closed) {
        pthread_mutex_unlock(&ch->mu);
        zerg_opt_int64_t out;
        out.tag = 1;
        return out;
    }
    /* Park. */
    int64_t slot = 0;
    zerg_chan_wait_node_t node;
    node.coro = zerg_current_coro;
    node.value_ptr = &slot;
    node.closed_flag = 0;
    zerg_chan_wait_push(&ch->recv_head, &ch->recv_tail, &node);
    zerg_coro_park(&ch->mu);
    /* Resumed: a sender wrote slot, or close stamped closed_flag. */
    zerg_opt_int64_t out;
    if (node.closed_flag) {
        out.tag = 1;
        return out;
    }
    out.tag = 0;
    out.payload.p0.a0 = slot;
    return out;
}

static void zerg_chan_int64_t_close(zerg_chan_int64_t *ch) {
    pthread_mutex_lock(&ch->mu);
    if (ch->closed) {
        pthread_mutex_unlock(&ch->mu);
        fprintf(stderr, "zerg: runtime: close on already-closed channel\n");
        exit(1);
    }
    ch->closed = 1;
    /* Drain parked senders — each panics on resume via closed_flag. */
    zerg_chan_wait_node_t *s;
    while ((s = zerg_chan_wait_pop(&ch->send_head, &ch->send_tail)) != 0) {
        s->closed_flag = 1;
        zerg_coro_t *t = s->coro;
        zerg_coro_unpark(t);
    }
    /* Drain parked receivers — each returns None on resume via closed_flag. */
    zerg_chan_wait_node_t *r;
    while ((r = zerg_chan_wait_pop(&ch->recv_head, &ch->recv_tail)) != 0) {
        r->closed_flag = 1;
        zerg_coro_t *t = r->coro;
        zerg_coro_unpark(t);
    }
    pthread_mutex_unlock(&ch->mu);
}
`
