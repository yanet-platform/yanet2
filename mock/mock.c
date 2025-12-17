#include "mock.h"
#include <assert.h>
#include <dlfcn.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <threads.h>
#include <time.h>

#include "common/exp_array.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "dataplane/config/zone.h"
#include "lib/controlplane/config/zone.h"
#include "worker.h"
#include "worker_mempool.h"

////////////////////////////////////////////////////////////////////////////////

static int
dataplane_load_module(
	struct dp_config *dp_config, void *handle, const char *name
) { // duplicates real dataplane method
	char loader_name[64];
	snprintf(loader_name, sizeof(loader_name), "%s%s", "new_module_", name);
	module_load_handler loader =
		(module_load_handler)dlsym(handle, loader_name);
	if (loader == NULL) {
		return -1;
	}
	struct module *module = loader();

	struct dp_module *dp_modules = ADDR_OF(&dp_config->dp_modules);
	if (mem_array_expand_exp(
		    &dp_config->memory_context,
		    (void **)&dp_modules,
		    sizeof(*dp_modules),
		    &dp_config->module_count
	    )) {
		// FIXME: free module
		return -1;
	}

	struct dp_module *dp_module = dp_modules + dp_config->module_count - 1;

	strtcpy(dp_module->name, module->name, sizeof(dp_module->name));
	dp_module->handler = module->handler;

	SET_OFFSET_OF(&dp_config->dp_modules, dp_modules);

	free(module);

	return 0;
}

