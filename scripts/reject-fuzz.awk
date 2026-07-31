# One mutation per run, at the FIRST place it applies. `mut` is optional in every binding
# pattern: the corpus is mostly `mut i := 0`, and a pattern without it matched one file.
#
# A COMMENT is not code. Mutating one produced a "refusal" that measured the lexer's opinion
# of prose, which is not what any of these kinds are about.
BEGIN { done = 0; in_enum = 0 }
{
  line = $0

  if (match(line, /^[ \t]*(pub )?enum /)) { in_enum = 1 }
  else if (in_enum && match(line, /^\}/)) { in_enum = 0 }

  iscode = !match(line, /^[ \t]*#/)
  if (!done && iscode) {
    isbind = match(line, /^[ \t]+(mut )?[a-z_][a-z_0-9]* := [0-9]+$/)
    if (KIND == "wrong-type" && isbind) {
      sub(/ := /, ": str = ", line); done = 1
    } else if (KIND == "write-immutable" && match(line, /^[ \t]+[a-z_][a-z_0-9]* := /)) {
      ind = line; sub(/[^ \t].*/, "", ind)
      nm = line; sub(/^[ \t]+/, "", nm); sub(/ :=.*/, "", nm)
      print line; line = ind nm " = 1"; done = 1
    } else if (KIND == "mixed-operands" && isbind) {
      line = line " + \"s\""; done = 1
    } else if (KIND == "int-condition" && match(line, /^[ \t]+if .*\{$/)) {
      ind = line; sub(/[^ \t].*/, "", ind); line = ind "if 1 {"; done = 1
    } else if (KIND == "extra-arg" && match(line, /[a-z_][a-z_0-9]*\([a-z_0-9][^()]*\)/)) {
      sub(/\)/, ", 1)", line); done = 1
    } else if (KIND == "missing-arg" && iscall(line) &&
               match(line, /[a-z_][a-z_0-9]*\([a-z_0-9][^(),]*, [^()]*\)/)) {
      # the mirror of extra-arg. Its absence is why `spawn work(5)` on a defaulted
      # parameter reached cc unnoticed: every mutation ADDED an argument, so a call
      # short of one was a shape this gate never produced.
      sub(/, [^()]*\)/, ")", line); done = 1
    }
  }
  print line
}

# iscall tells a CALL from the two declarations that look like one. `fn gcd(a: int, b: int)`
# and a variant `Line(int, int)` both match the call pattern, and deleting from either
# produces `undefined name` — a true refusal about a different rule, which is how this kind
# came to buy a raise in the no-place ceiling while barely testing the arity it names.
function iscall(s) {
  if (in_enum) return 0
  if (match(s, /^[ \t]*(pub )?fn /)) return 0
  if (match(s, /\(.*: /)) return 0
  return 1
}
END { if (!done) exit 3 }
