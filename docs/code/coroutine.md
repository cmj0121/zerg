# Zerg Coroutines & Channels

Concurrency in Zerg is **coroutines + channels only** — no shared mutable state, no locks, no
futures, no join/handle. It builds on the memory and error models in the
[Language Reference](../language.md). Also in [繁體中文](coroutine.zh-TW.md).

## `spawn`

`spawn f(args)` starts a coroutine (Go's `go`) on the runtime scheduler (see Scheduling for
the **M:N** model, and why it is cooperative rather than preemptive). It **returns nothing** — no
handle, no join/await; you observe results and completion **only through channels**. The callee is any
call — a plain function, a **method** (`spawn obj.run()`), or a **namespaced** function (`spawn mod.work()`),
mirroring `defer`, which takes the same callee forms (`defer f.close()`).

> A **closure literal** is not one of the three callee forms, and is refused by name — `E9009
NotImplemented: calling fn-expr — a callee is a plain name in this compiler`. The reason is the callee
> shape and nothing about closures: a lambda **does** capture (`add := fn (x: int) -> int { return x + n }`
> reads `n` and runs), so an environment exists; what is missing is a call through anything but a name.
> Bind the closure to a name and `spawn` it by that name.

- **Arguments are a snapshot** — taken where the `spawn` is **written**, not where the call runs. A
  `mut` binding written afterwards is not seen by the coroutine, which may not have started; a `list`,
  `map` or `struct` becomes the coroutine's **own value** at that point. (For a `list` that is realized as
  copy-on-write, so the capture costs an increment and the buffer is duplicated by whichever side writes
  first — an implementation detail, not a weaker guarantee: see [Values & Memory](../core/memory.md).)
  A **channel** is the contrast and the point: it is a
  **handle**, so the coroutine gets its own handle to the same channel and everything sent afterwards
  **is** seen. `defer` captures the same way, at the `defer`. **Values are snapshotted, handles are
  shared** — `zerg lint` says so as `L301`.
- **Fire-and-forget** — the runtime never tracks or joins the coroutine; to learn an outcome it must
  send it over a channel the observer holds.

  > **[not yet]** A program that `spawn`s cannot also take command-line arguments: `fn main(args: list[str])`
  > beside any `spawn` is _E9097 NotImplemented: main(args) in a program that uses concurrency_. `main` runs
  > as **coroutine 0**, and every scheduler entry shim takes a nullary function pointer, so there is nowhere
  > to thread `args` through; a concurrent program reads its configuration from the environment or a file
  > until one exists.

- **Captures are restricted** to **immutable values and `Ref` values** (channels, `Ref[T]`) — a `mut`
  reference cannot cross `spawn`, so coroutines never share mutable Zerg state (no data races). What
  crosses a boundary, and how, is **Sharing & the memory model**, next.

### The one thing not scope-owned

Everything else in Zerg is **scope-owned** — a value, a `defer`, a `Ref[T]` resource — cleaned up
deterministically when its scope exits. A **coroutine is the single, deliberate exception.** `spawn` cuts the
child loose: its lifetime is **not** tied to the scope that started it — it may outlive that scope or finish
long before, and no parent waits on it.

This is the whole point of fire-and-forget, and a **choice, not an omission**. Binding a coroutine's lifetime
to its spawning scope is exactly **structured concurrency** (a nursery that joins its children); Zerg declines
it to keep `spawn` handle-less and the model small. The costs are accepted and explicit: **no join, no
parent-waits, no automatic failure propagation** — coordination is the caller's, always through channels. A
child's failure reaches others only as the `Err` its channel close raises at their next receive (see
Unhandled aborts); at program end, still-running coroutines are simply no longer scheduled (see
Termination & deadlock).

A scope-owned _value_ may still **signal** a coroutine — a resource whose `drop` **sends** on a cancel
channel the coroutine watches — but that is cooperative signalling, not ownership: the coroutine observes
the value and _chooses_ to stop, and stays free to ignore it. The coroutine itself is never reclaimed by a
scope. (It must be a send, not a close: a cleanly closed receive arm is dropped rather than fired — see
`select`.)

## Sharing & the memory model

Because a coroutine boundary copies everything except `Ref` values, Zerg needs **no elaborate memory
model** — there is no shared mutable state to race over. One ordering guarantee is observable, and the
rest follows from it:

