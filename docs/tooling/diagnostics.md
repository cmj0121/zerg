# Zerg Compile Diagnostics

Every code the compiler reports, and the rule each one names. Part of the
[Language Reference](../language.md). Also in [繁體中文](diagnostics.zh-TW.md).

An `F` or an `L` code is what a **tool** says about a program that already builds, and the
[formatter and the linter](fmt.md) carry their own. An `E` code is not advice.

These are not advisory. A program that hits one does not build, so each is a **compile
error** the build stops on. They carry codes because a code is a **stable identity for a
rule** where a sentence is not: prose gets better, and a gate that pins the sentence turns
red when it does. The codes group by the **stage** that reports them, which is also the
order a build meets them:

| Range  | Stage    | What it reports                                                    |
| ------ | -------- | ------------------------------------------------------------------ |
| `E1xx` | lexical  | text that is not Zerg tokens                                       |
| `E2xx` | parser   | tokens that are not a Zerg form                                    |
| `E3xx` | checking | a form whose meaning does not hold together                        |
| `E4xx` | emitting | a form this compiler will not lower, including a `[not yet]`       |
| `E5xx` | building | the program as a set of files, which no single file's text answers |
| `E6xx` | parser   | the parser again: `E2xx` is full, and a range is a hundred numbers |
| `E7xx` | emitting | the emitter again: `E4xx` is full, for the same reason             |

`E5xx` is the one range that is not a point in that order. A build resolves imports before
it lexes what they name and looks for `fn main` after everything is emitted, so the driver's
own findings bracket the other four rather than sitting between two of them.

`E6xx` is not a stage at all — it is **`E2xx` continued**, and `E7xx` is **`E4xx` continued**
for the same reason twice over. A range is a hundred numbers and the parser used ninety-eight
of them; the rules that arrived when the parser's refusals were moved onto one reporting
channel had nowhere in `E2xx` to go. The emitter followed one change later — 50 of its 126
refusals carried no code at all, and `E4xx` had one number left — so the same move was made
again. Continuing in a fresh range is what keeps the two properties the scheme is for: a
number is never reused, and a reader who meets one can tell which stage is speaking.

**When a range fills, open a continuation range for that stage and close the old one.** Two
open ranges for one stage means two places to allocate from, and that is the exact condition
the three collisions in one week came out of. Closing is what `E299` is doing in the retired
table below: it was never issued, and retiring it is how a number is taken out of circulation
without being spent, so `E2xx` reads as **full** rather than as one slot somebody may still
take. `E499` is the same move one range over, made the day the emitter's numbers ran past
`E498`: it was never issued either, and `E4xx` reads as full because of it.

That is why the table above names a **stage** rather than a range, and why `make
error-codes-check` answers per stage:

```text
error-codes-check: next free code per stage — building E513, checking E399, emitting E746,
                                              lexical E112, parser E615
```

It reads both the ranges and their stages out of the table above rather than carrying its own
copy, so the answer for a stage is its **highest** range's — which is what a continuation
means. Adding a continuation range is therefore one row here, and the advice follows it.

A code sits at the **front of the message**, before the sentence: `E109 invalid escape in a
rune literal`. Where a diagnostic carries a place, the renderer's `error:` opens the line
ahead of it (`error: E109 …`); a refusal that has not learned its place yet prints the
message alone, so the code is the first thing on the line either way.

**A code exists when a gate pins it, and not before.** `scripts/refuse-check.sh` and
`scripts/reject-check.sh` assert the code rather than the sentence, and a `zerg` case that
pins prose instead is a failure by name — otherwise a list that is mostly codes with a few
sentences left in it looks finished from the outside. `scripts/error-codes-check.sh` holds
the three lists to each other: what the compiler reports, what a gate pins, and what this
table lists. Asking it that question is what found
**thirteen rules no case had ever made fire**; they are the last section of
`reject-check.sh`.