static int
dataplane_load_device(
	struct dp_config *dp_config, void *bin_hndl, const char *name
) { // duplicates real dataplane method
	char loader_name[64];
	snprintf(loader_name, sizeof(loader_name), "%s%s", "new_device_", name);
	device_load_handler loader =
		(device_load_handler)dlsym(bin_hndl, loader_name);
	if (loader == NULL) {
		return -1;
	}
	struct device *device = loader();

	struct dp_device *dp_devices = ADDR_OF(&dp_config->dp_devices);
	if (mem_array_expand_exp(
		    &dp_config->memory_context,
		    (void **)&dp_devices,
		    sizeof(*dp_devices),
		    &dp_config->device_count
	    )) {
		// FIXME: free device
		return -1;
	}

	struct dp_device *dp_device = dp_devices + dp_config->device_count - 1;

	strtcpy(dp_device->name, device->name, sizeof(dp_device->name));
	dp_device->input_handler = device->input_handler;
	dp_device->output_handler = device->output_handler;

	SET_OFFSET_OF(&dp_config->dp_devices, dp_devices);

	free(device);

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

static int
dataplane_initialize(
	void *storage,
	size_t cp_memory,
	size_t dp_memory,
	size_t workers_count,
	struct yanet_worker_mock *workers,
	struct cp_config **res_cp_config,
	struct dp_config **res_dp_config
) {
	struct dp_config *dp_config = (struct dp_config *)storage;
	memset(dp_config, 0, sizeof(*dp_config));

	dp_config->numa_idx = 0;
	dp_config->instance_count = 1;
	dp_config->instance_idx = 0;
	dp_config->storage_size = dp_memory + cp_memory;
	dp_config->worker_count = workers_count;

	block_allocator_init(&dp_config->block_allocator);
	block_allocator_put_arena(
		&dp_config->block_allocator,
		storage + sizeof(struct dp_config),
		dp_memory - sizeof(struct dp_config)
	);
	memory_context_init(
		&dp_config->memory_context, "dp", &dp_config->block_allocator
	);

	dp_config->config_lock = 0;

	dp_config->dp_modules = NULL;
	dp_config->module_count = 0;

	struct cp_config *cp_config =
		(struct cp_config *)((uintptr_t)storage + dp_memory);
	memset(cp_config, 0, sizeof(*cp_config));

	block_allocator_init(&cp_config->block_allocator);
	block_allocator_put_arena(
		&cp_config->block_allocator,
		storage + dp_memory + sizeof(struct cp_config),
		cp_memory - sizeof(struct cp_config)
	);
	memory_context_init(
		&cp_config->memory_context, "cp", &cp_config->block_allocator
	);

	struct cp_agent_registry *cp_agent_registry =
		(struct cp_agent_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_agent_registry)
		);
	cp_agent_registry->count = 0;
	SET_OFFSET_OF(&cp_config->agent_registry, cp_agent_registry);

	SET_OFFSET_OF(&dp_config->cp_config, cp_config);
	SET_OFFSET_OF(&cp_config->dp_config, dp_config);

	void *bin_hndl = dlopen(NULL, RTLD_NOW | RTLD_GLOBAL);

	int rc = dataplane_load_module(dp_config, bin_hndl, "forward");
	if (rc == -1) {
		// FIXME: Define a common error base and enum with errors for
		// modules and other parts
		return -2;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "route");
	if (rc == -1) {
		return -3;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "decap");
	if (rc == -1) {
		return -4;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "dscp");
	if (rc == -1) {
		return -5;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "nat64");
	if (rc == -1) {
		return -6;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "balancer");
	if (rc == -1) {
		return -7;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "pdump");
	if (rc == -1) {
		return -8;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "acl");
	if (rc == -1) {
		return -9;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "fwstate");
	if (rc == -1) {
		return -10;
	}

	rc = dataplane_load_device(dp_config, bin_hndl, "plain");
	if (rc == -1) {
		return -11;
	}

	rc = dataplane_load_device(dp_config, bin_hndl, "vlan");
	if (rc == -1) {
		return -12;
	}

	cp_config->cp_config_gen = NULL;
	struct agent agent;
	memory_context_init_from(
		&agent.memory_context, &cp_config->memory_context, "stub agent"
	);
	SET_OFFSET_OF(&agent.dp_config, dp_config);
	SET_OFFSET_OF(&agent.cp_config, cp_config);
	struct cp_config_gen *cp_config_gen = cp_config_gen_create(&agent);
	cp_config_gen->config_gen_ectx = NULL;
	SET_OFFSET_OF(&cp_config->cp_config_gen, cp_config_gen);

	struct dp_worker **workers_array = memory_balloc(
		&dp_config->memory_context,
		workers_count * sizeof(struct dp_worker *)
	);
	if (workers_array == NULL) {
		return -1;
	}

	// set dp_workers
	dp_config->worker_count = workers_count;
	SET_OFFSET_OF(&dp_config->workers, workers_array);

	struct dp_worker **dp_workers = ADDR_OF(&dp_config->workers);
	for (size_t i = 0; i < workers_count; ++i) {
		struct dp_worker **dp_worker = dp_workers + i;
		SET_OFFSET_OF(dp_worker, &workers[i].dp_worker);
	}

	// init counters

	counter_storage_allocator_init(
		&dp_config->counter_storage_allocator,
		&dp_config->memory_context,
		dp_config->worker_count
	);

	counter_storage_allocator_init(
		&cp_config->counter_storage_allocator,
		&cp_config->memory_context,
		dp_config->worker_count
	);

	counter_registry_link(&dp_config->worker_counters, NULL);

	*res_dp_config = dp_config;
	*res_cp_config = cp_config;

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
yanet_mock_init(
	struct yanet_mock *mock, struct yanet_mock_config *config, void *arena
) {
	if (arena == NULL) {
		arena = aligned_alloc(
			64, config->cp_memory + config->dp_memory
		);
		if (arena == NULL) {
			return -1;
		}
		mock->arena = arena;
	}
	if ((uintptr_t)arena % 64 != 0) {
		return -1;
	}
	mock->storage = arena;

	struct cp_config *cp_config;
	struct dp_config *dp_config;
	int res = dataplane_initialize(
		arena,
		config->cp_memory,
		config->dp_memory,
		config->worker_count,
		mock->workers,
		&cp_config,
		&dp_config
	);
	if (res != 0) {
		yanet_mock_free(mock);
		return res;
	}

	struct rte_mempool *mp = mock_mempool_create();

	// init worker mocks
	mock->worker_count = config->worker_count;
	for (size_t i = 0; i < config->worker_count; ++i) {
		memset(&mock->workers[i], 0, sizeof(struct yanet_worker_mock));
		mock->workers[i].cp_config = cp_config;
		mock->workers[i].dp_config = dp_config;
		mock->workers[i].dp_worker.gen = 1000000000000000;
		mock->workers[i].dp_worker.rx_mempool = mp;
	}

	dp_config->dp_topology.device_count = config->device_count;
	struct dp_port *devices = memory_balloc(
		&dp_config->memory_context,
		sizeof(struct dp_port) * config->device_count
	);
	if (devices == NULL) {
		return -1;
	}
	for (size_t i = 0; i < config->device_count; ++i) {
		struct dp_port *device = &devices[i];
		memset(device, 0, sizeof(struct dp_port));
		strcpy(device->port_name, config->devices[i].name);
		device->port_id = config->devices[i].id;
	}
	SET_OFFSET_OF(&dp_config->dp_topology.devices, devices);

	mock->cp_config = cp_config;
	mock->dp_config = dp_config;

	memset(&mock->current_time, 0, sizeof(struct timespec));

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

void
yanet_mock_free(struct yanet_mock *mock) {
	if (mock->worker_count > 0) {
		// All workers share the same mempool
		struct rte_mempool *mp = mock->workers[0].dp_worker.rx_mempool;
		if (mp != NULL) {
			free(mp);
		}
	}

	if (mock->arena != NULL) {
		free(mock->arena);
	}
}

////////////////////////////////////////////////////////////////////////////////

struct yanet_shm *
yanet_mock_shm(struct yanet_mock *mock) {
	return (struct yanet_shm *)mock->storage;
}

////////////////////////////////////////////////////////////////////////////////

extern void
set_current_time(struct timespec *ts);

////////////////////////////////////////////////////////////////////////////////

void
yanet_mock_set_current_time(struct yanet_mock *mock, struct timespec *ts) {
	mock->current_time = *ts;
}

struct timespec
yanet_mock_current_time(struct yanet_mock *mock) {
	return mock->current_time;
}

////////////////////////////////////////////////////////////////////////////////

struct packet_handle_result
yanet_mock_handle_packets(
	struct yanet_mock *mock, struct packet_list *packets, size_t worker_idx
) {
	struct yanet_worker_mock *worker = &mock->workers[worker_idx];

	// Set global time to the current mock time.
	set_current_time(&mock->current_time);

	// Handle packets.
	struct packet_handle_result result =
		yanet_worker_mock_handle_packets(worker, packets);

	return result;
}