> **A `send` on a channel happens-before the matching `receive` completes.**

The receiver sees the payload fully built
(it was snapshotted at the send); no other cross-coroutine ordering exists or is needed. That is the
whole memory model. Any ordering **beyond** this edge — the run-queue order in which ready coroutines are
resumed, the interleaving of unsynchronized coroutines — is **[implementation-defined]**: today several
worker threads drain one shared FIFO run queue, so a coroutine may resume on a different worker each
time and nothing about the interleaving is repeatable. No program may rely on it.

**What may cross a boundary** — as a `spawn`/closure capture or a channel payload:

- an **immutable value** — copied in (move-optimized when the source is dead);
- a **`Ref` value** (a channel, or a `Ref[T]`) — shared by reference, refcount-bumped;
- a **mutable, non-`Ref` value** — never shared; copied if sent.

So the invariant is exact: **no shared mutable _Zerg_ state.** A `Ref[T]` shared across coroutines
exposes only a **read-only view** — reads and non-`mut` methods, never a `mut` binding — so concurrent
readers need no lock. Anything that must be **mutated** under sharing is not shared: one coroutine owns
it (the actor pattern, below).

A `Ref[T]` over a **foreign handle** (see [FFI](../runtime/ffi.md)) is the one case Zerg cannot police — it cannot
see the resource's C-side state, so that resource's thread-safety is the foreign library's concern,
outside this model. The safe default is the same: give the `Ref[handle]` to **one** owner coroutine.

## Channels

A channel is a typed, by-ref conduit whose payloads are **copied** through it. It is a
**reference-counted value** — the built-in implementer of `Ref` (alongside `Ref[T]`; see
[Values & Memory](../core/memory.md)), the exception to scope-owning: freed when its last holder's scope
exits, and copying a value refcount-bumps any `Ref` value it contains while deep-copying the rest. A
channel is **FIFO** and **first-class**: it can be held in a struct field, carried as an enum
payload — which is how an actor's ask carries its reply channel — and sent over another
channel.

```text
ch := chan[int]()      # unbuffered — every send rendezvous with a receive
ch := chan[int](64)    # buffered, capacity 64
```

Capacity is the only knob; **send blocks when full, receive blocks when empty**. Unbuffered
(capacity 0) is a rendezvous — the send completes only when a receiver takes the value, and is Zerg's
one synchronization primitive.

The whole channel core in this chapter — buffered and unbuffered blocking, a close signalling the
receiver, a send on a closed channel aborting, the last sender auto-closing, and a payload
**deep-copied at the send**, so a receiver never shares the sender's storage.
Both channel error kinds are **reified and nameable**: a send on a closed channel raises
`SendOnClosedError`, `DeadlockError` is the clean, catchable abort described under Termination &
deadlock, and each answers an ordinary `err is …` test (see [Errors](errors.md)).

**`StopIteration` is nameable but deliberately not constructible.** `err is StopIteration` is a legal
test, but **no** program can `raise StopIteration(…)` — _E4063_ in **both** compilers ([Errors](errors.md)).
The sentinel is the runtime's own end-of-stream marker: a sender able to raise it would close its channel
wearing that marker, and its consumer would read a crash as a clean finish. Nothing a receiver sees answers
the test today either, because the receive below already tells a clean end from a crash by **which of the
two routes** it arrives on.

### Send — `ch <- v`

Send is **asymmetric** with receive: closing is the producer's decision, so it always knows. Send
never yields a value — it completes, blocks, or aborts:

| Channel state                                   | `ch <- v`                                                   |
| ----------------------------------------------- | ----------------------------------------------------------- |
| open, can proceed (room, or a waiting receiver) | completes; the value is **snapshotted at send**             |
| open, cannot proceed yet (full, or no receiver) | **blocks** — a not-yet-received channel is valid, not a bug |
| closed                                          | **aborts** (`SendOnClosedError`) — see [Aborts](errors.md)  |

### Receive — `<-ch`

Receive yields **`T?`**. The two ways a stream can end are two different things in this language,
and each already had its own: the stream **ending** is an **absence**, so it is `nil`; the producer
**dying** is a **failure**, so it is **raised**, carrying that producer's own `Err`. Nothing has to
ask which kind of ending it is holding, because the two do not arrive by the same route.

