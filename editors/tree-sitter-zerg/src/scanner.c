// The external scanner for tree-sitter-zerg: one token, the statement separator.
//
// GRAMMAR#stmt-sep makes a NEWLINE a statement separator, so a newline cannot simply be
// whitespace the lexer throws away. It also cannot simply be a token, because the same
// newline is insignificant inside a group: a wrapped argument list, an `import ( … )` and a
// list literal all span lines, and `F403` writes them that way.
//
// The rule that resolves both is tree-sitter's own: the scanner is asked FIRST at every
// position, and only for the tokens the parser can accept there. So a newline becomes a
// separator exactly where a statement could end, and falls through to `extras` as ordinary
// whitespace everywhere else. That is automatic semicolon insertion expressed as a lookup
// rather than as a list of exceptions — the design tree-sitter-go uses, for the same reason.
//
// It carries no state, so `serialize` and `deserialize` are empty and a resumed parse needs
// nothing put back.

#include "tree_sitter/parser.h"

enum TokenType {
	NEWLINE,
	STRING_CHUNK,
	MULTILINE_CHUNK,
	FSTRING_CHUNK,
	FCOMMAND_CHUNK,
};

// A run of ordinary characters inside a literal, up to whatever ends it.
//
// THE CONTENTS OF A LITERAL ARE NOT CODE, and the grammar cannot say so on its own: `comment`
// is an extra, so it is a candidate token at every position, and `#` inside a string starts
// one that runs to the end of the line. It matched further than a chunk that stops at the
// closing quote, and the lexer's tiebreak is length — so `f"{recv}#{name}"` in `emit.zg` lexed
// its `#` as a comment and swallowed the rest of the line, closing quote and all. A token
// precedence did not settle it and an `immediate` token did not either.
//
// The scanner does, because it is asked BEFORE the lexer at every position: while a chunk is
// what the parser wants next, nothing else is offered. It is also the shape every grammar with
// string interpolation ends up at, for this exact reason.
static bool scan_chunk(TSLexer *lexer, enum TokenType type) {
	bool any = false;
	for (;;) {
		int32_t c = lexer->lookahead;
		if (c == 0 && lexer->eof(lexer)) {
			break;
		}
		if (c == '\\') {
			break; // an escape is its own node, so the chunk ends here
		}
		if (type == FCOMMAND_CHUNK) {
			if (c == '`' || c == '{' || c == '}' || c == '\n') {
				break;
			}
		} else if (type == MULTILINE_CHUNK) {
			// a triple-quoted string ends at `"""` and holds a lone `"` happily, so the chunk
			// only stops where three of them start
			if (c == '"') {
				lexer->mark_end(lexer);
				lexer->advance(lexer, false);
				if (lexer->lookahead != '"') {
					any = true;
					continue;
				}
				lexer->advance(lexer, false);
				if (lexer->lookahead != '"') {
					any = true;
					continue;
				}
				break;
			}
		} else {
			if (c == '"' || c == '\n') {
				break;
			}
			if (type == FSTRING_CHUNK && (c == '{' || c == '}')) {
				// `{{` and `}}` are how a format string writes a literal brace
				// (GRAMMAR#fstr-lit), so a doubled one is content and a single one opens or
				// closes a hole. Without this the emitter's own `f"…{{ .tag = 1 }}…"` — which
				// writes C, and C is full of braces — could not be read at all.
				lexer->advance(lexer, false);
				if (lexer->lookahead != c) {
					break;
				}
				lexer->advance(lexer, false);
				any = true;
				lexer->mark_end(lexer);
				continue;
			}
		}
		lexer->advance(lexer, false);
		any = true;
		lexer->mark_end(lexer);
	}
	if (!any) {
		return false;
	}
	lexer->result_symbol = type;
	return true;
}

void *tree_sitter_zerg_external_scanner_create(void) { return NULL; }
void tree_sitter_zerg_external_scanner_destroy(void *payload) { (void)payload; }

unsigned tree_sitter_zerg_external_scanner_serialize(void *payload, char *buffer) {
	(void)payload;
	(void)buffer;
	return 0;
}

void tree_sitter_zerg_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {
	(void)payload;
	(void)buffer;
	(void)length;
}

bool tree_sitter_zerg_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid_symbols) {
	(void)payload;

	// A literal's contents come first: while one is being read, a newline is not a separator
	// and a `#` is not a comment.
	if (valid_symbols[STRING_CHUNK]) {
		return scan_chunk(lexer, STRING_CHUNK);
	}
	if (valid_symbols[MULTILINE_CHUNK]) {
		return scan_chunk(lexer, MULTILINE_CHUNK);
	}
	if (valid_symbols[FSTRING_CHUNK]) {
		return scan_chunk(lexer, FSTRING_CHUNK);
	}
	if (valid_symbols[FCOMMAND_CHUNK]) {
		return scan_chunk(lexer, FCOMMAND_CHUNK);
	}

	if (!valid_symbols[NEWLINE]) {
		return false;
	}

	// Spaces and tabs before the newline are not a separator, so they are skipped. A COMMENT
	// is not skipped: it is a node of the tree, and a scanner that swallowed it would leave
	// every trailing comment in this repository unhighlighted. Returning false hands it to
	// `extras`, after which this runs again — at the newline on the far side of it.
	while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r') {
		lexer->advance(lexer, true);
	}

	// End of input is NOT a separator here, and the first version of this said it was: it
	// returned the token without advancing the lexer, so tree-sitter asked again at the same
	// position, got the same zero-width token, and a seven-line file never finished parsing.
	// The last statement of a file needs no separator anyway — the statement list's trailing
	// one is optional — so there is nothing to emit.
	if (lexer->lookahead != '\n') {
		return false;
	}

	// The whole run collapses into one token. A blank line between two declarations is not a
	// second separator to the reader and should not be one to the grammar either.
	while (lexer->lookahead == '\n' || lexer->lookahead == '\r' || lexer->lookahead == ' ' ||
	       lexer->lookahead == '\t') {
		lexer->advance(lexer, true);
	}

	lexer->result_symbol = NEWLINE;
	return true;
}
