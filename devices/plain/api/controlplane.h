#pragma once

#include <stdint.h>

#include "controlplane/config/cp_device.h"

struct agent;
struct cp_device;

struct cp_device_plain_config {
	struct cp_device_config cp_device_config;
};

struct cp_device *
cp_device_plain_create(
	struct agent *agent, const struct cp_device_plain_config *config
);

void
cp_device_plain_free(struct cp_device *cp_device);

struct cp_device_plain_config *
cp_device_plain_config_create(
	const char *name, uint64_t input_count, uint64_t poutput_count
);

int
cp_device_plain_config_set_input_pipeline(
	struct cp_device_plain_config *cp_device_plain_config,
	uint64_t index,
	const char *name,
	uint64_t weight
);

int
cp_device_plain_config_set_output_pipeline(
	struct cp_device_plain_config *cp_device_plain_config,
	uint64_t index,
	const char *name,
	uint64_t weight
);

void
cp_device_plain_config_free(
	struct cp_device_plain_config *cp_device_plain_config
);
