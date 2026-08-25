#pragma once

#include "common/container_of.h"
#include "common/memory.h"

#include "lib/counters/counters.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/defines.h"
#include "lib/controlplane/config/registry.h"

#include "lib/dataplane/counters/module.h"
#include "lib/errors/errors.h"

struct counter_handle;
struct module_performance_counter;

/*
 * Structure cp_module reflects module configuration
 *
 * It is allocated by external agent inside its address space and
 * then linked into pipeline control chain.
 */
struct cp_module;

struct cp_module_device {
	char name[CP_DEVICE_NAME_LEN];
};

// Named counter registry for config-defined counters (per-rule, per-route,
// etc.). Carries a module-unique tag so its per-worker storage can be
// distinguished from other registries and from the predefined module
// counters.
struct cp_module_counter_registry {
	char tag[COUNTER_NAME_LEN];
	struct counter_registry registry;
};

// A module's declared link to a cp_object, identified by the object's
// (type, name) pair. Resolved to a per-worker object_ectx at execution-context
// build time, mirroring cp_module_device's name-based device linkage.
struct cp_module_object {
	char type[CP_OBJECT_TYPE_LEN];
	char name[CP_OBJECT_NAME_LEN];
};

struct cp_module {
	struct registry_item config_item;

	// Reference to dataplane module
	uint64_t dp_module_idx;

	char type[80];
	/*
	 * All module datas are accessible through registry so name
	 * should live somewhere there.
	 */
	char name[CP_MODULE_NAME_LEN];

	// Controlplane generation when this object was created
	uint64_t gen;

	// Counters declared inside module data
	struct counter_registry counter_registry;

	// Named counter registries for config-defined counters (per-rule,
	// per-route, etc.). Each registry carries a module-unique tag. All
	// registries are expanded into per-worker storages on config-gen
	// installation.
	//
	// The array holds shared-memory offset pointers to out-of-line
	// registries: a registry embeds self-relative offsets, so it must
	// never move after creation, while the pointer array itself may be
	// reallocated on growth.
	uint64_t runtime_counter_registry_count;
	struct cp_module_counter_registry **runtime_counter_registries;

	// Rx packet/byte counter (size 2: [packets, bytes])
	uint64_t rx_counter_id;
	// Tx packet/byte counter (size 2: [packets, bytes])
	uint64_t tx_counter_id;
	// Drop packet/byte counter (size 2: [packets, bytes])
	uint64_t drop_counter_id;

	// Pending-input packet/byte counter (size 2: [packets, bytes])
	uint64_t pending_input_counter_id;
	// Pending-output packet/byte counter (size 2: [packets, bytes])
	uint64_t pending_output_counter_id;

	// Runtime indices for the performance histogram counters.
	// These indices map to the actual counter storage locations for
	// the performance counters defined in cp_module->perf_counters_indices.
	// Used during packet processing to efficiently access latency tracking
	// histograms for different batch sizes.
	uint64_t perf_counters_indices[MODULE_ECTX_PERF_COUNTERS];

	// Link to the previous instance of the module configuration
	struct cp_module *prev;
	// Controlplane agent the configuration belongs to
	struct agent *agent;
	// Memory context for additional resources inside the configuration
	struct memory_context memory_context;

	uint64_t device_count;
	struct cp_module_device *devices;

	// Objects this module links to, declared via cp_module_link_object and
	// resolved to per-worker object execution contexts at build time.
	uint64_t object_count;
	struct cp_module_object *objects;
};

/**
 * Link a device to a module configuration.
 *
 * Associates a device with the module by name and returns its index.
 *
 * @param cp_module Pointer to the module configuration
 * @param name Name of the device to link
 * @param index Output parameter for the device index
 * @param err Error output parameter
 * @return 0 on success, negative error code on failure
 */
int
cp_module_link_device(
	struct cp_module *cp_module,
	const char *name,
	uint64_t *index,
	yanet_error **err
);

/**
 * Link an object to a module configuration.
 *
 * Associates a cp_object with the module by the object's type and name. If an
 * entry for the same (type, name) already exists, its index is returned
 * unchanged; otherwise a new entry is appended.
 *
 * @param cp_module Pointer to the module configuration
 * @param object_type Type identifier of the object to link
 * @param object_name Name identifier of the object to link
 * @param index Output parameter for the link index
 * @param err Error output parameter
 * @return 0 on success, negative error code on failure
 */
