#pragma once

#include "common/memory.h"

#include "counters/counters.h"

#include "controlplane/config/defines.h"

#include "controlplane/config/registry.h"

/*
 * Structure cp_module reflects module configuration
 *
 * It is allocated by external agent inside its adress space and
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

	// Rx packet counter
	uint64_t rx_counter_id;
	// Tx packet counter
	uint64_t tx_counter_id;
	// Rx bytes counter
	uint64_t rx_bytes_counter_id;
	// Tx bytes counter
	uint64_t tx_bytes_counter_id;

	// Link to the previous instance of the module configuration
	struct cp_module *prev;
	// Controlplane agent the configuration belongs to
	struct agent *agent;
	// Memory context for additional resources inside the configuration
	struct memory_context memory_context;

	uint64_t device_count;
	struct cp_module_device *devices;
};

int
cp_module_link_device(
	struct cp_module *cp_module, const char *name, uint64_t *index
);

int
cp_module_init(
	struct cp_module *cp_module,
	struct agent *agent,
	const char *module_type,
	const char *module_name
);

struct cp_module_registry {
	struct memory_context *memory_context;
	struct registry registry;
};

int
cp_module_registry_init(
	struct memory_context *memory_context,
	struct cp_module_registry *registry
);

int
cp_module_registry_copy(
	struct memory_context *memory_context,
	struct cp_module_registry *new_module_registry,
	struct cp_module_registry *old_module_registry
);

void
cp_module_registry_destroy(struct cp_module_registry *module_registry);

struct cp_module *
cp_module_registry_get(
	struct cp_module_registry *module_registry, uint64_t index
);

struct cp_module *
cp_module_registry_lookup(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name
);

int
cp_module_registry_upsert(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name,
	struct cp_module *module
);

int
cp_module_registry_delete(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name
);
