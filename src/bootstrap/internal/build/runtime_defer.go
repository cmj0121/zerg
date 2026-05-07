package build

// v0.12 Unit 5 — per-coroutine defer + main coroutine.
//
// v0.7's defer is a per-OS-thread stack (zerg_defer_*) keyed off
// _Thread_local storage. Under v0.12's M:N scheduler that breaks: a
// coroutine that pushes a defer on worker A and migrates to worker B
// would find the defer in A's stack but nothing in B's.
//
// U5 moves the defer stack into the coroutine struct. The coro entry
// stub (zerg_coro_entry from U1) walks the stack in LIFO order on
// DONE, running each fn(env) before swapping back to the worker.
//
// Main-coroutine wrapper:
//   v0.7 emits user `main()` body directly into C `main()`, with a
//   separate per-thread defer stack for top-level defers. v0.12 wraps
//   the user's main body in a coroutine spawned by C main(); the OS
//   main thread initialises the scheduler, spawns the main coro, then
//   drains. This unifies the defer machinery (every user-visible fn
//   body — including main — runs as a coroutine with its own defer
//   stack) and keeps park/yield available in main's body.
//
// The U6 cgen will route both regular spawn and the main-coro spawn
// through this same primitive.

const deferRuntimeC = `
/* zerg_coro_defer pushes (fn, env) onto the current coroutine's defer
   stack. The node is heap-allocated; zerg_coro_entry walks and frees
   the stack on the way out. Calling outside a coroutine context aborts
   — defers are meaningful only against a coroutine's lifecycle, not
   the OS thread. Heap (rather than stack) allocation is deliberate: the
   user pattern "for v in xs { defer cleanup(v) }" pushes one node per
   loop iteration, and we want the defer to outlive the loop body's
   stack frame. The struct layout (zerg_defer_node_t) lives in
   coroRuntimeC so zerg_coro_t can hold a strongly-typed head pointer
   and zerg_coro_entry can walk the stack without an extra dependency. */
static void zerg_coro_defer(void (*fn)(void *), void *env) {
    zerg_coro_t *c = zerg_current_coro;
    if (!c) {
        fprintf(stderr, "zerg: zerg_coro_defer called outside a coroutine\n");
        abort();
    }
    zerg_defer_node_t *node =
        (zerg_defer_node_t *)malloc(sizeof(zerg_defer_node_t));
    node->fn = fn;
    node->env = env;
    node->next = c->defer_head;
    c->defer_head = node;
}
`

// DeferRuntimeC exposes the U5 defer source so the targeted test can
// compile a driver against it. U6 will fold it into the v0.12 prelude
// emit path.
func DeferRuntimeC() string { return deferRuntimeC }
