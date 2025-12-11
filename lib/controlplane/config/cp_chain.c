#include "cp_chain.h"

#include "controlplane/config/zone.h"
#include "lib/controlplane/diag/diag.h"

static inline size_t
cp_chain_alloc_size(uint64_t length) {
	return sizeof(struct cp_chain) +
	       sizeof(struct cp_chain_module) * length;
}

struct cp_chain *
cp_chain_create(
	struct memory_context *memory_context,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct cp_chain_config *cp_chain_config
) {
	(void)dp_config;
	(void)cp_config_gen;

	struct cp_chain *new_chain = (struct cp_chain *)memory_balloc(
		memory_context, cp_chain_alloc_size(cp_chain_config->length)
	);
	if (new_chain == NULL) {
		NEW_ERROR(
			"failed to allocate memory for chain '%s'",
			cp_chain_config->name
		);
		return NULL;
	}

	if (counter_registry_init(
		    &new_chain->counter_registry, memory_context, 0
	    )) {
		NEW_ERROR(
			"failed to initialize counter registry for chain '%s'",
			cp_chain_config->name
		);
		goto error;
	}

	strtcpy(new_chain->name, cp_chain_config->name, sizeof(new_chain->name)
	);
	new_chain->length = cp_chain_config->length;

	for (uint64_t idx = 0; idx < cp_chain_config->length; ++idx) {
		strtcpy(new_chain->modules[idx].type,
			cp_chain_config->modules[idx].type,
			sizeof(new_chain->modules[idx].type));

		strtcpy(new_chain->modules[idx].name,
			cp_chain_config->modules[idx].name,
			sizeof(new_chain->modules[idx].name));
	}

	return new_chain;
error:

	memory_bfree(
		memory_context,
		new_chain,
		cp_chain_alloc_size(cp_chain_config->length)
	);

	return NULL;
}

void
cp_chain_free(
	struct memory_context *memory_context, struct cp_chain *cp_chain
) {
	memory_bfree(
		memory_context, cp_chain, cp_chain_alloc_size(cp_chain->length)
	);
}
