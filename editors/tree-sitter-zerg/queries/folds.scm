;; Folds from the tree.
;;
;; The ftplugin folds by brace depth, which is a good rule and a blind one: it cannot tell a
;; block from a map literal, and it has no idea that a `match` arm list is one thing. These do.
;;
;; A fold covers the whole node, so an editor using them shows the line that OPENS the
;; construct — the same thing `foldexpr`'s "lowest depth" rule buys, arrived at differently.

(block) @fold
(field_block) @fold
(variant_block) @fold
(spec_block) @fold
(impl_block) @fold
(match_expression) @fold
(select_statement) @fold
(for_select_statement) @fold
(import_statement) @fold
(argument_list) @fold
(parameter_list) @fold
(list_literal) @fold
(map_literal) @fold
