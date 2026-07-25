/*
 * ctx_arm64.S - the AArch64 context switch behind the zrt_ctx shim (Fork-A).
 *
 * Two symbols implement the whole coroutine machine's arch dependency:
 *   zrt_ctx_swap(from, to)  save callee-saved state into *from, resume *to
 *   zrt_ctx_init(c, base, size, entry, arg)  arm a fresh context to start entry(arg)
 *
 * The saved state is exactly the AArch64 callee-saved set - x19-x28, fp(x29),
 * lr(x30), sp, and d8-d15 - written into zrt_ctx.slots at these byte offsets:
 *
 *   0  x19   8  x20   16 x21   24 x22   32 x23   40 x24   48 x25   56 x26
 *   64 x27   72 x28   80 x29   88 x30   96 sp
 *   104 d8  112 d9  120 d10  128 d11  136 d12  144 d13  152 d14  160 d15
 *
 * The signal mask is deliberately not touched (that is what makes this cheap and
 * why ucontext is only a portable floor). A newly armed context resumes into a
 * trampoline that moves the saved arg into x0 and branches to entry; the entry
 * (the coroutine trampoline) never returns.
 */

#if defined(__APPLE__)
#define SYM(name) _##name
#else
#define SYM(name) name
#endif

	.text

	.globl SYM(zrt_ctx_swap)
	.p2align 2
SYM(zrt_ctx_swap):              /* x0 = from, x1 = to */
	/* save the current callee-saved state into *from */
	stp	x19, x20, [x0, #0]
	stp	x21, x22, [x0, #16]
	stp	x23, x24, [x0, #32]
	stp	x25, x26, [x0, #48]
	stp	x27, x28, [x0, #64]
	stp	x29, x30, [x0, #80]
	mov	x2, sp
	str	x2, [x0, #96]
	stp	d8, d9,   [x0, #104]
	stp	d10, d11, [x0, #120]
	stp	d12, d13, [x0, #136]
	stp	d14, d15, [x0, #152]
	/* restore the saved state from *to */
	ldp	x19, x20, [x1, #0]
	ldp	x21, x22, [x1, #16]
	ldp	x23, x24, [x1, #32]
	ldp	x25, x26, [x1, #48]
	ldp	x27, x28, [x1, #64]
	ldp	x29, x30, [x1, #80]
	ldr	x2, [x1, #96]
	mov	sp, x2
	ldp	d8, d9,   [x1, #104]
	ldp	d10, d11, [x1, #120]
	ldp	d12, d13, [x1, #136]
	ldp	d14, d15, [x1, #152]
	ret

	.globl SYM(zrt_ctx_init)
	.p2align 2
SYM(zrt_ctx_init):             /* x0=c, x1=base, x2=size, x3=entry, x4=arg */
	add	x5, x1, x2          /* high end of the stack */
	bic	x5, x5, #15         /* 16-byte align the initial sp */
	/* clear the saved-register area so a fresh context starts from a clean frame */
	stp	xzr, xzr, [x0, #0]
	stp	xzr, xzr, [x0, #16]
	stp	xzr, xzr, [x0, #32]
	stp	xzr, xzr, [x0, #48]
	stp	xzr, xzr, [x0, #64]
	stp	xzr, xzr, [x0, #80]
	stp	xzr, xzr, [x0, #104]
	stp	xzr, xzr, [x0, #120]
	stp	xzr, xzr, [x0, #136]
	stp	xzr, xzr, [x0, #152]
	/* stash entry+arg in callee-saved slots the trampoline reads, sp, and the
	 * resume address (lr) pointing at the trampoline */
	str	x4, [x0, #0]        /* x19 = arg */
	str	x3, [x0, #8]        /* x20 = entry */
#if defined(__APPLE__)
	adrp	x6, SYM(zrt_ctx_trampoline)@PAGE
	add	x6, x6, SYM(zrt_ctx_trampoline)@PAGEOFF
#else
	adrp	x6, SYM(zrt_ctx_trampoline)
	add	x6, x6, :lo12:SYM(zrt_ctx_trampoline)
#endif
	str	x6, [x0, #88]       /* x30 (lr) = trampoline */
	str	x5, [x0, #96]       /* sp = high end */
	ret

	/* the first-resume trampoline: entry(arg), then trap (entry must not return) */
	.p2align 2
SYM(zrt_ctx_trampoline):
	mov	x0, x19
	blr	x20
	brk	#0

#if defined(__linux__)
	.section .note.GNU-stack,"",%progbits
#endif
