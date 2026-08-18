#pragma once

#define FWSTATE_MODULE_NAME "fwstate"

#include "lib/controlplane/config/zone.h"

#include "lib/fwstate/config.h"

struct fwstate_module_config {
	struct cp_module cp_module;

	struct fwstate_config cfg;

	// Receive-side sync parameters: packet matching, timeouts, and
	// suppression. Split from the maps so the struct mirrors what the
	// module consumes.
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