| Channel state                            | `<-ch : T?`                                           |
| ---------------------------------------- | ----------------------------------------------------- |
| open, has a value                        | the value                                             |
| open, empty                              | **blocks**                                            |
| closed, values still buffered            | the value — **drains first**, no data lost            |
| closed clean (last sender left normally) | `nil`, and `nil` every time after — the end is sticky |
| closed by crash (last sender aborted)    | **raises** the producer's `Err`, message and kind     |

That split is the whole reason the reason cannot be lost. A `Result` made the receiver ask _which_
kind of `Right` it was holding before it could tell an ending from a death; anyone who forgot lost
the death. Nobody can forget now.

`chan[T?]` is **refused** (`E4003`) for the same reason it is now unnecessary: `nil` would mean both the
value that was sent and the end of the stream, and no operator can tell those apart. Wrap it in a struct,
or agree on a sentinel.

It is the **type** that is refused, not one way of writing it: a parameter, a result, a struct field,
a typed binding, a `type` declaration, a `chan` nested inside another type and the constructor
`chan[T?](…)` are all the same type spelled in different places, and every one of them is turned
away. The same goes for a channel a generic makes — instantiating a `chan[T]` template with an
optional is refused where the specialization is formed.

A receive answering a **carrier** is also what decides how a receive **of a receive** is written.
[`GRAMMAR#recv-base`](../../GRAMMAR) is self-recursive — `recv-base ::= '<-' recv-base | primary` — so
`<-` may stand in front of another receive, and a `chan[chan[int]]` is an ordinary thing to build.
But the outer receive answers `chan[int]?`, and a **carrier is not a channel**, so the nesting opens
it in between:

```text
x := <-((<-cc)!)      # yes — the inner handle is insisted on, and x is an int?
x := <-(<-cc)         # refused — `<-ch` needs a channel, and chan[int]? is not one
```

That is not a rule about nesting. **Every** channel operation asks the same question of what it is
handed — `<-x`, `x <- v`, `close(x)`, and both kinds of `select` arm — and for anything that is not a
channel end the answer is one refusal, by name and with a place: _E4043 `<-ch` needs a channel, and
chan[int]? is not one_.

Every need falls out of the four operators `T?` already had — the **receiver** chooses:

```text
v := <-ch ?? fallback      # the stream is over → fallback
if v := <-ch { … }         # v is a T inside the block; else is the end
v := <-ch!                 # insist: abort if it is over
v := <-ch?                 # hand the absence back (this function answers a T? too)
for { v := <-ch ?? break }               # drain until the end
for v in ch { use(v) }                   # the same drain, ending where the stream does
```

A **crash** close is in none of those lines, and that is the point: it raises. A receiver that wants
to read the reason and keep going demotes it with `guard { <-ch }`, the same way it would any other
failure.

`guard { <-ch }` hands back `Right(err)` carrying the producer's own Err,
and the receiver runs on. That is the whole of what a crash close asks of a program — decide, once,
whether this stream ending badly is your business — and `guard` is where that decision is written.
`?` on a receive, meanwhile, now needs only what any `T?` needs: the absence is an ordinary optional,
so the `Result[T]`-in-a-signature problem that used to be noted here is off the channel path.

A **`select` arm's body is a statement**, and the difference from a `match` arm is worth keeping
straight: a `match` arm must **yield** the match's value, so a statement cannot be its whole body
([Control Flow](control-flow.md)), while a `select` yields nothing and its arm simply **runs**
(`GRAMMAR#select-arm`). So `break` is ordinary in a select arm, and a block is just one statement
among the ones allowed there.

## Closing — automatic on the last sender, `close(ch)` when it must be early

A channel closes **by itself** when its **last send-capable holder goes** — the refcount is split by
direction:

- **send-count → 0 ⇒ close** (receivers still drain what is buffered),
- **no holders ⇒ free**.

**A holder goes when its binding's scope exits**, on **every** path out — the end of the block, a
`return`, a `break` or `continue`, or an abort unwind.

That is the everyday form, and the **only** one a crashing producer can take: an abort never reaches a
statement, so the unwind dropping its send end is what carries the reason. Normal completion and a
crash therefore close through the **same** path — **with a reason**: `StopIteration` for a clean exit,
the crashing `Err` for an abort. The receiver reads that as the `Right` above, so a crash reaches the
consumer as an ordinary error, never an orphaned channel it blocks on forever.

### `close(ch)` — ending a stream early

