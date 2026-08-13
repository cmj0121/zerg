" Zerg filetype plugin
" Buffer-local editing conventions for Zerg source.
if exists('b:did_ftplugin')
  finish
endif
let b:did_ftplugin = 1

" Zerg's line comment is '#' (see GRAMMAR, group 1).
setlocal commentstring=#\ %s
setlocal comments=:#

" A TAB per nesting level, displayed four columns wide.
"
" This said `expandtab` and four spaces, which is not what this language is written in:
" `zerg fmt`'s F101 indents with one tab per level, every source in the tree is indented
" that way, and `make fmt-self` holds them there. So a person typing in nvim produced
" spaces that the formatter turned into tabs on the next save — a whole-file diff on every
" write, caused by the editor and the formatter disagreeing about the same rule.
"
" The width is a display choice and stays four; the character is the formatter's and is not
" a choice at all.
setlocal noexpandtab
setlocal tabstop=4
setlocal shiftwidth=4
setlocal softtabstop=0

" --- folding ----------------------------------------------------------------------------
"
" Fold a block's BODY and leave the lines that BRACKET it visible, so a folded function
" still reads as `fn name(…) {` … `}`. Syntax folding cannot do this: a syntax fold always
" begins on the line its region opens on, so folding a function hides the `fn name(...)`
" line that says which function it is — the one thing a reader of a closed fold needs.
"
" The rule is one line long: a line's fold level is the LOWEST brace depth it reaches. The
" `fn f() {` line starts at depth 0 and never dips, so it is level 0 and stays out; the body
" is at depth 1 and folds; the closing `}` starts at depth 1 but dips to 0, so it is level 0
" and stays out too. Nesting falls out of the same rule with nothing added, and so does the
" middle of a chain — `} else {` dips to the outer level, so a closed `if` branch does not
" swallow the line that opens the `else`.
"
" The depth at a line's start is not enough for that: it puts the closing `}` at the body's
" own level, which folds the brace away with the block it ends. The lowest depth is the same
" number for every line that does not carry a closer, so nothing else moves.
setlocal foldmethod=expr
setlocal foldexpr=ZergFoldLevel(v:lnum)
setlocal foldlevel=99

" ZergFoldLevel returns the lowest brace depth line lnum reaches.
"
" Two memos per buffer, both invalidated by b:changedtick, because 'foldexpr' is called once
" per line and computing each one from the top would be quadratic. `b:zerg_fold_starts` is
" the depth at each line's start and is what carries the count forward; `b:zerg_fold_levels`
" is the answer. They are extended together, one scan of the line filling both, and forward
" only — which is the order Vim asks in.
"
" The level is clamped at 0. Source with more closers than openers — which is most source,
" halfway through being typed — would otherwise hand Vim a negative fold level.
function! ZergFoldLevel(lnum) abort
  if !exists('b:zerg_fold_tick') || b:zerg_fold_tick != b:changedtick
    let b:zerg_fold_tick = b:changedtick
    let b:zerg_fold_starts = [0]
    let b:zerg_fold_levels = []
  endif
  while len(b:zerg_fold_levels) < a:lnum
    let l:k = len(b:zerg_fold_levels) + 1
    let l:start = b:zerg_fold_starts[l:k - 1]
    let [l:net, l:dip] = s:DelimScan(l:k, s:fold_pat, s:fold_open)
    call add(b:zerg_fold_levels, max([0, l:start + l:dip]))
    call add(b:zerg_fold_starts, l:start + l:net)
  endwhile
  return b:zerg_fold_levels[a:lnum - 1]
endfunction

