#include "cp_device.h"

#include "common/container_of.h"

#include "dataplane/config/zone.h"

#include "controlplane/config/zone.h"

static inline uint64_t
cp_device_entry_alloc_size(uint64_t pipeline_count) {
	return sizeof(struct cp_device_entry) +
	       sizeof(struct cp_device_pipeline) * pipeline_count;
}

static struct cp_device_entry *
cp_device_entry_create(
	struct memory_context *memory_context,
	struct cp_device_entry_config *cp_device_entry_config
) {
	uint64_t alloc_size =
		cp_device_entry_alloc_size(cp_device_entry_config->count);
	struct cp_device_entry *cp_device_entry = (struct cp_device_entry *)
		memory_balloc(memory_context, alloc_size);
	if (cp_device_entry == NULL)
		return NULL;
	memset(cp_device_entry, 0, alloc_size);
	cp_device_entry->pipeline_count = cp_device_entry_config->count;
	for (uint64_t idx = 0; idx < cp_device_entry_config->count; ++idx) {
		struct cp_device_pipeline *cp_device_pipeline =
			cp_device_entry->pipelines + idx;
		struct cp_pipeline_weight_config *cp_device_pipeline_config =
			cp_device_entry_config->pipelines + idx;
		strtcpy(cp_device_pipeline->name,
			cp_device_pipeline_config->name,
			CP_PIPELINE_NAME_LEN);
		cp_device_pipeline->weight = cp_device_pipeline_config->weight;
	}

	return cp_device_entry;
}

struct cp_device *
cp_device_create(
	struct memory_context *memory_context,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct cp_device_config *device_config
) {
	// FIXME
	(void)dp_config;
	(void)cp_config_gen;

	struct cp_device *new_device = (struct cp_device *)memory_balloc(
		memory_context, sizeof(struct cp_device)
	);
	if (new_device == NULL) {
		return NULL;
	}
	memset(new_device, 0, sizeof(struct cp_device_config));
	strtcpy(new_device->name, device_config->name, CP_DEVICE_NAME_LEN);

	SET_OFFSET_OF(
		&new_device->input_pipelines,
		cp_device_entry_create(
			memory_context, device_config->input_pipelines
		)
	);
	if (new_device->input_pipelines == NULL) {
		cp_device_free(memory_context, new_device);
		return NULL;
	}

	SET_OFFSET_OF(
		&new_device->output_pipelines,
		cp_device_entry_create(
			memory_context, device_config->output_pipelines
		)
	);
	if (new_device->output_pipelines == NULL) {
		cp_device_free(memory_context, new_device);
		return NULL;
	}

	registry_item_init(&new_device->config_item);
	counter_registry_init(&new_device->counter_registry, memory_context, 0);

	return new_device;
}

static void
cp_device_entry_free(
	struct memory_context *memory_context,
	struct cp_device_entry *cp_device_entry
) {
	if (cp_device_entry == NULL)
		return;
	memory_bfree(
		memory_context,
		cp_device_entry,
		cp_device_entry_alloc_size(cp_device_entry->pipeline_count)
	);
}

void
cp_device_free(
	struct memory_context *memory_context, struct cp_device *cp_device
) {
	cp_device_entry_free(
		memory_context, ADDR_OF(&cp_device->output_pipelines)
	);

	cp_device_entry_free(
		memory_context, ADDR_OF(&cp_device->input_pipelines)
	);

	memory_bfree(memory_context, cp_device, sizeof(struct cp_device));
}

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
	struct cp_device *new_device
) {
	struct cp_device *old_device =
		cp_device_registry_lookup(device_registry, name);

	counter_registry_link(
		&new_device->counter_registry,
		(old_device != NULL) ? &old_device->counter_registry : NULL
	);

	return registry_replace(
		&device_registry->registry,
		cp_device_registry_item_cmp,
		name,
		&new_device->config_item,
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
