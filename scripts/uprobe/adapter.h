#pragma once

#include "cp_lock.h"
#include <stddef.h>
#include <stdint.h>

int
cp_lock_snapshot(
	const char *name,
	uint64_t inode,
	const uint64_t *offsets,
	struct cp_lock_site_stats *rows,
	struct cp_lock_counters *counts,
	char *error
);