int
cp_module_link_object(
	struct cp_module *cp_module,
	const char *object_type,
	const char *object_name,
	uint64_t *index,
	yanet_error **err
);

/**
 * Initialize a module configuration structure.
 *
 * Sets up counters and associates the module with the given agent. The
 * module starts with a zero reference count: it is dangling, known only to
 * its creator, and invisible to every generation until an upsert registers
 * it. A failed construction must be unwound by the module's own typed
 * destroy routine.
 *
 * @param cp_module Pointer to the module configuration to initialize
 * @param agent Pointer to the controlplane agent owning this module
 * @param module_type Type identifier for the module
 * @param module_name Name identifier for the module
 * @param err Error output parameter
 * @return 0 on success, negative error code on failure
 */
int
cp_module_init(
	struct cp_module *cp_module,
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	yanet_error **err
);

/**
 * Release resources allocated by cp_module_init.
 *
 * Must be called before freeing the module configuration struct itself.
 *
 * @param cp_module Pointer to the module configuration to finalize
 */
void
cp_module_fini(struct cp_module *cp_module);

// Get or create a named counter registry on the module.
//
// The tag must be unique within the module; on a repeat tag the existing
// registry is returned with its index. The index is written through
// `index_out`. Returns NULL on allocation failure.
struct counter_registry *
cp_module_counter_registry(
	struct cp_module *cp_module,
	const char *tag,
	uint64_t *index_out,
	yanet_error **err
);

// Try to take a dangling module out of circulation for destruction.
//
// A module's reference count is the number of live configuration
// generations that registered it; construction never takes a reference of
// its own. Zero therefore means dangling: the module is registered
// nowhere, no reader can reach it, and only its owner — the module code
// that knows its type and destructor — still knows it exists.
//
// Returns 0 when the count is zero, granting the caller the exclusive
// right to run the module's typed destroy routine immediately (registry
// slots are the only source of references, and a zero-count module sits in
// none of them, so no later operation can re-reference it). Returns -1
// with errno EAGAIN while a live generation still holds the module; the
// caller must keep its handle and retry once the generations drain.
//
// The count is read under the configuration lock, which every registry
// mutation also runs under, so the answer is stable against concurrent
// generation installs and releases in other processes.
int
cp_module_try_destroy(struct cp_module *cp_module, yanet_error **err);

struct cp_module_registry {
	struct memory_context *memory_context;
	struct registry registry;
};

/**
 * Initialize a module registry.
 *
 * Creates a new registry for managing module configurations with the
 * specified memory context.
 *
 * @param memory_context Memory context for registry allocations
 * @param registry Pointer to the registry structure to initialize
 * @param err Error output parameter
 * @return 0 on success, negative error code on failure
 */
int
cp_module_registry_init(
	struct memory_context *memory_context,
	struct cp_module_registry *registry,
	yanet_error **err
);

/**
 * Copy a module registry to a new instance.
 *
 * Creates a deep copy of an existing module registry, useful for
 * configuration updates and rollback scenarios.
 *
 * @param memory_context Memory context for the new registry
 * @param new_module_registry Pointer to the destination registry
 * @param old_module_registry Pointer to the source registry to copy
 * @param err Error output parameter
 * @return 0 on success, negative error code on failure
 */
int
cp_module_registry_copy(
	struct memory_context *memory_context,
	struct cp_module_registry *new_module_registry,
	struct cp_module_registry *old_module_registry,
	yanet_error **err
);

// Release all modules in the registry and free its resources.
//
// Idempotent on zero-init.
void
cp_module_registry_fini(struct cp_module_registry *module_registry);

/**
 * Get a module from the registry by index.
 *
 * Retrieves a module configuration using its numeric index in the registry.
 *
 * @param module_registry Pointer to the module registry
 * @param index Index of the module to retrieve
 * @return Pointer to the module configuration, or NULL if not found
 */
struct cp_module *
cp_module_registry_get(
	struct cp_module_registry *module_registry, uint64_t index
);

