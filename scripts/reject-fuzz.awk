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
    } else if (KIND == "write-immutable" && match(line, /^[ \t]+[a-z_][a-z_0-9]* := /) && ends_stmt(line)) {
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

# ends_stmt tells a binding that FINISHES on its line from one whose value runs on. The
# mutations are line-based, and this one writes its statement on the NEXT line — so a
# binding whose call the formatter wrapped (`box := Box(` … `)`) had the write inserted
# into the middle of the expression, and the parser refused `=` as an expression instead of
# refusing the write. That is a refusal about the mutator, and it counted against a ceiling
# that watches the checker's rules.
function ends_stmt(s,   i, c, depth) {
  depth = 0
  for (i = 1; i <= length(s); i++) {
    c = substr(s, i, 1)
    if (c == "(" || c == "[" || c == "{") depth++
    else if (c == ")" || c == "]" || c == "}") depth--
  }
  return depth == 0
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