`close(ch)` is the **conditional** form: end this stream _before_ my scope does. It is a **statement,
not a call** — `close` is a keyword, it names no function and yields no value, so it cannot be passed,
bound or spawned. `defer` takes it as its one non-expression form.

```text
close(ch)              # this stream is over
defer close(ch)        # …at the block's exit, on every path out including an abort unwind
```

It marks the **channel**, not a holder, and everything follows from that:

- **Idempotent by construction** — closing a closed channel changes nothing. No error, no abort, no
  bookkeeping to police.
- **No count moves**, so `ch` stays a perfectly good handle afterwards: still readable, still copyable.
- **Buffered values are still delivered** — a receive hands over what the channel holds before it
  answers the `Right`.
- **A send after it aborts** (`SendOnClosedError`) rather than being quietly dropped.
- **A receive-only end may not close** — a consumer must not end a stream on the producers' behalf. It
  is a compile error (_E5005 cannot close a receive-only channel_).

`close` does **not** replace auto-close, and two shapes say why. A **crashing** producer never reaches
any statement. And in **fan-in** the last of several producers to finish ends the stream with no
coordination at all — a channel-level close called by one sibling would end it for the others, which
is precisely the footgun this design avoids.

### Early close = narrow the scope

The third way needs no statement at all: put the send end in a tighter scope and let the exit close
it. Since scope exit now releases what a channel binding holds, the natural spelling is a **factory** —
create the channel, `spawn` the producer, and hand back a **receive-only** end:

```text
fn source(n: int) -> <-chan[int] {
    ch := chan[int](4)
    spawn producer(ch, n)
    return ch              # `ch`'s own hold ends with `source` — the producer is the last sender
}

for v in source(4) { use(v) }   # the caller was never a sender, so it cannot hold the stream open
```

This is the shape the rest of this chapter is written in, and the reason it works is the
per-direction refcount: the caller's end counts toward receive-count only, so the stream ends exactly
when the producer does.

`del ch` is **not** how you stop sending. `del` means one thing for every type — **revoke this name**
— so it drops this binding's hold _and_ makes any later use of `ch` a compile error (_`ch` is used
after del_); see [Values & Memory](../core/memory.md). Use it to give up a hold you are finished with,
never as a signal to a consumer.

### The send-coverage invariant

Auto-close is **level-triggered**: it fires the instant send-count hits 0, with no notion that "a
sender is coming later." So there's just one rule:

> From creation until you mean _no more sends ever_, **at least one send-capable holder must exist at
> every instant.**

A send end held by a coroutine keeps the send side alive for that coroutine's whole life, sleeping or
not; the only failure is a _gap_ with no send end at all.

```text
ch := chan[int]()
spawn consumer(ch)          # ch narrows to consumer's `<-chan[int]` param; creator still holds bidirectional `ch`
... delay ...               # SAFE: the creator's end keeps send-count ≥ 1 across the delay
spawn producer(ch)          # ch narrows to producer's `chan[int]<-` param
```

Safe: the consumer just **blocks** while the creator holds a send-capable end. It breaks only if you
**drop your own send end** _then_ delay before the producer exists — that gap closes the channel
early and the late send aborts. Rule of thumb: **release your own send end last** (as with Rust's
`mpsc`).

## Directional channels

A bare `chan[T]` is **bidirectional**. It **narrows** to a one-way end — **send-only** (`ch <- v`,
for a producer) or **receive-only** (`<-ch`, for a consumer).

**Narrowing is one-way**, never back to bidirectional — the safety guarantee: a send-only end
**cannot** receive (steal) values, a receive-only end cannot inject. A directional-typed target
(parameter, `return`, typed binding) **wraps** the end in the narrowed view — a position wraps a
value, never converts one ([Type System](../core/type-system.md)) — and does **not** drop your own
hold: the target gets its narrowed view while you keep your bidirectional end. A narrowed binding takes a
reference **of its own**, so ending its scope gives that reference back: to drop your own
contribution, end the binding's scope (the factory above, or a tighter block), `close(ch)` to end the
stream while keeping the handle, or `del ch` to give up the hold and the name together.

