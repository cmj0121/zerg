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

" Fold a block's BODY and leave the line that opens it visible, so a folded function still
" shows its own signature. Syntax folding cannot do this: a syntax fold always begins on
" the line its region opens on, so folding a function hides the `fn name(...)` line that
" says which function it is — the one thing a reader of a closed fold needs.
"
" The rule is one line long: a line's fold level is the brace depth at its START. The
" `fn f() {` line is still at depth 0 and stays out; the body is at depth 1 and folds; the
" closing `}` is also at depth 1, so it folds WITH the body rather than being left behind
" as an orphan. Nesting falls out of the same rule with nothing added.
setlocal foldmethod=expr
setlocal foldexpr=ZergFoldLevel(v:lnum)
setlocal foldlevel=99

" ZergFoldLevel returns the brace depth at the start of line lnum.
"
" Depths are memoized per buffer and invalidated by b:changedtick, because 'foldexpr' is
" called once per line and computing each one from the top would be quadratic. The memo is
" extended forward only, which is the order Vim asks in.
function! ZergFoldLevel(lnum) abort
  if !exists('b:zerg_fold_tick') || b:zerg_fold_tick != b:changedtick
    let b:zerg_fold_tick = b:changedtick
    let b:zerg_fold_depths = [0]
  endif
  while len(b:zerg_fold_depths) < a:lnum
    let l:k = len(b:zerg_fold_depths)
    call add(b:zerg_fold_depths, b:zerg_fold_depths[l:k - 1] + s:BraceDelta(l:k))
  endwhile
  return b:zerg_fold_depths[a:lnum - 1]
endfunction

" s:BraceDelta is the net brace change a line makes. Only braces that are CODE count: one
" inside a string, a comment, a decorator, or an f-string hole is text, and counting it
" would put every line after it at the wrong depth.
"
" Asking the syntax engine is what makes that distinction, and it is also the expensive
" part — so it is only asked on a line that could HOLD a fake brace, which needs a quote
" or a '#' somewhere on it. In this repo that is about one brace-bearing line in nine, and
" the check costs one regexp against a line already in hand.
function! s:BraceDelta(lnum) abort
  let l:line = getline(a:lnum)
  let l:quoted = l:line =~# '["''#]'
  let l:depth = 0
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
    endif
    let l:at = l:idx + 1
  endwhile
  return l:depth
endfunction

let b:undo_ftplugin = 'setlocal commentstring< comments< expandtab< shiftwidth< softtabstop< foldmethod< foldexpr< foldlevel<'
