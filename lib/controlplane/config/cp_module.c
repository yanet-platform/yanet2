#include "cp_module.h"

#include "common/container_of.h"

#include "dataplane/config/zone.h"

#include "controlplane/agent/agent.h"

#include "controlplane/config/zone.h"

int
cp_module_init(
	struct cp_module *cp_module,
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	cp_module_free_handler free_handler
) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	if (dp_config_lookup_module(
		    dp_config, module_type, &cp_module->dp_module_idx
	    )) {
		errno = ENXIO;
		return -1;
	}

	strtcpy(cp_module->type, module_type, sizeof(cp_module->type));
	strtcpy(cp_module->name, module_name, sizeof(cp_module->name));
	memory_context_init_from(
		&cp_module->memory_context, &agent->memory_context, module_name
	);

	SET_OFFSET_OF(&cp_module->agent, agent);

	cp_module->free_handler = free_handler;

	registry_item_init(&cp_module->config_item);

	if (counter_registry_init(
		    &cp_module->counter_registry, &cp_module->memory_context, 1
	    )) {
		return -1;
	}

	return 0;
}

int
cp_module_registry_init(
	struct memory_context *memory_context,
	struct cp_module_registry *new_module_registry
) {
	if (registry_init(memory_context, &new_module_registry->registry, 8)) {
		return -1;
	}

	SET_OFFSET_OF(&new_module_registry->memory_context, memory_context);
	return 0;
}

int
cp_module_registry_copy(
	struct memory_context *memory_context,
	struct cp_module_registry *new_module_registry,
	struct cp_module_registry *old_module_registry
) {
	if (registry_copy(
		    memory_context,
		    &new_module_registry->registry,
		    &old_module_registry->registry
	    )) {
		return -1;
	};

	SET_OFFSET_OF(&new_module_registry->memory_context, memory_context);
	return 0;
}

static void
cp_module_registry_item_free_cb(struct registry_item *item, void *data) {
	(void)data;

	struct cp_module *module =
		container_of(item, struct cp_module, config_item);

	struct agent *agent = ADDR_OF(&module->agent);
	SET_OFFSET_OF(&module->prev, agent->unused_module);
	SET_OFFSET_OF(&agent->unused_module, module);
}

void
cp_module_registry_destroy(struct cp_module_registry *module_registry) {
	struct memory_context *memory_context =
		ADDR_OF(&module_registry->memory_context);
	registry_destroy(
		&module_registry->registry,
		cp_module_registry_item_free_cb,
		memory_context
	);
}

struct cp_module *
cp_module_registry_get(
	struct cp_module_registry *module_registry, uint64_t index
) {
	struct registry_item *item =
		registry_get(&module_registry->registry, index);
	if (item == NULL)
		return NULL;
	return container_of(item, struct cp_module, config_item);
}

struct cp_module_cmp_data {
	const char *type;
	const char *name;
};

static int
cp_module_registry_item_cmp(
	const struct registry_item *item, const void *data
) {
	const struct cp_module *module =
		container_of(item, struct cp_module, config_item);

	const struct cp_module_cmp_data *cmp_data =
		(const struct cp_module_cmp_data *)data;

	int cmp = strncmp(module->name, cmp_data->name, sizeof(module->name));

	if (cmp)
		return cmp;

	return strncmp(module->type, cmp_data->type, sizeof(module->type));
}

int
cp_module_registry_lookup_index(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name,
	uint64_t *index
) {
	struct cp_module_cmp_data cmp_data = {
		.type = type,
		.name = name,
	};

	return registry_lookup(
		&module_registry->registry,
		cp_module_registry_item_cmp,
		&cmp_data,
		index
	);
}

struct cp_module *
cp_module_registry_lookup(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name
) {
	uint64_t index;

	if (cp_module_registry_lookup_index(
		    module_registry, type, name, &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&module_registry->registry, index),
		struct cp_module,
		config_item
	);
}

int
cp_module_registry_upsert(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name,
	struct cp_module *new_module
) {
	struct cp_module_cmp_data cmp_data = {
		.type = type,
		.name = name,
	};

	struct cp_module *old_module =
		cp_module_registry_lookup(module_registry, type, name);

	counter_registry_link(
		&new_module->counter_registry,
		(old_module != NULL) ? &old_module->counter_registry : NULL
	);

	return registry_replace(
		&module_registry->registry,
		cp_module_registry_item_cmp,
		&cmp_data,
		&new_module->config_item,
		cp_module_registry_item_free_cb,
		ADDR_OF(&module_registry->memory_context)
	);
}

int
cp_module_registry_delete(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name
) {
	struct cp_module_cmp_data cmp_data = {
		.type = type,
		.name = name,
	};

	return registry_replace(
		&module_registry->registry,
		cp_module_registry_item_cmp,
		&cmp_data,
		NULL,
		cp_module_registry_item_free_cb,
		ADDR_OF(&module_registry->memory_context)
	);
}
