#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

struct dp_config;
struct cp_config;

// Report whether storage of dp_memory and cp_memory bytes holds both zones
// whole, each aligned for its structure.
//
// The control-plane zone starts dp_memory bytes in, so dp_memory must hold
// the dataplane zone and keep the control-plane zone aligned: every reader
// of the storage assumes this placement instead of checking it.
bool
dp_storage_layout_valid(
	const void *storage, size_t dp_memory, size_t cp_memory
);

// Initialise dataplane and controlplane configuration zones inside a
// contiguous storage blob.
//
// On success *res_dp_config and *res_cp_config point into storage and
// are mutually navigable via offset pointers. Caller owns storage.
// The control-plane zone's lock is taken and released inside this
// routine; callers pair their own lock around any further control-plane
// mutations. Refuses storage whose layout dp_storage_layout_valid
// rejects. Returns 0 on success, -1 on failure.
int
dp_storage_init(
	uint32_t numa_idx,
	uint32_t instance_idx,
	void *storage,
	size_t dp_memory,
	size_t cp_memory,
	struct dp_config **res_dp_config,
	struct cp_config **res_cp_config
);

// Publish the readiness marker for this instance.
//
// Call this only after every control-plane mutation is complete and no
// lock is held, once the instance is fully initialised and ready for
// attach.
void
dp_config_mark_ready(struct dp_config *dp_config);
