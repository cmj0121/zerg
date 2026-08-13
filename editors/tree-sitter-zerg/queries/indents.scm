;; Indentation from the tree, for editors that indent that way.
;;
;; The ftplugin's `indentexpr` is what nvim uses here and it is the one held by
;; `make editor-align`; this file is for a client that prefers the tree, and it says the same
;; thing: a level per group, and the line that closes a group is at the outer level.

[
  (block)
  (field_block)
  (variant_block)
  (spec_block)
  (impl_block)
  (match_expression)
  (select_statement)
  (for_select_statement)
  (import_statement)
  (argument_list)
  (parameter_list)
  (list_literal)
  (map_literal)
] @indent.begin

[ "}" ")" "]" ] @indent.branch @indent.end

(comment) @indent.auto