Direction is also what makes auto-close **precise**, since the refcount is per direction: send-only
counts toward send-count, receive-only toward receive-count, bidirectional toward **both**. So a
consumer that must see "producer done" holds a **receive-only** end — a bidirectional consumer counts
as a sender and would hold the channel open forever. Bidirectional ends suit **symmetric-lifetime**
uses (a self-buffer, a shared worker-pool channel), where close and free coincide at the last
participant. A two-way dialogue uses **two** directional channels — one shared bidirectional channel
routes any value to any receiver, a race, not a conversation.

## `select` — waiting on many channels

`select` is the **only** multi-way wait: it watches several send/receive operations, blocks until one
is **ready**, and runs that arm. Among **several ready arms** the winner is **[implementation-defined]** —
the spec fixes only that the choice is **not positional**, so no arm is starved by where it is written;
the intended property is **fairness**. The bootstrap realizes it with a deterministic **round-robin
rotor** (the same fairness applied to a single wait), but a conforming implementation may choose any
ready arm, so no program may depend on which one wins.

```text
select {                    # picks ONE ready arm and runs it
    v := <-a => use(v)      # receive arm: ready when open with a value; v is a T
    b <- x   => sent()      # send arm: ready when the send can proceed
    _        => tick()      # nothing ready now → non-blocking
}

for select {                # the same wait as a LOOP: one ready arm per round …
    v := <-a => use(v)
    v := <-b => use(v)
}                           # … and it ENDS when every watched receive channel has
```

**A select picks; it does not end.** Ending is the loop's job, and `for select` is the loop that owns
it — no terminal arm, no counter, no flag, and the exit sits in the head where a reader looks for it.

- A receive arm binds a plain **`T`**. An arm that fires has a value by construction: a **cleanly**
  closed channel is an absence, so its arm is **dropped from the wait** — it never fires and never
  competes, which is what stops a finished producer from starving a live one.
- **The binding belongs to its own arm**, and to nothing else: it is not in scope in another arm, nor
  after the `select`. That is why every arm above may call its value `v` — the same rule an if-let
  binding and a `for` loop variable each get, and the reason a name reaching past its arm is an
  ordinary _undefined name_.
- A **crash** close **raises** out of the select, carrying the producer's `Err`. It never reaches an
  arm body, so no receiver can run over it without noticing.
- A **send arm** on a closed channel **aborts** when chosen (send-on-closed is a bug).
- **`_`** fires when no arm is ready **now**, making `select` non-blocking. It is **not** an answer
  to an exhausted select: "nothing yet" would be a lie once nothing can ever be ready, and a loop
  around that lie spins. It yields to the scheduler before it runs, so a poll loop cannot starve the
  worker it is on.
- A **one-shot** `select` whose watched receive channels have all ended has nowhere to go and
  **aborts** (`DeadlockError`) — waiting for something that cannot happen, named. `for select` ends
  instead; that is the difference between the two spellings.

The granularity is deliberate: a single receive answers the end per value, as `nil`; a `select` drops
the ended arm instead, so a clean close never joins the "has data" race and nothing spins.

Two consequences of the drop rule are worth stating outright, because a `select` that ignores them
hangs rather than misbehaves:

- **Closing a channel does not fire its own arm.** A clean close removes that arm from the wait; it
  never becomes ready. Whatever a close is meant to signal must be **sent** as a value, or be the end
  that `for select` waits for.
- **The loop ends only when _every_ watched receive channel has.** One channel that stays open — a cancel
  channel the canceller still holds, a mailbox nobody has finished with — keeps the loop running. A
  one-shot `select` in the same position has nothing left that can become ready, and the runtime
  reports it where the program's outcome is decided: `DeadlockError`, raised on `main`.

## Timers & cancellation

**Timeouts** and **cancellation** both fall out of channels and `select` — no new primitive. The
worked version of this section is [`examples/13_cancel.zg`](../../examples/13_cancel.zg), which
`make examples` builds with `zerg` **and runs**.

- **A timer is a channel.** `time.after(d)` yields a receive-only channel that becomes ready **once**
  after `d` (`time.ticker(d)` fires repeatedly); a `select` receive arm on it is a **timeout**. `d` is
  a stdlib duration in **nanoseconds** and the clock is an ambient-OS stdlib facility (like `env`),
  never a primitive — see [Standard Library](../runtime/stdlib.md).
- **Cancellation is a channel.** Hand a coroutine a **cancel channel** to watch in its `select`, and
  cancel by **sending a value** on it. Because `spawn` is fire-and-forget with **no handle, there's no
  preemptive kill** — cancellation is **cooperative**: a coroutine ends only by returning, or by
  observing a cancel or timeout arm and choosing to stop.