" --- indent -----------------------------------------------------------------------------
"
" Typing produced NO indentation at all: 'indentexpr' was empty and so were 'autoindent',
" 'smartindent' and 'cindent', so `fn f() {` <CR> put the cursor in column 1 and every level
" was a tab the person pressed. The formatter fixed it on the next write, which means the
" only way the file ever looked right was to run a tool over it.
"
" The rule is the FOLD's rule with a wider set of delimiters: a level is the lowest depth the
" line reaches, times 'shiftwidth'. The two share one scanner rather than counting for
" themselves, because a second counter is a second copy of one fact and the two disagree the
" first time either learns something.
"
" What it cannot share is the MEMO. 'foldexpr' is asked for every line once; 'indentexpr' is
" asked for one line on every keystroke, and the memo is invalidated by b:changedtick — so
" re-deriving from line 1 each time would be the whole file per character. This reads the
" PREVIOUS non-blank line instead, which is O(1) and the ordinary way an indenter works:
"
"   the level a line opens for the next one  =  its own level + (net - dip)
"
" `net - dip` is what the line leaves open after whatever it closed. It is `+1` for
" `fn f() {`, `0` for a plain statement, and `+1` for `} else {` — which nets 0 and would
" otherwise indent the else-branch as if it were a sibling of the if-branch's body.
"
" Then the line being typed dedents ITSELF by its own dip, which is what makes `}` jump back
" a level as the character is typed rather than after a reformat.
"
" A CONTINUED expression has no delimiter to count. `s + "…" + \n "…" + \n "…"` is one
" statement indented one level in, and the only thing that says so is that the line before it
" ended on an operator. So a continuation is worth exactly one level, taken when the chain
" OPENS and given back when it ends — read off two lines, not by walking back to the head:
"
"   prev ends open, the one before it does not   the chain starts here      +1
"   both end open                                already indented, stay      0
"   prev does not, the one before it does        the chain just ended       -1
"
" Without it, `=` over this repo's own `zergc.zg` flattened a wrapped help string against the
" statement that opened it, which is the failure that matters: re-indenting is supposed to be
" a fixpoint on source the formatter already wrote.
function! ZergIndent(lnum) abort
  let l:prev = prevnonblank(a:lnum - 1)
  if l:prev == 0
    return 0
  endif
  let [l:pnet, l:pdip] = s:DelimScan(l:prev, s:indent_pat, s:indent_open)
  let l:sw = shiftwidth()
  " indent() answers in COLUMNS and this file is indented with tabs displayed 'shiftwidth'
  " wide, so the division is the level. A line indented to something that is not a multiple
  " of a level floors to the level below it, which is the one a reader would call it.
  let l:level = indent(l:prev) / l:sw + (l:pnet - l:pdip)

  let l:before = prevnonblank(l:prev - 1)
  let l:level += s:EndsOpen(l:prev) - (l:before > 0 ? s:EndsOpen(l:before) : 0)

  let [l:_, l:dip] = s:DelimScan(a:lnum, s:indent_pat, s:indent_open)
  return max([0, l:level + l:dip]) * l:sw
endfunction

" s:EndsOpen is whether a line's last CODE character is a binary operator, which is the only
" evidence that the statement continues on the next line.
"
" A COMMENT LINE continues nothing, and that is checked first and by TEXT. Zerg's comment
" runs to the end of the line and there is no block form, so a line whose first non-blank
" character is '#' is a comment all the way across, whatever is written in it — including a
" doctest, which holds Zerg source on purpose. Asking the syntax engine instead did not
" answer this: the prompt in `# >>>` is `zergDocPrompt`, which resolves to `Special` and not
" to anything spelled "comment", so a bare prompt line read as code ending in `>` and put the
" example under it one level in. This repo's own `cli.zg` is where that showed.
"
" A trailing comment on a code line is still scanned past rather than cut at the first '#',
" because a '#' inside a string is not one — the same distinction the delimiter scan makes,
" asked of the syntax engine the same way.
"
" `<-` and `->` end no expression. They are the channel direction and the return arrow, and
" `tx: chan[int]<-` is a struct FIELD whose line ends in `-` — which put the next field one
" level in, turning two fields into a field and its continuation.
function! s:EndsOpen(lnum) abort
  let l:line = getline(a:lnum)
  if l:line =~# '^\s*#'
    return 0
  endif

  " A line with no '#' on it has no trailing comment, which is nearly every line — so the
  " answer is one regexp against text already in hand and the syntax engine is not asked at
  " all. The backward walk below costs a synID call PER CHARACTER, and it was paying that for
  " the whole of every trailing comment in the tree.
  if stridx(l:line, '#') < 0
    return s:OpenTail(substitute(l:line, '\s\+$', '', ''))
  endif

  let l:i = strlen(l:line) - 1
  while l:i >= 0
    if l:line[l:i] =~# '\s'
      let l:i -= 1
    elseif synIDattr(synID(a:lnum, l:i + 1, 1), 'name') =~? 'comment'
      let l:i -= 1
    else
      break
    endif
  endwhile
  return s:OpenTail(strpart(l:line, 0, l:i + 1))
