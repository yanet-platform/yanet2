#pragma once

#include <stddef.h>

#include "lib/controlplane/config/defines.h"
#include "lib/errors/errors.h"

struct agent;

struct pdump_cleanup_targets {
	size_t module_count;
	char (*modules)[CP_MODULE_NAME_LEN];
	size_t ring_count;
	char (*rings)[CP_OBJECT_NAME_LEN];
};

// Copy names of published pdump entities owned by the current head's
// predecessors.
//
// The result owns only process-local memory and retains no generation pin.
struct pdump_cleanup_targets *
pdump_cleanup_targets_new(struct agent *agent, yanet_error **err);

void
pdump_cleanup_targets_free(struct pdump_cleanup_targets *targets);
