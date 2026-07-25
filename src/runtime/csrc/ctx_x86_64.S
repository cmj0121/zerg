/*
 * ctx_x86_64.S - the x86-64 (System V) context switch behind the zrt_ctx shim.
 *
 * The arm64 file is the dev-arch main line; this is its x86-64 peer, selected by
 * the driver on amd64 hosts. Same contract: zrt_ctx_swap saves the callee-saved
 * set into *from and resumes *to; zrt_ctx_init arms a fresh context to start
 * entry(arg). Saved into zrt_ctx.slots (AT&T syntax, byte offsets):
 *
 *   0 rbx   8 rbp   16 r12   24 r13   32 r14   40 r15   48 rsp   56 rip
 *
 * A suspended context keeps its resume address at the top of its own stack; swap
 * saves/installs it around `ret`. A freshly armed context resumes into a
 * trampoline that moves the saved arg into %rdi and jumps to entry.
 */

#if defined(__APPLE__)
#define SYM(name) _##name
#else
#define SYM(name) name
#endif

	.text

	.globl SYM(zrt_ctx_swap)
	.p2align 4
SYM(zrt_ctx_swap):                 /* %rdi = from, %rsi = to */
	movq	%rbx, 0(%rdi)
	movq	%rbp, 8(%rdi)
	movq	%r12, 16(%rdi)
	movq	%r13, 24(%rdi)
	movq	%r14, 32(%rdi)
	movq	%r15, 40(%rdi)
	movq	%rsp, 48(%rdi)
	movq	(%rsp), %rax           /* caller's return address = resume rip */
	movq	%rax, 56(%rdi)
	movq	0(%rsi), %rbx
	movq	8(%rsi), %rbp
	movq	16(%rsi), %r12
	movq	24(%rsi), %r13
	movq	32(%rsi), %r14
	movq	40(%rsi), %r15
	movq	48(%rsi), %rsp
	movq	56(%rsi), %rax
	movq	%rax, (%rsp)           /* install resume rip as the return address */
	ret

	.globl SYM(zrt_ctx_init)
	.p2align 4
SYM(zrt_ctx_init):                 /* rdi=c, rsi=base, rdx=size, rcx=entry, r8=arg */
	leaq	(%rsi,%rdx), %rax      /* high end of the stack */
	andq	$-16, %rax             /* 16-byte align */
	subq	$16, %rax              /* reserve a slot; stays 16-aligned */
	movq	$0, 0(%rdi)            /* rbx */
	movq	$0, 8(%rdi)            /* rbp */
	movq	%rcx, 16(%rdi)         /* r12 = entry */
	movq	%r8, 24(%rdi)          /* r13 = arg */
	movq	$0, 32(%rdi)           /* r14 */
	movq	$0, 40(%rdi)           /* r15 */
	movq	%rax, 48(%rdi)         /* rsp */
	leaq	SYM(zrt_ctx_trampoline)(%rip), %rax
	movq	%rax, 56(%rdi)         /* rip = trampoline */
	ret

	.p2align 4
SYM(zrt_ctx_trampoline):
	movq	%r13, %rdi             /* arg */
	jmp	*%r12                  /* entry(arg); never returns */

#if defined(__linux__)
	.section .note.GNU-stack,"",@progbits
#endif
