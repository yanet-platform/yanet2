#include "cp_device.h"

#include "common/container_of.h"

#include "dataplane/config/zone.h"

#include "controlplane/config/zone.h"

int
cp_device_config_init(
	struct cp_device_config *cp_device_config,
	const char *type,
	const char *name,
	uint64_t input_pipeline_count,
	uint64_t output_pipeline_count
) {
	memset(cp_device_config, 0, sizeof(struct cp_device_config));
	strtcpy(cp_device_config->type, type, sizeof(cp_device_config->type));
	strtcpy(cp_device_config->name, name, CP_DEVICE_NAME_LEN);
	cp_device_config->input_pipelines = (struct cp_device_entry_config *)
		malloc(sizeof(struct cp_device_entry_config) +
		       sizeof(struct cp_pipeline_weight_config) *
			       input_pipeline_count);
	if (cp_device_config->input_pipelines == NULL) {
		goto error;
	}
	memset(cp_device_config->input_pipelines,
	       0,
	       sizeof(struct cp_device_entry_config) +
		       sizeof(struct cp_pipeline_weight_config) *
			       input_pipeline_count);
	cp_device_config->input_pipelines->count = input_pipeline_count;

	cp_device_config->output_pipelines = (struct cp_device_entry_config *)
		malloc(sizeof(struct cp_device_entry_config) +
		       sizeof(struct cp_pipeline_weight_config) *
			       output_pipeline_count);
	if (cp_device_config->output_pipelines == NULL) {
		goto error_output;
	}
	memset(cp_device_config->output_pipelines,
	       0,
	       sizeof(struct cp_device_entry_config) +
		       sizeof(struct cp_pipeline_weight_config) *
			       output_pipeline_count);
	cp_device_config->output_pipelines->count = output_pipeline_count;

	return 0;

error_output:
	free(cp_device_config->input_pipelines);

error:
	return -1;
}

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

int
cp_device_init(
	struct cp_device *cp_device,
	struct agent *agent,
	const struct cp_device_config *cp_device_config
) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	if (dp_config_lookup_device(
		    dp_config, cp_device_config->type, &cp_device->dp_device_idx
	    )) {
		errno = ENXIO;
		return -1;
	}

	memset(cp_device, 0, sizeof(struct cp_device_config));
	strtcpy(cp_device->type, cp_device_config->type, sizeof(cp_device->type)
	);
	strtcpy(cp_device->name, cp_device_config->name, sizeof(cp_device->name)
	);
	memory_context_init_from(
		&cp_device->memory_context,
		&agent->memory_context,
		cp_device_config->name
	);

	SET_OFFSET_OF(&cp_device->agent, agent);

	struct memory_context *memory_context = &cp_device->memory_context;

	SET_OFFSET_OF(
		&cp_device->input_pipelines,
		cp_device_entry_create(
			memory_context, cp_device_config->input_pipelines
		)
	);
	if (cp_device->input_pipelines == NULL)
		return -1;

	SET_OFFSET_OF(
		&cp_device->output_pipelines,
		cp_device_entry_create(
			memory_context, cp_device_config->output_pipelines
		)
	);
	if (cp_device->output_pipelines == NULL)
		return -1;

	registry_item_init(&cp_device->config_item);
	counter_registry_init(&cp_device->counter_registry, memory_context, 0);

	return 0;
}

struct cp_device *
cp_device_create(struct agent *agent, struct cp_device_config *device_config) {
	struct cp_device *new_device = (struct cp_device *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_device)
	);
	if (new_device == NULL) {
		return NULL;
	}

	if (cp_device_init(new_device, agent, device_config)) {
		cp_device_free(&agent->memory_context, new_device);
		return NULL;
	}

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
cp_device_destroy(
	struct memory_context *memory_context, struct cp_device *cp_device
) {
	cp_device_entry_free(
		memory_context, ADDR_OF(&cp_device->output_pipelines)
	);

	cp_device_entry_free(
		memory_context, ADDR_OF(&cp_device->input_pipelines)
	);
}

void
cp_device_free(
	struct memory_context *memory_context, struct cp_device *cp_device
) {
	cp_device_destroy(memory_context, cp_device);
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
	if (item == NULL) {
		return NULL;
	}
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
