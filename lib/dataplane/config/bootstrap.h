#pragma once

#include <stddef.h>
#include <stdint.h>

struct dp_config;
struct cp_config;

// Initialise dataplane and controlplane configuration zones inside a
// contiguous storage blob.
//
// On success *res_dp_config and *res_cp_config point into storage and
// are mutually navigable via offset pointers. Caller owns storage.
// Returns 0 on success, -1 on failure.
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
// Call this after releasing cp_config, once the instance is fully initialised
// and ready for attach.
void
dp_config_mark_ready(struct dp_config *dp_config);
