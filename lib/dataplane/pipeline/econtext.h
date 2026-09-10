#pragma once

#include <stdint.h>

#include "common/memory_address.h"
#include "common/rlist.h"
#include "lib/dataplane/device/device.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"

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
	// The same module, as an absolute address for the packet hot
	// path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct cp_module *abs_cp_module;

	// The module's counters, as absolute addresses for the packet hot
	// path.
	//
	// The publishing process derives them from the counter registry
	// ids of the cp_module and the module's counter storage before
	// the context is released to workers; they are zero until then.
	struct counter_value_handle *rx_counter;
	struct counter_value_handle *tx_counter;
	struct counter_value_handle *drop_counter;
	struct counter_value_handle *pending_input_counter;
	struct counter_value_handle *pending_output_counter;

	struct counter_storage *counter_storage;
	// The same storage, as an absolute address for the packet hot
	// path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct counter_storage *abs_counter_storage;

	// Per-worker storages for the module's runtime counter registries, in
	// parallel with cp_module.runtime_counter_registries. Offset pointer
	// to a separately allocated array of offset pointers, one per
	// registry.
	uint64_t runtime_counter_storage_count;
	// Offset pointer to the module's per-registry runtime storages,
	// owned by the control plane.
	struct counter_storage **runtime_counter_storages;
	// The same storages, as absolute addresses for the packet hot
	// path.
	//
	// Offset pointer to an array the publishing process fills from
	// the relative one before the context is released to workers; it
	// is zero until then.
	struct counter_storage **abs_runtime_counter_storages;
	// The array of absolute storages above, as an absolute address
	// for the packet hot path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct counter_storage **abs_runtime_counter_storages_base;
	// Offset pointer to the owning generation, owned by the control
	// plane.
	struct config_gen_ectx *config_gen_ectx;
	// The same generation, as an absolute address for the packet hot
	// path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct config_gen_ectx *abs_config_gen_ectx;
	uint16_t packet_recirc_limit;

	uint64_t mc_index_size;
	// Offset pointer to the module-to-config device index table,
	// owned by the control plane.
	uint64_t *mc_index;
	// The same table, as an absolute address for the packet hot path.
	//
	// The publishing process copies it from the relative field before
	// the context is released to workers; it is zero until then.
	uint64_t *abs_mc_index;

	uint64_t cm_index_size;
	// Offset pointer to the config-to-module device index table,
	// owned by the control plane.
	uint64_t *cm_index;
	// The same table, as an absolute address for the packet hot path.
	//
	// The publishing process copies it from the relative field before
	// the context is released to workers; it is zero until then.
	uint64_t *abs_cm_index;

	// One entry per object this module links to. Each entry owns a counter
	// storage spawned from the linked object's link counter registry and
	// references that object's per-worker execution context.
	uint64_t object_link_count;
	struct module_object_link_ectx *object_links;
	// The same link array, as an absolute address for the packet hot
	// path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct module_object_link_ectx *abs_object_links;
};

// Per-worker, per-link state for a module's link to a cp_object.
//
// counter_storage holds the values for the counters declared in the linked
// object's link counter registry, private to this module link on this worker.
// object_ectx references the linked object's per-worker execution context.
struct module_object_link_ectx {
	struct counter_storage *counter_storage;
	struct object_ectx *object_ectx;
	// The linked object's context, as an absolute address for the
	// packet hot path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct object_ectx *abs_object_ectx;
};

// Return the object link execution context at the given link index, or NULL
// when the index is out of range (the module has no object link at that slot).
static inline struct module_object_link_ectx *
object_link_get_address(struct module_ectx *module_ectx, uint64_t index) {
	if (index >= module_ectx->object_link_count) {
		return NULL;
	}
	return module_ectx->abs_object_links + index;
}

// Return the per-worker counter storage for the module's runtime counter
// registry at the given index, or NULL when the index is out of range.
static inline struct counter_storage *
module_ectx_counter_storage(struct module_ectx *module_ectx, uint64_t index) {
	if (index >= module_ectx->runtime_counter_storage_count) {
		return NULL;
	}
	return module_ectx->abs_runtime_counter_storages_base[index];
}

static inline uint64_t
module_ectx_encode_device(struct module_ectx *module_ectx, uint64_t index) {
	return module_ectx->abs_mc_index[index];
}

static inline uint64_t
module_ectx_decode_device(struct module_ectx *module_ectx, uint64_t index) {
	return module_ectx->abs_cm_index[index];
}

