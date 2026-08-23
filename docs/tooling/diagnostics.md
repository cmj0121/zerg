# Zerg Compile Diagnostics

Every code the compiler reports, and the rule each one names. Part of the
[Language Reference](../language.md). Also in [繁體中文](diagnostics.zh-TW.md).

An `F` or an `L` code is what a **tool** says about a program that already builds, and the
[formatter](fmt.md) and the [linter](lint.md) carry their own.

These are not advisory. A program that hits one does not build, so each is a **compile
error** the build stops on. They carry codes because a code is a **stable identity for a
rule** where a sentence is not: prose gets better, and a gate that pins the sentence turns
red when it does. The codes group by the **kind of question** a code answers, which is also the
order a build meets them:

| Range   | Stage    | The question it answers                                            |
| ------- | -------- | ------------------------------------------------------------------ |
| `E1xxx` | lexical  | text that is not Zerg tokens                                       |
| `E2xxx` | parser   | tokens that are not a Zerg form                                    |
| `E3xxx` | checking | a form whose meaning does not hold together                        |
| `E4xxx` | emitting | a form this compiler will not lower                                |
| `E5xxx` | building | the program as a set of files, which no single file's text answers |
| `E9xxx` | unbuilt  | a form the language has and this compiler has not built            |

**A range names the question, not the file.** The two are usually the same and the stage column
says which, but where they differ the question decides. `emit.zg` is _AST -> C, with the minimal
typecheck emit needs_, so twenty-two checking rules are reported from it and stay `E3xxx`: which
file asks a question is an implementation fact, and a reader looking a code up wants to know what
kind of thing went wrong. A source that is not UTF-8 is `E1xxx` for the same reason, though the
driver is what opens the file.