A reject case keeps a **sentence** as well only where several cases share a code, since what
each one then proves is which values the rule named. The seed keeps sentence matching
throughout: codes are the language's contract, and the seed is the tool that builds the
shipping compiler rather than a part of it (the line
[Conformance](../conformance.md) draws when it declines to mark the seed's gaps).

## The catalogue

| Code   | Rule                                                                                                    |
| ------ | ------------------------------------------------------------------------------------------------------- |
| `E101` | a string literal is not closed before the end of the line                                               |
| `E102` | a rune literal is empty                                                                                 |
| `E103` | a rune literal holds exactly one character, and this holds more                                         |
| `E104` | this character is not part of any Zerg token                                                            |
| `E105` | a triple-quoted string is never closed                                                                  |
| `E106` | a raw string has no closing quote on this line                                                          |
| `E107` | a command literal has no closing backtick                                                               |
| `E108` | a based number needs a digit immediately after its prefix                                               |
| `E109` | invalid escape in a … literal                                                                           |
| `E110` | a string literal may not contain a NUL                                                                  |
| `E111` | `…` is not UTF-8 text, and a Zerg source file is UTF-8 text                                             |
| `E201` | `close` is not a select arm head                                                                        |
| `E202` | a select needs at least one arm                                                                         |
| `E203` | `…` is not a select arm head                                                                            |
| `E204` | expected `…`, found `…`                                                                                 |
| `E205` | expected a newline or `;` to separate statements, found `…`                                             |
| `E206` | `Either[…, …]` has the same type on both sides                                                          |
| `E207` | a parameterized `…[…]` as … — **[not yet]**                                                             |
| `E208` | `#[derive(…)]` has no declaration under it                                                              |
| `E210` | a `spec` member with a BODY — **[not yet]**                                                             |
| `E211` | an associated value is not a `spec` member                                                              |
| `E212` | a generic enum `…[…]` — **[not yet]**                                                                   |
| `E213` | an enum discriminant is distinct across variants, and `… = …` repeats one already given                 |
| `E214` | a discriminant `… = …` on an enum whose variants carry a payload — its tag is opaque                    |
| `E215` | a generic struct `…[…]` — **[not yet]**                                                                 |
| `E217` | the decorator `#[…]` — **[not yet]**                                                                    |
| `E218` | an associated value binding `… := …` in an `impl` — **[not yet]**                                       |
| `E219` | `…` as an `impl` item — **[not yet]**                                                                   |
| `E221` | a struct pattern `…{…}` — **[not yet]**                                                                 |
| `E222` | calling … — **[not yet]**                                                                               |
| `E223` | the named argument `…:` — **[not yet]**                                                                 |
| `E224` | `unsafe { … }` as an EXPRESSION — **[not yet]**                                                         |
| `E225` | an f-string ':spec' format spec — **[not yet]**                                                         |
| `E226` | an f-string '!r' / '!s' / '!a' conversion — **[not yet]**                                               |
| `E227` | the f-string '{expr=}' self-documenting form — **[not yet]**                                            |
| `E230` | an associated type is not a `spec` member                                                               |
| `E231` | an associated type binding `type … = …` in an `impl` — **[not yet]**                                    |
| `E232` | a tuple pattern in a `match` arm — **[not yet]**                                                        |
| `E233` | an array type `[T; N]` — **[not yet]**                                                                  |
| `E234` | an `as` binding in a `match` arm — **[not yet]**                                                        |
| `E235` | an interpolating command literal — **[not yet]**                                                        |
| `E236` | a command literal — **[not yet]**                                                                       |
| `E238` | a destructuring binding `(a, b) := …` — **[not yet]**                                                   |
| `E239` | a range with no lower bound — **[not yet]**                                                             |
| `E240` | a list pattern in a `match` arm — **[not yet]**                                                         |
| `E241` | an or-pattern — **[not yet]**                                                                           |
| `E242` | `for mut v in …` — **[not yet]**                                                                        |
| `E243` | a struct pattern `…{…}` in a `match` arm — **[not yet]**                                                |
| `E244` | this program nests more than … levels deep                                                              |
| `E245` | `…` is a reserved word and cannot name …                                                                |
| `E246` | a tuple type has two elements or more                                                                   |
| `E247` | `pub import` is not a form                                                                              |
| `E248` | `pub` does not go on `init()`                                                                           |
| `E249` | `pub` does not go on an `impl` block                                                                    |
| `E250` | a decorator leads its declaration and `pub` sits inside it: write `#[…] pub fn …`, not …                |
| `E251` | a free function is never `mut fn`                                                                       |
| `E252` | `pub` does not go on an `unsafe { … }` group                                                            |
| `E253` | a module-level `unsafe { … }` group does not nest                                                       |
| `E254` | a module-level `unsafe { … }` group holds declarations                                                  |
| `E255` | `pub` binds to a declaration, and a statement takes none                                                |
| `E256` | this module-level `unsafe { … }` group is never closed                                                  |
| `E257` | `…` is a reserved word and cannot name a binding                                                        |
| `E258` | `…(…)` converts a VALUE and was given none                                                              |
| `E259` | `…(…)` converts one value, and this gives …                                                             |
| `E260` | `list[T](…)` converts a VALUE and was given none                                                        |
| `E261` | `list[T](…)` converts one value, and this gives …                                                       |
| `E262` | a match arm's guard goes before the `=>`                                                                |
| `E263` | a parameter is `mut &` or nothing                                                                       |
| `E264` | a standalone `unsafe fn` declaration — **[not yet]**                                                    |
| `E265` | an associated type projection `….…` — **[not yet]**                                                     |
| `E266` | a value generic parameter `…: …` — **[not yet]**                                                        |
| `E267` | an import path is a string                                                                              |
| `E268` | `…[…]` with no call after it — **[not yet]**                                                            |
| `E269` | an `if` EXPRESSION whose branch has more than one statement — **[not yet]**                             |
| `E270` | a binding head in an `if` EXPRESSION — **[not yet]**                                                    |
| `E271` | `asm(…)` — **[not yet]**                                                                                |
| `E272` | `…(…)` converts a VALUE and was given none                                                              |
| `E273` | `…(…)` converts one value, and this gives …                                                             |
| `E275` | a call writes its type arguments, and a postfix `[ … ]` is an index                                     |
| `E276` | a `spec` member that is neither a signature nor a provided method                                       |
| `E277` | an `impl` in neither the spec's module nor the type's                                                   |
| `E278` | `#[derive(S)]` on an enum, and a method takes a `This`                                                  |
| `E279` | `#[derive(S)]` on an enum, and a variant carries no value                                               |
| `E280` | `#[obj]` on something that is not a `spec`                                                              |
| `E281` | `#[obj]` and a `mut fn` — a wrapped value is a copy                                                     |
| `E282` | `#[obj]` and a method taking `This` — an object has forgotten its type                                  |
| `E283` | `#[derive(…)]` on something with no structure to read                                                   |
| `E284` | a `??` right-hand diverge with a trailing `if` guard                                                    |
| `E285` | a default on a closure parameter — **[not yet]**                                                        |
| `E286` | a `mut &` parameter in a function type — **[not yet]**                                                  |
| `E287` | an `unsafe` `spec` signature — **[not yet]**                                                            |
| `E291` | an `impl` carrying its own type parameters `[…]` — **[not yet]**                                        |
| `E292` | an `impl` on `…[…]` — a type ARGUMENT on the target — **[not yet]**                                     |
| `E288` | a 1-tuple `( e, )` — a single `( expr )` is grouping                                                    |
| `E289` | a trailing comma before a closing `)`, `]` or `}`                                                       |
| `E290` | a `{`-opening expression at the start of an `if`/`for`/`with`/`match` head                              |
| `E293` | `…` is a reserved word and cannot name a field                                                          |
| `E294` | expected a field name (or a tuple index) after `.`, found `…`                                           |
| `E295` | `del …` names nothing this program declares                                                             |
| `E296` | `del …` names a function, struct, enum or variant — not a binding                                       |
| `E297` | `…` is used after del                                                                                   |
| `E298` | `…` is used after del on some paths                                                                     |
| `E301` | `…` is not a public member of module `…`                                                                |
| `E302` | `…` is not a place, and an assignment needs one                                                         |
| `E303` | cannot assign to `…`: it is a module `const`, and a constant is never written                           |
| `E304` | `type … = …` over a non-scalar — **[not yet]**                                                          |
| `E305` | cannot assign to `…`: it is a module binding, and the top level is immutable                            |
| `E306` | cannot assign to `this`: a method's receiver is a copy, and the form that writes through …              |
| `E307` | cannot assign to `…`: it is immutable                                                                   |
| `E308` | cannot assign through `…`: it is immutable                                                              |
| `E309` | parameter `…` of `…` is a `mut &` and cannot have a default                                             |
| `E310` | the default for `…` of `…` is …, and the parameter is …                                                 |
| `E311` | `…` carries … and this … …                                                                              |
| `E312` | argument … of `…` is a `mut &` and cannot cross a `…`: a borrow may not be captured, and …              |
| `E313` | cannot store through …                                                                                  |
| `E314` | no spec named `…`                                                                                       |
| `E315` | `…` is parameterized by …, and this `impl` gives … type argument(s)                                     |
| `E316` | `…` extends `…`, and nothing in this program declares a spec by that name                               |
| `E317` | `….…` does not match what `…` requires: …                                                               |
| `E318` | `…` does not implement `…`, which `…` requires                                                          |
| `E319` | the integer literal `…` does not fit an `int`                                                           |
| `E320` | a `str` is not indexable                                                                                |
| `E321` | an `if` expression answers ONE type, and its branches give … and …                                      |
| `E322` | a `match` answers ONE type, and its arms give … and …                                                   |
| `E323` | … borrows …, which is a value rather than a place                                                       |
| `E324` | … writes back to `this`, and the enclosing method holds its receiver by value                           |
| `E325` | … writes back to `…`, which is not `mut`                                                                |
| `E326` | `…` is given to two `mut &` parameters of `…` in one call                                               |
| `E327` | `…` takes … and this gives …                                                                            |
| `E328` | `…` needs … and this gives …                                                                            |
| `E329` | element … of this list literal is …, and this gives …                                                   |
| `E330` | `…` is not a value a … holds                                                                            |
| `E331` | this divides by a constant `0`                                                                          |
| `E332` | this expression's value is past what an `int` holds, so it cannot be measured against …                 |
| `E333` | this function's answer is …, and this gives …                                                           |
| `E335` | cannot bind … to a … binding: `…`                                                                       |
| `E336` | the binding `…` gives …, which has no type of its own                                                   |
| `E337` | `type … = …` names no type                                                                              |
| `E338` | a struct field or an enum payload is …, and this gives …                                                |
| `E339` | cannot assign … to …, which holds …                                                                     |
| `E340` | argument … of `…` is …, and this gives …                                                                |
| `E341` | an optional is not an operand of `…`                                                                    |
| `E342` | operator `…` has no meaning on … and …                                                                  |
| `E343` | operator `…` takes bool operands, and these are … and …                                                 |
| `E344` | operator `…` takes int operands, and these are … and …                                                  |
| `E345` | operator `…` takes numeric operands, and these are … and …                                              |
| `E346` | operator `…` orders two numbers or two strs, and these are … and …                                      |
| `E347` | cannot compare a variant with a number — a variant is a value of ITS enum                               |
| `E348` | cannot compare … and … — they are different kinds of value                                              |
| `E349` | operator `…` has no meaning on …                                                                        |
| `E350` | operator `not` takes a bool operand, and this one is …                                                  |
| `E351` | operator `-` takes a numeric operand, and this one is …                                                 |
| `E352` | operator `~` takes an int operand, and this one is …                                                    |
| `E353` | operator `…` has … on one side and … on the other, and an operator's operands must …                    |
| `E354` | the condition of … is an optional, and a condition is bool — bind it with `if v := x { … }`             |
| `E355` | the condition of … must be bool, and Zerg has no truthiness                                             |
| `E356` | `…` re-binds a `const`                                                                                  |
| `E357` | `const …` shadows a binding already visible here                                                        |
| `E358` | the top-level binding `…` may not be `mut` outside a module-level                                       |
| `E359` | `….…()` renders the value as text, so it takes no arguments beyond the value                            |
| `E360` | `….…()` renders the value as text, so it is a plain `fn` and not a `mut fn`                             |
| `E361` | `….…()` answers the `str` the value shows as                                                            |
| `E362` | `…` is declared twice, once as a generic                                                                |
| `E363` | `…` is declared both as a generic and as a plain function                                               |
| `E364` | `This` is the self type, and … is outside an `impl`                                                     |
| `E365` | `…` declares a parameter named `…` twice                                                                |
| `E366` | `…(…)` converts ONE value                                                                               |
| `E367` | `…(…)` does not parse a `str`                                                                           |
| `E369` | `…` holds an …, and an … is not callable                                                                |
| `E370` | `…` needs a value for … (…): only a `T?` field has an implicit default, and it is `nil`                 |
| `E371` | `this` is a method's receiver, and this function has none                                               |
| `E372` | undefined name `…`                                                                                      |
| `E374` | a slice bound is an int, and this is …                                                                  |
| `E375` | a list index is an int, and this is …                                                                   |
| `E376` | no field `…` on …                                                                                       |
| `E377` | `.…` reads a tuple element, and … is not a tuple                                                        |
| `E378` | a tuple of … has no `.…`                                                                                |
| `E379` | `for … in` walks a list, a map, a str, a range or a channel, and … is not iterable                      |
| `E380` | raise carries an `Err`, or a message to build one from                                                  |
| `E381` | `…` is declared twice, once as one kind of declaration and once as another                              |
| `E382` | `…` is declared twice as the same kind — every module flattens into one namespace                       |
| `E383` | a variant is named through its enum, and this one is bare                                               |
| `E384` | a side of an `Either` is named through its type, and this one is bare                                   |
| `E385` | a closure parameter has no type, and its position gives it none                                         |
| `E386` | a call through a function value gives the wrong number of arguments                                     |
| `E387` | `…` is declared in a module-level `unsafe { … }` group, and this is safe code                           |
| `E388` | module `…` has no `…`                                                                                   |
| `E389` | `…` is already … — an import binds a name into the one value namespace                                  |
| `E390` | this position needs a value, and nil is what it was given                                               |
| `E391` | `…` opens a statement at the top level, and a compiled program runs nothing there                       |
| `E392` | cannot `…` to `…`: only a `mut` collection can modify its elements                                      |
| `E393` | cannot `…` `…`: a collection is frozen against structural change inside its own `for` loop              |
| `E394` | `…(…)` on a `float` — write the verb: `math.trunc` / `floor` / `ceil` / `round`                         |
| `E395` | a conversion is one step: `…` -> `…` is `…` -> `int` -> `…`, so write the two                           |
| `E396` | `…` is not a compiler primitive — the `__zrt_…` set is closed                                           |
| `E397` | the compiler primitive `…` takes … and this gives …                                                     |
| `E398` | operand … of the compiler primitive `…` is …, and this gives …                                          |
| `E401` | `…` outside of a loop: it belongs to a `for`, and a `select` arm is not one                             |
| `E402` | a `from` cause is an `Err`, and … is not one                                                            |
| `E403` | `…` leaving a `guard` block — **[not yet]**                                                             |
| `E404` | a channel of optionals is refused                                                                       |
| `E405` | `…(…)` names one side of an `Either`, which holds exactly one value                                     |
| `E406` | `?.` reads through an optional, and … is not one                                                        |
| `E407` | `int(v)` reads the discriminant, and enum `…` carries a payload, so its tag is opaque                   |
| `E408` | `?` early-returns the RIGHT of …, so the enclosing function must answer a carrier with …                |
| `E409` | a generic METHOD `….…[…]` — **[not yet]**                                                               |
| `E410` | `…` has been instantiated … times and is still asking for more                                          |
| `E411` | the type parameter `…` of `…` is not decided by this call                                               |
| `E412` | `…` does not implement `…`, which `…`'s type parameter `…` is bounded by                                |
| `E413` | the raw-pointer built-in `…` — **[not yet]**                                                            |
| `E414` | the compile-time built-in `…[T]` — **[not yet]**                                                        |
| `E415` | an `impl` on the built-in type `…` — **[not yet]**                                                      |
| `E416` | the `spec` `…` used as a TYPE (…) — **[not yet]**                                                       |
| `E417` | `str(…)` over a list bridges bytes or code points, and this is …                                        |
| `E418` | `…(…)` converts a value, and … may not have one                                                         |
| `E419` | an enum converts to `int`                                                                               |
| `E420` | `….of(n)` reverses the discriminant, and enum `…` carries a payload, so its tag is opaque               |
| `E421` | `[…]` indexes a value, and … may not have one                                                           |
| `E422` | `…` MUTATES its list, and `…` is a value rather than a place — **[not yet]**                            |
| `E423` | an open-ended range has no upper bound here — **[not yet]**                                             |
| `E424` | `….…(…)` is an associated function — **[not yet]**                                                      |
| `E425` | undefined function `…`                                                                                  |
| `E426` | `…` has … fields and this gives …                                                                       |
| `E427` | variant pattern `…` cannot match a subject of type …                                                    |
| `E428` | non-exhaustive match: missing variant ….…                                                               |
| `E430` | `…` on a … needs an `Eq` — there is no structural equality by default                                   |
| `E431` | a map key of type … — **[not yet]**                                                                     |
| `E432` | `…` is declared … and the value is …: unwrap it with `?? …`, `!` or `if … := …`                         |
| `E433` | `print` needs a value, and … may not have one                                                           |
| `E434` | `if … := …` over a … — **[not yet]**                                                                    |
| `E435` | `…` is declared to answer …, and its body falls off the end                                             |
| `E436` | `#[derive(…)]` — **[not yet]**                                                                          |
| `E437` | cannot derive `…`: the derivable specs are compiler-owned, and a `spec` you write is …                  |
| `E438` | `#[derive(Eq)]` on `…` — **[not yet]**                                                                  |
| `E444` | the list method `…` — **[not yet]**                                                                     |
| `E445` | structural equality over a container — **[not yet]**                                                    |
| `E446` | a refcounted box `Ref(x)` / `deref(r)` — **[not yet]**                                                  |
| `E449` | rendering a … as text — **[not yet]**                                                                   |
| `E451` | `…` declares `…` twice                                                                                  |
| `E452` | `…` is part of a cycle of by-value declarations                                                         |
| `E453` | `…` declares … named `…` twice                                                                          |
| `E454` | this expression chains more than … levels deep                                                          |
| `E455` | `…(…)` converts a scalar, and … is not one                                                              |
| `E456` | `…` is not a variant of `…`                                                                             |
| `E457` | `…` is a variant of `…`, not of `…`                                                                     |
| `E458` | this catch-all arm makes the following arms unreachable                                                 |
| `E459` | `…(…)` says which side of an `Either` a value is, so it needs a declared one to be                      |
| `E460` | a … is an identity rather than a value, and the language gives it no equality                           |
| `E461` | a second `impl Into[…] for …` — **[not yet]**                                                           |
| `E462` | `in` over a list whose elements have no `==` — **[not yet]**                                            |
| `E463` | `in` over anything but a list, a map, a range or an error kind — **[not yet]**                          |
| `E464` | `into` is a method of the `Into` spec, and no built-in type implements it                               |
| `E465` | `…` is part of the fixed-width ladder — **[not yet]**                                                   |
| `E466` | the built-in `set` — **[not yet]**                                                                      |
| `E467` | non-exhaustive match: missing a catch-all `_` arm                                                       |
| `E468` | a `return` with no value, in a function declared to answer …                                            |
| `E469` | … is a `mut &`, and a function VALUE cannot carry one — **[not yet]**                                   |
| `E470` | `del …` on a CHANNEL — **[not yet]**                                                                    |
| `E471` | `…[…](…)` as a constructor — **[not yet]**                                                              |
| `E472` | `nil` as a `match` pattern — **[not yet]**                                                              |
| `E473` | a … may hold no value, so `…` has nothing to compare                                                    |
| `E474` | the discriminant of `….…` is not a compile-time constant                                                |
| `E475` | a fill count is a compile-time constant, and … is not one                                               |
| `E476` | a fill count is how many copies to make, and `…` is negative                                            |
| `E477` | a range arm's bound is a compile-time constant, and `…` is not one                                      |
| `E478` | `…` needs a channel, and … is not one                                                                   |
| `E479` | a map entry is `key: value`, and this one has no `:`                                                    |
| `E480` | … whose value has no type this compiler can name — **[not yet]**                                        |
| `E481` | `…` re-binds a name a `match` arm's pattern already binds — **[not yet]**                               |
| `E482` | the field `…` of `…` is module-private, so it must carry a default                                      |
| `E483` | the default on field `…` reads the field `…` — **[not yet]**                                            |
| `E484` | the mutable global `…` may not be `pub`                                                                 |
| `E485` | import cycle: `…` -> `…` -> `…`                                                                         |
| `E486` | a destructuring assignment `(a, b) = …` — **[not yet]**                                                 |
| `E487` | `…` applies to the `struct`, `enum` or `spec` that follows it, and what follows is `…`                  |
| `E488` | an `unsafe fn(…)` TYPE — **[not yet]**                                                                  |
| `E489` | an `impl` on `….…` — a dotted target — **[not yet]**                                                    |
| `E490` | an `impl`'s spec is named by a bare `type-name`, and `….…` is reached through an import                 |
| `E491` | a generic `type …[…] = …` — **[not yet]**                                                               |
| `E492` | a sub-pattern inside a variant payload — **[not yet]**                                                  |
| `E493` | a range used as a value — **[not yet]**                                                                 |
| `E494` | `is …` names one of the built-in error kinds — **[not yet]**                                            |
| `E495` | a decorator holds at least one item, and `#[]` names nothing to apply                                   |
| `E496` | the decorator `#[sealed]` — reserved — **[not yet]**                                                    |
| `E497` | a `#[derive]` names the specs to generate                                                               |
| `E498` | a channel is bidirectional, receive-only or send-only                                                   |
| `E501` | this entry file declares no `fn main`                                                                   |
| `E502` | cannot resolve import `…` under any source root                                                         |
| `E503` | cannot receive on a send-only `…`                                                                       |
| `E504` | cannot send on a receive-only `…`                                                                       |
| `E505` | cannot close a receive-only channel `…`                                                                 |
| `E506` | a channel direction only narrows: a `…` cannot fill a `…`                                               |
| `E507` | `…` is a module this build compiles and this module did not import                                      |
| `E508` | `…` is not a public type of module `…`                                                                  |
| `E509` | `…` is module-private, and … is on a `pub` declaration                                                  |
| `E510` | `…` is not a public field of `…`, which module `…` declared                                             |
| `E511` | the module `atomic` ships and cannot be imported — **[not yet]**                                        |
| `E512` | `…` names a test file, and a normal build compiles none                                                 |
| `E601` | `…` needs a name, and `…` is not one                                                                    |
| `E602` | a `<-` prefix is a channel direction: only `<-chan[T]` is a type                                        |
| `E603` | `mut` before a declaration in an `impl` marks a `mut fn` method, and this is not a `fn`                 |
| `E604` | `is` wants a type name on its right                                                                     |
| `E605` | `…` is a statement, and an expression is wanted here — **[not yet]**                                    |
| `E606` | `…` is not an expression this compiler reads — **[not yet]**                                            |
| `E607` | a match arm's body is an expression, and this one is a statement — **[not yet]**                        |
| `E608` | an f-string's literal text is malformed                                                                 |
| `E609` | an f-string hole holds more than one expression                                                         |
| `E610` | `…` cannot name a struct/enum/spec/type alias — a declared type's name begins with an UPPER-CASE LETTER |
| `E611` | `…` is a prelude name — … — and cannot name …                                                           |
| `E612` | `…` applies to the … that follows it, and a statement is not one                                        |
| `E613` | a second decorator on one item — merge them into its comma list                                         |
| `E614` | an `#[allow]` names the lint codes it suppresses, and this one names none                               |
| `E701` | a `…` takes a … or a …, and this bare value is neither side                                             |
| `E702` | no field `…` on … (optional chain `?.…`)                                                                |
| `E703` | `?` on a … — it unwraps the Left of a carrier — **[not yet]**                                           |
| `E704` | `?` propagates a right the enclosing function does not answer                                           |
| `E705` | two modules both define `…` and at least one is `pub` — **[not yet]**                                   |
| `E706` | `…` and `…` both define `…` — one flat namespace — **[not yet]**                                        |
| `E707` | no type named `…` (…)                                                                                   |
| `E708` | `!` on a … — it forces a Result[T] or a T? — **[not yet]**                                              |
| `E709` | `??` on a … — its left side is a Result[T] or a T? — **[not yet]**                                      |
| `E710` | `is …` wants an Err on its left, found a …                                                              |
| `E711` | `in …` wants an Err on its left, found a …                                                              |
| `E712` | `list[…](…)` converts a `str` to its bytes, and this is …                                               |
| `E713` | `list[…](…)` — a `str` bridges to its bytes or its code points                                          |
| `E714` | rendering a … as text — an enum has no name for its variant — **[not yet]**                             |
| `E715` | `[…]` indexes a list or a map, and this is …                                                            |
| `E716` | the method `…` on an Err takes no argument — **[not yet]**                                              |
| `E717` | the method `…` on a Err — the `Error` interface is three names — **[not yet]**                          |
| `E718` | `ok_or` takes the error to answer an absence with, and none was given — **[not yet]**                   |
| `E719` | `ok_or` takes ONE error to answer an absence with — **[not yet]**                                       |
| `E720` | `ok_or` answers an absence with an `Err`, and this is a … — **[not yet]**                               |
| `E721` | `ok` forgets the Right and takes no argument — **[not yet]**                                            |
| `E722` | the method `…` on a … — a carrier answers `ok_or` and `ok` — **[not yet]**                              |
| `E723` | `….…(…)` — an enum type answers `of(n)` and its own variants — **[not yet]**                            |
| `E724` | `….of(n)` takes one integer — the discriminant to reverse                                               |
| `E725` | the method `…` on a … — **[not yet]**                                                                   |
| `E726` | `…` is testable but not constructible                                                                   |
| `E727` | no type named `…` to construct                                                                          |
| `E728` | no variant named `…` — a constructor pattern names one of the subject's                                 |
| `E729` | non-exhaustive match: missing the Left or the Right case                                                |
| `E730` | non-exhaustive match: missing the `true` or the `false` case                                            |
| `E731` | the pattern `…` on a Result[T] — it has Left and Right — **[not yet]**                                  |
| `E732` | these constants depend on each other and none can be given a value first                                |
| `E733` | `fn main() -> …` — the entry answers nothing, an int or a Result[nil] — **[not yet]**                   |
| `E734` | main(args) in a program that uses concurrency — **[not yet]**                                           |
| `E735` | a closure captures `…`, and this compiler cannot work out what it holds                                 |
| `E736` | … of anything but a function, a method, or a namespaced function — **[not yet]**                        |
| `E737` | … of `…` — not a function, a method, or a namespaced function — **[not yet]**                           |
| `E738` | `len` takes no argument, and this gives …                                                               |
| `E739` | `has` asks about one key, and this gives …                                                              |
| `E740` | the map method `…` — **[not yet]**                                                                      |
| `E741` | the field `…` on an Err — it has `msg` and `kind` — **[not yet]**                                       |
| `E742` | `…` has … type parameters and this gives …                                                              |
| `E743` | `..=` with no upper bound is not a range — **[not yet]**                                                |
| `E744` | a `spawn`/`defer` of `…`, a binding that HOLDS a function — **[not yet]**                               |
| `E745` | `…` is declared twice in this file — one scope declares a name once                                     |

They are reported the moment a file is **read**, before its imports are scanned — scanning
them parses, and a parser handed unreadable text can only say something untrue about it.
That is what it used to say: `` `b'b` is not an expression this compiler reads ``, which
names the wrong layer, the wrong problem, and a fragment of what the person wrote.

`E108` had no message at all. `0x` lowered to a C `0x`, which cc read as zero, so a
malformed literal compiled and the program answered 0. It says **immediately** because the
digit is part of the prefix's own production: `0x_1F` has digits after `0x` and is still not
a number, since a grouping `_` sits between two digits and there is none to its left.

`E274` stood among them and has **retired**. It reported a bare name in pattern position —
"`Zzz` is a variant of some enum, and a pattern names one through its enum" — decided by the
name's first letter, in a parser that had resolved nothing and knew of no enum. So it fired
on programs that declared none, and the sentence naming the enum was simply false. A bare
name in pattern position is **always** a fresh binding
([Grammar](../surface/grammar.md)), whatever its case, and the mistake the rule was aimed at
— two variants written without their enum — is `E458` instead: the first binds everything,
so the arms below it are unreachable. The number is not reused.

### Retired codes

**A retired number is never reused.** A code is a stable identity, and reusing one makes an
old build's message mean something it never meant — a report from a user, a log, a bug
filed last year, all silently reassigned to a different rule. So a retired code leaves the
table above and is listed here instead, and the range it sat in goes on counting from its
own high-water mark.

That is also what makes the catalogue **queryable**. `make error-codes-check` reports the
**next free code in every range**, which is the question anybody adding a rule has —
including two agents working in parallel, who between them collided on `E387`, `E477` and
`E288`/`E289` in a single week because the only way to ask was to read the table by eye.
The answer is only reliable while every number below the mark is accounted for, so the gate
holds each range to exactly that: a number that is neither listed above nor listed here is a
**gap**, and a gap is a code somebody may reissue without knowing.

| Code   | Why it retired                                                                             |
| ------ | ------------------------------------------------------------------------------------------ |
| `E209` | a closure parameter with no type — the form is built, so the refusal went with it          |
| `E216` | a default on a struct field — built                                                        |
| `E220` | a nested `{ … }` block as a statement — built                                              |
| `E228` | stamped twice by the code conversion, and dropped from the site no case reached            |
| `E229` | stamped twice by the code conversion, and dropped from the site no case reached            |
| `E237` | a `with` block — built, and an example took the refusal's place                            |
| `E274` | a bare name in pattern position, decided by its first letter — see above; `E458` covers it |
| `E334` | a local annotation naming no type — one rule for four sites now, and `E707` is the one     |
| `E368` | `…` is not generic — the branch that reported it left, and the code left with it           |
| `E373` | a name declared as both a module constant and a function — the rule is `E381`'s            |
| `E429` | a closure capturing a name — built                                                         |
| `E439` | stamped twice by the code conversion, and dropped from the site no case reached            |
| `E440` | stamped twice by the code conversion, and dropped from the site no case reached            |
| `E441` | stamped twice by the code conversion, and dropped from the site no case reached            |
| `E442` | stamped twice by the code conversion, and dropped from the site no case reached            |
| `E443` | stamped twice by the code conversion, and dropped from the site no case reached            |
| `E447` | never issued: the conversion pass that numbered `E4xx` skipped it                          |
| `E448` | an ordering that comes from `Ord` — the rule it named is the checker's, and always was     |
| `E450` | no field `…` on a type — the same rule as the row that kept the number below it            |
| `E299` | never issued: `E2xx` closed at `E298`, and the parser's numbers continue in `E6xx`         |
| `E499` | never issued: `E4xx` closed at `E498`, and the emitter's numbers continue in `E7xx`        |
