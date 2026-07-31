# One mutation per run, at the FIRST place it applies. `mut` is optional in every binding
# pattern: the corpus is mostly `mut i := 0`, and a pattern without it matched one file.
BEGIN { done = 0 }
{
  line = $0
  if (!done) {
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
    }
  }
  print line
}
END { if (!done) exit 3 }
