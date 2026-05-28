#include "cp_function.h"

#include "common/container_of.h"

#include "controlplane/config/cp_chain.h"
#include "controlplane/config/zone.h"

#include "lib/errors/errors.h"

#include <assert.h>
#include <string.h>

static inline size_t
cp_function_alloc_size(uint64_t chain_count) {
	return sizeof(struct cp_function) +
	       sizeof(struct cp_function_chain) * chain_count;
}

struct cp_function *
cp_function_new(struct memory_context *memory_context, uint64_t chain_count) {
	size_t alloc_size = cp_function_alloc_size(chain_count);
	struct cp_function *self =
		(struct cp_function *)memory_balloc(memory_context, alloc_size);
	if (self == NULL) {
		return NULL;
	}

	memset(self, 0, alloc_size);

	SET_OFFSET_OF(&self->memory_context, memory_context);
	self->chain_count = chain_count;

	return self;
}

void
cp_function_free(struct cp_function *self) {
	if (self == NULL) {
		return;
	}

	struct memory_context *mctx = ADDR_OF(&self->memory_context);
	size_t alloc_size = cp_function_alloc_size(self->chain_count);
	memory_bfree(mctx, self, alloc_size);
}

int
cp_function_init(
	struct cp_function *self,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct cp_function_config *cp_function_config,
	yanet_error **err
) {
	assert(self->chain_count == cp_function_config->chain_count);

	struct memory_context *mctx = ADDR_OF(&self->memory_context);

	registry_item_init(&self->config_item);

	if (counter_registry_init(&self->counter_registry, mctx, 0)) {
		yanet_error_add(
			err,
			"failed to initialize counter registry for function "
			"'%s'",
			cp_function_config->name
		);
		goto err_out;
	}

	strtcpy(self->name, cp_function_config->name, CP_PIPELINE_NAME_LEN);

	self->counter_packet_in_count = counter_registry_register(
		&self->counter_registry, "input", 1, err
	);
	if (self->counter_packet_in_count == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'input' counter for function '%s'",
			cp_function_config->name
		);
		goto err_out;
	}

	self->counter_packet_out_count = counter_registry_register(
		&self->counter_registry, "output", 1, err
	);
	if (self->counter_packet_out_count == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'output' counter for function '%s'",
			cp_function_config->name
		);
		goto err_out;
	}

	self->counter_packet_drop_count = counter_registry_register(
		&self->counter_registry, "drop", 1, err
	);
	if (self->counter_packet_drop_count == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'drop' counter for function '%s'",
			cp_function_config->name
		);
		goto err_out;
	}

	self->counter_packet_in_bytes = counter_registry_register(
		&self->counter_registry, "input_bytes", 1, err
	);
	if (self->counter_packet_in_bytes == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'input_bytes' counter for function "
			"'%s'",
			cp_function_config->name
		);
		goto err_out;
	}

	self->counter_packet_out_bytes = counter_registry_register(
		&self->counter_registry, "output_bytes", 1, err
	);
	if (self->counter_packet_out_bytes == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'output_bytes' counter for "
			"function '%s'",
			cp_function_config->name
		);
		goto err_out;
	}

	self->counter_packet_drop_bytes = counter_registry_register(
		&self->counter_registry, "drop_bytes", 1, err
	);
	if (self->counter_packet_drop_bytes == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'drop_bytes' counter for function "
			"'%s'",
			cp_function_config->name
		);
		goto err_out;
	}

	self->counter_packet_in_hist = counter_registry_register(
		&self->counter_registry, "input histogram", 8, err
	);
	if (self->counter_packet_in_hist == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'input histogram' counter for "
			"function '%s'",
			cp_function_config->name
		);
		goto err_out;
	}

	for (uint64_t idx = 0; idx < cp_function_config->chain_count; ++idx) {
		struct cp_chain *new_chain = cp_chain_new(
			mctx, cp_function_config->chains[idx].chain->length
		);
		if (new_chain == NULL) {
			yanet_error_add(
				err,
				"failed to allocate chain for function '%s'",
				cp_function_config->name
			);
			goto err_out;
		}

		if (cp_chain_init(
			    new_chain,
			    dp_config,
			    cp_config_gen,
			    cp_function_config->chains[idx].chain,
			    err
		    )) {
			cp_chain_free(new_chain);
			goto err_out;
		}

		SET_OFFSET_OF(&self->chains[idx].cp_chain, new_chain);
		self->chains[idx].weight =
			cp_function_config->chains[idx].weight;
	}

	return 0;