Twenty-five codes sat in a range that named a different question and were re-seated before the
first release; they are listed under [Retired codes](#retired-codes), which is where a number that
moved goes.

`E5xxx` is the one range that is not a point in that order. A build resolves imports before
it lexes what they name and looks for `fn main` after everything is emitted, so the driver's
own findings bracket the other four rather than sitting between two of them.

`E9xxx` is not a stage at all, and it is the one range whose codes are not about the program
in front of it. A `[not yet]` says the LANGUAGE has this form and this compiler has not built
it, which is a different kind of sentence from every other row above — and a different kind
of life: it retires when the form is built rather than when a rule changes its mind. 104 of
them sat in the stage ranges, one future hole apiece punched through the middle. In a range
of their own that churn is confined and the five stage ranges stay dense.

**A range is a thousand numbers, and a stage owns exactly one.** It was not always so: the
scheme was three digits and a hundred numbers a range, the parser used ninety-eight of them
and continued in `E6xx`, the emitter did the same in `E7xx`, and the first digit stopped
naming the stage — the one thing about a code a reader cannot work out for themselves. Four
digits retire that idea whole. There is no continuation range, no rule for opening one, and
no reader who has to learn which two ranges are really one. If a range ever does fill, open a
continuation for that stage and close the old one — two open ranges for one stage means two
places to allocate from, which is exactly what three collisions in one week came out of.

**The three-digit numbering was retired as a whole**, in one commit, before Zerg had a
release. No four-digit code repeats a three-digit one because they are different strings, so
a message printed by an older build still means what it meant — which is the whole of what
retiring a number protects. The mapping from old to new is in that commit and not here: a
catalogue is what the codes ARE, and carrying a second table of what they were would be a
list nobody can ever finish reading.

`make error-codes-check` answers, per range, what the next free code is:

```text
error-codes-check: next free code per range — lexical E1014, parser E2071, checking E3113,
                                              emitting E4074, building E5017, unbuilt E9106
```

It reads the ranges out of the table above rather than carrying its own copy, so adding one
is a row here and nothing else.

A code sits at the **front of the message**, before the sentence: `E1009 invalid escape in a
rune literal`. Where a diagnostic carries a place, the renderer's `error:` opens the line
ahead of it (`error: E1009 …`); a refusal that has not learned its place yet prints the
message alone, so the code is the first thing on the line either way.

**A code is declared once, in the compiler.** `src/compiler/zerg/rule.zg` is the registry:
one variant of `pub enum Rule` per rule, carrying its number, and every reporting channel
takes a `Rule` rather than a string. So a hand-spelled code is a type error, a rule with no
identity cannot be written, and renumbering the scheme is an edit to that one file. It is
also where two parallel changes meet: a new code is a line there, so a second one lands on
the same lines and **git** says so, before CI is asked.

**A code exists when a gate pins it, and not before.** `scripts/refuse-check.sh` and
`scripts/reject-check.sh` assert the code rather than the sentence, and a `zerg` case that
pins prose instead is a failure by name — otherwise a list that is mostly codes with a few
sentences left in it looks finished from the outside. `scripts/error-codes-check.sh` holds
the four lists to each other: what the registry declares, what a site reports, what a gate
pins, and what this table lists. Asking it that question is what found
**thirteen rules no case had ever made fire**; they are the last section of
`reject-check.sh`.

A reject case keeps a **sentence** as well only where several cases share a code, since what
each one then proves is which values the rule named. The seed keeps sentence matching
throughout: codes are the language's contract, and the seed is the tool that builds the
shipping compiler rather than a part of it (the line
[Conformance](../conformance.md) draws when it declines to mark the seed's gaps).

## The catalogue

| Code    | Rule                                                                                                    |
| ------- | ------------------------------------------------------------------------------------------------------- |
| `E1001` | a string literal is not closed before the end of the line                                               |
| `E1002` | a rune literal is empty                                                                                 |
| `E1003` | a rune literal holds exactly one character, and this holds more                                         |
| `E1004` | this character is not part of any Zerg token                                                            |
| `E1005` | a triple-quoted string is never closed                                                                  |
| `E1006` | a raw string has no closing quote on this line                                                          |
| `E1007` | a command literal has no closing backtick                                                               |
| `E1008` | a based number needs a digit immediately after its prefix                                               |
| `E1009` | invalid escape in a … literal                                                                           |
| `E1010` | a string literal may not contain a NUL                                                                  |
| `E1011` | `…` is not UTF-8 text, and a Zerg source file is UTF-8 text                                             |
| `E1012` | an f-string literal is not closed before the end of the line                                            |
| `E1013` | a bare `}` in an f-string is not text — write `}}` for one                                              |
| `E2001` | `close` is not a select arm head                                                                        |
| `E2002` | a select needs at least one arm                                                                         |
| `E2003` | `…` is not a select arm head                                                                            |
| `E2004` | expected `…`, found `…`                                                                                 |
| `E2005` | expected a newline or `;` to separate statements, found `…`                                             |
| `E2006` | `Either[…, …]` has the same type on both sides                                                          |
| `E2007` | `#[derive(…)]` has no declaration under it                                                              |
| `E2008` | an associated value is not a `spec` member                                                              |
| `E2010` | a discriminant `… = …` on an enum whose variants carry a payload — its tag is opaque                    |
| `E2011` | an associated type is not a `spec` member                                                               |
| `E2012` | this program nests more than … levels deep                                                              |
| `E2013` | `…` is a reserved word and cannot name …                                                                |
| `E2014` | a tuple type has two elements or more                                                                   |
| `E2015` | `pub import` is not a form                                                                              |
| `E2016` | `pub` does not go on `init()`                                                                           |
| `E2017` | `pub` does not go on an `impl` block                                                                    |
| `E2018` | a decorator leads its declaration and `pub` sits inside it: write `#[…] pub fn …`, not …                |
| `E2019` | a free function is never `mut fn`                                                                       |
| `E2020` | `pub` does not go on an `unsafe { … }` group                                                            |
| `E2021` | a module-level `unsafe { … }` group does not nest                                                       |
| `E2022` | a module-level `unsafe { … }` group holds declarations                                                  |
| `E2023` | `pub` binds to a declaration, and a statement takes none                                                |
| `E2024` | this module-level `unsafe { … }` group is never closed                                                  |
| `E2025` | `…` is a reserved word and cannot name a binding                                                        |
| `E2026` | `…(…)` converts a VALUE and was given none                                                              |
| `E2027` | `…(…)` converts one value, and this gives …                                                             |
| `E2028` | `list[T](…)` converts a VALUE and was given none                                                        |
| `E2029` | `list[T](…)` converts one value, and this gives …                                                       |
| `E2030` | a match arm's guard goes before the `=>`                                                                |
| `E2031` | a parameter is `mut &` or nothing                                                                       |
| `E2032` | an import path is a string                                                                              |
| `E2033` | `bytearray(…)` / `runearray(…)` converts a VALUE and was given none                                     |
| `E2034` | `bytearray(…)` / `runearray(…)` converts one value, and this gives …                                    |
| `E2035` | a call writes its type arguments, and a postfix `[ … ]` is an index                                     |
| `E2036` | a `spec` member that is neither a signature nor a provided method                                       |
| `E2044` | a `??` right-hand diverge with a trailing `if` guard                                                    |
| `E2045` | a 1-tuple `( e, )` — a single `( expr )` is grouping                                                    |
| `E2046` | a trailing comma before a closing `)`, `]` or `}`                                                       |
| `E2047` | a `{`-opening expression at the start of an `if`/`for`/`with`/`match` head                              |
| `E2048` | `…` is a reserved word and cannot name a field                                                          |
| `E2049` | expected a field name (or a tuple index) after `.`, found `…`                                           |
| `E2054` | `…` needs a name, and `…` is not one                                                                    |
| `E2055` | a `<-` prefix is a channel direction: only `<-chan[T]` is a type                                        |
| `E2056` | `mut` before a declaration in an `impl` marks a `mut fn` method, and this is not a `fn`                 |
| `E2057` | `is` wants a type name on its right                                                                     |
| `E2058` | an f-string's literal text is malformed                                                                 |
| `E2059` | an f-string hole holds more than one expression                                                         |
| `E2060` | `…` cannot name a struct/enum/spec/type alias — a declared type's name begins with an UPPER-CASE LETTER |
| `E2061` | `…` is a prelude name — … — and cannot name …                                                           |
| `E2062` | `…` applies to the … that follows it, and a statement is not one                                        |
| `E2063` | a second decorator on one item — merge them into its comma list                                         |
| `E2064` | an `#[allow]` names the lint codes it suppresses, and this one names none                               |
| `E2065` | a map entry is `key: value`, and this one has no `:`                                                    |
| `E2066` | `…` applies to the `struct`, `enum` or `spec` that follows it, and what follows is `…`                  |
| `E2067` | an `impl`'s spec is named by a bare `type-name`, and `….…` is reached through an import                 |
| `E2068` | a decorator holds at least one item, and `#[]` names nothing to apply                                   |
| `E2069` | a `#[derive]` names the specs to generate                                                               |
| `E2070` | a channel is bidirectional, receive-only or send-only                                                   |
| `E3001` | `…` is not a public member of module `…`                                                                |
| `E3002` | `…` is not a place, and an assignment needs one                                                         |
| `E3003` | cannot assign to `…`: it is a module `const`, and a constant is never written                           |
| `E3004` | cannot assign to `…`: it is a module binding, and the top level is immutable                            |
| `E3005` | cannot assign to `this`: a method's receiver is a copy, and the form that writes through …              |
| `E3006` | cannot assign to `…`: it is immutable                                                                   |
| `E3007` | cannot assign through `…`: it is immutable                                                              |
| `E3008` | parameter `…` of `…` is a `mut &` and cannot have a default                                             |
| `E3009` | the default for `…` of `…` is …, and the parameter is …                                                 |
| `E3010` | `…` carries … and this … …                                                                              |
| `E3011` | argument … of `…` is a `mut &` and cannot cross a `…`: a borrow may not be captured, and …              |
| `E3012` | cannot store through …                                                                                  |
| `E3013` | no spec named `…`                                                                                       |
| `E3014` | `…` is parameterized by …, and this `impl` gives … type argument(s)                                     |
| `E3015` | `…` extends `…`, and nothing in this program declares a spec by that name                               |
| `E3016` | `….…` does not match what `…` requires: …                                                               |
| `E3017` | `…` does not implement `…`, which `…` requires                                                          |
| `E3018` | the integer literal `…` does not fit an `int`                                                           |
| `E3019` | a `str` is not indexable                                                                                |
| `E3020` | an `if` expression answers ONE type, and its branches give … and …                                      |
| `E3021` | a `match` answers ONE type, and its arms give … and …                                                   |
| `E3022` | … borrows …, which is a value rather than a place                                                       |
| `E3023` | … writes back to `this`, and the enclosing method holds its receiver by value                           |
| `E3024` | … writes back to `…`, which is not `mut`                                                                |
| `E3025` | `…` is given to two `mut &` parameters of `…` in one call                                               |
| `E3026` | `…` takes … and this gives …                                                                            |
| `E3027` | `…` needs … and this gives …                                                                            |
| `E3028` | element … of this list literal is …, and this gives …                                                   |
| `E3029` | `…` is not a value a … holds                                                                            |
| `E3030` | this divides by a constant `0`                                                                          |
| `E3031` | this expression's value is past what an `int` holds, so it cannot be measured against …                 |
| `E3032` | this function's answer is …, and this gives …                                                           |
| `E3033` | cannot bind … to a … binding: `…`                                                                       |
| `E3034` | the binding `…` gives …, which has no type of its own                                                   |
| `E3035` | `type … = …` names no type                                                                              |
| `E3036` | a struct field or an enum payload is …, and this gives …                                                |
| `E3037` | cannot assign … to …, which holds …                                                                     |
| `E3038` | argument … of `…` is …, and this gives …                                                                |
| `E3039` | an optional is not an operand of `…`                                                                    |
| `E3040` | operator `…` has no meaning on … and …                                                                  |
| `E3041` | operator `…` takes bool operands, and these are … and …                                                 |
| `E3042` | operator `…` takes int operands, and these are … and …                                                  |
| `E3043` | operator `…` takes numeric operands, and these are … and …                                              |
| `E3044` | operator `…` orders two numbers or two strs, and these are … and …                                      |
| `E3045` | cannot compare a variant with a number — a variant is a value of ITS enum                               |
| `E3046` | cannot compare … and … — they are different kinds of value                                              |
| `E3047` | operator `…` has no meaning on …                                                                        |
| `E3048` | operator `not` takes a bool operand, and this one is …                                                  |
| `E3049` | operator `-` takes a numeric operand, and this one is …                                                 |
| `E3050` | operator `~` takes an int operand, and this one is …                                                    |
| `E3051` | operator `…` has … on one side and … on the other, and an operator's operands must …                    |
| `E3052` | the condition of … is an optional, and a condition is bool — bind it with `if v := x { … }`             |
| `E3053` | the condition of … must be bool, and Zerg has no truthiness                                             |
| `E3054` | `…` re-binds a `const`                                                                                  |
| `E3055` | `const …` shadows a binding already visible here                                                        |
| `E3056` | the top-level binding `…` may not be `mut` outside a module-level                                       |
| `E3057` | `….…()` renders the value as text, so it takes no arguments beyond the value                            |
| `E3058` | `….…()` renders the value as text, so it is a plain `fn` and not a `mut fn`                             |
| `E3059` | `….…()` answers the `str` the value shows as                                                            |
| `E3060` | `…` is declared twice, once as a generic                                                                |
| `E3061` | `…` is declared both as a generic and as a plain function                                               |
| `E3062` | `This` is the self type, and … is outside an `impl`                                                     |
| `E3063` | `…` declares a parameter named `…` twice                                                                |
| `E3064` | `…(…)` converts ONE value                                                                               |
| `E3065` | `…(…)` does not parse a `str`                                                                           |
| `E3066` | `…` holds an …, and an … is not callable                                                                |
| `E3067` | `…` needs a value for … (…): only a `T?` field has an implicit default, and it is `nil`                 |
| `E3068` | `this` is a method's receiver, and this function has none                                               |
| `E3069` | undefined name `…`                                                                                      |
| `E3070` | a slice bound is an int, and this is …                                                                  |
| `E3071` | a list index is an int, and this is …                                                                   |
| `E3072` | no field `…` on …                                                                                       |
| `E3073` | `.…` reads a tuple element, and … is not a tuple                                                        |
| `E3074` | a tuple of … has no `.…`                                                                                |
| `E3075` | `for … in` walks a list, a map, a str, a range or a channel, and … is not iterable                      |
| `E3076` | raise carries an `Err`, or a message to build one from                                                  |
| `E3077` | `…` is declared twice, once as one kind of declaration and once as another                              |
| `E3078` | `…` is declared twice as the same kind — every module flattens into one namespace                       |
| `E3079` | a variant is named through its enum, and this one is bare                                               |
| `E3080` | a side of an `Either` is named through its type, and this one is bare                                   |
| `E3081` | a closure parameter has no type, and its position gives it none                                         |
| `E3082` | a call through a function value gives the wrong number of arguments                                     |
| `E3083` | `…` is declared in a module-level `unsafe { … }` group, and this is safe code                           |
| `E3084` | module `…` has no `…`                                                                                   |
| `E3085` | `…` is already … — an import binds a name into the one value namespace                                  |
| `E3086` | this position needs a value, and nil is what it was given                                               |
| `E3087` | `…` opens a statement at the top level, and a compiled program runs nothing there                       |
| `E3088` | cannot `…` to `…`: only a `mut` collection can modify its elements                                      |
| `E3089` | cannot `…` `…`: a collection is frozen against structural change inside its own `for` loop              |
| `E3090` | `…(…)` on a `float` — write the verb: `math.trunc` / `floor` / `ceil` / `round`                         |
| `E3091` | a conversion is one step: `…` -> `…` is `…` -> `int` -> `…`, so write the two                           |
| `E3092` | `…` is not a compiler primitive — the `__zrt_…` set is closed                                           |
| `E3093` | the compiler primitive `…` takes … and this gives …                                                     |
| `E3094` | operand … of the compiler primitive `…` is …, and this gives …                                          |
| `E3095` | an enum discriminant is distinct across variants, and `… = …` repeats one already given                 |
| `E3096` | an `impl` in neither the spec's module nor the type's                                                   |
| `E3097` | `#[derive(S)]` on an enum, and a method takes a `This`                                                  |
| `E3098` | `#[derive(S)]` on an enum, and a variant carries no value                                               |
| `E3099` | `#[obj]` on something that is not a `spec`                                                              |
| `E3100` | `#[obj]` and a `mut fn` — a wrapped value is a copy                                                     |
| `E3101` | `#[obj]` and a method taking `This` — an object has forgotten its type                                  |
| `E3102` | `#[derive(…)]` on something with no structure to read                                                   |
| `E3103` | `del …` names nothing this program declares                                                             |
| `E3104` | `del …` names a function, struct, enum or variant — not a binding                                       |
| `E3105` | `…` is used after del                                                                                   |
| `E3106` | `…` is used after del on some paths                                                                     |
| `E3107` | a channel of optionals is refused                                                                       |
| `E3108` | the mutable global `…` may not be `pub`                                                                 |
| `E3109` | cannot receive on a send-only `…`                                                                       |
| `E3110` | cannot send on a receive-only `…`                                                                       |
| `E3111` | cannot close a receive-only channel `…`                                                                 |
| `E3112` | a channel direction only narrows: a `…` cannot fill a `…`                                               |
| `E4001` | `…` outside of a loop: it belongs to a `for`, and a `select` arm is not one                             |
| `E4002` | a `from` cause is an `Err`, and … is not one                                                            |
| `E4004` | `…(…)` names one side of an `Either`, which holds exactly one value                                     |
| `E4005` | `?.` reads through an optional, and … is not one                                                        |
| `E4006` | `int(v)` reads the discriminant, and enum `…` carries a payload, so its tag is opaque                   |
| `E4007` | `?` early-returns the RIGHT of …, so the enclosing function must answer a carrier with …                |
| `E4008` | `…` has been instantiated … times and is still asking for more                                          |
| `E4009` | the type parameter `…` of `…` is not decided by this call                                               |
| `E4010` | `…` does not implement `…`, which `…`'s type parameter `…` is bounded by                                |
| `E4011` | `str(…)` over a list bridges bytes or code points, and this is …                                        |
| `E4012` | `…(…)` converts a value, and … may not have one                                                         |
| `E4013` | an enum converts to `int`                                                                               |
| `E4014` | `….of(n)` reverses the discriminant, and enum `…` carries a payload, so its tag is opaque               |
| `E4015` | `[…]` indexes a value, and … may not have one                                                           |
| `E4016` | undefined function `…`                                                                                  |
| `E4017` | `…` has … fields and this gives …                                                                       |
| `E4018` | variant pattern `…` cannot match a subject of type …                                                    |
| `E4019` | non-exhaustive match: missing variant ….…                                                               |
| `E4020` | `…` on a … needs an `Eq` — there is no structural equality by default                                   |
| `E4021` | `…` is declared … and the value is …: unwrap it with `?? …`, `!` or `if … := …`                         |
| `E4022` | `print` needs a value, and … may not have one                                                           |
| `E4023` | `…` is declared to answer …, and its body falls off the end                                             |
| `E4024` | cannot derive `…`: the derivable specs are compiler-owned, and a `spec` you write is …                  |
| `E4025` | `…` declares `…` twice                                                                                  |
| `E4026` | `…` is part of a cycle of by-value declarations                                                         |
| `E4027` | `…` declares … named `…` twice                                                                          |
| `E4028` | this expression chains more than … levels deep                                                          |
| `E4029` | `…(…)` converts a scalar, and … is not one                                                              |
| `E4030` | `…` is not a variant of `…`                                                                             |
| `E4031` | `…` is a variant of `…`, not of `…`                                                                     |
| `E4032` | this catch-all arm makes the following arms unreachable                                                 |
| `E4033` | `…(…)` says which side of an `Either` a value is, so it needs a declared one to be                      |
| `E4034` | a … is an identity rather than a value, and the language gives it no equality                           |
| `E4035` | `into` is a method of the `Into` spec, and no built-in type implements it                               |
| `E4036` | non-exhaustive match: missing a catch-all `_` arm                                                       |
| `E4037` | a `return` with no value, in a function declared to answer …                                            |
| `E4038` | a … may hold no value, so `…` has nothing to compare                                                    |
| `E4039` | the discriminant of `….…` is not a compile-time constant                                                |
| `E4040` | a fill count is a compile-time constant, and … is not one                                               |
| `E4041` | a fill count is how many copies to make, and `…` is negative                                            |
| `E4042` | a range arm's bound is a compile-time constant, and `…` is not one                                      |
| `E4043` | `…` needs a channel, and … is not one                                                                   |
| `E4045` | the field `…` of `…` is module-private, so it must carry a default                                      |
| `E4053` | a `…` takes a … or a …, and this bare value is neither side                                             |
| `E4054` | no field `…` on … (optional chain `?.…`)                                                                |
| `E4055` | `?` propagates a right the enclosing function does not answer                                           |
| `E4056` | no type named `…` (…)                                                                                   |
| `E4057` | `is …` wants an Err on its left, found a …                                                              |
| `E4058` | `in …` wants an Err on its left, found a …                                                              |
| `E4059` | `list[…](…)` converts a `str` to its bytes, and this is …                                               |
| `E4060` | `list[…](…)` — a `str` bridges to its bytes or its code points                                          |
| `E4061` | `[…]` indexes a list or a map, and this is …                                                            |
| `E4062` | `….of(n)` takes one integer — the discriminant to reverse                                               |
| `E4063` | `…` is testable but not constructible                                                                   |
| `E4064` | no type named `…` to construct                                                                          |
| `E4065` | no variant named `…` — a constructor pattern names one of the subject's                                 |
| `E4066` | non-exhaustive match: missing the Left or the Right case                                                |
| `E4067` | non-exhaustive match: missing the `true` or the `false` case                                            |
| `E4068` | these constants depend on each other and none can be given a value first                                |
| `E4069` | a closure captures `…`, and this compiler cannot work out what it holds                                 |
| `E4070` | `len` takes no argument, and this gives …                                                               |
| `E4071` | `has` asks about one key, and this gives …                                                              |
| `E4072` | `…` has … type parameters and this gives …                                                              |
| `E4073` | `…` is declared twice in this file — one scope declares a name once                                     |
| `E5001` | this entry file declares no `fn main`                                                                   |
| `E5002` | cannot resolve import `…`, and where it was looked for                                                  |
| `E5007` | `…` is a module this build compiles and this module did not import                                      |
| `E5008` | `…` is not a public type of module `…`                                                                  |
| `E5009` | `…` is module-private, and … is on a `pub` declaration                                                  |
| `E5010` | `…` is not a public field of `…`, which module `…` declared                                             |
| `E5011` | `…` names a test file, and a normal build compiles none                                                 |
| `E5012` | `…` is not an import path — the escape, the second spelling, the `.zg`, the name, the reserved word     |
| `E5013` | `…` is both a file and a directory, and a module is one shape or the other                              |
| `E5014` | import cycle: `…` -> `…` -> `…`                                                                         |
| `E5015` | `…` is the module this file is already part of                                                          |
| `E5016` | this unit emits more than `…` bytes of C — `$ZERG_EMIT_MAX`                                             |
| `E9001` | a parameterized `…[…]` as …                                                                             |
| `E9002` | a `spec` member with a BODY                                                                             |
| `E9003` | a generic enum `…[…]`                                                                                   |
| `E9004` | a generic struct `…[…]`                                                                                 |
| `E9005` | the decorator `#[…]`                                                                                    |
| `E9006` | an associated value binding `… := …` in an `impl`                                                       |
| `E9007` | `…` as an `impl` item                                                                                   |
| `E9008` | a struct pattern `…{…}`                                                                                 |
| `E9009` | calling …                                                                                               |
| `E9010` | the named argument `…:`                                                                                 |
| `E9011` | `unsafe { … }` as an EXPRESSION                                                                         |
| `E9012` | an f-string ':spec' format spec                                                                         |
| `E9013` | an f-string '!r' / '!s' / '!a' conversion                                                               |
| `E9014` | the f-string '{expr=}' self-documenting form                                                            |
| `E9015` | an associated type binding `type … = …` in an `impl`                                                    |
| `E9016` | a tuple pattern in a `match` arm                                                                        |
| `E9017` | an array type `[T; N]`                                                                                  |
| `E9018` | an `as` binding in a `match` arm                                                                        |
| `E9019` | an interpolating command literal                                                                        |
| `E9020` | a command literal                                                                                       |
| `E9021` | a destructuring binding `(a, b) := …`                                                                   |
| `E9022` | a range with no lower bound                                                                             |
| `E9023` | a list pattern in a `match` arm                                                                         |
| `E9024` | an or-pattern                                                                                           |
| `E9025` | `for mut v in …`                                                                                        |
| `E9026` | a struct pattern `…{…}` in a `match` arm                                                                |
| `E9027` | a standalone `unsafe fn` declaration                                                                    |
| `E9028` | an associated type projection `….…`                                                                     |
| `E9029` | a value generic parameter `…: …`                                                                        |
| `E9030` | `…[…]` with no call after it                                                                            |
| `E9031` | an `if` EXPRESSION whose branch has more than one statement                                             |
| `E9032` | a binding head in an `if` EXPRESSION                                                                    |
| `E9033` | `asm(…)`                                                                                                |
| `E9034` | a default on a closure parameter                                                                        |
| `E9035` | a `mut &` parameter in a function type                                                                  |
| `E9036` | an `unsafe` `spec` signature                                                                            |
| `E9037` | an `impl` carrying its own type parameters `[…]`                                                        |
| `E9038` | an `impl` on `…[…]` — a type ARGUMENT on the target                                                     |
| `E9039` | `…` is a statement, and an expression is wanted here                                                    |
| `E9040` | `…` is not an expression this compiler reads                                                            |
| `E9041` | a match arm's body is an expression, and this one is a statement                                        |
| `E9042` | `type … = …` over a non-scalar                                                                          |
| `E9043` | `…` leaving a `guard` block                                                                             |
| `E9044` | a generic METHOD `….…[…]`                                                                               |
| `E9045` | the raw-pointer built-in `…`                                                                            |
| `E9046` | the compile-time built-in `…[T]`                                                                        |
| `E9047` | an `impl` on the built-in type `…`                                                                      |
| `E9048` | the `spec` `…` used as a TYPE (…)                                                                       |
| `E9049` | `…` MUTATES its list, and `…` is a value rather than a place                                            |
| `E9050` | an open-ended range has no upper bound here                                                             |
| `E9051` | `….…(…)` is an associated function                                                                      |
| `E9052` | a map key of type …                                                                                     |
| `E9053` | `if … := …` over a …                                                                                    |
| `E9054` | `#[derive(…)]`                                                                                          |
| `E9055` | `#[derive(Eq)]` on `…`                                                                                  |
| `E9056` | the list method `…`                                                                                     |
| `E9057` | structural equality over a container                                                                    |
| `E9058` | a refcounted box `Ref(x)` / `deref(r)`                                                                  |
| `E9059` | rendering a … as text                                                                                   |
| `E9060` | a second `impl Into[…] for …`                                                                           |
| `E9061` | `in` over a list whose elements have no `==`                                                            |
| `E9062` | `in` over anything but a list, a map, a range or an error kind                                          |
| `E9063` | `…` is part of the fixed-width ladder                                                                   |
| `E9064` | the built-in `set`                                                                                      |
| `E9065` | … is a `mut &`, and a function VALUE cannot carry one                                                   |
| `E9066` | `del …` on a CHANNEL                                                                                    |
| `E9067` | `…[…](…)` as a constructor                                                                              |
| `E9068` | `nil` as a `match` pattern                                                                              |
| `E9069` | … whose value has no type this compiler can name                                                        |
| `E9070` | `…` re-binds a name a `match` arm's pattern already binds                                               |
| `E9071` | the default on field `…` reads the field `…`                                                            |
| `E9072` | a destructuring assignment `(a, b) = …`                                                                 |
| `E9073` | an `unsafe fn(…)` TYPE                                                                                  |
| `E9074` | an `impl` on `….…` — a dotted target                                                                    |
| `E9075` | a generic `type …[…] = …`                                                                               |
| `E9076` | a sub-pattern inside a variant payload                                                                  |
| `E9077` | a range used as a value                                                                                 |
| `E9078` | `is …` names one of the built-in error kinds                                                            |
| `E9079` | the decorator `#[sealed]` — reserved                                                                    |
| `E9080` | `?` on a … — it unwraps the Left of a carrier                                                           |
| `E9081` | two modules both define `…` and at least one is `pub`                                                   |
| `E9082` | `…` and `…` both define `…` — one flat namespace                                                        |
| `E9083` | `!` on a … — it forces a Result[T] or a T?                                                              |
| `E9084` | `??` on a … — its left side is a Result[T] or a T?                                                      |
| `E9085` | rendering a … as text — an enum has no name for its variant                                             |
| `E9086` | the method `…` on an Err takes no argument                                                              |
| `E9087` | the method `…` on a Err — the `Error` interface is three names                                          |
| `E9088` | `ok_or` takes the error to answer an absence with, and none was given                                   |
| `E9089` | `ok_or` takes ONE error to answer an absence with                                                       |
| `E9090` | `ok_or` answers an absence with an `Err`, and this is a …                                               |
| `E9091` | `ok` forgets the Right and takes no argument                                                            |
| `E9092` | the method `…` on a … — a carrier answers `ok_or` and `ok`                                              |
| `E9093` | `….…(…)` — an enum type answers `of(n)` and its own variants                                            |
| `E9094` | the method `…` on a …                                                                                   |
| `E9095` | the pattern `…` on a Result[T] — it has Left and Right                                                  |
| `E9096` | `fn main() -> …` — the entry answers nothing, an int or a Result[nil]                                   |
| `E9097` | main(args) in a program that uses concurrency                                                           |
| `E9098` | … of anything but a function, a method, or a namespaced function                                        |
| `E9099` | … of `…` — not a function, a method, or a namespaced function                                           |
| `E9100` | the map method `…`                                                                                      |
| `E9101` | the field `…` on an Err — it has `msg` and `kind`                                                       |
| `E9102` | `..=` with no upper bound is not a range                                                                |
| `E9103` | a `spawn`/`defer` of `…`, a binding that HOLDS a function                                               |
| `E9104` | the module `atomic` ships and cannot be imported                                                        |
| `E9105` | a remote package — the path names a host, and resolving one needs a package layer                       |
| `E9106` | module `…` declares the function `…`, and a module's function is not a value here                       |

They are reported the moment a file is **read**, before its imports are scanned — scanning
them parses, and a parser handed unreadable text can only say something untrue about it.
That is what it used to say: `` `b'b` is not an expression this compiler reads ``, which
names the wrong layer, the wrong problem, and a fragment of what the person wrote.

`E1008` had no message at all. `0x` lowered to a C `0x`, which cc read as zero, so a
malformed literal compiled and the program answered 0. It says **immediately** because the
digit is part of the prefix's own production: `0x_1F` has digits after `0x` and is still not
a number, since a grouping `_` sits between two digits and there is none to its left.

One rule stood among them and has **retired**. It reported a bare name in pattern position —
"`Zzz` is a variant of some enum, and a pattern names one through its enum" — decided by the
name's first letter, in a parser that had resolved nothing and knew of no enum. So it fired
on programs that declared none, and the sentence naming the enum was simply false. A bare
name in pattern position is **always** a fresh binding
([Grammar](../surface/grammar.md)), whatever its case, and the mistake the rule was aimed at
— two variants written without their enum — is `E4032` instead: the first binds everything,
so the arms below it are unreachable.

### Retired codes

**A retired number is never reused.** A code is a stable identity, and reusing one makes an
old build's message mean something it never meant — a report from a user, a log, a bug
filed last year, all silently reassigned to a different rule. So a retired code leaves the
table above and is listed here instead, and the range it sat in goes on counting from its
own high-water mark.

That is also what makes the catalogue **queryable**. `make error-codes-check` reports the
**next free code in every range**, which is the question anybody adding a rule has —
including two agents working in parallel, who between them collided on three numbers in a
single week because the only way to ask was to read the table by eye. The answer is only
reliable while every number below the mark is accounted for, so the gate holds each range to
exactly that: a number that is neither listed above nor listed here is a **gap**, and a gap
is a code somebody may reissue without knowing.

**Twenty-five, and they are one event rather than twenty-five.** A range names the KIND OF
QUESTION a code answers, and these twenty-five sat in a range that named a different one. They
were re-seated in a single pass **before the first release**, which is the only time it is
free: nothing has ever shipped under these numbers, so no report, no log and no bug filed
last year carries one. They are listed here all the same, because the rule that a number is
never reused is a rule about the NUMBER and not about who happens to have seen it.

| Code    | Now     | Why it left                                                   |
| ------- | ------- | ------------------------------------------------------------- |
| `E2009` | `E3095` | a repeated discriminant is a meaning, not a form              |
| `E2037` | `E3096` | an orphan `impl` is coherence, which is a meaning             |
| `E2038` | `E3097` | what a `derive` can generate is a meaning                     |
| `E2039` | `E3098` | as above                                                      |
| `E2040` | `E3099` | what `#[obj]` may go on is a meaning                          |
| `E2041` | `E3100` | as above                                                      |
| `E2042` | `E3101` | as above                                                      |
| `E2043` | `E3102` | what a `derive` reads is a meaning                            |
| `E2050` | `E3103` | whether a name is declared is a meaning                       |
| `E2051` | `E3104` | what kind of thing a name is, is a meaning                    |
| `E2052` | `E3105` | whether a name still has storage is a meaning                 |
| `E2053` | `E3106` | as above, over a branch                                       |
| `E4003` | `E3107` | which element types a channel may carry is a type rule        |
| `E4044` | `E2065` | `GRAMMAR#map-entry` — a form                                  |
| `E4046` | `E3108` | what a declaration may be marked is a meaning                 |
| `E4047` | `E5014` | an import cycle is the program as a set of files              |
| `E4048` | `E2066` | where a decorator may go is a form                            |
| `E4049` | `E2067` | `GRAMMAR#impl-decl` — a form                                  |
| `E4050` | `E2068` | `GRAMMAR#decorator` — a form                                  |
| `E4051` | `E2069` | `GRAMMAR#deco-item` — a form                                  |
| `E4052` | `E2070` | `GRAMMAR#chan-type` — a form                                  |
| `E5003` | `E3109` | a channel's direction is a type rule, not a whole-program one |
| `E5004` | `E3110` | as above                                                      |
| `E5005` | `E3111` | as above                                                      |
| `E5006` | `E3112` | as above                                                      |

Twenty-seven others were measured and are **not** findings, which is what deciding the
question settled: the twenty-two checking rules `emit.zg` reports (it is _AST -> C, with the
minimal typecheck emit needs_, so the file is an implementation fact), the four visibility
rules that are about the program as a set of files, and `E1011` — a source that is not UTF-8
is a question about TEXT, and the driver reports it only because the driver is what opens the
file.
