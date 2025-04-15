#include "cp_device.h"

#include "common/container_of.h"

#include "dataplane/config/zone.h"

#include "controlplane/config/zone.h"

static inline uint64_t
cp_device_alloc_size(uint64_t pipeline_map_size) {
	return sizeof(struct cp_device) + sizeof(uint64_t) * pipeline_map_size;
}

struct cp_device *
cp_device_create(
	struct memory_context *memory_context,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct cp_device_config *device_config
) {
	(void)dp_config;

	uint64_t pipeline_map_size = 0;
	for (uint64_t pipeline_weight_idx = 0;
	     pipeline_weight_idx < device_config->pipeline_weight_count;
	     ++pipeline_weight_idx) {
		struct cp_pipeline_weight *pipeline_weight =
			device_config->pipeline_weights + pipeline_weight_idx;
		pipeline_map_size += pipeline_weight->weight;
	}

	struct cp_device *new_device = (struct cp_device *)memory_balloc(
		memory_context, cp_device_alloc_size(pipeline_map_size)
	);
	if (new_device == NULL) {
		return NULL;
	}

	registry_item_init(&new_device->config_item);

	new_device->pipeline_map_size = pipeline_map_size;
	strtcpy(new_device->name, device_config->name, CP_DEVICE_NAME_LEN);

	uint64_t weight_pos = 0;

	for (uint64_t pipeline_weight_idx = 0;
	     pipeline_weight_idx < device_config->pipeline_weight_count;
	     ++pipeline_weight_idx) {
		struct cp_pipeline_weight *pipeline_weight =
			device_config->pipeline_weights + pipeline_weight_idx;
		uint64_t pipeline_index;
		if (cp_config_gen_lookup_pipeline_index(
			    cp_config_gen,
			    pipeline_weight->name,
			    &pipeline_index
		    )) {
			goto error;
		}
		uint64_t weight = pipeline_weight->weight;
		while (weight--) {
			new_device->pipeline_map[weight_pos++] = pipeline_index;
		}
	}

	return new_device;

error:
	cp_device_free(memory_context, new_device);
	return NULL;
}

void
cp_device_free(
	struct memory_context *memory_context, struct cp_device *device
) {
	memory_bfree(
		memory_context,
		device,
		cp_device_alloc_size(device->pipeline_map_size)
	);
}

// Pipeline registry

int
cp_device_registry_init(
	struct memory_context *memory_context,
	struct cp_device_registry *new_device_registry
) {
	if (registry_init(memory_context, &new_device_registry->registry, 8)) {
		return -1;
	}

	SET_OFFSET_OF(&new_device_registry->memory_context, memory_context);
	return 0;
}

int
cp_device_registry_copy(
	struct memory_context *memory_context,
	struct cp_device_registry *new_device_registry,
	struct cp_device_registry *old_device_registry
) {
	if (registry_copy(
		    memory_context,
		    &new_device_registry->registry,
		    &old_device_registry->registry
	    )) {
		return -1;
	};

	SET_OFFSET_OF(&new_device_registry->memory_context, memory_context);
	return 0;
}

static void
cp_device_registry_item_free_cb(struct registry_item *item, void *data) {
	struct cp_device *device =
		container_of(item, struct cp_device, config_item);
	struct memory_context *memory_context = (struct memory_context *)data;
	cp_device_free(memory_context, device);
}

void
cp_device_registry_destroy(struct cp_device_registry *device_registry) {
	struct memory_context *memory_context =
		ADDR_OF(&device_registry->memory_context);
	registry_destroy(
		&device_registry->registry,
		cp_device_registry_item_free_cb,
		memory_context
	);
}

struct cp_device *
cp_device_registry_get(
	struct cp_device_registry *device_registry, uint64_t index
) {
	struct registry_item *item =
		registry_get(&device_registry->registry, index);
	if (item == NULL)
		return NULL;
	return container_of(item, struct cp_device, config_item);
}

static int
cp_device_registry_item_cmp(
	const struct registry_item *item, const void *data
) {
	const struct cp_device *device =
		container_of(item, struct cp_device, config_item);

	return strncmp(device->name, (const char *)data, CP_DEVICE_NAME_LEN);
}

struct cp_device *
cp_device_registry_lookup(
	struct cp_device_registry *device_registry, const char *name
) {
	uint64_t index;
	if (registry_lookup(
		    &device_registry->registry,
		    cp_device_registry_item_cmp,
		    name,
		    &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&device_registry->registry, index),
		struct cp_device,
		config_item
	);
}

int
cp_device_registry_upsert(
	struct cp_device_registry *device_registry,
	const char *name,
	struct cp_device *device
) {
	return registry_replace(
		&device_registry->registry,
		cp_device_registry_item_cmp,
		name,
		&device->config_item,
		cp_device_registry_item_free_cb,
		ADDR_OF(&device_registry->memory_context)
	);
}

int
cp_device_registry_delete(
	struct cp_device_registry *device_registry, const char *name
) {
	return registry_replace(
		&device_registry->registry,
		cp_device_registry_item_cmp,
		name,
		NULL,
		cp_device_registry_item_free_cb,
		ADDR_OF(&device_registry->memory_context)
	);
}
