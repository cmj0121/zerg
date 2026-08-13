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
    let [l:net, l:dip] = s:BraceScan(l:k)
    call add(b:zerg_fold_levels, max([0, l:start + l:dip]))
    call add(b:zerg_fold_starts, l:start + l:net)
  endwhile
  return b:zerg_fold_levels[a:lnum - 1]
endfunction

" s:BraceScan reads one line and answers with two numbers, both relative to the depth the
" line starts at: the NET brace change, which carries the count to the next line, and the
" lowest depth the line DIPS to, which is that line's own fold level. They differ only on a
" line that closes something — `}` nets -1 and dips to -1, `} else {` nets 0 and dips to -1.
"
" Only braces that are CODE count: one inside a string, a comment, a decorator, or an
" f-string hole is text, and counting it would put every line after it at the wrong depth.
"
" Asking the syntax engine is what makes that distinction, and it is also the expensive
" part — so it is only asked on a line that could HOLD a fake brace, which needs a quote
" or a '#' somewhere on it. In this repo that is about one brace-bearing line in nine, and
" the check costs one regexp against a line already in hand.
function! s:BraceScan(lnum) abort
  let l:line = getline(a:lnum)
  let l:quoted = l:line =~# '["''#]'
  let l:depth = 0
  let l:dip = 0
  let l:at = 0
  while 1
    let l:idx = match(l:line, '[{}]', l:at)
    if l:idx < 0
      break
    endif
    " synID wants a 1-based column; an unclaimed brace has no syntax item, and with syntax
    " off it has none either — which folds by braces alone, the honest degradation.
    if !l:quoted || synIDattr(synID(a:lnum, l:idx + 1, 1), 'name') ==# ''
      let l:depth += l:line[l:idx] ==# '{' ? 1 : -1
      let l:dip = min([l:dip, l:depth])
    endif
    let l:at = l:idx + 1
  endwhile
  return [l:depth, l:dip]
endfunction

let b:undo_ftplugin = 'setlocal commentstring< comments< expandtab< tabstop< shiftwidth< softtabstop< foldmethod< foldexpr< foldlevel<'