err_out:
	cp_function_fini(self);
	return -1;
}

void
cp_function_fini(struct cp_function *self) {
	for (uint64_t idx = 0; idx < self->chain_count; ++idx) {
		struct cp_chain *chain = ADDR_OF(&self->chains[idx].cp_chain);
		if (chain == NULL) {
			continue;
		}
		cp_chain_fini(chain);
		cp_chain_free(chain);
		SET_OFFSET_OF(&self->chains[idx].cp_chain, NULL);
	}

	counter_registry_fini(&self->counter_registry);
}

// Pipeline registry

int
cp_function_registry_init(
	struct memory_context *memory_context,
	struct cp_function_registry *new_function_registry,
	yanet_error **err
) {
	if (registry_init(
		    memory_context, &new_function_registry->registry, 8
	    )) {
		yanet_error_add(err, "failed to initialize function registry");
		return -1;
	}

	SET_OFFSET_OF(&new_function_registry->memory_context, memory_context);
	return 0;
}

int
cp_function_registry_copy(
	struct memory_context *memory_context,
	struct cp_function_registry *new_function_registry,
	struct cp_function_registry *old_function_registry,
	yanet_error **err
) {
	if (registry_copy(
		    memory_context,
		    &new_function_registry->registry,
		    &old_function_registry->registry
	    )) {
		yanet_error_add(err, "failed to copy function registry");
		return -1;
	};

	SET_OFFSET_OF(&new_function_registry->memory_context, memory_context);
	return 0;
}

static void
cp_function_registry_item_free_cb(struct registry_item *item, void *data) {
	// TODO: drop the data parameter from registry_item_free_func once all
	// registry consumers move memory_context into their struct.
	(void)data;
	struct cp_function *function =
		container_of(item, struct cp_function, config_item);
	cp_function_fini(function);
	cp_function_free(function);
}

void
cp_function_registry_fini(struct cp_function_registry *function_registry) {
	registry_fini(
		&function_registry->registry,
		cp_function_registry_item_free_cb,
		NULL
	);
}

struct cp_function *
cp_function_registry_get(
	struct cp_function_registry *function_registry, uint64_t index
) {
	struct registry_item *item =
		registry_get(&function_registry->registry, index);
	if (item == NULL) {
		return NULL;
	}
	return container_of(item, struct cp_function, config_item);
}

static int
cp_function_registry_item_cmp(
	const struct registry_item *item, const void *data
) {
	const struct cp_function *function =
		container_of(item, struct cp_function, config_item);

	return strncmp(
		function->name, (const char *)data, CP_FUNCTION_NAME_LEN
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
	struct cp_function *new_function,
	yanet_error **err
) {
	struct cp_function *old_function =
		cp_function_registry_lookup(function_registry, name);

	if (counter_registry_link(
		    &new_function->counter_registry,
		    (old_function != NULL) ? &old_function->counter_registry
					   : NULL,
		    err
	    )) {
		yanet_error_add(err, "failed to link counter registry");
		return -1;
	}

	for (uint64_t idx = 0; idx < new_function->chain_count; ++idx) {
		struct cp_chain *new_chain =
			ADDR_OF(&new_function->chains[idx].cp_chain);

		struct cp_chain *old_chain = NULL;
		for (uint64_t idx = 0;
		     old_function != NULL && idx < old_function->chain_count;
		     ++idx) {
			if (!strncmp(
				    new_chain->name,
				    ADDR_OF(&old_function->chains[idx].cp_chain)
					    ->name,
				    CP_CHAIN_NAME_LEN
			    )) {
				old_chain = ADDR_OF(
					&old_function->chains[idx].cp_chain
				);
				break;
			}
		}
		// TODO: unlink on fail?
		counter_registry_link(
			&new_chain->counter_registry,
			(old_chain != NULL) ? &old_chain->counter_registry
					    : NULL,
			err
		);
	}

	if (registry_replace(
		    &function_registry->registry,
		    cp_function_registry_item_cmp,
		    name,
		    &new_function->config_item,
		    cp_function_registry_item_free_cb,
		    NULL
	    )) {
		yanet_error_add(err, "failed to replace function in registry");
		return -1;
	}

	return 0;
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
		NULL
	);
}
