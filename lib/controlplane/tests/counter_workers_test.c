/*
 * Tests for per-worker counter reads under divergent registries.
 *
 * Every worker's config_gen_ectx owns its own counter storage registry,
 * and those registries may hold different storages. The per-worker read
 * returns each worker's matched set independently; the union across
 * workers lives in the Go bindings. The worker-counter read follows the
 * same shape: each call snapshots one worker's own storage of the
 * shared worker-counter registry, selected by the worker index.
 *
 * Divergence is produced by writing distinct values into the two workers'
 * pipeline storages and by registering an extra tagged storage in only
 * one worker's registry.
 */

#include "api/agent.h"
#include "api/counter.h"

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_pipeline.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CW_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)

#define CW_WORKER_COUNT 2
#define CW_W0_INPUT 0x10
#define CW_W1_INPUT 0x20
#define CW_W1_EXTRA_0 0xAA
#define CW_W1_EXTRA_1 0xBB
#define CW_W0_WORKER 0x30
#define CW_W1_WORKER 0x40

static int
install_empty_pipeline(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name
) {
	yanet_error *err = NULL;
	struct cp_pipeline_config *cfg =
		calloc(1, sizeof(struct cp_pipeline_config));
	TEST_ASSERT_NOT_NULL(cfg, "failed to allocate pipeline config");
	strncpy(cfg->name, name, CP_PIPELINE_NAME_LEN - 1);
	cfg->length = 0;
	struct cp_pipeline_config *cfgs[] = {cfg};
	int rc =
		cp_config_update_pipelines(dp_config, cp_config, 1, cfgs, &err);
	free(cfg);
	TEST_ASSERT_SUCCESS(
		rc,
		"update_pipelines failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	return TEST_SUCCESS;
}

static int
install_device(
	struct agent *agent,
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name,
	const char *input_pipeline
) {
	yanet_error *err = NULL;
	struct cp_device_plain_config *cfg =
		cp_device_plain_config_new(name, 1, 0, &err);
	TEST_ASSERT_NOT_NULL(
		cfg,
		"device config new failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_device_plain_config_set_input_pipeline(cfg, 0, input_pipeline, 1);
	struct cp_device *dev = cp_device_plain_new(agent, cfg, &err);
	cp_device_plain_config_free(cfg);
	TEST_ASSERT_NOT_NULL(
		dev,
		"device new failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *devs[] = {dev};
	int rc = cp_config_update_devices(dp_config, cp_config, 1, devs, &err);
	TEST_ASSERT_SUCCESS(
		rc,
		"update_devices failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	return TEST_SUCCESS;
}

// Index of a counter in a storage's registry, or COUNTER_INVALID when the
// registry does not carry the name.
static uint64_t
counter_index_by_name(struct counter_storage *storage, const char *name) {
	struct counter_registry *registry = ADDR_OF(&storage->registry);
	struct counter *counters = ADDR_OF(&registry->names);
	for (uint64_t idx = 0; idx < registry->count; ++idx) {
		if (strcmp(counters[idx].name, name) == 0) {
			return idx;
		}
	}
	return COUNTER_INVALID;
}

// Write value into the first slot of a counter named name in a worker's
// pipeline storage, so the two workers report different snapshots.
static int
write_pipeline_counter(
	struct config_gen_ectx *ectx, const char *counter_name, uint64_t value
) {
	struct cp_config_counter_storage_registry *registry =
		ADDR_OF(&ectx->counter_storage_registry);
	struct counter_storage *storage =
		cp_config_counter_storage_registry_lookup_pipeline(
			registry, "dev0", "pipe0"
		);
	TEST_ASSERT_NOT_NULL(
		storage, "no pipeline counter storage in the worker registry"
	);

	uint64_t idx = counter_index_by_name(storage, counter_name);
	TEST_ASSERT(
		idx != COUNTER_INVALID,
		"counter '%s' not found in the pipeline registry",
		counter_name
	);

	uint64_t *values =
		counter_handle_get_value(counter_get_value_handle(idx, storage)
		);
	values[0] = value;
	return TEST_SUCCESS;
}

// Write value into the first slot of a counter named name in a worker's
// own storage of the shared worker-counter registry, so the two workers
// report different snapshots through the worker-counter read.
static int
write_worker_counter(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	const char *counter_name,
	uint64_t value
) {
	struct counter_storage **storages =
		ADDR_OF(&dp_config->worker_counter_storages);
	struct counter_storage *storage = ADDR_OF(storages + worker_idx);

	uint64_t idx = counter_index_by_name(storage, counter_name);
	TEST_ASSERT(
		idx != COUNTER_INVALID,
		"counter '%s' not found in the worker counter registry",
		counter_name
	);

	uint64_t *values =
		counter_handle_get_value(counter_get_value_handle(idx, storage)
		);
	values[0] = value;
	return TEST_SUCCESS;
}

// Register a tagged storage carrying one counter in a single worker's
// registry, with marker values in both of the counter's slots.
static int
insert_worker1_extra_storage(struct cp_config *cp_config) {
	yanet_error *err = NULL;
	struct memory_context *memory_context =
		&cp_config->counter_storage_memory_context;

	struct counter_registry *registry = (struct counter_registry *)
		memory_balloc(memory_context, sizeof(*registry));
	TEST_ASSERT_NOT_NULL(
		registry, "failed to allocate the extra counter registry"
	);
	memset(registry, 0, sizeof(*registry));
	TEST_ASSERT_SUCCESS(
		counter_registry_init(registry, memory_context, 0),
		"failed to init the extra counter registry"
	);

	uint64_t counter_id =
		counter_registry_register(registry, "w1_only", 2, &err);
	TEST_ASSERT(
		counter_id != COUNTER_INVALID,
		"failed to register the extra counter: %s",
		err ? yanet_error_message(err) : "?"
	);

	// Assign the counter's pool offset, the same way every config upsert
	// links a registry before a storage is spawned from it.
	TEST_ASSERT_SUCCESS(
		counter_registry_link(registry, NULL, &err),
		"failed to link the extra counter registry: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct counter_storage *storage =
		counter_storage_spawn(memory_context, NULL, registry);
	TEST_ASSERT_NOT_NULL(storage, "failed to spawn the extra storage");

	uint64_t *values = counter_handle_get_value(
		counter_get_value_handle(counter_id, storage)
	);
	values[0] = CW_W1_EXTRA_0;
	values[1] = CW_W1_EXTRA_1;

	struct counter_tag tags[] = {
		{.key = "object_type", .value = "extra"},
		{.key = "object_name", .value = "w1"},
		{.key = "kind", .value = "object"},
	};

	cp_config_lock(cp_config);
	struct cp_config_gen *config_gen = cp_config_gen_acquire(cp_config);
	cp_config_unlock(cp_config);

	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(config_gen, 1);
	TEST_ASSERT_NOT_NULL(ectx, "worker 1 has no execution context");

	TEST_ASSERT_SUCCESS(
		cp_config_counter_storage_registry_insert(
			ADDR_OF(&ectx->counter_storage_registry),
			tags,
			3,
			storage,
			&err
		),
		"failed to insert the extra storage: %s",
		err ? yanet_error_message(err) : "?"
	);

	cp_config_lock(cp_config);
	cp_config_gen_release(cp_config, config_gen);
	cp_config_unlock(cp_config);
	return TEST_SUCCESS;
}

// Diverge the workers' registries: distinct pipeline values everywhere
// plus an extra storage only worker 1 knows about.
static int
make_workers_diverge(
	struct agent *agent,
	struct dp_config *dp_config,
	struct cp_config *cp_config
) {
	TEST_ASSERT_SUCCESS(
		install_empty_pipeline(dp_config, cp_config, "pipe0"),
		"failed to install pipe0"
	);
	TEST_ASSERT_SUCCESS(
		install_device(agent, dp_config, cp_config, "dev0", "pipe0"),
		"failed to install dev0 with input pipeline pipe0"
	);

	cp_config_lock(cp_config);
	struct cp_config_gen *config_gen = cp_config_gen_acquire(cp_config);
	cp_config_unlock(cp_config);

	for (uint64_t w_idx = 0; w_idx < CW_WORKER_COUNT; ++w_idx) {
		struct config_gen_ectx *ectx =
			cp_config_gen_worker_ectx(config_gen, w_idx);
		TEST_ASSERT_NOT_NULL(
			ectx,
			"worker %lu has no execution context",
			(unsigned long)w_idx
		);
		uint64_t value = w_idx == 0 ? CW_W0_INPUT : CW_W1_INPUT;
		TEST_ASSERT_SUCCESS(
			write_pipeline_counter(ectx, "input", value),
			"failed to write worker %lu pipeline counter",
			(unsigned long)w_idx
		);
	}

	cp_config_lock(cp_config);
	cp_config_gen_release(cp_config, config_gen);
	cp_config_unlock(cp_config);

	TEST_ASSERT_SUCCESS(
		insert_worker1_extra_storage(cp_config),
		"failed to register the worker-1-only storage"
	);
	return TEST_SUCCESS;
}

// Verifies that the per-worker read returns each worker's matched set
// independently: the worker-1-only storage shows up with its own values
// in worker 1's set and not at all in worker 0's.
static int
test_per_worker_sets(struct dp_config *dp_config) {
	struct counter_tag tags[] = {
		{.key = "object_type", .value = "extra"},
		{.key = "object_name", .value = "w1"},
		{.key = "kind", .value = "object"},
	};

	struct counter_worker_set_list *sets =
		yanet_get_counters_by_tags_per_worker(
			dp_config, tags, 3, NULL, NULL
		);
	TEST_ASSERT_NOT_NULL(
		sets, "yanet_get_counters_by_tags_per_worker returned NULL"
	);
	TEST_ASSERT_EQUAL(
		sets->worker_count, CW_WORKER_COUNT, "unexpected worker count"
	);

	struct counter_worker_set *set0 = yanet_get_counter_worker_set(sets, 0);
	struct counter_worker_set *set1 = yanet_get_counter_worker_set(sets, 1);
	TEST_ASSERT_NOT_NULL(set0, "worker 0 set is missing");
	TEST_ASSERT_NOT_NULL(set1, "worker 1 set is missing");
	TEST_ASSERT_EQUAL(0, set0->groups->group_count, "worker 0 matched");
	TEST_ASSERT_EQUAL(
		1, set1->groups->group_count, "worker 1 matched group count"
	);

	struct counter_group *group = yanet_get_counter_group(set1->groups, 0);
	TEST_ASSERT_NOT_NULL(group, "worker 1 set has no group");
	TEST_ASSERT_EQUAL(1, group->count, "worker 1 match count");

	// The whole storage folds into one block stating its tags once:
	// every registered tag is there, in the group and not per handle.
	TEST_ASSERT_EQUAL(3, group->tag_count, "worker 1 group tag count");
	int seen_object_name = 0;
	for (size_t i = 0; i < group->tag_count; ++i) {
		if (strcmp(group->tags[i].key, "object_name") == 0) {
			TEST_ASSERT(
				strcmp(group->tags[i].value, "w1") == 0,
				"object_name tag value is '%s', expected 'w1'",
				group->tags[i].value
			);
			seen_object_name = 1;
		}
	}
	TEST_ASSERT(seen_object_name, "object_name tag missing from group");

	struct counter_handle *handle = yanet_get_group_counter(group, 0);
	TEST_ASSERT_NOT_NULL(handle, "worker 1 group has no handle");
	TEST_ASSERT(
		strcmp(handle->name, "w1_only") == 0,
		"counter name is '%s', expected 'w1_only'",
		handle->name
	);
	TEST_ASSERT_EQUAL(2, handle->size, "counter size");
	TEST_ASSERT_EQUAL(
		CW_W1_EXTRA_0,
		yanet_get_counter_value(handle->values, 0),
		"worker 1 extra counter slot 0"
	);
	TEST_ASSERT_EQUAL(
		CW_W1_EXTRA_1,
		yanet_get_counter_value(handle->values, 1),
		"worker 1 extra counter slot 1"
	);

	TEST_ASSERT_NULL(
		yanet_get_counter_worker_set(sets, CW_WORKER_COUNT),
		"out-of-range worker set must return NULL"
	);

	yanet_counter_worker_set_list_free(sets);
	return TEST_SUCCESS;
}

// Verifies that a counter present in every worker's registry keeps each
// worker's own value in that worker's set: one read over both workers
// returns distinct snapshots for the shared counter.
static int
test_per_worker_shared_counter(struct dp_config *dp_config) {
	struct counter_tag tags[] = {
		{.key = "device", .value = "dev0"},
		{.key = "kind", .value = "pipeline"},
	};

	struct counter_worker_set_list *sets =
		yanet_get_counters_by_tags_per_worker(
			dp_config, tags, 2, NULL, NULL
		);
	TEST_ASSERT_NOT_NULL(
		sets, "yanet_get_counters_by_tags_per_worker returned NULL"
	);

	const uint64_t expected[CW_WORKER_COUNT] = {CW_W0_INPUT, CW_W1_INPUT};
	for (uint64_t w_idx = 0; w_idx < CW_WORKER_COUNT; ++w_idx) {
		struct counter_worker_set *set =
			yanet_get_counter_worker_set(sets, w_idx);
		TEST_ASSERT_NOT_NULL(set, "worker set is missing");

		// The pipeline storage carries several counters, and they
		// all fold into one block stating the storage's tags once.
		TEST_ASSERT_EQUAL(
			1,
			set->groups->group_count,
			"worker %lu pipeline storage group count",
			(unsigned long)w_idx
		);
		struct counter_group *only_group =
			yanet_get_counter_group(set->groups, 0);
		TEST_ASSERT_NOT_NULL(
			only_group,
			"worker %lu has no group",
			(unsigned long)w_idx
		);
		TEST_ASSERT(
			only_group->count > 1,
			"the pipeline storage must fold several counters "
			"into one group"
		);

		struct counter_handle *handle = NULL;
		for (uint64_t g_idx = 0;
		     g_idx < set->groups->group_count && handle == NULL;
		     ++g_idx) {
			struct counter_group *group =
				yanet_get_counter_group(set->groups, g_idx);
			if (group == NULL) {
				break;
			}
			for (uint64_t idx = 0; idx < group->count; ++idx) {
				struct counter_handle *cur =
					yanet_get_group_counter(group, idx);
				if (cur != NULL &&
				    strcmp(cur->name, "input") == 0) {
					handle = cur;
					break;
				}
			}
		}
		TEST_ASSERT_NOT_NULL(
			handle,
			"worker %lu set lacks the input counter",
			(unsigned long)w_idx
		);
		TEST_ASSERT_EQUAL(
			expected[w_idx],
			yanet_get_counter_value(handle->values, 0),
			"worker %lu input value",
			(unsigned long)w_idx
		);
	}

	yanet_counter_worker_set_list_free(sets);
	return TEST_SUCCESS;
}

// Verifies that the worker-counter read snapshots only the named
// worker's storage: each worker reports its own written value, every
// worker carries the shared registry's full counter set, and an
// out-of-range worker index is refused.
static int
test_worker_counters_per_worker(struct dp_config *dp_config) {
	const uint64_t expected[CW_WORKER_COUNT] = {CW_W0_WORKER, CW_W1_WORKER};
	for (uint64_t w_idx = 0; w_idx < CW_WORKER_COUNT; ++w_idx) {
		TEST_ASSERT_SUCCESS(
			write_worker_counter(
				dp_config, w_idx, "iterations", expected[w_idx]
			),
			"failed to write worker %lu iterations counter",
			(unsigned long)w_idx
		);
	}

	uint64_t registry_count = 0;
	for (uint64_t w_idx = 0; w_idx < CW_WORKER_COUNT; ++w_idx) {
		struct counter_handle_list *list =
			yanet_get_worker_counters(dp_config, w_idx);
		TEST_ASSERT_NOT_NULL(
			list,
			"worker %lu counter read returned NULL",
			(unsigned long)w_idx
		);
		if (w_idx == 0) {
			registry_count = list->count;
			TEST_ASSERT(
				registry_count > 0,
				"worker counter registry is empty"
			);
		} else {
			TEST_ASSERT_EQUAL(
				registry_count,
				list->count,
				"worker %lu counter count differs from worker "
				"0",
				(unsigned long)w_idx
			);
		}

		struct counter_handle *handle = NULL;
		for (size_t idx = 0; idx < list->count; ++idx) {
			struct counter_handle *cur =
				yanet_get_counter(list, idx);
			if (cur != NULL &&
			    strcmp(cur->name, "iterations") == 0) {
				handle = cur;
				break;
			}
		}
		TEST_ASSERT_NOT_NULL(
			handle,
			"worker %lu read lacks the iterations counter",
			(unsigned long)w_idx
		);
		TEST_ASSERT_EQUAL(
			expected[w_idx],
			yanet_get_counter_value(handle->values, 0),
			"worker %lu iterations value",
			(unsigned long)w_idx
		);
		yanet_counter_handle_list_free(list);
	}

	TEST_ASSERT_NULL(
		yanet_get_worker_counters(dp_config, CW_WORKER_COUNT),
		"out-of-range worker counter read must return NULL"
	);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = CW_WORKER_COUNT,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		fprintf(stderr, "dataplane_ut_shm returned NULL\n");
		dataplane_ut_free(ut);
		return 1;
	}

	yanet_error *err = NULL;
	struct agent *agent =
		agent_attach(shm, 0, "cnt-workers", CW_TEST_MEMORY_LIMIT, &err);
	if (agent == NULL) {
		fprintf(stderr, "agent_attach failed\n");
		dataplane_ut_free(ut);
		return 1;
	}

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	int res = make_workers_diverge(agent, dp_config, cp_config);
	if (res == TEST_SUCCESS) {
		res = test_per_worker_sets(dp_config);
	}
	if (res == TEST_SUCCESS) {
		res = test_per_worker_shared_counter(dp_config);
	}
	if (res == TEST_SUCCESS) {
		res = test_worker_counters_per_worker(dp_config);
	}

	agent_detach(agent);
	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