endfunction

" s:OpenTail is the test itself: does this text end on a binary operator, and is that operator
" not the tail of `<-` or `->`, which end no expression.
function! s:OpenTail(text) abort
  return a:text =~# '[-+*/%<>=&|^]$' && a:text !~# '\%(<-\|->\)$'
endfunction

setlocal indentexpr=ZergIndent(v:lnum)
" The keys that re-ask. `0{` and `0}` are what make a brace typed at the start of a line move
" to its own level; `o`/`O` cover opening a line, and `<C-F>` and `=` ask directly. The
" default list also carries `:` and `0#`, and both are wrong here: `:` is a type annotation
" in `x: int` and `#` opens a comment, so either would re-indent the line under the cursor in
" the middle of writing something that has nothing to do with a block.
setlocal indentkeys=0{,0},0),0],!^F,o,O

" --- the shared scanner ------------------------------------------------------------------
"
" s:DelimScan reads one line and answers with two numbers, both relative to the depth the
" line starts at: the NET change, which carries the count to the next line, and the lowest
" depth the line DIPS to, which is the line's own level. They differ only on a line that
" closes something — `}` nets -1 and dips to -1, `} else {` nets 0 and dips to -1.
"
" The delimiters are an ARGUMENT because the two callers ask different questions. Folding is
" about BLOCKS, so it passes braces alone: a wrapped argument list is not a fold and `(` must
" not open one. Indenting is about GROUPS, and the formatter indents inside all three — F403
" puts one argument per line inside `(`, F404 does the same for an `import ( … )`, and a list
" literal wraps inside `[` — so it passes all of them. Braces alone was wrong here and the
" re-indent of this repo's own sources is what said so: an `import` group and a wrapped call
" both came back flat against the left margin.
"
" Only delimiters that are CODE count: one inside a string, a comment, a decorator, or an
" f-string hole is text, and counting it would put every line after it at the wrong depth.
"
" Asking the syntax engine is what makes that distinction, and it is also the expensive
" part — so it is only asked on a line that could HOLD a fake delimiter, which needs a quote
" or a '#' somewhere on it. The check costs one regexp against a line already in hand.
let s:fold_pat = '[{}]'
let s:fold_open = '{'
let s:indent_pat = '[][(){}]'
let s:indent_open = '([{'

function! s:DelimScan(lnum, pat, openers) abort
  let l:line = getline(a:lnum)
  " Where the first quote or '#' is, rather than whether there is one: a delimiter to the LEFT
  " of it cannot be inside a string, a comment, a decorator or an f-string hole, so it needs no
  " syntax query. On an ordinary line with a trailing comment that is every delimiter on it.
  let l:quote = match(l:line, '["''#]')
  let l:depth = 0
  let l:dip = 0
  let l:at = 0
  while 1
    let l:idx = match(l:line, a:pat, l:at)
    if l:idx < 0
      break
    endif
    " synID wants a 1-based column; an unclaimed delimiter has no syntax item, and with
    " syntax off it has none either — which counts by text alone, the honest degradation.
    if l:quote < 0 || l:idx < l:quote || synIDattr(synID(a:lnum, l:idx + 1, 1), 'name') ==# ''
      let l:depth += stridx(a:openers, l:line[l:idx]) >= 0 ? 1 : -1
      let l:dip = min([l:dip, l:depth])
    endif
    let l:at = l:idx + 1
  endwhile
  return [l:depth, l:dip]
endfunction

" Every option this file set, given back — including the ones added after it was written,
" which is the half that goes stale. `:setfiletype other` on a Zerg buffer left it folding by
" braces and building with `zerg` because the list had not grown with the file.
let b:undo_ftplugin = 'setlocal commentstring< comments< expandtab< tabstop< shiftwidth< softtabstop<'
      \ . ' foldmethod< foldexpr< foldlevel< indentexpr< indentkeys<'
