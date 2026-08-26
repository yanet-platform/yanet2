/*
 * Regression tests for GH#885: counter_handle snapshots must stay valid
 * across controlplane generation swaps that free or unshare the storage a
 * handle was acquired against.
 *
 * These port two confirmed use-after-free repros against the copy-on-read
 * fix: a value-page surface (the agent counters API snapshots
 * handle->values under cp_config_lock) and a tag-string surface (tags are
 * copied into fixed-size fields instead of borrowed from the freed
 * generation arena).
 */

#include "api/agent.h"
#include "api/counter.h"

#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_pipeline.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CNT_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)

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

// Install "dev0", optionally wiring input_pipeline as its single input
// pipeline.
//
// Passing NULL for input_pipeline drops the reference, which is how the
// value-snapshot test removes the pipeline's counter storage.
static int
install_device(
	struct agent *agent,
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name,
	const char *input_pipeline
) {
	yanet_error *err = NULL;
	uint64_t input_count = (input_pipeline != NULL) ? 1 : 0;
	struct cp_device_plain_config *cfg =
		cp_device_plain_config_new(name, input_count, 0, &err);
	TEST_ASSERT_NOT_NULL(
		cfg,
		"device config new failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	if (input_pipeline != NULL) {
		cp_device_plain_config_set_input_pipeline(
			cfg, 0, input_pipeline, 1
		);
	}
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
	// The live generation references the device from here on, so an
	// owner free attempt is answered with EAGAIN and the handle would be
	// retried once the generations drain. The arena is reclaimed wholesale
	// at teardown.
	yanet_error *free_err = NULL;
	TEST_ASSERT(
		cp_device_plain_free(dev, &free_err) == -1 && errno == EAGAIN,
		"freeing a generation-referenced device must fail with EAGAIN"
	);
	yanet_error_free(free_err);
	return TEST_SUCCESS;
}

// Verifies that a counter_handle's value snapshot stays readable and
// unchanged after an update removes the entity the handle was acquired
// against, both through the single-value read and the batched read.
static int
test_value_snapshot_survives_removal(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent =
		agent_attach(shm, 0, "cnt-885v", CNT_TEST_MEMORY_LIMIT, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	TEST_ASSERT_SUCCESS(
		install_empty_pipeline(dp_config, cp_config, "pipe0"),
		"failed to install pipe0"
	);
	TEST_ASSERT_SUCCESS(
		install_device(agent, dp_config, cp_config, "dev0", "pipe0"),
		"failed to install dev0 with input pipeline pipe0"
	);

	struct counter_handle_list *list =
		yanet_get_pipeline_counters(dp_config, "dev0", "pipe0");
	TEST_ASSERT_NOT_NULL(list, "yanet_get_pipeline_counters returned NULL");
	TEST_ASSERT(
		list->count > 0,
		"no pipeline counter storage matched dev0/pipe0"
	);

	struct counter_handle *handle = yanet_get_counter(list, 0);
	uint64_t before = yanet_get_counter_value(handle->values, 0, 0);

	// Re-install dev0 with no input pipeline: pipe0's counter storage is
	// not respawned, so its value block's refcount drops to zero and the
	// old generation's value pages are freed.
	TEST_ASSERT_SUCCESS(
		install_device(agent, dp_config, cp_config, "dev0", NULL),
		"failed to remove dev0's input pipeline"
	);

	uint64_t after = yanet_get_counter_value(handle->values, 0, 0);
	TEST_ASSERT_EQUAL(
		after, before, "snapshot value changed after entity removal"
	);

	uint64_t *batched =
		calloc(list->instance_count * handle->size, sizeof(uint64_t));
	TEST_ASSERT_NOT_NULL(batched, "failed to allocate batched read buffer");
	yanet_get_counter_values(
		handle->values, handle->size, list->instance_count, batched
	);
	TEST_ASSERT_EQUAL(
		batched[0],
		before,
		"batched read returned a different snapshot value"
	);
	free(batched);

	yanet_counter_handle_list_free(list);
	agent_detach(agent);
	return TEST_SUCCESS;
}

// Verifies that a counter_handle's tag strings stay readable and correct
// after a controlplane generation swap frees the arena they were
// originally borrowed from.
static int
test_tag_strings_survive_generation_swap(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent =
		agent_attach(shm, 0, "cnt-885t", CNT_TEST_MEMORY_LIMIT, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	TEST_ASSERT_SUCCESS(
		install_device(agent, dp_config, cp_config, "dev0", NULL),
		"failed to install dev0"
	);

	struct counter_handle_list *list =
		yanet_get_device_counters(dp_config, "dev0");
	TEST_ASSERT_NOT_NULL(list, "yanet_get_device_counters returned NULL");
	TEST_ASSERT(list->count > 0, "no counter storage matched dev0");

	struct counter_handle *handle = yanet_get_counter(list, 0);
	TEST_ASSERT(handle->tag_count > 0, "handle has no tags to verify");

	// Trigger: any controlplane update installs a new generation and
	// synchronously frees the old one, which pre-fix left handle->tags
	// pointing into freed shm.
	int rc = cp_config_update_modules(dp_config, cp_config, 0, NULL, &err);
	TEST_ASSERT_SUCCESS(
		rc,
		"update_modules trigger failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	int found = 0;
	for (size_t i = 0; i < handle->tag_count; ++i) {
		if (strcmp(handle->tags[i].key, "device") == 0) {
			TEST_ASSERT(
				strcmp(handle->tags[i].value, "dev0") == 0,
				"device tag value mismatch after swap: got %s",
				handle->tags[i].value
			);
			found = 1;
		}
	}
	TEST_ASSERT(found, "device tag missing after generation swap");

	yanet_counter_handle_list_free(list);
	agent_detach(agent);
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
		.worker_count = 1,
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

	int res = test_value_snapshot_survives_removal(shm);
	if (res == TEST_SUCCESS) {
		res = test_tag_strings_survive_generation_swap(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
