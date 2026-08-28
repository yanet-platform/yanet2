#pragma once

#include <assert.h>

struct cp_config;

// Configuration whose lock the calling thread holds, if any;
// maintained by the lock, try-lock and unlock routines. A thread locks
// at most one configuration at a time, which the lock routines assert.
extern __thread struct cp_config *cp_config_locked_by_thread;

// Abort in debug builds unless the calling thread holds the lock of
// this configuration.
//
// The shared lock word stores a process id and cannot tell threads or
// instances apart, so the check reads the per-thread record; a sibling
// thread holding the lock, or another instance's lock, does not
// satisfy it. A configuration of NULL matches a held record of none,
// which keeps ownerless test registries exempt from the check.
static inline void
cp_config_assert_locked(const struct cp_config *cp_config) {
	(void)cp_config;
	assert(cp_config_locked_by_thread == cp_config);
}
