/**
 * tree-sitter-zerg — a parser for Zerg, for editors that ask for a tree.
 *
 * WHAT THIS IS AND WHAT IT IS NOT. Everything else in this repository is held to the
 * compiler by CALLING it: `zerg fmt` is the formatter, `zerg lsp` asks `emit_files_diag`
 * rather than checking anything itself, and the two facts the vim files must repeat are
 * held by `make editor-align`. This file cannot be held that way. It is a second
 * implementation of GRAMMAR, and no diff can compare a tree-sitter rule with a BNF
 * production or with `parser.zg`.
 *
 * So what holds it is a CORPUS rather than a diff: `make -C editors treesitter` parses every
 * `.zg` file in the tree — the compiler's own sources, the standard library, the examples and
 * the private corpus — and fails on a single ERROR or MISSING node. That is weaker than the
 * other gates here and is worth saying plainly: it can only see a form some file contains.
 * It is also the strongest check available for a grammar written twice, and it is why the
 * file list is everything rather than a sample.
 *
 * WHY IT EARNS ITS KEEP anyway: `syntax/zerg.vim` colours by regular expression, and its own
 * comment admits the load-bearing guess — `\<\u\w*\>` makes every capitalised word a type,
 * because a highlighter cannot run name resolution. A parser does not guess. It also gives
 * structural folds and text objects, which a regex cannot.
 */

const PREC = {
	coalesce: 1,
	or: 2,
	and: 3,
	cmp: 4,
	range: 5,
	add: 6,
	mul: 7,
	unary: 8,
	postfix: 9,
};

