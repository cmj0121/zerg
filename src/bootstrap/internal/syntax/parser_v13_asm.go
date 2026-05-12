package syntax

// parser_v13_asm.go holds the parse logic for the v0.13 inline-assembly
// statement (`asm { body }`). The body is delivered by the lexer as a single
// KindAsmBody token whose Value is the verbatim bytes between (but not
// including) the braces — see lexer.go's scanAsmBodyForLexIdent. All work
// here is on the assembled body string; brace counting and string-literal
// awareness already happened at lex time.
//
// The split rules are deliberately minimal:
//   - Each `${name}` (where `name` is one valid identifier per the v0.5
//     `isValidIdentifier` rule) becomes one AsmChunkInterp.
//   - Everything else becomes AsmChunkText, preserving byte order so the
//     formatter can reconstruct the body and so the cgen (U4) sees the
//     literal asm tokens between operand placeholders.
//
// Anything that resembles an interp but fails the shape check (`${` with no
// matching `}` before EOF; `${}` with no name; `${1abc}` with a digit
// leader) is reported with a precise diagnostic anchored on the offending
// `$`. U3 will tighten the surface further (unknown binding, wrong type);
// here we only enforce that the syntactic shape is well-formed.

// parseAsmStmt consumes the `asm` keyword and its companion KindAsmBody
// payload, splits the body at `${name}` interp markers, and returns the
// assembled *AsmBlock.
func (p *parser) parseAsmStmt() (Stmt, error) {
	kw := p.advance() // consume `asm`
	if p.peek().Kind != KindAsmBody {
		// The lexer's contract is that every KindAsm token is followed by
		// exactly one KindAsmBody. If we land here, either the gate fell
		// through (e.g. the source declared `# requires: v0.13` but the
		// body never closed and the lexer surfaced its error before queueing
		// the body) or the lexer's queue logic regressed. Either way, this
		// is a parser-level diagnostic — the user sees a focused error and
		// the harness can pin the regression.
		return nil, errorAt(kw.Pos, "expected '{' after 'asm'")
	}
	body := p.advance()
	chunks, err := splitAsmBody(body.Pos, body.Value)
	if err != nil {
		return nil, err
	}
	return &AsmBlock{
		Pos:          kw.Pos,
		OpenBracePos: body.Pos,
		Chunks:       chunks,
		BodyRaw:      body.Value,
	}, nil
}

// splitAsmBody walks raw verbatim and returns the AsmChunk list. `openPos`
// is the source position of the opening `{` — used as the origin for
// per-byte position tracking through the body. The position tracker walks
// through newlines and tabs the same way the lexer does so per-chunk
// diagnostics line up with the source.
func splitAsmBody(openPos Position, raw string) ([]AsmChunk, error) {
	// The opening `{` itself was at openPos; the body starts at the byte
	// after it. Increment the column past the `{` so chunks anchored at
	// the body's first byte report the right position.
	cur := Position{Line: openPos.Line, Column: openPos.Column + 1}
	var chunks []AsmChunk
	i := 0
	textStart := 0
	textStartPos := cur
	flushText := func(upto int) {
		if upto > textStart {
			chunks = append(chunks, AsmChunk{
				Pos:  textStartPos,
				Kind: AsmChunkText,
				Text: raw[textStart:upto],
			})
		}
	}
	advancePos := func(b byte) {
		if b == '\n' {
			cur.Line++
			cur.Column = 1
		} else {
			cur.Column++
		}
	}
	for i < len(raw) {
		if raw[i] == '$' && i+1 < len(raw) && raw[i+1] == '$' {
			// `$$` is reserved at v0.13. Treating it as two literal `$`
			// would silently admit a confusing form whose user intent is
			// ambiguous (literal-dollar vs. typo for `${`). Reject with a
			// focused diagnostic so the surface stays unambiguous. If a
			// later version introduces an escape for literal `$`, the
			// rejection lifts; for now, `${name}` is the only special
			// shape in an asm body.
			return nil, errorAt(cur, "'$$' is reserved in asm body; use '${name}' for interpolation")
		}
		if raw[i] == '$' && i+1 < len(raw) && raw[i+1] == '{' {
			dollarPos := cur
			flushText(i)
			// Walk past `${`.
			advancePos(raw[i])     // $
			advancePos(raw[i+1])   // {
			nameStart := i + 2
			nameStartPos := cur
			j := nameStart
			for j < len(raw) && raw[j] != '}' {
				j++
			}
			if j == len(raw) {
				return nil, errorAt(dollarPos, "unterminated '${' in asm body")
			}
			name := raw[nameStart:j]
			if name == "" {
				return nil, errorAt(dollarPos, "empty interpolation '${}' in asm body")
			}
			if !isAsmInterpIdent(name) {
				return nil, errorAt(nameStartPos, "invalid interpolation name %q in asm body", name)
			}
			// Advance position past the name + closing `}`.
			for k := nameStart; k < j; k++ {
				advancePos(raw[k])
			}
			advancePos(raw[j]) // `}`
			chunks = append(chunks, AsmChunk{
				Pos:     dollarPos,
				Kind:    AsmChunkInterp,
				Name:    name,
				NamePos: nameStartPos,
			})
			i = j + 1
			textStart = i
			textStartPos = cur
			continue
		}
		advancePos(raw[i])
		i++
	}
	flushText(len(raw))
	return chunks, nil
}

// isAsmInterpIdent mirrors the lexer's identifier rule (isIdentStart +
// isIdentPart) so the `${name}` surface accepts exactly the names the rest
// of the language accepts. Keeping the check local to the asm parser avoids
// dragging in the loader's isValidIdentifier (which also enforces the
// keyword-collision rule — undesirable here because every accepted name is
// a binding lookup at U3, not a fresh declaration).
func isAsmInterpIdent(s string) bool {
	if len(s) == 0 {
		return false
	}
	if !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentPart(s[i]) {
			return false
		}
	}
	return true
}
