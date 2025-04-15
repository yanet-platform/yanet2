#include "cp_pipeline.h"

#include "common/container_of.h"

#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"

static inline uint64_t
cp_pipeline_alloc_size(uint64_t length) {
	return sizeof(struct cp_pipeline) +
	       sizeof(struct cp_pipeline_module) * length;
}

struct cp_pipeline *
cp_pipeline_create(
	struct memory_context *memory_context,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct pipeline_config *pipeline_config
) {
	struct cp_pipeline *new_pipeline = (struct cp_pipeline *)memory_balloc(
		memory_context, cp_pipeline_alloc_size(pipeline_config->length)
	);
	if (new_pipeline == NULL) {
		return NULL;
	}

	registry_item_init(&new_pipeline->config_item);

	new_pipeline->length = pipeline_config->length;
	strtcpy(new_pipeline->name, pipeline_config->name, CP_PIPELINE_NAME_LEN
	);

	counter_registry_init(
		&new_pipeline->counter_registry, memory_context, 0
	);

	for (uint64_t module_idx = 0; module_idx < pipeline_config->length;
	     ++module_idx) {
		uint64_t index;
		if (dp_config_lookup_module(
			    dp_config,
			    pipeline_config->modules[module_idx].type,
			    &index
		    )) {
			goto error;
		}

		if (cp_config_gen_lookup_module_index(
			    cp_config_gen,
			    index,
			    pipeline_config->modules[module_idx].name,
			    &new_pipeline->modules[module_idx].index
		    )) {
			goto error;
		}

		char counter_name[COUNTER_NAME_LEN];
		snprintf(
			counter_name, COUNTER_NAME_LEN, "stage-%lu", module_idx
		);

		new_pipeline->modules[module_idx].counter_id =
			counter_registry_register(
				&new_pipeline->counter_registry, counter_name, 4
			);
	}

	return new_pipeline;

error:
	cp_pipeline_free(memory_context, new_pipeline);
	return NULL;
}

void
cp_pipeline_free(
	struct memory_context *memory_context, struct cp_pipeline *pipeline
) {
	memory_bfree(
		memory_context,
		pipeline,
		cp_pipeline_alloc_size(pipeline->length)
	);
}

// Pipeline registry

int
cp_pipeline_registry_init(
	struct memory_context *memory_context,
	struct cp_pipeline_registry *new_pipeline_registry
) {
	if (registry_init(
		    memory_context, &new_pipeline_registry->registry, 8
	    )) {
		return -1;
	}

	SET_OFFSET_OF(&new_pipeline_registry->memory_context, memory_context);
	return 0;
}

int
cp_pipeline_registry_copy(
	struct memory_context *memory_context,
	struct cp_pipeline_registry *new_pipeline_registry,
	struct cp_pipeline_registry *old_pipeline_registry
) {
	if (registry_copy(
		    memory_context,
		    &new_pipeline_registry->registry,
		    &old_pipeline_registry->registry
	    )) {
		return -1;
	};

	SET_OFFSET_OF(&new_pipeline_registry->memory_context, memory_context);
	return 0;
}

static void
cp_pipeline_registry_item_free_cb(struct registry_item *item, void *data) {
	struct cp_pipeline *pipeline =
		container_of(item, struct cp_pipeline, config_item);
	struct memory_context *memory_context = (struct memory_context *)data;
	cp_pipeline_free(memory_context, pipeline);
}

void
cp_pipeline_registry_destroy(struct cp_pipeline_registry *pipeline_registry) {
	struct memory_context *memory_context =
		ADDR_OF(&pipeline_registry->memory_context);
	registry_destroy(
		&pipeline_registry->registry,
		cp_pipeline_registry_item_free_cb,
		memory_context
	);
}

struct cp_pipeline *
cp_pipeline_registry_get(
	struct cp_pipeline_registry *pipeline_registry, uint64_t index
) {
	struct registry_item *item =
		registry_get(&pipeline_registry->registry, index);
	if (item == NULL)
		return NULL;
	return container_of(item, struct cp_pipeline, config_item);
}

static int
cp_pipeline_registry_item_cmp(
	const struct registry_item *item, const void *data
) {
	const struct cp_pipeline *pipeline =
		container_of(item, struct cp_pipeline, config_item);

	return strncmp(
		pipeline->name, (const char *)data, CP_PIPELINE_NAME_LEN
	);
}

int
cp_pipeline_registry_lookup_index(
	struct cp_pipeline_registry *pipeline_registry,
	const char *name,
	uint64_t *index
) {
	return registry_lookup(
		&pipeline_registry->registry,
		cp_pipeline_registry_item_cmp,
		name,
		index
	);
}

struct cp_pipeline *
cp_pipeline_registry_lookup(
	struct cp_pipeline_registry *pipeline_registry, const char *name
) {
	uint64_t index;
	if (cp_pipeline_registry_lookup_index(
		    pipeline_registry, name, &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&pipeline_registry->registry, index),
		struct cp_pipeline,
		config_item
	);
}

int
cp_pipeline_registry_upsert(
	struct cp_pipeline_registry *pipeline_registry,
	const char *name,
	struct cp_pipeline *pipeline
) {
	return registry_replace(
		&pipeline_registry->registry,
		cp_pipeline_registry_item_cmp,
		name,
		&pipeline->config_item,
		cp_pipeline_registry_item_free_cb,
		ADDR_OF(&pipeline_registry->memory_context)
	);
}

int
cp_pipeline_registry_delete(
	struct cp_pipeline_registry *pipeline_registry, const char *name
) {
	return registry_replace(
		&pipeline_registry->registry,
		cp_pipeline_registry_item_cmp,
		name,
		NULL,
		cp_pipeline_registry_item_free_cb,
		ADDR_OF(&pipeline_registry->memory_context)
	);
}
