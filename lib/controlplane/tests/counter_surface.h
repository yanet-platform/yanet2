#pragma once

// A pipeline-and-device counter surface for a registry lookup to match.

#include "api/agent.h"

#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_pipeline.h"
#include "lib/controlplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include <stdio.h>
#include <stdlib.h>

static int
install_pipelines(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name_prefix,
	size_t count
) {
	yanet_error *err = NULL;
	struct cp_pipeline_config **cfgs = calloc(count, sizeof(*cfgs));
	TEST_ASSERT_NOT_NULL(cfgs, "failed to allocate pipeline config array");

	for (size_t idx = 0; idx < count; ++idx) {
		cfgs[idx] = calloc(1, sizeof(struct cp_pipeline_config));
		TEST_ASSERT_NOT_NULL(
			cfgs[idx], "failed to allocate pipeline config %zu", idx
		);
		snprintf(
			cfgs[idx]->name,
			CP_PIPELINE_NAME_LEN,
			"%s-%zu",
			name_prefix,
			idx
		);
		cfgs[idx]->length = 0;
	}

	int rc = cp_config_update_pipelines(
		dp_config, cp_config, count, cfgs, &err
	);

	for (size_t idx = 0; idx < count; ++idx) {
		free(cfgs[idx]);
	}
	free(cfgs);

	TEST_ASSERT_SUCCESS(
		rc,
		"update_pipelines failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	return TEST_SUCCESS;
}

// Install a device with every prefix-and-index pipeline as equal input.
static int
install_device(
	struct agent *agent,
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *device_name,
	const char *pipeline_prefix,
	size_t pipeline_count
) {
	yanet_error *err = NULL;
	struct cp_device_plain_config *cfg = cp_device_plain_config_new(
		device_name, pipeline_count, 0, &err
	);
	TEST_ASSERT_NOT_NULL(
		cfg,
		"device config new failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	char name[CP_PIPELINE_NAME_LEN];
	for (size_t idx = 0; idx < pipeline_count; ++idx) {
		snprintf(name, sizeof(name), "%s-%zu", pipeline_prefix, idx);
		int rc = cp_device_plain_config_set_input_pipeline(
			cfg, idx, name, 1
		);
		TEST_ASSERT_EQUAL(
			rc, 0, "set_input_pipeline failed at index %zu", idx
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
	cp_device_plain_free(dev);
	return TEST_SUCCESS;
}

// Install a pipeline set and a device wired to it under one name prefix.
static int
install_counter_surface(
	struct agent *agent,
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *device_name,
	const char *pipeline_prefix,
	size_t pipeline_count
) {
	TEST_ASSERT_SUCCESS(
		install_pipelines(
			dp_config, cp_config, pipeline_prefix, pipeline_count
		),
		"failed to install pipelines for %s",
		pipeline_prefix
	);
	TEST_ASSERT_SUCCESS(
		install_device(
			agent,
			dp_config,
			cp_config,
			device_name,
			pipeline_prefix,
			pipeline_count
		),
		"failed to install device %s",
		device_name
	);
	return TEST_SUCCESS;
}
