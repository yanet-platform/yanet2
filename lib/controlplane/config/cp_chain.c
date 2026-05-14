#include "cp_chain.h"

#include "controlplane/config/zone.h"

#include <assert.h>
#include <stdio.h>

static inline size_t
cp_chain_alloc_size(uint64_t length) {
	return sizeof(struct cp_chain) +
	       sizeof(struct cp_chain_module) * length;
}

struct cp_chain *
cp_chain_new(struct memory_context *memory_context, uint64_t length) {
	size_t alloc_size = cp_chain_alloc_size(length);
	struct cp_chain *self =
		(struct cp_chain *)memory_balloc(memory_context, alloc_size);
	if (self == NULL) {
		return NULL;
	}

	memset(self, 0, alloc_size);

	SET_OFFSET_OF(&self->memory_context, memory_context);
	self->length = length;

	return self;
}

void
cp_chain_free(struct cp_chain *self) {
	if (self == NULL) {
		return;
	}

	struct memory_context *mctx = ADDR_OF(&self->memory_context);
	size_t alloc_size = cp_chain_alloc_size(self->length);
	memory_bfree(mctx, self, alloc_size);
}

int
cp_chain_init(
	struct cp_chain *self,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct cp_chain_config *cp_chain_config,
	yanet_error **err
) {
	(void)dp_config;
	(void)cp_config_gen;

	struct memory_context *mctx = ADDR_OF(&self->memory_context);

	assert(self->length == cp_chain_config->length);

	if (counter_registry_init(&self->counter_registry, mctx, 0)) {
		yanet_error_add(
			err,
			"failed to initialize counter registry for chain '%s'",
			cp_chain_config->name
		);
		goto err_out;
	}

	strtcpy(self->name, cp_chain_config->name, sizeof(self->name));

	for (uint64_t idx = 0; idx < cp_chain_config->length; ++idx) {
		strtcpy(self->modules[idx].type,
			cp_chain_config->modules[idx].type,
			sizeof(self->modules[idx].type));

		strtcpy(self->modules[idx].name,
			cp_chain_config->modules[idx].name,
			sizeof(self->modules[idx].name));

		char tsc_counter_name[COUNTER_NAME_LEN];
		snprintf(
			tsc_counter_name,
			sizeof(tsc_counter_name),
			"stage %lu tsc",
			idx
		);

		self->modules[idx].tsc_counter_id = counter_registry_register(
			&self->counter_registry, tsc_counter_name, 8, err
		);
		if (self->modules[idx].tsc_counter_id == COUNTER_INVALID) {
			yanet_error_add(
				err,
				"failed to register '%s' counter for chain "
				"'%s'",
				tsc_counter_name,
				cp_chain_config->name
			);
			goto err_out;
		}
	}

	return 0;

err_out:
	cp_chain_fini(self);
	return -1;
}

void
cp_chain_fini(struct cp_chain *self) {
	counter_registry_free(&self->counter_registry);
}
