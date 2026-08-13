#pragma once

#define FWSTATE_MODULE_NAME "fwstate"

#include "lib/controlplane/config/zone.h"

#include "lib/fwstate/config.h"

// Sentinel for "no object link at this slot". object_link_get_address
// returns NULL for any index >= object_link_count, so a config with no
// map link at this slot resolves to a NULL fwtable.
#define FWSTATE_OBJECT_LINK_NONE UINT64_MAX

struct fwstate_module_config {
	struct cp_module cp_module;

	// Object link indices for the v4 and v6 fwtables, declared by the
	// module constructor and resolved at execution-context build time
	// into per-worker entries naming the linked fwstate-map objects.
	// The FWSTATE_OBJECT_LINK_NONE sentinel marks an absent link, and
	// that family's sync frames are then counted and dropped without
	// inserting.
	uint64_t v4_object_link_idx;
	uint64_t v6_object_link_idx;

	// Receive-side sync parameters: packet matching, timeouts, and
	// suppression. Kept standalone so the struct mirrors what the module
	// consumes.
	struct fwstate_sync_config sync_config;

	// Module-level counters, registered by fwstate_module_config_new.
	// Each counter_id is resolved per-worker via counter_get_address().
	// size=2 counters hold [packets, bytes]; size=1 counters hold
	// [packets].
	uint64_t sync_packets_counter_id;
	uint64_t passthrough_counter_id;
	uint64_t sync_v4_inserted_counter_id;
	uint64_t sync_v6_inserted_counter_id;
	uint64_t sync_v4_insert_failed_counter_id;
	uint64_t sync_v6_insert_failed_counter_id;
	uint64_t sync_v4_suppressed_counter_id;
	uint64_t sync_v6_suppressed_counter_id;
	uint64_t external_dropped_counter_id;
	uint64_t internal_forwarded_counter_id;
};
