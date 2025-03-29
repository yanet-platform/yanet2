#pragma once

#include <stdbool.h>
#include <stdint.h>

#include <sys/types.h>

#include "common/memory.h"

struct dp_config;

/*
 * Structure module_data reflects module configuration
 *
 * It is allocated by external agent inside its adress space and
 * then linked into pipeline control chain.
 */
struct module_data;

/*
 * Callback used to free module configuration data.
 * Agent creating a module configuration should provide the callback
 * to free replaced module data after configuration update.
 */
typedef void (*module_data_free_handler)(struct module_data *module_data);

struct module_data {
	// Reference to dataplane module
	uint64_t index;
	// Controlplane generation when this object was created
	uint64_t gen;
	// Link to the previous instance of the module configuration
	struct module_data *prev;
	// Controlplane agent the configuration belongs to
	struct agent *agent;
	/*
	 * The fuunction valid only in execution context of owning agent.
	 * If owning agent is `dead` the the data should be freed
	 * during agent destroy.
	 */
	module_data_free_handler free_handler;
	/*
	 * All module datas are accessible through registry so name
	 * should live somewhere there.
	 */
	char name[80];
	// Memory context for additional resources inside the configuration
	struct memory_context memory_context;
};

struct cp_module {
	struct module_data *data;
};

/*
 * Module configuration registry is used to track all configurations
 * uploaded into controlplane. This structure is linked into configuration
 * generation where module index inside a pipeline denotes the position of
 * corresponding module_data in the module registry.
 */
struct cp_module_registry {
	uint64_t count;
	struct cp_module modules[];
};

/*
 * Pipeline descriptor contains length of a pipeline (count in modules)
 * and indexes of modules to be processed inside module registry.
 */
struct cp_pipeline {
	char name[80];
	uint64_t length;
	uint64_t refcnt;
	uint64_t module_indexes[];
};

/*
 * Pipeline registry contains all existing pipelines.
 * After reading a packet a dataplane worker evaluates index of a
 * pipeline assigned to process the packet and fetchs pipeline descriptor
 * from the pipeline registry insdide active configuration generation.
 */
struct cp_pipeline_registry {
	uint64_t count;
	struct cp_pipeline *pipelines[];
};

struct cp_device_pipeline_map {
	uint64_t size;
	uint64_t pipelines[];
};

/*
 * TODO: we have to load pipelines configuration and device binding
 * atomically as well as allow to configure the device binding so
 * the structure bellow should be reimplemented.
 */
struct cp_device_registry {
	uint64_t count;
	struct cp_device_pipeline_map *device_map[];
};

struct cp_config_gen;

/*
 * Configuration generation denotes a snapshot of controlplane
 * packet processing configuration. It contains module and pipeline
 * registries also with pipeline to device binding.
 *
 * On each update a new copy of the current active configuration generation
 * is instantiated and modified. After all updates are done the new generation
 * replaces an old one. However the previous could be still in use by
 * dataplane workers so the updater should wait until new generation reaches
 * all workers before resorce freeing.
 */
struct cp_config_gen {
	uint64_t gen;

	struct cp_pipeline_registry *pipeline_registry;
	struct cp_module_registry *module_registry;
	struct cp_device_registry *device_registry;

	struct cp_config_gen *prev;
};

struct agent;
/*
 * The structure contains agents attached to controplane configuration
 * zone.
 */
struct cp_agent_registry;
struct cp_agent_registry {
	uint64_t count;
	struct cp_agent_registry *prev;
	struct agent *agents[];
};

/*
 * Controplane configuration memory zone entry point.
 * This structure is placed just after controplane start address
 * and used for any controplane configuration manipulations.
 */
struct cp_config {
	/*
	 * The allocator owns whole controplane memory zone except
	 * this structure itself.
	 */
	struct block_allocator block_allocator;
	/*
	 * Controlplane memory context used to provide access to the
	 * allocator and account memory operations.
	 */
	struct memory_context memory_context;
	/*
	 * Relative porinter to the corresponding dataplane memory zone
	 * structure.
	 */
	struct dp_config *dp_config;

	/*
	 * Identifier of a process changinf the controplane configuration.
	 */
	pid_t config_lock;

	/*
	 * Relative pointer to the current active packet processing
	 * configuration.
	 */
	struct cp_config_gen *cp_config_gen;

	/*
	 * Registry of agent attached to the controplane configuration
	 * memory zone.
	 */
	struct cp_agent_registry *agent_registry;
};

/*
 * Try to lock controlplane configuration.
 * The function does not support recursive locking.
 */
bool
cp_config_try_lock(struct cp_config *cp_config);

/*
 * Wait until controplane is locked by the current process.
 * The function does not support recursive locking.
 */
void
cp_config_lock(struct cp_config *cp_config);

/*
 * Unlock controplane configuration.
 * The function returns false in case when controplane was not locked
 * by the current process.
 */
bool
cp_config_unlock(struct cp_config *cp_config);

/*
 * The routine updates one or more module confings linking them into
 * existing pipelines. Already existing modules are updated preserving its
 * index while new modules are to be appended to the tail of module list.
 * This means that pipilenes are not mutating here except address recoding to
 * the new configuration generation container.
 */
int
cp_config_update_modules(
	struct agent *agent,
	uint64_t module_count,
	struct module_data **module_datas
);

struct module_config {
	char type[80];
	char name[80];
};

struct pipeline_config {
	char name[80];
	uint64_t length;
	struct module_config modules[0];
};

int
cp_config_update_pipelines(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t pipeline_count,
	struct pipeline_config **pipelines
);

struct pipeline_weight {
	char name[80];
	uint64_t weight;
};

struct device_pipeline_map {
	uint64_t device_id;
	uint64_t count;
	struct pipeline_weight pipelines[];
};

int
cp_config_update_devices(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t device_count,
	struct device_pipeline_map *pipelines[]
);
