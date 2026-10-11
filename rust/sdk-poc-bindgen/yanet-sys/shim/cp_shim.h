// Control-plane helpers over the inline C the Rust api crates build on.
//
// Every function is a thin wrapper of a header-inline C routine (block
// allocation, LPM construction, relative-pointer reads), so the Rust side
// never re-implements allocator or LPM logic on the cold path.
#pragma once

#include <stddef.h>
#include <stdint.h>

#include "lib/errors/errors.h"

struct agent;
struct cp_module;
struct lpm;

// Allocates a zeroed block from the agent's root memory context, NULL when
// the agent memory is exhausted.
void *
yanet_sys_cp_balloc(struct agent *agent, size_t size);

// Returns a block allocated by yanet_sys_cp_balloc.
void
yanet_sys_cp_bfree(struct agent *agent, void *block, size_t size);

// Initialises an LPM whose memory context is a child of the module's.
int
yanet_sys_cp_lpm_init(
	struct lpm *lpm, struct cp_module *owner, const char *name
);

// Releases every block of an initialised LPM.
void
yanet_sys_cp_lpm_free(struct lpm *lpm);

int
yanet_sys_cp_lpm_insert(
	struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
);

// Agent the module was created by, resolved from its relative link.
struct agent *
yanet_sys_cp_module_agent(struct cp_module *cp_module);

// Copies the address and size of up to capacity arenas of the agent and
// returns the total arena count.
size_t
yanet_sys_cp_agent_arenas(
	struct agent *agent, uintptr_t *starts, uint64_t *sizes, size_t capacity
);
