/*
 * sys.c - the Zerg runtime's minimal system surface.
 *
 * For the Phase 1d core this is only abort-message output; `print` stays libc
 * printf in emitted code and does not route through here, and real io is a later
 * phase. Keeping it isolated marks the one spot a freestanding backend swaps for
 * a platform console.
 */
#include "zergrt.h"

#include <stdio.h>

void zrt_report(const char *msg) {
	if (msg != NULL) {
		fputs(msg, stderr);
		fputc('\n', stderr);
	}
}
