#include <stdlib.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"

#include "lib/controlplane/agent/agent.h"

#include "lib/errors/errors.h"

// Destroy a plain device: base teardown, then the wrapper allocation.
static void
cp_device_plain_destroy(struct cp_device *cp_device) {
	struct agent *agent = ADDR_OF(&cp_device->agent);
	cp_device_fini(cp_device);
	memory_bfree(
		&agent->memory_context,
		cp_device,
		sizeof(struct cp_device_plain)
	);
}

struct cp_device *
cp_device_plain_new(
	struct agent *agent,
	const struct cp_device_plain_config *config,
	yanet_error **err
) {
	// Reclaim this type's parked entries before the wrapper allocation:
	// a parked instance can be most of the arena, so reclaiming it first
	// can be the difference between success and failure under pressure.
	cp_device_drain_parked(agent, "plain", cp_device_plain_destroy);

	struct cp_device_plain *cp_device_plain =
		(struct cp_device_plain *)memory_balloc(
			&agent->memory_context, sizeof(struct cp_device_plain)
		);
	if (cp_device_plain == NULL) {
		yanet_error_add(err, "memory allocation failed");
		return NULL;
	}

	memset(cp_device_plain, 0, sizeof(struct cp_device_plain));

	if (cp_device_init(
		    &cp_device_plain->cp_device,
		    agent,
		    &config->cp_device_config,
		    err
	    )) {
		memory_bfree(
			&agent->memory_context,
			cp_device_plain,
			sizeof(struct cp_device_plain)
		);
		return NULL;
	}

	return &cp_device_plain->cp_device;
}

void
cp_device_plain_free(struct cp_device *cp_device) {
	cp_device_release(cp_device);
}

struct cp_device_plain_config *
cp_device_plain_config_new(
	const char *name,
	uint64_t input_count,
	uint64_t output_count,
	yanet_error **err
) {
	struct cp_device_plain_config *cp_device_plain_config =
		(struct cp_device_plain_config *)malloc(
			sizeof(struct cp_device_plain_config)
		);
	if (cp_device_plain_config == NULL) {
		yanet_error_add(err, "memory allocation failed");
		return NULL;
	}

	if (cp_device_config_init(
		    &cp_device_plain_config->cp_device_config,
		    "plain",
		    name,
		    input_count,
		    output_count,
		    err
	    )) {
		goto error_init;
	}

	return cp_device_plain_config;

error_init:
	free(cp_device_plain_config);
	return NULL;
}

int
cp_device_plain_config_set_input_pipeline(
	struct cp_device_plain_config *cp_device_plain_config,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	return cp_device_config_set_input_pipeline(
		&cp_device_plain_config->cp_device_config, index, name, weight
	);
}

int
cp_device_plain_config_set_output_pipeline(
	struct cp_device_plain_config *cp_device_plain_config,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	return cp_device_config_set_output_pipeline(
		&cp_device_plain_config->cp_device_config, index, name, weight
	);
}

void
cp_device_plain_config_free(
	struct cp_device_plain_config *cp_device_plain_config
) {
	cp_device_config_fini(&cp_device_plain_config->cp_device_config);
	free(cp_device_plain_config);
}
