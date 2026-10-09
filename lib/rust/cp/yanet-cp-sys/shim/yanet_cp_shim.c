#include "yanet_cp_shim.h"

#include <errno.h>
#include <stdalign.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "common/memory.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_device.h"
#include "lib/controlplane/config/zone.h"
#include "lib/errors/errors.h"

// Copy the formatted error chain into the caller buffer and release it.
static void
report(yanet_error *error, const char *fallback, char *err, uint64_t len) {
	if (err == NULL || len == 0) {
		yanet_error_free(error);
		return;
	}
	char *message = error != NULL ? yanet_error_format(error) : NULL;
	snprintf(err, len, "%s", message != NULL ? message : fallback);
	free(message);
	yanet_error_free(error);
}

void
yanet_cp_shim_layout(struct yanet_cp_shim_layout *layout) {
	*layout = (struct yanet_cp_shim_layout){
		.cp_device_size = sizeof(struct cp_device),
		.cp_device_align = alignof(struct cp_device),
		.cp_device_agent = offsetof(struct cp_device, agent),
		.cp_device_input = offsetof(struct cp_device, input_pipelines),
		.cp_device_output =
			offsetof(struct cp_device, output_pipelines),
		.agent_arena_count = offsetof(struct agent, arena_count),
		.agent_arenas = offsetof(struct agent, arenas),
		.arena_size = sizeof(struct agent_arena),
		.arena_data = offsetof(struct agent_arena, data),
		.arena_len = offsetof(struct agent_arena, size),
		.device_name_len = CP_DEVICE_NAME_LEN,
		.pipeline_name_len = CP_PIPELINE_NAME_LEN,
	};
}

static int
set_pipelines(
	struct cp_device_config *config,
	const struct yanet_cp_shim_device_request *request
) {
	for (uint64_t idx = 0; idx < request->input_count; ++idx) {
		if (cp_device_config_set_input_pipeline(
			    config,
			    idx,
			    request->input[idx].name,
			    request->input[idx].weight
		    )) {
			return -1;
		}
	}
	for (uint64_t idx = 0; idx < request->output_count; ++idx) {
		if (cp_device_config_set_output_pipeline(
			    config,
			    idx,
			    request->output[idx].name,
			    request->output[idx].weight
		    )) {
			return -1;
		}
	}
	return 0;
}

struct cp_device *
yanet_cp_shim_device_new(
	struct agent *agent,
	const struct yanet_cp_shim_device_request *request,
	int32_t *precondition,
	char *err,
	uint64_t err_len
) {
	yanet_error *error = NULL;
	*precondition = 0;

	struct cp_device_config config;
	memset(&config, 0, sizeof(config));
	if (cp_device_config_init(
		    &config,
		    request->type,
		    request->name,
		    request->input_count,
		    request->output_count,
		    &error
	    )) {
		report(error, "failed to initialize device config", err, err_len
		);
		return NULL;
	}
	if (set_pipelines(&config, request)) {
		cp_device_config_fini(&config);
		report(NULL, "invalid pipeline binding", err, err_len);
		return NULL;
	}

	struct cp_device *device = (struct cp_device *)memory_balloc(
		&agent->memory_context, request->size
	);
	if (device == NULL) {
		cp_device_config_fini(&config);
		report(NULL, "device memory allocation failed", err, err_len);
		return NULL;
	}
	memset(device, 0, request->size);

	if (cp_device_init_layout(
		    device, agent, &config, request->config_layout, &error
	    )) {
		memory_bfree(&agent->memory_context, device, request->size);
		cp_device_config_fini(&config);
		*precondition = yanet_error_kind(error) ==
				YANET_ERROR_FAILED_PRECONDITION;
		report(error, "failed to initialize device", err, err_len);
		return NULL;
	}

	cp_device_config_fini(&config);
	return device;
}

int
yanet_cp_shim_device_free(
	struct cp_device *device, uint64_t size, char *err, uint64_t err_len
) {
	yanet_error *error = NULL;
	if (cp_device_try_destroy(device, &error)) {
		if (errno == EAGAIN) {
			yanet_error_free(error);
			return 1;
		}
		report(error, "failed to destroy device", err, err_len);
		return -1;
	}

	struct agent *agent = ADDR_OF(&device->agent);
	cp_device_fini(device);
	memory_bfree(&agent->memory_context, device, size);
	return 0;
}

int
yanet_cp_shim_device_read(
	struct agent *agent,
	const char *type,
	const char *name,
	uint64_t config_layout,
	uint64_t offset,
	void *out,
	uint64_t len
) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	cp_config_lock(cp_config);

	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct cp_device *device = cp_device_registry_lookup(
		&cp_config_gen->device_registry, type, name
	);
	if (device == NULL) {
		cp_config_unlock(cp_config);
		return 1;
	}
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	if (device->dp_device_idx >= dp_config->device_count ||
	    (ADDR_OF(&dp_config->dp_devices) + device->dp_device_idx)
			    ->config_layout != config_layout) {
		cp_config_unlock(cp_config);
		return 2;
	}

	memcpy(out, (const char *)device + offset, len);

	cp_config_unlock(cp_config);
	return 0;
}