module.exports = grammar({
	name: "zerg",

	// The newline is an external token so it can be a statement separator where a statement
	// could end and whitespace everywhere else — see src/scanner.c. It stays in `extras` too,
	// because the scanner is only consulted where the parser can accept a separator.
	externals: ($) => [
		$._newline,
		$._string_chunk,
		$._multiline_chunk,
		$._fstring_chunk,
		$._fcommand_chunk,
	],
	extras: ($) => [$.comment, /[ \t\r\n]/],

	word: ($) => $.identifier,

	// A conflict is a place where one prefix can still become two things, and the parser is
	// told to keep both alive until the input decides — `{ x: …` is a map entry or a block
	// opening with a typed binding, `Point {` is a struct pattern or a name and a block.
	//
	// THIS LIST IS REBUILT, NOT APPENDED TO. It was accreted one entry at a time as `generate`
	// named them, which leaves entries that a later rule change made unnecessary — and a stale
	// conflict silently suppresses the report for a REAL ambiguity in that same state. It was
	// emptied and regrown from nothing after the last rule change; every entry here is one the
	// generator still demands.
	conflicts: ($) => [
		[$.channel_type],
		[$._base_type, $.generic_type],
		[$.pointer_type],
		[$.function_declaration],
		[$._statement, $._primary, $.match_arm],
		[$._type, $.optional_type],
		[$.for_statement, $._primary],
		[$.return_statement, $._if_head],
		[$.parameter, $.type_identifier],
		[$.send_statement, $.expression_statement],
		[$.raise_statement],
		[$.raise_expression],
		[$.if_statement, $.if_expression],
		[$.argument_list, $.pattern_arguments],
		[$.continue_statement],
		[$.break_statement],
		[$.method_call, $.field_expression],
		[$.return_statement],
		[$.block, $.unsafe_group],
		[$.list_literal, $.list_pattern],
		[$._bind_target, $._primary],
		[$._primary, $.literal_pattern],
		[$._primary, $._range_bound],
		[$.type_identifier, $.qualified_type, $._primary],
		[$._primary, $.identifier_pattern],
		[$.binding, $._primary],
		[$._statement, $._primary],
		[$.qualified_type, $._primary],
		[$.type_identifier, $._primary],
	],

	rules: {
		// GRAMMAR#program — `program ::= stmt-list`. Script mode is why the top level is a
		// statement list and not a declaration list.
		source_file: ($) => optional($._statement_list),

		// `stmt-sep+` and not one separator, which GRAMMAR spells and the first version of this
		// did not. With one, two statements with a COMMENT LINE between them failed to parse:
		// the scanner collapses a run of blank lines into a single token, so a blank line was
		// fine, but a comment interrupts the run and leaves a separator on each side of it.
		// Every file in this repository has that shape somewhere.
		//
		// The statements are OPTIONAL inside the separators, not the other way round. With the
		// list as a whole optional and its contents required, `{` newline `}` — an empty block
		// spanning two lines, which the corpus has as an expression yielding nil — consumed the
		// separator and then had to find a statement that was not there.
		_statement_list: ($) =>
			choice(
				repeat1($._statement_separator),
				seq(
					repeat($._statement_separator),
					$._statement,
					repeat(seq(repeat1($._statement_separator), $._statement)),
					repeat($._statement_separator),
				),
			),

		_statement_separator: ($) => choice($._newline, ";"),

		comment: (_) => token(seq("#", /.*/)),

		// --- statements -----------------------------------------------------------------

		_statement: ($) =>
			choice(
				$.import_statement,
				$._declaration,
				$.nop_statement,
				$.binding,
				$.assignment,
				$.print_statement,
				$.return_statement,
				$.raise_statement,
				$.break_statement,
				$.continue_statement,
				$.spawn_statement,
				$.send_statement,
				$.close_statement,
				$.defer_statement,
				$.del_statement,
				$.block,
				$.with_statement,
				$.if_statement,
				$.for_statement,
				$.for_select_statement,
				$.select_statement,
				$.expression_statement,
			),

		nop_statement: (_) => "nop",

		block: ($) => seq("{", optional($._statement_list), "}"),

		// GRAMMAR#binding — `mut`/`const` and the inferred `:=`, or a typed `x: T = e`.
		binding: ($) =>
			choice(
				seq(
					optional(choice("mut", "const")),
					field("name", $._bind_target),
					":=",
					field("value", $._expression),
				),
				seq(
					optional(choice("mut", "const")),
					field("name", $.identifier),
					":",
					field("type", $._type),
					"=",
					field("value", $._expression),
				),
			),

		_bind_target: ($) =>
			choice($.identifier, $.tuple_pattern, $.struct_pattern),

		assignment: ($) =>
			seq(field("target", $._lvalue), "=", field("value", $._expression)),

		_lvalue: ($) =>
			choice($.identifier, $.field_expression, $.index_expression),

		print_statement: ($) => seq("print", $._expression),

		return_statement: ($) =>
			seq(
				"return",
				optional($._expression),
				optional(seq("if", $._expression)),
			),

		raise_statement: ($) =>
			seq($.raise_expression, optional(seq("if", $._expression))),
		raise_expression: ($) =>
			seq("raise", $._expression, optional(seq("from", $._expression))),

		break_statement: ($) =>
			seq("break", optional(seq("if", $._expression))),
		continue_statement: ($) =>
			seq("continue", optional(seq("if", $._expression))),

		spawn_statement: ($) => seq("spawn", $._expression),
		send_statement: ($) =>
			seq(
				field("channel", $._expression),
				"<-",
				field("value", $._expression),
			),
		close_statement: ($) => seq("close", "(", $._expression, ")"),
		defer_statement: ($) =>
			seq("defer", choice($.close_statement, $._expression)),
		del_statement: ($) => seq("del", $.identifier),

		with_statement: ($) =>
			seq(
				"with",
				$._expression,
				optional(seq("as", $.identifier)),
				$.block,
			),

		if_statement: ($) =>
			prec.right(
				seq(
					"if",
					field("condition", $._if_head),
					field("consequence", $.block),
					repeat(
						seq(
							"else",
							"if",
							field("condition", $._if_head),
							field("consequence", $.block),
						),
					),
					optional(seq("else", field("alternative", $.block))),
				),
			),

		_if_head: ($) =>
			choice($._expression, seq($.identifier, ":=", $._expression)),

		for_statement: ($) =>
			seq(
				"for",
				optional(
					choice(
						seq(
							field("binder", $._bind_target),
							"in",
							field("iterable", $._expression),
						),
						field("condition", $._expression),
					),
				),
				$.block,
			),

		for_select_statement: ($) =>
			seq("for", "select", "{", repeat($.select_arm), "}"),
		select_statement: ($) => seq("select", "{", repeat($.select_arm), "}"),

		select_arm: ($) =>
			seq(
				choice(
					seq(
						optional(seq(choice($.identifier, "_"), ":=")),
						"<-",
						$._expression,
					),
					seq($._expression, "<-", $._expression),
					"_",
				),
				"=>",
				$._statement,
				optional($._statement_separator),
			),

		expression_statement: ($) => $._expression,

		// --- imports ---------------------------------------------------------------------

		import_statement: ($) =>
			seq(
				"import",
				choice(
					$.import_spec,
					seq(
						"(",
						repeat(
							seq(
								$.import_spec,
								optional($._statement_separator),
							),
						),
						")",
					),
				),
			),

		import_spec: ($) =>
			seq(
				optional("pub"),
				field("path", $.string_literal),
				optional(seq("as", field("alias", $.identifier))),
			),

		// --- declarations ------------------------------------------------------------------

		_declaration: ($) =>
			seq(
				repeat($.decorator),
				choice(
					$.function_declaration,
					$.struct_declaration,
					$.enum_declaration,
					$.spec_declaration,
					$.impl_declaration,
					$.type_declaration,
					$.init_declaration,
					$.unsafe_group,
				),
			),

		decorator: ($) => seq("#[", sepBy1(",", $.decorator_item), "]"),
		decorator_item: ($) =>
			seq(
				$.identifier,
				optional(seq("(", sepBy1(",", $._expression), ")")),
			),

		function_declaration: ($) =>
			seq(
				optional("pub"),
				optional("unsafe"),
				optional("mut"),
				"fn",
				field("name", $.identifier),
				optional($.type_parameters),
				"(",
				optional($.parameter_list),
				")",
				optional($.return_type),
				optional(field("body", $.block)),
			),

		return_type: ($) => seq("->", $._type),

		parameter_list: ($) => sepBy1(",", $.parameter),
		parameter: ($) =>
			seq(
				optional(seq("mut", "&")),
				field("name", $.identifier),
				optional(seq(":", field("type", $._type))),
				optional(seq("=", field("default", $._expression))),
			),

		struct_declaration: ($) =>
			seq(
				optional("pub"),
				"struct",
				field("name", $.identifier),
				optional($.type_parameters),
				$.field_block,
			),

		field_block: ($) => blockOf($, $.field_definition),

		field_definition: ($) =>
			seq(
				optional("pub"),
				field("name", $.identifier),
				":",
				field("type", $._type),
				optional(seq("=", $._expression)),
			),

		enum_declaration: ($) =>
			seq(
				optional("pub"),
				"enum",
				field("name", $.identifier),
				optional($.type_parameters),
				$.variant_block,
			),

		variant_block: ($) => blockOf($, $.variant_definition),

		variant_definition: ($) =>
			seq(
				field("name", $.identifier),
				optional(seq("(", sepBy1(",", $._type), ")")),
				optional(seq("=", $._expression)),
			),

		spec_declaration: ($) =>
			seq(
				optional("pub"),
				"spec",
				field("name", $.identifier),
				optional($.type_parameters),
				optional(seq(":", $.bound)),
				$.spec_block,
			),

		spec_block: ($) =>
			blockOf($, choice($.function_declaration, $.value_binding)),

		value_binding: ($) => seq($.identifier, ":=", $._expression),

		impl_declaration: ($) =>
			seq(
				"impl",
				optional($.type_parameters),
				field("spec", $._type),
				optional(seq("for", field("target", $._type))),
				$.impl_block,
			),

		impl_block: ($) =>
			blockOf(
				$,
				choice(
					seq(repeat($.decorator), $.function_declaration),
					$.value_binding,
				),
			),

		type_declaration: ($) =>
			seq(
				optional("pub"),
				"type",
				field("name", $.identifier),
				optional($.type_parameters),
				"=",
				field("value", $._type),
			),

		init_declaration: ($) => seq("init", "(", ")", $.block),

		unsafe_group: ($) =>
			seq("unsafe", "{", optional($._statement_list), "}"),

		type_parameters: ($) => seq("[", sepBy1(",", $.type_parameter), "]"),
		type_parameter: ($) => seq($.identifier, optional(seq(":", $.bound))),
		// A bound may carry TYPE ARGUMENTS — `spec Ord: Same[int]`, `fn f[T: Into[int]]` — which
		// is what makes a parameterized spec sayable at all.
		bound: ($) => sepBy1("+", choice($.generic_type, $.type_identifier)),

		// --- types -------------------------------------------------------------------------

		_type: ($) => choice($.optional_type, $._base_type),
		optional_type: ($) => seq($._base_type, "?"),

		_base_type: ($) =>
			choice(
				$.generic_type,
				$.qualified_type,
				$.type_identifier,
				$.function_type,
				$.tuple_type,
				$.array_type,
				$.channel_type,
				$.pointer_type,
			),

		type_identifier: ($) => $.identifier,
		qualified_type: ($) =>
			seq($.identifier, repeat1(seq(".", $.identifier))),
		generic_type: ($) =>
			seq(choice($.qualified_type, $.type_identifier), $.type_arguments),
		type_arguments: ($) =>
			seq("[", sepBy1(",", choice($._type, $._expression)), "]"),

		function_type: ($) =>
			seq(
				optional("unsafe"),
				"fn",
				"(",
				optional(sepBy1(",", seq(optional(seq("mut", "&")), $._type))),
				")",
				optional($.return_type),
			),

		tuple_type: ($) => seq("(", $._type, repeat1(seq(",", $._type)), ")"),
		array_type: ($) => seq("[", $._type, ";", $._expression, "]"),
		channel_type: ($) =>
			choice(
				seq("chan", "[", $._type, "]", optional("<-")),
				seq("<-", "chan", "[", $._type, "]"),
			),
		pointer_type: ($) => seq("ptr", optional(seq("[", $._type, "]"))),

		// --- expressions ---------------------------------------------------------------------

		_expression: ($) =>
			choice(
				$.coalesce_expression,
				$.binary_expression,
				$.unary_expression,
				$.range_expression,
				$.is_expression,
				$.in_expression,
				$._unary_operand,
			),

		coalesce_expression: ($) =>
			prec.right(
				PREC.coalesce,
				seq(
					$._expression,
					"??",
					choice(
						$._expression,
						$.break_statement,
						$.continue_statement,
						$.return_statement,
						$.raise_expression,
					),
				),
			),

		binary_expression: ($) => {
			const table = [
				[PREC.or, "or"],
				[PREC.and, "and"],
				[PREC.cmp, choice("==", "!=", "<", ">", "<=", ">=")],
				[PREC.add, choice("+", "-", "+%", "-%", "|", "^")],
				[PREC.mul, choice("*", "/", "//", "%", "*%", "<<", ">>", "&")],
			];
			return choice(
				...table.map(([precedence, operator]) =>
					prec.left(
						precedence,
						seq(
							field("left", $._expression),
							field("operator", operator),
							field("right", $._expression),
						),
					),
				),
			);
		},

		is_expression: ($) =>
			prec.left(PREC.cmp, seq($._expression, "is", $.type_identifier)),
		in_expression: ($) =>
			prec.left(PREC.cmp, seq($._expression, "in", $._expression)),

		range_expression: ($) =>
			prec.left(
				PREC.range,
				choice(
					seq($._expression, "..", optional($._expression)),
					seq($._expression, "..=", $._expression),
				),
			),

		unary_expression: ($) =>
			prec.right(
				PREC.unary,
				seq(choice("not", "~", "-", "-%"), $._expression),
			),

		_unary_operand: ($) =>
			choice($.receive_expression, $._postfix_expression),
		receive_expression: ($) =>
			prec.right(PREC.unary, seq("<-", $._unary_operand)),

		_postfix_expression: ($) =>
			choice(
				$._primary,
				$.call_expression,
				$.method_call,
				$.field_expression,
				$.index_expression,
				$.force_expression,
				$.try_expression,
				$.optional_chain,
			),

		// `list[byte](s)` is a conversion, and its callee is a TYPE — including a generic one,
		// which is the form every `str`/`list` conversion in this tree is written in.
		call_expression: ($) =>
			prec(
				PREC.postfix,
				seq(
					field(
						"function",
						choice(
							$.identifier,
							$.field_expression,
							$.type_identifier,
							$.generic_type,
							$.qualified_type,
						),
					),
					$.argument_list,
				),
			),
		method_call: ($) =>
			prec(
				PREC.postfix,
				seq(
					field("receiver", $._postfix_expression),
					".",
					field("method", $.identifier),
					$.argument_list,
				),
			),
		field_expression: ($) =>
			prec(
				PREC.postfix,
				seq(
					field("object", $._postfix_expression),
					".",
					field("field", choice($.identifier, $.integer_literal)),
				),
			),
		optional_chain: ($) =>
			prec(PREC.postfix, seq($._postfix_expression, "?.", $.identifier)),
		index_expression: ($) =>
			prec(
				PREC.postfix,
				seq(
					field("object", $._postfix_expression),
					"[",
					$._expression,
					"]",
				),
			),
		force_expression: ($) =>
			prec(PREC.postfix, seq($._postfix_expression, "!")),
		try_expression: ($) =>
			prec(PREC.postfix, seq($._postfix_expression, "?")),

		argument_list: ($) =>
			seq("(", optional(sepBy1(",", $.argument)), optional(","), ")"),
		argument: ($) => seq(optional(seq($.identifier, ":")), $._expression),

		_primary: ($) =>
			choice(
				$._literal,
				$.format_string,
				$.format_command,
				$.identifier,
				$.parenthesized_expression,
				$.tuple_literal,
				$.list_literal,
				$.fill_literal,
				$.map_literal,
				$.function_literal,
				$.match_expression,
				$.guard_expression,
				$.if_expression,
				$.unsafe_expression,
				$.block,
				$.channel_new,
				$.asm_expression,
			),

		parenthesized_expression: ($) => seq("(", $._expression, ")"),
		tuple_literal: ($) =>
			seq(
				"(",
				$._expression,
				repeat1(seq(",", $._expression)),
				optional(","),
				")",
			),
		list_literal: ($) =>
			seq(
				"[",
				optional(seq(sepBy1(",", $._expression), optional(","))),
				"]",
			),
		fill_literal: ($) => seq("[", $._expression, ";", $._expression, "]"),
		map_literal: ($) =>
			choice(
				seq("{", ":", "}"),
				seq("{", sepBy1(",", $.map_entry), optional(","), "}"),
			),
		map_entry: ($) =>
			seq(
				field("key", $._expression),
				":",
				field("value", $._expression),
			),

		function_literal: ($) =>
			seq(
				"fn",
				"(",
				optional($.parameter_list),
				")",
				optional($.return_type),
				$.block,
			),

		match_expression: ($) =>
			seq(
				"match",
				field("value", $._expression),
				"{",
				repeat($.match_arm),
				"}",
			),
		match_arm: ($) =>
			seq(
				field("pattern", $._pattern),
				optional(seq("if", field("guard", $._expression))),
				"=>",
				field("body", choice($.block, $._statement)),
				optional($._statement_separator),
			),

		guard_expression: ($) => seq("guard", $.block),
		if_expression: ($) =>
			prec.right(
				seq(
					"if",
					$._if_head,
					$.block,
					repeat(seq("else", "if", $._if_head, $.block)),
					"else",
					$.block,
				),
			),
		unsafe_expression: ($) => seq("unsafe", $.block),
		channel_new: ($) =>
			seq("chan", "[", $._type, "]", "(", optional($._expression), ")"),
		asm_expression: ($) =>
			seq(
				"asm",
				"(",
				$.string_literal,
				repeat(seq(",", $.asm_operand)),
				")",
			),
		asm_operand: ($) =>
			seq(
				choice("in", "out", "lateout", "inout"),
				"(",
				$.string_literal,
				")",
				optional($._expression),
			),

		// --- patterns -------------------------------------------------------------------------

		_pattern: ($) => choice($.or_pattern, $._sub_pattern),
		or_pattern: ($) =>
			prec.left(seq($._sub_pattern, repeat1(seq("|", $._sub_pattern)))),
		_sub_pattern: ($) =>
			seq($._pattern_core, optional(seq("as", $.identifier))),

		_pattern_core: ($) =>
			choice(
				$.variant_pattern,
				$.struct_pattern,
				$.tuple_pattern,
				$.list_pattern,
				$.range_pattern,
				$.literal_pattern,
				$.identifier_pattern,
				$.wildcard_pattern,
			),

		// `Either.Left(v)` and the bare `Full(v)`. GRAMMAR#variant-pat spells the qualified form
		// only, and the compiler's parser reads both — a variant is reachable unqualified inside
		// the module that declares it, which is how the corpus writes one.
		variant_pattern: ($) =>
			choice(
				seq(
					$.type_identifier,
					".",
					$.identifier,
					optional($.pattern_arguments),
				),
				seq($.type_identifier, $.pattern_arguments),
			),
		pattern_arguments: ($) =>
			seq("(", optional(sepBy1(",", $._pattern)), ")"),
		struct_pattern: ($) =>
			seq(
				$.type_identifier,
				"{",
				optional(
					choice(
						seq(
							sepBy1(",", $.field_pattern),
							optional(seq(",", "..")),
						),
						"..",
					),
				),
				"}",
			),
		field_pattern: ($) => seq($.identifier, optional(seq(":", $._pattern))),
		tuple_pattern: ($) => seq("(", sepBy1(",", $._pattern), ")"),
		list_pattern: ($) =>
			seq(
				"[",
				optional(
					sepBy1(
						",",
						choice($._pattern, seq("..", optional($.identifier))),
					),
				),
				"]",
			),
		range_pattern: ($) =>
			prec(
				1,
				seq(
					$._range_bound,
					choice(
						seq("..", optional($._range_bound)),
						seq("..=", $._range_bound),
					),
				),
			),
		_range_bound: ($) =>
			choice(seq(optional("-"), $._literal), $.identifier),
		literal_pattern: ($) => seq(optional("-"), $._literal),
		identifier_pattern: ($) => $.identifier,
		wildcard_pattern: (_) => "_",

		// --- literals ---------------------------------------------------------------------------

		_literal: ($) =>
			choice(
				$.boolean_literal,
				$.nil_literal,
				$.float_literal,
				$.integer_literal,
				$.string_literal,
				$.raw_string_literal,
				$.rune_literal,
				$.byte_literal,
				$.command_literal,
				$.this_literal,
			),

		boolean_literal: (_) => choice("true", "false"),
		nil_literal: (_) => "nil",
		this_literal: (_) => "this",

		integer_literal: (_) =>
			token(
				choice(
					/[0-9](_?[0-9])*/,
					/0x[0-9a-fA-F](_?[0-9a-fA-F])*/,
					/0o[0-7](_?[0-7])*/,
					/0b[01](_?[01])*/,
				),
			),

		float_literal: (_) =>
			token(
				choice(
					/[0-9](_?[0-9])*\.[0-9](_?[0-9])*([eE][-+]?[0-9](_?[0-9])*)?/,
					/[0-9](_?[0-9])*[eE][-+]?[0-9](_?[0-9])*/,
				),
			),

		// A LITERAL IS ONE TOKEN, or its contents are lexed as code. The first version of this
		// spelled a string as a sequence of characters, so `"fn"` in `token.zg` lexed the `fn`
		// inside it as the keyword — which is why `lexer.zg`, `parser.zg` and `emit.zg` failed
		// to parse in their entirety, and why `b'#'` in `check.zg` opened a comment.
		//
		// The chunk carries a token PRECEDENCE as well, because immediacy alone does not settle
		// it: a comment matches to the end of the line and is therefore LONGER than the chunk
		// that stops at the closing quote, so `"#["` in `lexer.zg` and `f"{recv}#{name}"` in
		// `emit.zg` opened a comment that swallowed the rest of the line. Longest-match is the
		// lexer's tiebreak only when no precedence says otherwise.
		//
		// The chunks are `token.immediate`, which binds to the character before it with no
		// extras skipped, so nothing inside a literal can be anything but part of it. It is
		// spelled that way rather than as one big token so that an escape stays a NODE and can
		// be highlighted as one.
		string_literal: ($) =>
			choice(
				seq(
					'"""',
					repeat(choice($.escape_sequence, $._multiline_chunk)),
					token.immediate('"""'),
				),
				seq(
					'"',
					repeat(choice($.escape_sequence, $._string_chunk)),
					token.immediate('"'),
				),
			),
		raw_string_literal: (_) => token(seq('r"', /[^"\n]*/, '"')),
		command_literal: (_) => token(seq("`", /[^`\n]*/, "`")),
		rune_literal: (_) =>
			token(
				seq(
					"'",
					choice(/\\[ntr0\\'"]/, /\\u\{[0-9a-fA-F]+\}/, /[^'\\\n]/),
					"'",
				),
			),
		byte_literal: (_) =>
			token(
				seq(
					"b'",
					choice(/\\[ntr0\\']/, /\\x[0-9a-fA-F]{2}/, /[^'\\\n]/),
					"'",
				),
			),
		escape_sequence: (_) =>
			token.immediate(
				seq(
					"\\",
					choice(
						/[ntr0\\'"]/,
						/u\{[0-9a-fA-F]+\}/,
						/x[0-9a-fA-F]{2}/,
					),
				),
			),

		// GRAMMAR#fstr-lit — the holes are parsed, which is the whole point of having a tree:
		// a regular expression can find the braces and cannot find what is between them.
		format_string: ($) =>
			seq(
				'f"',
				repeat(
					choice(
						$.interpolation,
						$.escape_sequence,
						$._fstring_chunk,
					),
				),
				token.immediate('"'),
			),
		format_command: ($) =>
			seq(
				"f`",
				repeat(choice($.interpolation, $._fcommand_chunk)),
				token.immediate("`"),
			),
		interpolation: ($) =>
			seq(
				"{",
				$._expression,
				optional("="),
				optional($.format_conversion),
				optional($.format_spec),
				"}",
			),
		format_conversion: (_) => token(seq("!", /[rsa]/)),
		format_spec: (_) => token(seq(":", /[^{}\n]*/)),

		identifier: (_) => /[a-zA-Z_][a-zA-Z0-9_]*/,
	},
});

function sepBy1(sep, rule) {
	return seq(rule, repeat(seq(sep, rule)));
}

// A braced list of items separated by statement separators — a struct's fields, an enum's
// variants, a spec's and an impl's members. Written out four times it was four places to edit
// when the separator handling changed, and the separator handling is what the comment on
// `_statement_list` records having been wrong twice.
function blockOf($, item) {
	return seq(
		"{",
		repeat($._statement_separator),
		repeat(seq(item, repeat($._statement_separator))),
		"}",
	);
}
