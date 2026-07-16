# Zerg Coroutines & Channels

Concurrency in Zerg is **coroutines + channels only** — no shared mutable state, no locks, no
futures, no join/handle. It builds on the memory and error models in the
[Language Reference](language.md). Also in [繁體中文](coroutine.zh-TW.md).

## `spawn`

`spawn f(args)` starts a coroutine (Go's `go`) on an **M:N scheduler**. It **returns nothing** — no
handle, no join/await; results and completion are observed **only through channels**.

- **Fire-and-forget** — the runtime never tracks or joins the coroutine; to learn an outcome it must
  send it over a channel the observer holds.
- **Captures are restricted** to **immutable values and `Ref` values** (channels, `Ref[T]`) — a `mut`
  reference cannot cross `spawn`, so coroutines never share mutable Zerg state (no data races). What
  crosses a boundary, and how, is **Sharing & the memory model**, next.

## Sharing & the memory model

Because a coroutine boundary copies everything except `Ref` values, Zerg needs **no elaborate memory
model** — there is no shared mutable state to race over. One ordering guarantee is observable, and the
rest follows from it:

> **A `send` on a channel happens-before the matching `receive` completes.**

The receiver sees the payload fully built (it was snapshotted at the send); no other cross-coroutine
ordering exists or is needed. That is the whole memory model.

**What may cross a boundary** — as a `spawn`/closure capture or a channel payload:

- an **immutable value** — copied in (move-optimized when the source is dead);
- a **`Ref` value** (a channel, or a `Ref[T]`) — shared by reference, refcount-bumped;
- a **mutable, non-`Ref` value** — never shared; copied if sent.

So the invariant is exact: **no shared mutable _Zerg_ state.** A `Ref[T]` shared across coroutines
exposes only a **read-only view** — reads and non-`mut` methods, never a `mut` binding — so concurrent
readers need no lock. Anything that must be **mutated** under sharing is not shared: one coroutine owns
it (the actor pattern, below).

A `Ref[T]` over a **foreign handle** (see [FFI](ffi.md)) is the one case Zerg cannot police — it cannot
see the resource's C-side state, so that resource's thread-safety is the foreign library's concern,
outside this model. The safe default is the same: give the `Ref[handle]` to **one** owner coroutine.

## Channels

A channel is a typed, by-ref conduit whose payloads are **copied** through it. It is a
**reference-counted value** — the built-in implementer of `Ref` (alongside `Ref[T]`; see
[Language Reference](language.md)), the exception to scope-owning: freed when its last holder's scope
exits, and copying a value refcount-bumps any `Ref` value it contains while deep-copying the rest. A
channel is **FIFO** and **first-class** (it can be sent over another channel).

```text
ch := chan[int]()      # unbuffered — every send rendezvous with a receive
ch := chan[int](64)    # buffered, capacity 64
```

Capacity is the only knob; **send blocks when full, receive blocks when empty**. Unbuffered
(capacity 0) is a rendezvous — the send completes only when a receiver takes the value, and is Zerg's
one synchronization primitive.

### Send — `ch <- v`

Send is **asymmetric** with receive: closing is the producer's decision, so it always knows. Send
never yields a value — it completes, blocks, or aborts:

| Channel state                                   | `ch <- v`                                                    |
| ----------------------------------------------- | ------------------------------------------------------------ |
| open, can proceed (room, or a waiting receiver) | completes; the value is **snapshotted at send**              |
| open, cannot proceed yet (full, or no receiver) | **blocks** — a not-yet-received channel is valid, not a bug  |
| closed                                          | **aborts** (`SendOnClosedError`) — see [Aborts](language.md) |

### Receive — `<-ch`

Receive yields **`Result[T]`**: `Left(v)` is a value; `Right(err)` means the channel is **closed and
drained**, `err` being _why_ — so a crash reason is never lost. It never means "empty, wait" (that
blocks).

| Channel state                            | `<-ch : Result[T]`                                  |
| ---------------------------------------- | --------------------------------------------------- |
| open, has a value                        | `Left(v)`                                           |
| open, empty                              | **blocks**                                          |
| closed, values still buffered            | `Left(v)` — **drains first**, no data lost          |
| closed clean (last sender left normally) | `Right(StopIteration)` — the end-of-stream sentinel |
| closed by crash (last sender aborted)    | `Right(err)` — the propagated crash `Err`           |

Most code treats **any `Right` as "stop"**, inspecting `err` only when it needs the reason. Every
need falls out of existing operators — the **receiver** chooses:

```text
v := <-ch?                 # propagate the close reason up (a crash cascades)
v := <-ch!                 # force: a crash Err re-raises as an abort here
v := <-ch ?? fallback      # default on any close
if v := <-ch { … }         # run the block only on a value (Left)
loop { v := <-ch ?? break }              # drain until any close
match <-ch { Left(v) -> use(v)  Right(e) -> report(e) }
```

Because closed is the `Right` side, `chan[U?]` is unambiguous: a **sent `nil`** is `Left(nil)`, a
**closed** channel is `Right`.

## Closing — automatic, on the last sender

Zerg has **no explicit `close`**. A channel closes when its **last send-capable holder's scope
exits** — the refcount is split by direction:

- **send-count → 0 ⇒ close** (receivers still drain what is buffered),
- **no holders ⇒ free**.

Normal completion and a crash close through the **same** path: an aborting producer's unwind drops
its send end (decrementing channel refcounts), and if it was the last sender the channel closes —
**with a reason**: `StopIteration` for a clean exit, the crashing `Err` for an abort. The receiver reads
that as the `Right` above, so a crash reaches the consumer as an ordinary error, never an orphaned
channel it blocks on forever.

**Early close = narrow the scope** — to signal "no more values" before the producer's scope ends, put
its send end in a tighter block:

```text
{
    out := ch.send
    produce(out)
}              # out's scope ends → auto-close (if last sender)
cleanup()
```

`del ch` does the same directly — dropping your hold now closes the channel if you were its last sender,
without a tighter block (see the [Language Reference](language.md)).

### The send-coverage invariant

Auto-close is **level-triggered**: it fires the instant send-count hits 0, with no notion that "a
sender is coming later." Hence one rule:

> From creation until you mean _no more sends ever_, **at least one send-capable holder must exist at
> every instant.**

A send end held by a coroutine keeps the send side alive for that coroutine's whole life, sleeping or
not; the only failure is a _gap_ with no send end at all.

```text
ch := chan[int]()
spawn consumer(ch.recv)     # the creator still holds bidirectional `ch`
... delay ...               # SAFE: the creator's end keeps send-count ≥ 1 across the delay
spawn producer(ch.send)
```

Safe: the consumer just **blocks** while the creator holds a send-capable end. It breaks only if you
**drop your own send end** _then_ delay before the producer exists — that gap closes the channel
early and the late send aborts. Rule of thumb: **release your own send end last** (as with Rust's
`mpsc`).

## Directional channels

A bare `chan[T]` is **bidirectional**. It **narrows** to a one-way end — **send-only** (`ch <- v`,
for a producer) or **receive-only** (`<-ch`, for a consumer).

**Narrowing is one-way**, never back to bidirectional — the safety guarantee: a send-only end
**cannot** receive (steal) values, a receive-only end cannot inject. It is a safe built-in upcast at
a directional-typed target (parameter, `return`, typed binding); an explicit narrow (`ch.send` /
`ch.recv`) drops your own bidirectional contribution.

Direction is also what makes auto-close **precise**, since the refcount is per direction: send-only
counts toward send-count, receive-only toward receive-count, bidirectional toward **both**. So a
consumer that must see "producer done" holds a **receive-only** end — a bidirectional consumer counts
as a sender and would hold the channel open forever. Bidirectional ends suit **symmetric-lifetime**
uses (a self-buffer, a shared worker-pool channel), where close and free coincide at the last
participant. A two-way dialogue uses **two** directional channels — one shared bidirectional channel
routes any value to any receiver, a race, not a conversation.

## `select` — waiting on many channels

`select` is the **only** multi-way wait: it watches several send/receive operations, blocks until one
is **ready**, and runs that arm; ties are broken **fairly** (not by position, so no arm starves).

```text
select {
    v := <-a -> use(v)      # receive arm: ready when open with a value
    b <- x   -> sent()      # send arm: ready when the send can proceed
    done     -> break       # all watched receive channels closed → fires once
    _        -> tick()      # nothing ready now → non-blocking
}
```

A receive arm binds the same `Result[T]` as a plain receive — `Left(v)` on a value, `Right(err)` on a
crash close:

- A **cleanly** closed receive arm is **dropped** (never fires, never spins) and feeds `done`; a
  **crash**-closed arm instead **surfaces** `Right(err)` — a crash is never silently dropped.
- A **send arm** on a closed channel **aborts** when chosen (send-on-closed is a bug).
- **`done`** fires **once** when every watched receive channel has closed (the select is _exhausted_)
  — clean fan-in termination without a join or a manual countdown.
- **`_`** fires when no arm is ready now, making `select` non-blocking.
- All closed with **no `done` and no `_`** → **aborts** (`DeadlockError`), a safety net for a
  forgotten shutdown. `done` precedes `_`.

The granularity is deliberate: a single receive surfaces closure per value; `select` aggregates a
clean close into one `done` while still surfacing a crash — so a clean close never joins the "has
data" race and nothing spins.

## Timers & cancellation

**Timeouts** and **cancellation** both fall out of channels and `select` — no new primitive.

- **A timer is a channel.** A stdlib `after(d)` yields a receive-only channel that becomes ready **once**
  after a duration `d` (`ticker(d)` fires repeatedly); a `select` receive arm on it is a **timeout**. `d`
  is a stdlib duration and the clock is an ambient-OS stdlib facility (like `env`), never a primitive.
- **Cancellation is a channel.** Hand a coroutine a **cancel channel** to watch in its `select`; the
  canceller closes it and the coroutine sees that arm fire and bails. Because `spawn` is fire-and-forget
  with **no handle, there is no preemptive kill** — cancellation is **cooperative**: a coroutine ends
  only by returning, or by observing a cancel or timeout arm and choosing to stop.

```text
select {
    v := <-work           -> handle(v)   # real work
    _ := <-after(timeout) -> stop()      # timeout — the timer channel became ready
    _ := <-cancel         -> stop()      # cancellation — someone closed `cancel`
}
```

## Shared state — the actor pattern

Zerg has no locks and no shared mutable state, yet real programs need coordinated mutable state — a
counter, a cache, a registry. The answer is a **pattern**, not a new primitive: an **actor** is a
coroutine that **exclusively owns** some `mut` state, reachable only by messages on a channel. One
coroutine drains its mailbox one message at a time, so writes **serialize with no lock**, and since no
one else holds the state there is no data race.

```text
enum Cmd {
    Add(int),                 # a write
    Get(chan[int].send),      # a read — carries a reply channel
}

fn counter(inbox: chan[Cmd].recv) {
    mut n := 0                       # the state: a plain mut int, owned here alone
    loop cmd in inbox {              # drains until the last sender leaves
        match cmd {
            Add(d)   -> n = n + d    # the write happens inside the owner
            Get(rep) -> rep <- n     # reply on the caller's channel
        }
    }
}
```

- **tell** (fire-and-forget) is a plain send — `inbox <- Add(5)`.
- **ask** (request-reply) sends a fresh reply channel and blocks on it —
  `rep := chan[int]();  inbox <- Get(rep.send);  v := <-rep!`.
- **Teardown is automatic** — when the last client drops its send end, `inbox` closes, the `loop` ends,
  and the owner's `mut` state is freed; the ordinary channel-close and scope-owned rules, nothing added.

The inbox is a `Ref` value, so **sharing the actor is sharing the inbox** (refcount-bumped) — every copy
and coroutine that holds it talks to the one owner. This, not a `Ref[T]`, is how mutable state is shared:
a `Ref[T]` shares a value **read-only**, an actor **serializes writes** behind an owner. A resource that
must be serialized (a non-thread-safe `Ref[handle]`) is likewise owned by one actor.

## Unhandled aborts

An abort never caught by `guard` (see the error model) **kills only that coroutine** — its stack
unwinds (freeing scopes, decrementing channel refcounts) while everything else runs on. This is
fire-and-forget, but the failure is **not lost**: closing a channel as the last sender carries the
crash `Err`, which the consumer reads as `Right(err)` (a clean finish carries `StopIteration` instead).

At the source the death is otherwise **silent**. An **optional compiler flag** additionally reports
each unhandled abort — the `Err`, the coroutine, a backtrace — to `stderr`. It is **purely
observational**: behaviour is identical with or without it, and default builds carry no overhead.

For a _structured_ outcome — partial results, a specific error, or a failure that would not otherwise
close a watched channel — the coroutine still `guard`s and sends over a channel. Making a death
_fatal_ is the observer's job (react to `Right(err)` and abort), never the `spawn`'s.

## Scheduling & fairness

The M:N scheduler is **fair**: every **ready** coroutine eventually runs, and **no coroutine can
indefinitely starve others** — not even a CPU-bound one that never touches a channel. You may `spawn`
freely; a busy worker cannot freeze unrelated coroutines.

This is a guarantee about the **observable property, not the mechanism**. _How_ fairness is achieved —
preemption, compiler-inserted safepoints, reduction counting — is an implementation detail the language
does not fix; only the property is promised.

Two limits bound it:

- **A blocking `extern` call is not preemptible.** It parks its OS thread inside a C frame Zerg does not
  own (see [FFI](ffi.md)); fairness covers Zerg coroutines, not a thread stuck in C. The runtime may grow
  its thread pool, but a long blocking call is thread-occupying — prefer non-blocking C APIs.
- **Fairness moves the _ready_; it does not unstick the _blocked_.** When every coroutine is blocked with
  no possible progress that is a deadlock, caught separately (below); the `select` tie-break is this same
  fairness applied to a single wait.

## Termination & deadlock

- **Program lifetime** — when the main stack returns, the **program ends**; still-running coroutines
  stop where they are and the OS reclaims everything. There is no join, so drive a coroutine to a
  channel-observed completion if it must finish before exit.
- **A send with no receivers just blocks** — even when the receive side is provably empty forever,
  Zerg does not abort it; waiting or bailing is the **caller's** call (e.g. a `select` with a cancel
  or timeout arm).
- **Global deadlock detection** — if every coroutine is blocked with no possible progress, the runtime
  raises **`DeadlockError`** rather than hanging. A lone blocked sender while others progress is not
  individually detected.
