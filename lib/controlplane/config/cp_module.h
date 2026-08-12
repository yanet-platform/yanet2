#pragma once

#include "common/container_of.h"
#include "common/memory.h"

#include "counters/counters.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/defines.h"
#include "controlplane/config/registry.h"

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

/*
 * Callback used to free module configuration data.
 * Agent creating a module configuration should provide the callback
 * to free replaced module data after configuration update.
 */
typedef void (*cp_module_free_handler)(struct cp_module *cp_module);

struct cp_module_device {
	char name[CP_DEVICE_NAME_LEN];
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

	// Link to the next module parked on the same agent's list, once this
	// module's reference count reaches zero.
	//
	// Only the zero-transition handler sets it, and only a later reclaim
	// for this module's own type reads it. It stays unset until that
	// transition happens. The parked list's tail refers to itself
	// instead of ending at null, so a parked entry's link is never null
	// — which also marks that this module is already parked.
	struct cp_module *parked_next;
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
 * Sets up counters and associates the module with the given agent. Before
 * any of that allocates, this reclaims whatever the agent parked for this
 * type since the last such call, using the destructor supplied here, so a
 * recreation under memory pressure benefits from the space a parked
 * instance would free rather than failing before reaching that reclaim.
 * The destructor is used only for this call and never stored. A different
 * type sharing the same agent is left for its own next call to collect.
 *
 * @param cp_module Pointer to the module configuration to initialize
 * @param agent Pointer to the controlplane agent owning this module
 * @param module_type Type identifier for the module
 * @param module_name Name identifier for the module
 * @param destroy Destructor for a parked module of this same type
 * @param err Error output parameter
 * @return 0 on success, negative error code on failure
 */
int
cp_module_init(
	struct cp_module *cp_module,
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	cp_module_free_handler destroy,
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

// The single handler for a module reference count reaching zero, reached
// the same way from a registry drop or an explicit release.
//
// Parks the module on its agent's list instead of destroying it, because
// this generic layer does not know the module's type, and an agent can
// host more than one type at once — as when acl and fwstate share one.
// The module's own type-specific reclaim destroys it later.
//
// Idempotent: once set, a parked entry's link is never null again, so a
// duplicate transition leaves it in place instead of relinking it. Every
// caller already holds the configuration lock, so this handler must not
// take it itself.
void
cp_module_registry_item_free_cb(struct registry_item *item, void *data);

// Drop the reference construction took on the caller's behalf.
//
// The zero-transition handler runs only when this drop is the last
// reference, so a caller must not assume the call freed anything: a live
// or pinned configuration generation may still hold the module. A module
// parked here is not destroyed on the spot — the next construction call
// for the same type reclaims it, or it is freed along with the agent's
// arena.
//
// Takes the module's own agent's configuration lock itself, unlike the
// registry-driven path to the same handler, which already runs under
// that lock.
void
cp_module_release(struct cp_module *cp_module);

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