**Cancel by sending, not by closing.** A close is an end-of-stream, not an event: a cleanly closed
receive arm is _dropped_, so closing `cancel` never fires the `cancel` arm. Closing it instead feeds
the LOOP's ending — which waits for **every** watched receive channel, so it comes only once the work
is finished too. The two are complementary rather than interchangeable: **send** to stop early,
**close** (or simply let the last holder go) to say "there will be no cancellation", which is what
lets the loop end when the work runs out.

```text
fn stage(work: <-chan[int], cancel: <-chan[int], out: chan[int]<-) {
    mut total := 0

    for select {
        v := <-work           => { total = total + v }          # v is an int: an arm that fires has a value
        <-cancel              => { out <- total; return }       # stopped early — a value was SENT
        <-time.after(1000000) => { out <- total; return }       # timeout — 1ms, in nanoseconds
    }
    out <- total                                                # work and cancel both ended
}

fn main() {
    cancel := chan[int](1)
    out := chan[int](1)

    spawn stage(source(3), cancel, out)

    cancel <- 1        # stop it now …
    # close(cancel)    # … or: no cancellation — let the work finish and the loop end

    print (<-out)!
}
```

The loop is not decoration. A one-shot `select` whose channels have all ended — and with no `_` —
aborts with `DeadlockError`, which is how a forgotten shutdown announces itself.

**A timer costs a coroutine.** `after` and `ticker` are ordinary Zerg over one runtime leaf that parks
a coroutine until a monotonic deadline, so **each live timer is a coroutine with its own 256KB stack**.
An `after` inside a loop — as in the `select` above — allocates one **per iteration**, and each lives
until its deadline passes and its value is taken. **A `ticker` cannot be stopped**: nothing cancels a
sleep, so its coroutine lives until the program does. Put a ticker at the top of a program, not inside
a loop.

The scheduler half is built in the runtime: an idle worker sleeps to the nearest deadline instead of
spinning, and a pending sleep is never called a deadlock.

## Shared state — the actor pattern

The worked version of this section is [`examples/12_actor.zg`](../../examples/12_actor.zg), built and
run by `make examples`.

Zerg has no locks and no shared mutable state, yet real programs need coordinated mutable state — a
counter, a cache, a registry. The answer is a **pattern**, not a new primitive: an **actor** is a
coroutine that **exclusively owns** some `mut` state, reachable only by messages on a channel. One
coroutine drains its mailbox one message at a time, so writes **serialize with no lock**, and since no
one else holds the state there is no data race.

```text
enum Cmd {
    Add(int)                  # a write
    Get(chan[int]<-)          # a read — carries a reply channel
}

fn answer(rep: chan[int]<-, n: int) -> int {
    rep <- n                         # reply on the caller's channel…
    return n                         # …and leave the state as it was
}

fn counter(inbox: <-chan[Cmd]) {
    mut n := 0                       # the state: a plain mut int, owned here alone

    for cmd in inbox {               # drains until the last sender leaves
        n = match cmd {              # every write to the state is this one assignment
            Cmd.Add(d)   => n + d    # the write happens inside the owner
            Cmd.Get(rep) => answer(rep, n)
        }
    }
}
```

`answer` exists because **a match arm's body is an expression**: a send is a statement and cannot
stand in an arm, so the reply travels through a call whose value is the state to keep. That also has
the pleasant effect of making the owner's state writable in exactly one place. (A block `{ … }` is an
expression, and one holding the send would serve here too — `Cmd.Get(rep) => { rep <- n; n }` builds and
runs. `answer` is kept because it puts the one write in one place, not because the block is refused.)

- **tell** (fire-and-forget) is a plain send — `inbox <- Add(5)`.
- **ask** (request-reply) sends a fresh reply channel and blocks on it —
  `rep := chan[int](1);  inbox <- Get(rep);  v := (<-rep)!`. `Get`'s field is typed `chan[int]<-`, so
  `rep` narrows to send-only as it enters the message, while the caller keeps its receive end.
- **Teardown is automatic** — when the last client drops its send end, `inbox` closes, the `for` ends,
  and the owner's `mut` state is freed; the ordinary channel-close and scope-owned rules, nothing added.

