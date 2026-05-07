package build

// v0.12 Unit 4 — wait_group on park/unpark.
//
// Replaces v0.7's pthread mutex + condvar wait_group with a coroutine-aware
// variant: zerg_waitgroup_wait parks the caller on the wait_group's wait
// queue, freeing the underlying OS worker. zerg_waitgroup_add (incl.
// done == add(-1)) drains the wait queue and unparks every waiter when the
// counter transitions to zero. The wait-node layout is intentionally the
// same shape as the chan wait node so the U6 cgen can share helpers.
//
// Surface stays compatible with v0.7's WaitGroup-method emission so U6's
// cgen integration is a body-only change: add / done / wait.

const waitgroupRuntimeC = `
typedef struct zerg_waitgroup {
    pthread_mutex_t mu;
    int64_t counter;
    zerg_chan_wait_node_t *wait_head, *wait_tail;
} zerg_waitgroup_t;

static zerg_waitgroup_t *zerg_waitgroup_make(void) {
    zerg_waitgroup_t *wg = (zerg_waitgroup_t *)calloc(1, sizeof(*wg));
    pthread_mutex_init(&wg->mu, 0);
    return wg;
}

static void zerg_waitgroup_add(zerg_waitgroup_t *wg, int64_t delta) {
    pthread_mutex_lock(&wg->mu);
    wg->counter += delta;
    if (wg->counter < 0) {
        pthread_mutex_unlock(&wg->mu);
        fprintf(stderr, "zerg: runtime: wait_group counter went negative\n");
        exit(1);
    }
    if (wg->counter == 0) {
        /* Drain wait queue — all waiters resume. */
        zerg_chan_wait_node_t *n;
        while ((n = zerg_chan_wait_pop(&wg->wait_head, &wg->wait_tail)) != 0) {
            zerg_coro_t *t = n->coro;
            zerg_coro_unpark(t);
        }
    }
    pthread_mutex_unlock(&wg->mu);
}

static void zerg_waitgroup_done(zerg_waitgroup_t *wg) {
    zerg_waitgroup_add(wg, -1);
}

static void zerg_waitgroup_wait(zerg_waitgroup_t *wg) {
    pthread_mutex_lock(&wg->mu);
    if (wg->counter == 0) {
        pthread_mutex_unlock(&wg->mu);
        return;
    }
    zerg_chan_wait_node_t node;
    node.coro = zerg_current_coro;
    node.value_ptr = 0;
    node.closed_flag = 0;
    zerg_chan_wait_push(&wg->wait_head, &wg->wait_tail, &node);
    /* Park hands the mu unlock to the worker post-swap so an unparker
       (zerg_waitgroup_add fn dropping counter to zero) never observes
       this coro on the wait queue before its ctx is fully saved. */
    zerg_coro_park(&wg->mu);
    /* Resumed: the counter hit zero and unpark fired. Nothing to check —
       wait_group has no closed/closed_flag analogue. */
}
`
