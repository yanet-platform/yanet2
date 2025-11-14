#include "cp_pipeline.h"

#include "common/container_of.h"

#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"

#include <string.h>

static inline uint64_t
cp_pipeline_alloc_size(uint64_t length) {
	return sizeof(struct cp_pipeline) +
	       sizeof(struct cp_pipeline_function) * length;
}

struct cp_pipeline *
cp_pipeline_create(
	struct memory_context *memory_context,
	struct cp_config_gen *cp_config_gen,
	struct cp_pipeline_config *cp_pipeline_config
) {
	// FIXME
	(void)cp_config_gen;

	struct cp_pipeline *new_pipeline = (struct cp_pipeline *)memory_balloc(
		memory_context,
		cp_pipeline_alloc_size(cp_pipeline_config->length)
	);
	if (new_pipeline == NULL) {
		return NULL;
	}

	memset(new_pipeline,
	       0,
	       cp_pipeline_alloc_size(cp_pipeline_config->length));
	registry_item_init(&new_pipeline->config_item);

	new_pipeline->length = cp_pipeline_config->length;
	strtcpy(new_pipeline->name,
		cp_pipeline_config->name,
		CP_PIPELINE_NAME_LEN);

	if (counter_registry_init(
		    &new_pipeline->counter_registry, memory_context, 0
	    )) {
		goto error;
	}

	// FIXME return error on counter failure
	new_pipeline->counter_packet_in_count = counter_registry_register(
		&new_pipeline->counter_registry, "input", 1
	);
	new_pipeline->counter_packet_out_count = counter_registry_register(
		&new_pipeline->counter_registry, "output", 1
	);
	new_pipeline->counter_packet_drop_count = counter_registry_register(
		&new_pipeline->counter_registry, "drop", 1
	);
	new_pipeline->counter_packet_in_hist = counter_registry_register(
		&new_pipeline->counter_registry, "input histogram", 8
	);

	for (uint64_t idx = 0; idx < cp_pipeline_config->length; ++idx) {
		strtcpy(new_pipeline->functions[idx].name,
			cp_pipeline_config->functions[idx],
			sizeof(new_pipeline->functions[idx].name));
		char counter_name[COUNTER_NAME_LEN];
		snprintf(
			counter_name,
			sizeof(counter_name),
			"stage %lu tsc histogram",
			idx
		);

		new_pipeline->functions[idx].tsc_counter_id =
			counter_registry_register(
				&new_pipeline->counter_registry, counter_name, 8
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
	//	counter_registry_destroy(&pipeline->counter_registry);

	// FIXME free functions

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
	struct cp_pipeline *new_pipeline
) {
	struct cp_pipeline *old_pipeline =
		cp_pipeline_registry_lookup(pipeline_registry, name);

	counter_registry_link(
		&new_pipeline->counter_registry,
		(old_pipeline != NULL) ? &old_pipeline->counter_registry : NULL
	);

	return registry_replace(
		&pipeline_registry->registry,
		cp_pipeline_registry_item_cmp,
		name,
		&new_pipeline->config_item,
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