struct chain_ectx {
	struct cp_chain *cp_chain;
	struct counter_storage *counter_storage;
	// Absolute counterparts of the chain's counters, for the packet
	// hot path.
	//
	// The publishing process derives them from the counter registry
	// ids of the cp_chain and the chain's counter storage before the
	// context is released to workers; they are zero until then.
	struct counter_value_handle *abs_counter_packet_pending_input;
	struct counter_value_handle *abs_counter_packet_pending_output;
	// Offset pointer to an array of per-slot offset pointers to the
	// chain's module contexts, mirroring the tail below.
	//
	// Owned by the control plane: creation fills it and the free path
	// walks it, so the relative addresses stay available after the
	// tail is turned into absolute ones.
	struct module_ectx **module_ptrs;
	uint64_t length;
	struct packet_front schedule;
	// Absolute addresses of the chain's module contexts.
	//
	// The publishing process copies them from the module_ptrs array
	// before the context is released to workers; they are zero until
	// then, and the packet hot path loads them directly.
	struct module_ectx *modules[];
};

struct function_ectx {
	struct cp_function *cp_function;
	// Absolute counterparts of the function's counters, for the packet
	// hot path.
	//
	// The publishing process derives them from the counter registry
	// ids of the cp_function and the function's counter storage
	// before the context is released to workers; they are zero until
	// then.
	struct counter_value_handle *abs_counter_packet_in;
	struct counter_value_handle *abs_counter_packet_out;
	struct counter_value_handle *abs_counter_packet_drop;
	struct counter_value_handle *abs_counter_packet_pending_input;
	struct counter_value_handle *abs_counter_packet_pending_output;
	struct counter_storage *counter_storage;
	uint64_t chain_count;
	// Offset pointer to an array of per-slot offset pointers to the
	// function's chains, mirroring the chains array.
	//
	// Owned by the control plane: creation fills it and the free and
	// link paths walk it, so the relative addresses stay available
	// after the chains array holds absolute ones.
	struct chain_ectx **chain_ptrs;
	// The function's chains, as absolute addresses for the packet hot
	// path.
	//
	// The publishing process copies them from the chain_ptrs array
	// before the context is released to workers; they are zero until
	// then, and the packet hot path loads them directly.
	struct chain_ectx **chains;
	// The chains array, as an absolute address for the packet hot
	// path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct chain_ectx **abs_chains;
	uint64_t chain_map_size;
	// The function's chains, indexed by packet hash for the demux.
	//
	// Creation writes relative addresses; the publishing process
	// recodes them to absolute ones in place, re-deriving the
	// weighted expansion from the chains array and the controlplane
	// weights so repeated passes stay correct.
	struct chain_ectx *chain_map[];
};

struct pipeline_ectx {
	struct cp_pipeline *cp_pipeline;
	// Absolute counterparts of the pipeline's counters, for the packet
	// hot path.
	//
	// The publishing process derives them from the counter registry
	// ids of the cp_pipeline and the pipeline's counter storage
	// before the context is released to workers; they are zero until
	// then.
	struct counter_value_handle *abs_counter_packet_in;
	struct counter_value_handle *abs_counter_packet_out;
	struct counter_value_handle *abs_counter_packet_drop;
	struct counter_value_handle *abs_counter_packet_pending_input;
	struct counter_value_handle *abs_counter_packet_pending_output;
	struct counter_storage *counter_storage;
	// Offset pointer to an array of per-slot offset pointers to the
	// pipeline's functions, mirroring the tail below.
	//
	// Owned by the control plane: creation fills it and the free and
	// link paths walk it, so the relative addresses stay available
	// after the tail is turned into absolute ones.
	struct function_ectx **function_ptrs;
	uint64_t length;
	struct packet_front schedule;
	// Absolute addresses of the pipeline's functions.
	//
	// The publishing process copies them from the function_ptrs
	// array before the context is released to workers; they are zero
	// until then, and the packet hot path loads them directly.
	struct function_ectx *functions[];
};

enum device_entry_direction {
	device_entry_direction_input,
	device_entry_direction_output,
};

// Worklist state of a device entry, for the owning worker only.
//
// The ready state rides the generation's ready list and the home
// state its home list. The scheduling path moves an entry to ready
// from the home list, and the round works every ready entry back
// home.
enum device_entry_schedule_state {
	device_entry_schedule_ready = 0,
	device_entry_schedule_home = 1,
};

struct device_entry_ectx {
	device_handler handler;

	// Worker-side worklist membership and round dispatch.
	//
	// The node links this entry into one of the generation's two
	// worklists and the direction selects input or output processing;
	// the back reference reaches the owning device without an array
	// walk (an offset pointer filled by the control plane, as is the
	// direction). List links are the owning worker's business only.
	struct rlist schedule_node;
	uint8_t schedule_list;
	uint8_t direction;
	// Offset pointer to the owning device, kept by the control plane.
	struct device_ectx *device_ectx;
	// The same device, as an absolute address for the packet hot
	// path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct device_ectx *abs_device_ectx;

