#pragma once

#include <stdint.h>

#include "common/memory_address.h"
#include "lib/dataplane/device/device.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"

#include "lib/dataplane/counters/module.h"

struct counter_value_handle;
struct counter_storage;

struct cp_module;
struct cp_chain;
struct cp_function;
struct cp_pipeline;
struct cp_device;
struct cp_object;

struct cp_config_gen;
struct cp_config_counter_storage_registry;
struct dp_config;

struct module_ectx {
	module_handler handler;
	struct cp_module *cp_module;

	struct counter_value_handle *rx_counter;
	struct counter_value_handle *tx_counter;
	struct counter_value_handle *drop_counter;
	struct counter_value_handle *pending_input_counter;
	struct counter_value_handle *pending_output_counter;

	struct counter_value_handle *perf_counters[MODULE_ECTX_PERF_COUNTERS];

	struct counter_storage *counter_storage;
	struct counter_storage *config_counter_storage;
	struct config_gen_ectx *config_gen_ectx;

	uint64_t mc_index_size;
	uint64_t *mc_index;

	uint64_t cm_index_size;
	uint64_t *cm_index;

	// One entry per object this module links to. Each entry owns a counter
	// storage spawned from the linked object's link counter registry and
	// references that object's per-worker execution context.
	uint64_t object_link_count;
	struct module_object_link_ectx *object_links;
};

// Per-worker, per-link state for a module's link to a cp_object.
//
// counter_storage holds the values for the counters declared in the linked
// object's link counter registry, private to this module link on this worker.
// object_ectx references the linked object's per-worker execution context.
struct module_object_link_ectx {
	struct counter_storage *counter_storage;
	struct object_ectx *object_ectx;
};

// Return the object link execution context at the given link index, or NULL
// when the index is out of range (the module has no object link at that slot).
static inline struct module_object_link_ectx *
object_link_get_address(struct module_ectx *module_ectx, uint64_t index) {
	if (index >= module_ectx->object_link_count) {
		return NULL;
	}
	struct module_object_link_ectx *links =
		ADDR_OF(&module_ectx->object_links);
	return links + index;
}

static inline uint64_t
module_ectx_encode_device(struct module_ectx *module_ectx, uint64_t index) {
	uint64_t *mc_index = ADDR_OF(&module_ectx->mc_index);
	return mc_index[index];
}

static inline uint64_t
module_ectx_decode_device(struct module_ectx *module_ectx, uint64_t index) {
	uint64_t *cm_index = ADDR_OF(&module_ectx->cm_index);
	return cm_index[index];
}

struct chain_module_ectx {
	struct module_ectx *module_ectx;
	struct counter_value_handle *tsc_counter;
};

struct chain_ectx {
	struct cp_chain *cp_chain;
	struct counter_storage *counter_storage;
	struct counter_value_handle *counter_packet_pending_input;
	struct counter_value_handle *counter_packet_pending_output;
	uint64_t length;
	struct packet_front schedule;
	struct chain_module_ectx modules[];
};

struct function_ectx {
	struct cp_function *cp_function;
	struct counter_value_handle *counter_packet_in;
	struct counter_value_handle *counter_packet_out;
	struct counter_value_handle *counter_packet_drop;
	struct counter_value_handle *counter_packet_pending_input;
	struct counter_value_handle *counter_packet_pending_output;
	struct counter_storage *counter_storage;
	uint64_t chain_count;
	struct chain_ectx **chains;
	uint64_t chain_map_size;
	struct chain_ectx *chain_map[];
};

struct pipeline_ectx {
	struct cp_pipeline *cp_pipeline;
	struct counter_value_handle *counter_packet_in;
	struct counter_value_handle *counter_packet_out;
	struct counter_value_handle *counter_packet_drop;
	struct counter_value_handle *counter_packet_pending_input;
	struct counter_value_handle *counter_packet_pending_output;
	struct counter_storage *counter_storage;
	uint64_t length;
	struct packet_front schedule;
	struct function_ectx *functions[];
};

struct device_entry_ectx {
	device_handler handler;
	struct counter_value_handle *counter_packet_rx;
	struct counter_value_handle *counter_packet_entry;
	struct counter_value_handle *counter_packet_tx;
	struct counter_value_handle *counter_packet_drop;
	struct counter_value_handle *counter_packet_pending_input;
	struct counter_value_handle *counter_packet_pending_output;
	uint64_t pipeline_count;
	struct pipeline_ectx **pipelines;
	uint64_t pipeline_map_size;
	// Per-entry scratch front the worker round demuxes traffic into.
	//
	// The round pops pending input/output for this device's entry point
	// into this front, runs the entry, then merges the result back and
	// leaves the front empty, so it is reused in place across rounds
	// instead of being reinitialized on the stack every iteration.
	struct packet_front schedule;
	uint64_t pipeline_map[];
};

struct device_ectx {
	struct cp_device *cp_device;
	struct counter_storage *counter_storage;
	struct device_entry_ectx *input_pipelines;
	struct device_entry_ectx *output_pipelines;
};

// Per-worker execution context for a cp_object.
//
// Owns the counter_storage spawned from the object's counter_registry for this
// worker. Counters are read by name through the storage, and the storage is
// reachable through the config generation's tag-indexed counter storage
// registry under the object_type and object_name tags.
struct object_ectx {
	struct cp_object *cp_object;
	struct counter_storage *counter_storage;
};

struct config_gen_ectx {
	struct cp_config_gen *cp_config_gen;
	struct phy_device_map *phy_device_maps;

	struct cp_config_counter_storage_registry *counter_storage_registry;

	// Offset pointer to a separately allocated array of object_ectx offset
	// pointers, one per object index, parallel to devices below. Lives in
	// its own allocation because devices[] is the trailing flexible array
	// member.
	uint64_t object_count;
	struct object_ectx **objects;

	// Per-worker scratch front reused across worker rounds.
	//
	// Initialized once when the ectx is created and left clean at the
	// end of every worker round, so the worker loop reuses it in place
	// instead of reinitializing a fresh front on each iteration.
	struct packet_front packet_front;

	uint64_t device_count;
	struct device_ectx *devices[];
};

// Returns the dp_config that the worker's active config generation
// belongs to, reached through the module_ectx -> config_gen_ectx ->
// cp_config_gen offset-pointer chain.
//
// Returns NULL if any hop of the chain is not wired, so callers must treat
// a NULL result as "unavailable" and fall back accordingly rather than
// dereferencing it.
struct dp_config *
module_ectx_dp_config(struct module_ectx *module_ectx);
