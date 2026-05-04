package syntax

// v0.7 Unit 4 — typeck for the `select` statement. Each arm is one of four
// shapes:
//
//   - SelectRecvBind:    `v := <- ch` — Chan must be chan[T]; binds v to T
//                        (unwrapped — typeck handles the Option strip) for
//                        the body only.
//   - SelectRecvDiscard: `<- ch`      — Chan must be chan[T]; no binding.
//   - SelectSend:        `ch <- v`    — Chan chan[T]; Value unifies with T
//                        (with the v0.6 T → T? lift on Option-typed T).
//   - SelectDefault:     `_`          — no channel op; body only.
//
// Per-arm body walk uses a fresh scope rooted at c.scope so the recv-bind
// name vanishes at arm boundary (mirrors checkMatch's per-arm scope). The
// at-most-one-default rule is enforced by counting SelectDefault arms — the
// parser admits any number; rejection lands here.

func (c *checker) checkSelectStmt(s *SelectStmt) error {
	defaultCount := 0
	for i := range s.Arms {
		arm := &s.Arms[i]
		if arm.Op == SelectDefault {
			defaultCount++
			if defaultCount > 1 {
				return typeErr(arm.Pos, "select can have at most one default arm")
			}
		}
		if err := c.checkSelectArm(arm); err != nil {
			return err
		}
	}
	return nil
}

func (c *checker) checkSelectArm(arm *SelectArm) error {
	armScope := newScope(c.scope)
	switch arm.Op {
	case SelectRecvBind:
		chT, err := c.checkExpr(arm.Chan)
		if err != nil {
			return err
		}
		if !isChanType(chT) {
			return typeErr(arm.Chan.ExprPos(),
				"select recv requires chan, got %s", chT)
		}
		if isReservedV07BuiltinName(arm.BindName) ||
			isReservedV07ConcurName(arm.BindName) ||
			arm.BindName == "close" {
			return typeErr(arm.BindNamePos,
				"name %q is reserved (built-in)", arm.BindName)
		}
		if !armScope.declare(arm.BindName, binding{kind: bindLet, typ: chT.Element}) {
			return typeErr(arm.BindNamePos,
				"name %q already declared in this scope", arm.BindName)
		}
	case SelectRecvDiscard:
		chT, err := c.checkExpr(arm.Chan)
		if err != nil {
			return err
		}
		if !isChanType(chT) {
			return typeErr(arm.Chan.ExprPos(),
				"select recv requires chan, got %s", chT)
		}
	case SelectSend:
		chT, err := c.checkExpr(arm.Chan)
		if err != nil {
			return err
		}
		if !isChanType(chT) {
			return typeErr(arm.Chan.ExprPos(),
				"select send requires chan, got %s", chT)
		}
		elem := chT.Element
		newVal, vt, err := c.checkExprLift(arm.Value, elem)
		if err != nil {
			return err
		}
		if newVal != arm.Value {
			arm.Value = newVal
		}
		if !c.assignableTo(vt, elem) {
			return typeErr(arm.Value.ExprPos(),
				"cannot send value of type %s on %s", vt, chT)
		}
	case SelectDefault:
		// no channel op
	default:
		return typeErr(arm.Pos, "internal: unknown select arm kind %d", arm.Op)
	}

	saved := c.scope
	c.scope = armScope
	err := c.checkBlock(arm.Body)
	c.scope = saved
	return err
}
