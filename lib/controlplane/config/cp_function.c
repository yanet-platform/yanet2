#include "cp_function.h"

#include "common/container_of.h"

#include "controlplane/config/cp_chain.h"
#include "controlplane/config/zone.h"

#include <string.h>

static inline uint64_t
cp_function_alloc_size(uint64_t chain_count) {
	return sizeof(struct cp_function) +
	       sizeof(struct cp_function_chain) * chain_count;
}

struct cp_function *
cp_function_create(
	struct memory_context *memory_context,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct cp_function_config *cp_function_config
) {
	const size_t alloc_size =
		cp_function_alloc_size(cp_function_config->chain_count);
	struct cp_function *new_function =
		(struct cp_function *)memory_balloc(memory_context, alloc_size);
	if (new_function == NULL) {
		return NULL;
	}

	memset(new_function, 0, alloc_size);

	registry_item_init(&new_function->config_item);

	new_function->chain_count = cp_function_config->chain_count;
	strtcpy(new_function->name,
		cp_function_config->name,
		CP_FUNCTION_NAME_LEN);

	if (counter_registry_init(
		    &new_function->counter_registry, memory_context, 0
	    )) {
		goto error;
	}

	new_function->counter_packet_in_count = counter_registry_register(
		&new_function->counter_registry, "input", 1
	);
	new_function->counter_packet_out_count = counter_registry_register(
		&new_function->counter_registry, "output", 1
	);
	new_function->counter_packet_drop_count = counter_registry_register(
		&new_function->counter_registry, "drop", 1
	);
	new_function->counter_packet_in_hist = counter_registry_register(
		&new_function->counter_registry, "input histogram", 8
	);

	for (uint64_t chain_idx = 0;
	     chain_idx < cp_function_config->chain_count;
	     ++chain_idx) {

		struct cp_chain *new_chain = cp_chain_create(
			memory_context,
			dp_config,
			cp_config_gen,
			cp_function_config->chains[chain_idx].chain
		);

		if (new_chain == NULL) {
			goto error;
		}

		SET_OFFSET_OF(
			&new_function->chains[chain_idx].cp_chain, new_chain
		);
		new_function->chains[chain_idx].weight =
			cp_function_config->chains[chain_idx].weight;
	}

	return new_function;

error:
	cp_function_free(memory_context, new_function);
	return NULL;
}

void
cp_function_free(
	struct memory_context *memory_context, struct cp_function *function
) {
	//	counter_registry_destroy(&function->counter_registry);

	for (uint64_t idx = 0; idx < function->chain_count; ++idx) {
		struct cp_chain *cp_chain =
			ADDR_OF(&function->chains[idx].cp_chain);
		if (cp_chain == NULL)
			continue;

		cp_chain_free(memory_context, cp_chain);
	}

	memory_bfree(
		memory_context,
		function,
		cp_function_alloc_size(function->chain_count)
	);
}

// Pipeline registry

int
cp_function_registry_init(
	struct memory_context *memory_context,
	struct cp_function_registry *new_function_registry
) {
	if (registry_init(
		    memory_context, &new_function_registry->registry, 8
	    )) {
		return -1;
	}

	SET_OFFSET_OF(&new_function_registry->memory_context, memory_context);
	return 0;
}

int
cp_function_registry_copy(
	struct memory_context *memory_context,
	struct cp_function_registry *new_function_registry,
	struct cp_function_registry *old_function_registry
) {
	if (registry_copy(
		    memory_context,
		    &new_function_registry->registry,
		    &old_function_registry->registry
	    )) {
		return -1;
	};

	SET_OFFSET_OF(&new_function_registry->memory_context, memory_context);
	return 0;
}

static void
cp_function_registry_item_free_cb(struct registry_item *item, void *data) {
	struct cp_function *function =
		container_of(item, struct cp_function, config_item);
	struct memory_context *memory_context = (struct memory_context *)data;
	cp_function_free(memory_context, function);
}

void
cp_function_registry_destroy(struct cp_function_registry *function_registry) {
	struct memory_context *memory_context =
		ADDR_OF(&function_registry->memory_context);
	registry_destroy(
		&function_registry->registry,
		cp_function_registry_item_free_cb,
		memory_context
	);
}

struct cp_function *
cp_function_registry_get(
	struct cp_function_registry *function_registry, uint64_t index
) {
	struct registry_item *item =
		registry_get(&function_registry->registry, index);
	if (item == NULL)
		return NULL;
	return container_of(item, struct cp_function, config_item);
}

static int
cp_function_registry_item_cmp(
	const struct registry_item *item, const void *data
) {
	const struct cp_function *function =
		container_of(item, struct cp_function, config_item);

	return strncmp(
		function->name, (const char *)data, CP_PIPELINE_NAME_LEN
	);
}

int
cp_function_registry_lookup_index(
	struct cp_function_registry *function_registry,
	const char *name,
	uint64_t *index
) {
	return registry_lookup(
		&function_registry->registry,
		cp_function_registry_item_cmp,
		name,
		index
	);
}

struct cp_function *
cp_function_registry_lookup(
	struct cp_function_registry *function_registry, const char *name
) {
	uint64_t index;
	if (cp_function_registry_lookup_index(
		    function_registry, name, &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&function_registry->registry, index),
		struct cp_function,
		config_item
	);
}

int
cp_function_registry_upsert(
	struct cp_function_registry *function_registry,
	const char *name,
	struct cp_function *new_function
) {
	struct cp_function *old_function =
		cp_function_registry_lookup(function_registry, name);

	counter_registry_link(
		&new_function->counter_registry,
		(old_function != NULL) ? &old_function->counter_registry : NULL
	);

	return registry_replace(
		&function_registry->registry,
		cp_function_registry_item_cmp,
		name,
		&new_function->config_item,
		cp_function_registry_item_free_cb,
		ADDR_OF(&function_registry->memory_context)
	);
}

int
cp_function_registry_delete(
	struct cp_function_registry *function_registry, const char *name
) {
	return registry_replace(
		&function_registry->registry,
		cp_function_registry_item_cmp,
		name,
		NULL,
		cp_function_registry_item_free_cb,
		ADDR_OF(&function_registry->memory_context)
	);
}