The inbox is a `Ref` value, so **sharing the actor is sharing the inbox** (refcount-bumped) — every copy
and coroutine that holds it talks to the one owner. This, not a `Ref[T]`, is how mutable state is shared:
a `Ref[T]` shares a value **read-only**, an actor **serializes writes** behind an owner. A resource that
must be serialized (a non-thread-safe `Ref[handle]`) is likewise owned by one actor.

For a single shared scalar, the lower-level alternative is a stdlib **`Atomic`** held behind an immutable
`:=` (the binding is immutable; the atomic's interior is not — see [Modules & Programs](../runtime/package.md)). It
provides lock-free `load` / `store` / `swap` / `fetch_add` / `compare_swap`.

> **[not yet]** And not because of anything about atomics: an `Atomic[int]` IS a `Ref[int]`, and there
> is no `Ref[T]` yet (`E9058`). The **import** is what is refused, rather than a type nothing declares
> reaching the emitter — _E9104 the module `atomic` ships and cannot be imported — it declares `Atomic[T]`,
> and a generic struct is a form this compiler has not built. Share state across coroutines with a channel
> until it has_ — so the actor above is the pattern that works today. The explicit
> **memory-ordering argument** and a **generic `Atomic[T]`** are **[not yet]** in the language as well.

## A producer — the generator pattern

A **generator is not a language feature** — it is a **coroutine that sends to a channel**, drained by the
consumer with `for v in ch`. The channel _is_ the `Iterator`: it yields values until the producer's scope
exits and the channel closes, and the close is what ends the loop. There is no `yield` keyword and
no generator type; the `send` is the yield.

```text
fn range_gen(lo: int, hi: int, out: chan[int]<-) {
    mut n := lo

    for n < hi {
        out <- n            # "yield" n — blocks until the consumer takes it
        n = n + 1
    }
}                           # out's scope ends → channel closes (if last sender)

fn range(lo: int, hi: int) -> <-chan[int] {
    ch := chan[int]()
    spawn range_gen(lo, hi, ch)
    return ch               # the caller gets a receive-only end and is never a sender
}

for v in range(0, 10) { use(v) }   # drains until the producer's channel closes
```

Early consumer exit is the one wrinkle. If the consumer stops first (a `break`), a blocking `out <- n` waits
forever — Zerg does not abort a receiver-less send (see Termination & deadlock). The producer opts into
stopping the **same cooperative way as any coroutine**: watch a **cancel channel** in a `select` (see Timers &
cancellation) and bail when the consumer closes it. That is the existing mechanism, not a new one.

A dedicated **ergonomic wrapper** — hiding the value/cancel plumbing behind one `for v in generate(...)` that
auto-wires the channels and tears the producer down at loop exit — is **deferred**. It would be pure stdlib
sugar over exactly the pieces above, added only if the need proves real (DDD), never a language change.

## Unhandled aborts

An abort never caught by `guard` (see the error model) **kills only that coroutine** — its stack
unwinds (freeing scopes, decrementing channel refcounts) while everything else runs on. This is
fire-and-forget, but the failure is **not lost**: closing a channel as the last sender carries the
crash `Err`, which is **raised at the consumer's next receive** (a clean finish gives `nil` instead —
Receive, above). Measured: a producer that aborts after one send makes the consumer's second `<-ch`
re-raise `IOError: disk went away` rather than answer an absence. The **one** abort a coroutine boundary
does not contain is a `StackOverflowError`, which ends the process from any stack — see
[Errors](errors.md).

The runtime **reports it on `stderr`** — the `Err`'s message, as an abort at the top level prints one
— and then that coroutine is gone and the program runs on. The report is **purely observational**:
it is what the unwind already knows, printed on its way past, and nothing about the program's
behaviour depends on it.

> **[not yet]** The report is the message and nothing more. Naming the **coroutine** it came from,
> printing a **backtrace**, and a **compiler flag** to choose whether any of it is printed are all
> unbuilt — so a program with many coroutines gets a reason without a `spawn` site to attach it to.

For a _structured_ outcome — partial results, a specific error, or a failure that wouldn't otherwise
close a watched channel — the coroutine still `guard`s and sends over a channel. Making a death
_fatal_ is the observer's job (react to `Right(err)` and abort), never the `spawn`'s.

## Scheduling

