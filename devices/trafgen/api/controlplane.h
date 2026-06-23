#pragma once

#include <stdint.h>

#include "controlplane/config/cp_device.h"

struct agent;
struct cp_device;
struct yanet_error;

struct cp_device_trafgen_config {
	struct cp_device_config cp_device_config;
};

struct cp_device *
cp_device_trafgen_new(
	struct agent *agent,
	const struct cp_device_trafgen_config *config,
	yanet_error **err
);

void
cp_device_trafgen_free(struct cp_device *cp_device);

// Reclaim trafgen devices parked on the agent's unused_device list.
//
// Call after a device update, once the retiring generation no longer
// references the parked devices.
void
cp_device_trafgen_drain_unused(struct agent *agent);

struct cp_device_trafgen_config *
cp_device_trafgen_config_new(
	const char *name,
	uint64_t input_count,
	uint64_t output_count,
	yanet_error **err
);

int
cp_device_trafgen_config_set_input_pipeline(
	struct cp_device_trafgen_config *cp_device_trafgen_config,
	uint64_t index,
	const char *name,
	uint64_t weight
);

int
cp_device_trafgen_config_set_output_pipeline(
	struct cp_device_trafgen_config *cp_device_trafgen_config,
	uint64_t index,
	const char *name,
	uint64_t weight
);

void
cp_device_trafgen_config_free(
	struct cp_device_trafgen_config *cp_device_trafgen_config
);

// Set the target aggregate packet rate in packets per second.
int
cp_device_trafgen_set_rate(struct cp_device *cp_device, uint64_t rate_pps);

// Replace the frame set replayed by the generator.
//
// frames is the concatenation of frame_count raw L2 frames whose individual
// lengths are given by lengths; frames_size must equal the sum of lengths. The
// data is copied into shared memory, so the caller keeps ownership of frames
// and lengths.
int
cp_device_trafgen_set_frames(
	struct cp_device *cp_device,
	const uint8_t *frames,
	uint64_t frames_size,
	const uint32_t *lengths,
	uint32_t frame_count,
	yanet_error **err
);