	// The entry's counters, as absolute addresses for the packet hot
	// path.
	//
	// The publishing process derives them from the counter registry
	// ids of the entry's controlplane counterpart and the device's
	// counter storage before the context is released to workers; they
	// are zero until then.
	struct counter_value_handle *counter_packet_rx;
	struct counter_value_handle *counter_packet_entry;
	struct counter_value_handle *counter_packet_tx;
	struct counter_value_handle *counter_packet_drop;
	struct counter_value_handle *counter_packet_recirc_drop;
	struct counter_value_handle *counter_packet_pending_input;
	struct counter_value_handle *counter_packet_pending_output;
	uint64_t pipeline_count;
	// Offset pointer to an array of per-slot offset pointers to the
	// entry's pipelines, mirroring the pipelines array.
	//
	// Owned by the control plane: creation fills it and the free and
	// link paths walk it, so the relative addresses stay available
	// after the pipelines array holds absolute ones.
	struct pipeline_ectx **pipeline_ptrs;
	// The entry's pipelines, as absolute addresses for the packet hot
	// path.
	//
	// The publishing process copies them from the pipeline_ptrs
	// array before the context is released to workers; they are zero
	// until then, and the packet hot path loads them directly.
	struct pipeline_ectx **pipelines;
	// The pipelines array, as an absolute address for the packet hot
	// path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct pipeline_ectx **abs_pipelines;
	uint64_t pipeline_map_size;
	// Per-entry inbox for packets awaiting processing.
	//
	// The worker detaches each batch before invoking the entry, so packets
	// routed back here during processing remain queued for the next
	// traversal.
	struct packet_front schedule;
	// The entry's pipelines, indexed by packet hash for the demux.
	//
	// Creation writes relative addresses; the publishing process
	// recodes them to absolute ones in place, re-deriving the
	// weighted expansion from the pipelines array and the
	// controlplane weights so repeated passes stay correct.
	struct pipeline_ectx *pipeline_map[];
};

struct device_ectx {
	struct cp_device *cp_device;
	// The same device, as an absolute address for the packet hot
	// path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct cp_device *abs_cp_device;
	struct counter_storage *counter_storage;
	struct device_entry_ectx *input_pipelines;
	struct device_entry_ectx *output_pipelines;
	// The device's entries, as absolute addresses for the packet hot
	// path.
	//
	// The publishing process copies them from the relative fields
	// above before the context is released to workers; they are zero
	// until then, and the packet hot path loads them directly.
	struct device_entry_ectx *abs_input_pipelines;
	struct device_entry_ectx *abs_output_pipelines;
};

// Per-worker execution context for a cp_object.
//
// Owns the counter_storage spawned from the object's counter_registry for this
// worker. Counters are read by name through the storage, and the storage is
// reachable through the config generation's tag-indexed counter storage
// registry under the object_type and object_name tags.
struct object_ectx {
	struct cp_object *cp_object;
	// The same object, as an absolute address for the packet hot
	// path.
	//
	// The publishing process copies it from the relative field above
	// before the context is released to workers; it is zero until
	// then.
	struct cp_object *abs_cp_object;
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

	// The device-entry home list.
	//
	// At rest every entry is linked onto it. The first build links
	// each device's input entry before its output one; a round that
	// runs entries leaves them parked in run order instead. A
	// round drains the list onto its local untouched list and works
	// entries back home through the ready list, so the home list is
	// empty while a round runs. Links are raw pointers built only by
	// the owning worker, which also raises the ready flag on first
	// build; the control plane leaves these fields zeroed.
	struct rlist entry_list;
	// The queue of entries a packet was scheduled onto.
	//
	// Filled by the scheduling path and drained by the round, which
	// moves each processed entry back to the home list; empty
	// between rounds. Shares the link contract of the home list.
	struct rlist ready_list;
	uint8_t schedules_ready;

	// Offset pointer to an array of per-slot offset pointers to the
	// devices, mirroring the tail below.
	//
	// Owned by the control plane: creation fills it and the free,
	// link and module-build paths walk it, so the relative addresses
	// stay available after the tail is turned into absolute ones.
	struct device_ectx **device_ptrs;
	uint64_t device_count;
	// The generation's devices, as absolute addresses for the packet
	// hot path.
	//
	// The publishing process copies them from the device_ptrs array
	// before the context is released to workers; they are zero until
	// then, and the packet hot path loads them directly.
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