`spawn` runs coroutines on a **cooperative M:N scheduler** — many coroutines multiplexed over several OS
threads — and cooperative is the load-bearing word. A coroutine yields **only** at a channel operation, a
`select`, or a sleep, and nothing takes it off its worker until it does. Every coroutine that parks is
therefore fairly served: among coroutines that yield, none can indefinitely starve another, and the
mechanism is not fixed — a round-robin rotor is what this implementation uses and a conforming one may
choose otherwise.

**A coroutine that never parks occupies one worker for as long as it runs**, and that is the rule rather
than a shortfall against one. The shape of the failure is a count, not a switch: one spinner costs a core,
`M` spinners leave nothing to run anything else — including `main` — and on a single-CPU host (`M` = 1) the
first spinner is already the whole program. So an unbounded compute loop needs a channel operation in it,
the same discipline every cooperative runtime asks for. Preemption would lift that requirement; it is a
[door](../../FUTURE.md#preemptive-scheduling), not a promise this page makes.

`M` is the worker count — one OS thread per CPU, capped at 16 — draining one shared FIFO run queue, and a
coroutine migrates freely between workers, so it may resume on a thread it never started on. **`main` is
coroutine 0**: it is queued on that run queue before any worker exists, and the thread that called it
becomes a worker rather than a supervisor. So the pool is up around `main`'s first statement, not started
in reaction to the first `spawn`, and `M` is the whole budget — no thread is held back for the program's
own coroutine.

Two limits bound the model:

- **A blocking foreign (FFI) call is not preemptible.** It parks its OS thread inside a C frame Zerg does not
  own (see [FFI](../runtime/ffi.md)); fairness covers Zerg coroutines, not a thread stuck in C. It occupies **one
  worker** and the others keep running — the same accounting as a CPU-bound coroutine — but a long blocking
  call is thread-occupying, so prefer non-blocking C APIs, and expect it to block the whole program when
  `M` is 1.
- **Fairness moves the _ready_; it does not unstick the _blocked_.** When every coroutine is blocked with
  no possible progress that is a deadlock, caught separately (below); the `select` tie-break is this same
  fairness applied to a single wait.

## Termination & deadlock

- **Program lifetime** — when the main stack returns, the **program ends**. There's no join, so drive
  a coroutine to a channel-observed completion if it must finish before exit.

  What ending the program guarantees is a statement about the **run queue**: nothing parked is ever
  resumed and nothing queued is ever started. It does **not** stop a coroutine that is already
  **running**, because nothing preempts one (see Scheduling) — a coroutine mid-computation
  on another worker runs on until it parks or returns, and the process outlives `main` for exactly
  that long. Both halves are observable. With a single worker a spinning coroutine holds it, so
  `main` cannot resume — let alone return — until that coroutine yields; with several workers `main`
  returns while the spinner is still going, and the process ends when the spinner does. So read `main`
  returning as _no further scheduling_ rather than as a kill, and give any coroutine whose work must be
  cut short a cancel channel to observe.

- **A send with no receivers just blocks** — even when the receive side is provably empty forever,
  Zerg doesn't abort it; waiting or bailing is the **caller's** call (e.g. a `select` with a cancel
  or timeout arm).
- **Global deadlock detection** — if every coroutine is blocked with no possible progress, the runtime
  raises **`DeadlockError`** rather than hanging. A lone blocked sender while others progress is not
  individually detected, and a pending sleep is not a deadlock — a coroutine waiting on a timer is going
  to make progress, so the detector stands down while any sleep is outstanding.

  `DeadlockError` is a **clean abort** like any other: it unwinds, runs the pending `defer`s, and a
  `guard` catches it. Two properties are worth knowing before catching one.

  - **The victim is `main`.** The abort is raised on `main`'s coroutine, never on an arbitrary member of
    the blocked cycle. A deadlock is a statement about the **whole program**, so it lands where the
    program's outcome is decided — in `main`'s `guard` and `main`'s exit status. Handing it to some other
    coroutine's `guard`, which knows nothing about the global condition, would let it be swallowed.
  - **Every detection raises; there is no one-shot.** A `guard` inside a retry loop therefore turns a
    deadlock into a **livelock** — the program keeps going round, reporting its reason each time. That is
    the deliberate trade: a one-shot detector goes silent on the second occurrence and hangs, which is
    the exact failure this mechanism exists to prevent. A `guard` around a deadlock should change
    something or stop, not retry unchanged.