/**
 * Look up a module in the registry by type and name.
 *
 * Searches for a module configuration matching the specified type and name.
 *
 * @param module_registry Pointer to the module registry
 * @param type Module type identifier
 * @param name Module name identifier
 * @return Pointer to the module configuration, or NULL if not found
 */
struct cp_module *
cp_module_registry_lookup(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name
);

/**
 * Insert or update a module in the registry.
 *
 * Adds a new module to the registry or updates an existing one with the
 * same type and name. If a module exists, it will be replaced.
 *
 * @param module_registry Pointer to the module registry
 * @param type Module type identifier
 * @param name Module name identifier
 * @param module Pointer to the module configuration to insert/update
 * @return 0 on success, negative error code on failure
 */
int
cp_module_registry_upsert(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name,
	struct cp_module *module,
	yanet_error **err
);

/**
 * Delete a module from the registry.
 *
 * Removes a module configuration from the registry by type and name.
 *
 * @param module_registry Pointer to the module registry
 * @param type Module type identifier
 * @param name Module name identifier
 * @return 0 on success, negative error code on failure
 */
int
cp_module_registry_delete(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name
);

/**
 * Get the number of modules in the registry.
 *
 * Returns the total count of module configurations currently stored
 * in the registry.
 *
 * @param module_registry Pointer to the module registry
 * @return Number of modules in the registry
 */
size_t
cp_module_registry_size(struct cp_module_registry *module_registry);

/**
 * Parse raw performance counter data into structured performance metrics.
 *
 * This function processes a raw histogram counter (named "hist_N" where N is
 * 0-5) and converts it into a module_performance_counter structure. It:
 * 1. Extracts the batch size index from the counter name
 * 2. Aggregates counter values across all worker threads
 * 3. Calculates mean latency from accumulated nanoseconds
 * 4. Populates latency histogram buckets with batch counts
 *
 * The counter must be one of the 6 performance histogram counters (hist_0
 * through hist_5) that track latency for different batch sizes:
 * - hist_0: 1 packet
 * - hist_1: 2-3 packets
 * - hist_2: 4-7 packets
 * - hist_3: 8-15 packets
 * - hist_4: 16-31 packets
 * - hist_5: 32+ packets
 *
 * The output counter structure will have its latency_ranges array allocated
 * and populated with histogram data. The caller is responsible for freeing
 * this memory.
 *
 * @param counter_handle Handle to the raw counter data from the registry
 * @param workers Number of worker threads to aggregate data from
 * @param idx Output parameter for the batch size index (0-5)
 * @param counter Output parameter for the parsed performance counter structure
 * @return 0 on success, -1 on failure (sets errno to EINVAL or ENOMEM)
 */
int
cp_module_parse_performance_counter(
	struct counter_handle *counter_handle,
	size_t workers,
	size_t *idx,
	struct module_performance_counter *counter
);

/**
 * Parse raw tx/rx counter data and aggregate across workers.
 *
 * This function checks if the provided counter handle corresponds to one of
 * the module's tx/rx counters (tx, rx, tx_bytes, rx_bytes). If it matches,
 * the function aggregates the counter values across all worker threads and
 * stores the result in the appropriate output parameter.
 *
 * The function is designed to be called iteratively for each counter in a
 * module's counter list, allowing selective processing of tx/rx counters
 * while ignoring other counter types.
 *
 * @param counter_handle Handle to the raw counter data from the registry
 * @param workers Number of worker threads to aggregate data from
 * @param tx Output parameter for aggregated tx packet counter
 * @param rx Output parameter for aggregated rx packet counter
 * @param tx_bytes Output parameter for aggregated tx bytes counter
 * @param rx_bytes Output parameter for aggregated rx bytes counter
 * @return 0 on success (counter matched and aggregated),
 *         1 if counter name doesn't match any tx/rx counter (not an error),
 *         -1 on failure (sets errno to EINVAL for invalid parameters)
 */
int
cp_module_parse_tx_rx(
	struct counter_handle *counter_handle,
	size_t workers,
	uint64_t *tx,
	uint64_t *rx,
	uint64_t *tx_bytes,
	uint64_t *rx_bytes
);
