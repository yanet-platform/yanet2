// Helpers over the C of `common/` that the SDK builds embedded objects with.
//
// One wrapper per C routine, header-inline in C, compiled into the shim
// library with hidden visibility as the dataplane shim is. The routines are
// those needed to construct and release embedded LPM tables.
#pragma once

#include <stddef.h>
#include <stdint.h>

#include "shim_visibility.h"

struct lpm;
struct memory_context;

// Initialisation opens a named child context and allocates its first LPM page.
//
// The common/lpm.h contract returns 0 on success or -1 with errno ENOMEM on
// exhaustion. A failed table is unusable but still requires
// yanet_sys_common_lpm_free to unlink its context from the parent.
YANET_SYS_HIDDEN int
yanet_sys_common_lpm_init(
	struct lpm *lpm, struct memory_context *memory_context, const char *name
);

// Insertion maps an inclusive range of big-endian keys to one value.
//
// The supplied key width is in bytes. The common/lpm.h contract returns 0 on
// success or -1 with errno ENOMEM when a page cannot be allocated. Failure
// leaves the table valid for yanet_sys_common_lpm_free.
YANET_SYS_HIDDEN int
yanet_sys_common_lpm_insert(
	struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
);

// lpm_free (common/lpm.h): returns every page of the LPM and unlinks its
// context from the parent.
//
// Cannot fail. Sound after a failed init or insert.
YANET_SYS_HIDDEN void
yanet_sys_common_lpm_free(struct lpm *lpm);
