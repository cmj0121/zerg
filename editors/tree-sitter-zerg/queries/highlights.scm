;; Highlighting from a TREE rather than from a regular expression.
;;
;; `syntax/zerg.vim` colours by pattern and says so in its own comment: `\<\u\w*\>` makes
;; every capitalised word a type, "a highlight heuristic, not a grammar rule", because a
;; highlighter that cannot parse cannot tell a type from a variant from a constructor call.
;; Here they are different nodes, so they get different captures — and a lowercase type name
;; is coloured correctly for the first time.

; --- comments and literals ---------------------------------------------------------------

(comment) @comment @spell

(integer_literal) @number
(float_literal) @number.float
(boolean_literal) @boolean
(nil_literal) @constant.builtin
(this_literal) @variable.builtin

(string_literal) @string
(raw_string_literal) @string
(rune_literal) @character
(byte_literal) @character
(command_literal) @string.special
(escape_sequence) @string.escape

(format_string) @string
(format_command) @string.special
(interpolation "{" @punctuation.special)
(interpolation "}" @punctuation.special)
(format_conversion) @string.special
(format_spec) @string.special

; --- declarations ---------------------------------------------------------------------------

(function_declaration name: (identifier) @function)
(struct_declaration name: (identifier) @type)
(enum_declaration name: (identifier) @type)
(spec_declaration name: (identifier) @type.definition)
(type_declaration name: (identifier) @type.definition)
(variant_definition name: (identifier) @constructor)
(field_definition name: (identifier) @variable.member)
(parameter name: (identifier) @variable.parameter)
(type_parameter (identifier) @type.parameter)

(decorator) @attribute
(decorator_item (identifier) @attribute)

(import_spec path: (string_literal) @string.special.path)
(import_spec alias: (identifier) @module)

; --- types ---------------------------------------------------------------------------------
;
; A type is a type because it is IN a type position, which is the whole difference from the
; vim file. `list`, `int` and `str` are ordinary identifiers the parser resolves; nothing here
; has to guess from their spelling, and nothing has to keep a list of them in step with the
; compiler's.

(type_identifier) @type
(qualified_type (identifier) @type)
(generic_type (type_identifier) @type)
(optional_type "?" @punctuation.special)
(channel_type "chan" @type.builtin)
(pointer_type "ptr" @type.builtin)

; --- expressions -----------------------------------------------------------------------------

(call_expression function: (identifier) @function.call)
(method_call method: (identifier) @function.method.call)
(field_expression field: (identifier) @variable.member)
(argument (identifier) @variable.parameter (_))

(variant_pattern (type_identifier) @type (identifier) @constructor)
(struct_pattern (type_identifier) @type)
(field_pattern (identifier) @variable.member)
(identifier_pattern) @variable
(wildcard_pattern) @character.special

; --- keywords ---------------------------------------------------------------------------------

[
  "fn"
  "struct"
  "enum"
  "spec"
  "impl"
  "type"
  "init"
] @keyword

["pub" "mut" "const"] @keyword.modifier
["import" "as" "from"] @keyword.import
["return" "break" "continue" "raise" "guard" "defer" "del"] @keyword.return
["if" "else" "match" "with"] @keyword.conditional
["for" "in"] @keyword.repeat
["spawn" "select" "close" "chan"] @keyword.coroutine
["print"] @keyword

; `nop` is the whole of `nop_statement`, so tree-sitter inlines the token and there is no
; anonymous `"nop"` node to name. The named rule is what the query has to ask for.
(nop_statement) @keyword
["not" "and" "or" "is"] @keyword.operator

; The danger surface, as its own capture rather than an ordinary keyword. `unsafe` is the
; trust boundary and `asm` is inline assembly; the vim file gives them a bold red of their
; own and this says the same thing in the way a tree-sitter theme understands.
["unsafe" "asm"] @keyword.exception

; --- operators and punctuation -------------------------------------------------------------------

[
  "+" "-" "*" "/" "//" "%" "+%" "-%" "*%"
  "==" "!=" "<" ">" "<=" ">="
  "<<" ">>" "&" "|" "^" "~"
  "=" ":=" "??" "?." "!" "?"
  "->" "=>" "<-" ".." "..=" "+"
] @operator

[";" "," ":" "."] @punctuation.delimiter
["(" ")" "[" "]" "{" "}"] @punctuation.bracket
