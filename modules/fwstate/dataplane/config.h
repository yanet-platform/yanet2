#pragma once

#define FWSTATE_MODULE_NAME "fwstate"

#include "controlplane/config/zone.h"

#include "fwstate/config.h"

// Sentinel for "no object link at this slot". object_link_get_address
// returns NULL for any index >= object_link_count, so a module with no
// map link at this slot resolves to a NULL fwtable.
#define FWSTATE_OBJECT_LINK_NONE UINT64_MAX

struct fwstate_module_config {
	struct cp_module cp_module;

	struct fwstate_sync_config sync_config;

	// Object link indices for the v4 and v6 fwtables, set by
	// fwstate_module_config_set via cp_module_link_object and resolved
	// at ectx build time into per-worker object_ectx entries.
	uint64_t v4_object_link_idx;
	uint64_t v6_object_link_idx;

	// Module-level counters, registered by fwstate_module_config_new.
	uint64_t sync_packets_counter_id;
	uint64_t passthrough_counter_id;
	uint64_t sync_v4_inserted_counter_id;
	uint64_t sync_v6_inserted_counter_id;
	uint64_t sync_v4_insert_failed_counter_id;
	uint64_t sync_v6_insert_failed_counter_id;
	uint64_t external_dropped_counter_id;
	uint64_t internal_forwarded_counter_id;
};
