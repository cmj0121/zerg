package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// closeChan checks `close(ch)` — the one built-in a channel has (docs/code/coroutine.md,
// GRAMMAR group 9). It marks the CHANNEL no-longer-sendable: a property of the channel and
// not of a holder, so it moves no count, the name stays usable and readable afterwards, and
// closing twice changes nothing.
//
// It does not replace auto-close, which stays the everyday shape — a producer closes by
// FINISHING. A crashing producer never reaches a `close` call, and its unwind is what
// carries the crash Err to the receiver; and producers fanning into one channel need no
// coordination because the last to return closes it. `close` is the early form.
//
// Two refusals, and both are bugs a type system can catch. A non-channel has no send end
// at all; a receive-only `<-chan[T]` must not be able to end a stream on the producers'
// behalf.
func (c *checker) closeChan(n *ast.Call) Type {
	if len(n.Args) != 1 {
		c.synthArgs(n)
		c.errorf(n.Span(), "close takes exactly one argument, the channel to close")
		return Nil
	}
	arg := n.Args[0].Value
	at := c.synth(arg)
	ch, ok := at.(*types.Chan)
	if !ok {
		if !bad(at) {
			c.errorf(arg.Span(), "close requires a channel, found %s", at)
		}
		return Nil
	}
	if ch.Dir == types.ChanRecv {
		// spelled out rather than printed: types.Chan.String() drops the direction, so `%s`
		// alone would name the bidirectional `chan[T]` this is refusing.
		c.errorf(arg.Span(), "cannot close a receive-only channel <-chan[%s]: a consumer may not end a stream on the producers' behalf", ch.Elem)
	}
	return Nil
}
