#include <stdlib.h>

#include "controlplane.h"

#include <string.h>

#include "config.h"

#include "common/container_of.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "lib/errors/errors.h"

#include "controlplane/agent/agent.h"
#include "dataplane/config/zone.h"

struct cp_device *
cp_device_trafgen_new(
	struct agent *agent,
	const struct cp_device_trafgen_config *config,
	yanet_error **err
) {
	struct cp_device_trafgen *cp_device_trafgen =
		(struct cp_device_trafgen *)memory_balloc(
			&agent->memory_context, sizeof(struct cp_device_trafgen)
		);
	if (cp_device_trafgen == NULL) {
		yanet_error_add(err, "memory allocation failed");
		return NULL;
	}

	memset(cp_device_trafgen, 0, sizeof(struct cp_device_trafgen));
	SET_OFFSET_OF(
		&cp_device_trafgen->cp_device.parent_memory_context,
		&agent->memory_context
	);
	cp_device_trafgen->cp_device.alloc_size =
		sizeof(struct cp_device_trafgen);

	if (cp_device_init(
		    &cp_device_trafgen->cp_device,
		    agent,
		    &config->cp_device_config,
		    err
	    )) {
		memory_bfree(
			&agent->memory_context,
			cp_device_trafgen,
			sizeof(struct cp_device_trafgen)
		);
		return NULL;
	}

	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	uint64_t worker_count = dp_config->worker_count;
	if (worker_count > 0) {
		uint64_t states_size =
			sizeof(struct trafgen_worker_state) * worker_count;
		struct trafgen_worker_state *states = memory_balloc(
			&cp_device_trafgen->cp_device.memory_context,
			states_size
		);
		if (states == NULL) {
			yanet_error_add(err, "failed to allocate worker state");
			cp_device_fini(&cp_device_trafgen->cp_device);
			memory_bfree(
				&agent->memory_context,
				cp_device_trafgen,
				sizeof(struct cp_device_trafgen)
			);
			return NULL;
		}
		memset(states, 0, states_size);
		SET_OFFSET_OF(&cp_device_trafgen->worker_states, states);
		cp_device_trafgen->worker_count = worker_count;
	}

	return &cp_device_trafgen->cp_device;
}

void
cp_device_trafgen_free(struct cp_device *cp_device) {
	struct cp_device_trafgen *config =
		container_of(cp_device, struct cp_device_trafgen, cp_device);
	struct memory_context *memory_context = &cp_device->memory_context;

	// Release the subclass-owned buffers before the base teardown, then the
	// base resources, then the wrapper struct.
	struct trafgen_worker_state *states = ADDR_OF(&config->worker_states);
	if (states != NULL) {
		memory_bfree(
			memory_context,
			states,
			sizeof(struct trafgen_worker_state) *
				config->worker_count
		);
	}

	uint8_t *frames = ADDR_OF(&config->frames);
	if (frames != NULL) {
		memory_bfree(memory_context, frames, config->frames_size);
	}

	uint32_t *lengths = ADDR_OF(&config->frame_lengths);
	if (lengths != NULL) {
		memory_bfree(
			memory_context,
			lengths,
			sizeof(uint32_t) * config->frame_count
		);
	}

	uint32_t *offsets = ADDR_OF(&config->frame_offsets);
	if (offsets != NULL) {
		memory_bfree(
			memory_context,
			offsets,
			sizeof(uint32_t) * config->frame_count
		);
	}

	cp_device_fini(cp_device);
	cp_device_free(cp_device);
}

void
cp_device_trafgen_drain_unused(struct agent *agent) {
	cp_device_agent_drain_unused(agent, cp_device_trafgen_free);
}

struct cp_device_trafgen_config *
cp_device_trafgen_config_new(
	const char *name,
	uint64_t input_count,
	uint64_t output_count,
	yanet_error **err
) {
	struct cp_device_trafgen_config *cp_device_trafgen_config =
		(struct cp_device_trafgen_config *)malloc(
			sizeof(struct cp_device_trafgen_config)
		);
	if (cp_device_trafgen_config == NULL) {
		yanet_error_add(err, "memory allocation failed");
		return NULL;
	}

	if (cp_device_config_init(
		    &cp_device_trafgen_config->cp_device_config,
		    "trafgen",
		    name,
		    input_count,
		    output_count,
		    err
	    )) {
		goto error_init;
	}

	return cp_device_trafgen_config;

error_init:
	free(cp_device_trafgen_config);
	return NULL;
}

int
cp_device_trafgen_config_set_input_pipeline(
	struct cp_device_trafgen_config *cp_device_trafgen_config,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	return cp_device_config_set_input_pipeline(
		&cp_device_trafgen_config->cp_device_config, index, name, weight
	);
}

int
cp_device_trafgen_config_set_output_pipeline(
	struct cp_device_trafgen_config *cp_device_trafgen_config,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	return cp_device_config_set_output_pipeline(
		&cp_device_trafgen_config->cp_device_config, index, name, weight
	);
}

void
cp_device_trafgen_config_free(
	struct cp_device_trafgen_config *cp_device_trafgen_config
) {
	cp_device_config_fini(&cp_device_trafgen_config->cp_device_config);
	free(cp_device_trafgen_config);
}

int
cp_device_trafgen_set_rate(struct cp_device *cp_device, uint64_t rate_pps) {
	struct cp_device_trafgen *config =
		container_of(cp_device, struct cp_device_trafgen, cp_device);
	config->rate_pps = rate_pps;
	return 0;
}

// Load the frames replayed by the generator.
//
// Called once on a freshly created device, so it only allocates. The buffers
// live in the device's memory_context and are released by
// cp_device_trafgen_free.
int
cp_device_trafgen_set_frames(
	struct cp_device *cp_device,
	const uint8_t *frames,
	uint64_t frames_size,
	const uint32_t *lengths,
	uint32_t frame_count,
	yanet_error **err
) {
	struct cp_device_trafgen *config =
		container_of(cp_device, struct cp_device_trafgen, cp_device);
	struct memory_context *memory_context = &cp_device->memory_context;

	if (frame_count == 0 || frames_size == 0) {
		return 0;
	}

	uint64_t index_size = sizeof(uint32_t) * frame_count;
	uint8_t *frames_buf = memory_balloc(memory_context, frames_size);
	uint32_t *lengths_buf = memory_balloc(memory_context, index_size);
	uint32_t *offsets_buf = memory_balloc(memory_context, index_size);

	if (frames_buf == NULL || lengths_buf == NULL || offsets_buf == NULL) {
		yanet_error_add(err, "failed to allocate frame storage");
		if (frames_buf != NULL) {
			memory_bfree(memory_context, frames_buf, frames_size);
		}
		if (lengths_buf != NULL) {
			memory_bfree(memory_context, lengths_buf, index_size);
		}
		if (offsets_buf != NULL) {
			memory_bfree(memory_context, offsets_buf, index_size);
		}
		return -1;
	}

	memcpy(frames_buf, frames, frames_size);

	uint32_t offset = 0;
	for (uint32_t idx = 0; idx < frame_count; ++idx) {
		lengths_buf[idx] = lengths[idx];
		offsets_buf[idx] = offset;
		offset += lengths[idx];
	}

	SET_OFFSET_OF(&config->frames, frames_buf);
	SET_OFFSET_OF(&config->frame_lengths, lengths_buf);
	SET_OFFSET_OF(&config->frame_offsets, offsets_buf);
	config->frames_size = frames_size;
	config->frame_count = frame_count;

	return 0;
}
