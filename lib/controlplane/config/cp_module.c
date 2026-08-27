#include "cp_module.h"

#include "common/container_of.h"

#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"

#include <errno.h>

int
cp_module_init(
	struct cp_module *cp_module,
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	yanet_error **err
) {
	memset(cp_module, 0, sizeof(struct cp_module));

	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	if (dp_config_lookup_module(
		    dp_config, module_type, &cp_module->dp_module_idx
	    )) {
		yanet_error_add(
			err,
			"module type '%s' not found in dataplane config",
			module_type
		);
		return -1;
	}

	strtcpy(cp_module->type, module_type, sizeof(cp_module->type));
	strtcpy(cp_module->name, module_name, sizeof(cp_module->name));
	memory_context_init_from(
		&cp_module->memory_context, &agent->memory_context, module_name
	);

	SET_OFFSET_OF(&cp_module->agent, agent);

	registry_item_init(&cp_module->config_item);

	if (counter_registry_init(
		    &cp_module->counter_registry, &cp_module->memory_context, 0
	    )) {
		yanet_error_add(
			err,
			"failed to initialize counter registry for module "
			"'%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}

	cp_module->rx_counter_id = counter_registry_register(
		&cp_module->counter_registry, "rx", 2, err
	);
	if (cp_module->rx_counter_id == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'rx' counter for module '%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}
	cp_module->tx_counter_id = counter_registry_register(
		&cp_module->counter_registry, "tx", 2, err
	);
	if (cp_module->tx_counter_id == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'tx' counter for module '%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}
	cp_module->drop_counter_id = counter_registry_register(
		&cp_module->counter_registry, "drop", 2, err
	);
	if (cp_module->drop_counter_id == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'drop' counter for module '%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}
	cp_module->pending_input_counter_id = counter_registry_register(
		&cp_module->counter_registry, "pending_input", 2, err
	);
	if (cp_module->pending_input_counter_id == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'pending_input' counter for module "
			"'%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}
	cp_module->pending_output_counter_id = counter_registry_register(
		&cp_module->counter_registry, "pending_output", 2, err
	);
	if (cp_module->pending_output_counter_id == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'pending_output' counter for "
			"module "
			"'%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}

	uint64_t any_idx;
	if (cp_module_link_device(cp_module, "", &any_idx, err)) {
		goto fail;
	}

	return 0;

fail:
	cp_module_fini(cp_module);
	return -1;
}

void
cp_module_fini(struct cp_module *cp_module) {
	counter_registry_fini(&cp_module->counter_registry);

	struct cp_module_counter_registry **runtime_registries =
		ADDR_OF(&cp_module->runtime_counter_registries);
	if (runtime_registries != NULL) {
		for (uint64_t i = 0;
		     i < cp_module->runtime_counter_registry_count;
		     ++i) {
			struct cp_module_counter_registry *entry =
				ADDR_OF(runtime_registries + i);
			counter_registry_fini(&entry->registry);
			memory_bfree(
				&cp_module->memory_context,
				entry,
				sizeof(*entry)
			);
		}
		memory_bfree(
			&cp_module->memory_context,
			runtime_registries,
			sizeof(*runtime_registries) *
				cp_module->runtime_counter_registry_count
		);
	}
	SET_OFFSET_OF(&cp_module->runtime_counter_registries, NULL);
	cp_module->runtime_counter_registry_count = 0;

	struct cp_module_device *devices = ADDR_OF(&cp_module->devices);
	if (devices != NULL) {
		memory_bfree(
			&cp_module->memory_context,
			devices,
			sizeof(struct cp_module_device) *
				cp_module->device_count
		);
	}
	SET_OFFSET_OF(&cp_module->devices, NULL);
	cp_module->device_count = 0;

	struct cp_module_object *objects = ADDR_OF(&cp_module->objects);
	if (objects != NULL) {
		memory_bfree(
			&cp_module->memory_context,
			objects,
			sizeof(struct cp_module_object) *
				cp_module->object_count
		);
	}
	SET_OFFSET_OF(&cp_module->objects, NULL);
	cp_module->object_count = 0;

	SET_OFFSET_OF(&cp_module->agent, NULL);

	memory_context_fini(&cp_module->memory_context);
}

struct counter_registry *
cp_module_counter_registry(
	struct cp_module *cp_module,
	const char *tag,
	uint64_t *index_out,
	yanet_error **err
) {
	struct cp_module_counter_registry **runtime_registries =
		ADDR_OF(&cp_module->runtime_counter_registries);
	for (uint64_t idx = 0; idx < cp_module->runtime_counter_registry_count;
	     ++idx) {
		struct cp_module_counter_registry *entry =
			ADDR_OF(runtime_registries + idx);
		if (!strncmp(entry->tag, tag, COUNTER_NAME_LEN)) {
			if (index_out != NULL) {
				*index_out = idx;
			}
			return &entry->registry;
		}
	}

	uint64_t old_count = cp_module->runtime_counter_registry_count;
	uint64_t new_count = old_count + 1;

	// Both fallible allocations happen before anything published is
	// touched, so every failure path below leaves the previous array
	// intact and usable.
	struct cp_module_counter_registry *slot =
		(struct cp_module_counter_registry *)memory_balloc(
			&cp_module->memory_context, sizeof(*slot)
		);
	if (slot == NULL) {
		yanet_error_add(
			err,
			"failed to allocate counter registry '%s' for module "
			"'%s:%s'",
			tag,
			cp_module->type,
			cp_module->name
		);
		return NULL;
	}
	strtcpy(slot->tag, tag, sizeof(slot->tag));
	if (counter_registry_init(
		    &slot->registry, &cp_module->memory_context, 0
	    )) {
		memory_bfree(&cp_module->memory_context, slot, sizeof(*slot));
		yanet_error_add(
			err,
			"failed to initialize counter registry '%s' for module "
			"'%s:%s'",
			tag,
			cp_module->type,
			cp_module->name
		);
		return NULL;
	}

	struct cp_module_counter_registry **new_registries =
		(struct cp_module_counter_registry **)memory_balloc(
			&cp_module->memory_context,
			sizeof(*new_registries) * new_count
		);
	if (new_registries == NULL) {
		counter_registry_fini(&slot->registry);
		memory_bfree(&cp_module->memory_context, slot, sizeof(*slot));
		yanet_error_add(
			err,
			"failed to allocate counter registry '%s' for module "
			"'%s:%s'",
			tag,
			cp_module->type,
			cp_module->name
		);
		return NULL;
	}

	// A registry embeds self-relative offsets, so it is allocated
	// out-of-line and never moved; growing therefore only reallocates
	// the pointer array. Each pointer slot is re-derived against its new
	// slot address instead of byte-copied — a copied offset would
	// resolve to a shifted, bogus target.
	for (uint64_t idx = 0; idx < old_count; ++idx) {
		SET_OFFSET_OF(
			new_registries + idx, ADDR_OF(runtime_registries + idx)
		);
	}
	SET_OFFSET_OF(new_registries + old_count, slot);

	memory_bfree(
		&cp_module->memory_context,
		runtime_registries,
		sizeof(*runtime_registries) * old_count
	);
	SET_OFFSET_OF(&cp_module->runtime_counter_registries, new_registries);
	cp_module->runtime_counter_registry_count = new_count;

	if (index_out != NULL) {
		*index_out = old_count;
	}
	return &slot->registry;
}

int
cp_module_link_device(
	struct cp_module *cp_module,
	const char *name,
	uint64_t *index,
	yanet_error **err
) {
	struct cp_module_device *devices = ADDR_OF(&cp_module->devices);
	for (uint64_t idx = 0; idx < cp_module->device_count; ++idx) {
		if (!strncmp(devices[idx].name, name, CP_DEVICE_NAME_LEN)) {
			*index = idx;
			return 0;
		}
	}

	devices = (struct cp_module_device *)memory_brealloc(
		&cp_module->memory_context,
		devices,
		sizeof(struct cp_module_device) * cp_module->device_count,
		sizeof(struct cp_module_device) * (cp_module->device_count + 1)
	);
	if (devices == NULL) {
		yanet_error_add(
			err,
			"failed to reallocate devices array for module '%s:%s'",
			cp_module->type,
			cp_module->name
		);
		return -1;
	}

	strtcpy(devices[cp_module->device_count].name, name, CP_DEVICE_NAME_LEN
	);
	SET_OFFSET_OF(&cp_module->devices, devices);
	*index = cp_module->device_count;
	++cp_module->device_count;

	return 0;
}

int
cp_module_link_object(
	struct cp_module *cp_module,
	const char *object_type,
	const char *object_name,
	uint64_t *index,
	yanet_error **err
) {
	struct cp_module_object *objects = ADDR_OF(&cp_module->objects);
	for (uint64_t idx = 0; idx < cp_module->object_count; ++idx) {
		if (!strncmp(
			    objects[idx].type, object_type, CP_OBJECT_TYPE_LEN
		    ) &&
		    !strncmp(
			    objects[idx].name, object_name, CP_OBJECT_NAME_LEN
		    )) {
			*index = idx;
			return 0;
		}
	}

	objects = (struct cp_module_object *)memory_brealloc(
		&cp_module->memory_context,
		objects,
		sizeof(struct cp_module_object) * cp_module->object_count,
		sizeof(struct cp_module_object) * (cp_module->object_count + 1)
	);
	if (objects == NULL) {
		yanet_error_add(
			err,
			"failed to reallocate objects array for module '%s:%s'",
			cp_module->type,
			cp_module->name
		);
		return -1;
	}

	strtcpy(objects[cp_module->object_count].type,
		object_type,
		CP_OBJECT_TYPE_LEN);
	strtcpy(objects[cp_module->object_count].name,
		object_name,
		CP_OBJECT_NAME_LEN);
	SET_OFFSET_OF(&cp_module->objects, objects);
	*index = cp_module->object_count;
	++cp_module->object_count;

	return 0;
}

int
cp_module_try_destroy(struct cp_module *cp_module, yanet_error **err) {
	struct agent *agent = ADDR_OF(&cp_module->agent);
	struct cp_config *cp_config =
		(agent != NULL) ? ADDR_OF(&agent->cp_config) : NULL;

	if (cp_config != NULL) {
		cp_config_lock(cp_config);
	}

	uint64_t refcnt = cp_module->config_item.refcnt;

	if (refcnt == 0) {
		// Reserve the item against a racing re-registration before
		// releasing the lock: a publisher holding a stale copied
		// handle could otherwise install this pointer into a new
		// generation between this check and the typed destroy.
		registry_item_mark_destroying(&cp_module->config_item);
	}

	if (cp_config != NULL) {
		cp_config_unlock(cp_config);
	}

	if (refcnt != 0) {
		// errno is set last, right before the return, so the error
		// formatting above cannot clobber what the caller reads.
		yanet_error_add(
			err,
			"module '%s:%s' is still referenced by a live "
			"generation",
			cp_module->type,
			cp_module->name
		);
		errno = EAGAIN;
		return -1;
	}

	return 0;
}

int
cp_module_registry_init(
	struct memory_context *memory_context,
	struct cp_module_registry *new_module_registry,
	yanet_error **err
) {
	if (registry_init(memory_context, &new_module_registry->registry, 8)) {
		yanet_error_add(err, "failed to initialize module registry");
		return -1;
	}

	SET_OFFSET_OF(&new_module_registry->memory_context, memory_context);
	return 0;
}

int
cp_module_registry_copy(
	struct memory_context *memory_context,
	struct cp_module_registry *new_module_registry,
	struct cp_module_registry *old_module_registry,
	yanet_error **err
) {
	if (registry_copy(
		    memory_context,
		    &new_module_registry->registry,
		    &old_module_registry->registry
	    )) {
		yanet_error_add(err, "failed to copy module registry");
		return -1;
	};

	SET_OFFSET_OF(&new_module_registry->memory_context, memory_context);

	// Mirror each copied item's new generation reference into its own
	// agent.
	//
	// The reference count now counts exactly these registry references,
	// so the per-agent live count and the items' reference counts move in
	// lockstep: both track only references that can outlive a creator.
	for (uint64_t idx = 0; idx < new_module_registry->registry.capacity;
	     ++idx) {
		struct cp_module *module =
			cp_module_registry_get(new_module_registry, idx);
		if (module == NULL) {
			continue;
		}
		struct agent *agent = ADDR_OF(&module->agent);
		if (agent != NULL) {
			agent->loaded_module_count += 1;
		}
	}

	return 0;
}

void
cp_module_registry_fini(struct cp_module_registry *module_registry) {
	// Mirror each remaining item's dropped generation reference into its
	// own agent before releasing it.
	//
	// The zero transition releases nothing: an item that leaves its last
	// registry becomes dangling and is destroyed by its owner's next free
	// attempt, or reclaimed with the agent's arena if that owner is gone.
	// The same allocation-failure guard applies as elsewhere: a registry
	// whose backing storage never allocated reports a nonzero capacity
	// with nothing to walk.
	if (ADDR_OF(&module_registry->registry.items) != NULL) {
		for (uint64_t idx = 0; idx < module_registry->registry.capacity;
		     ++idx) {
			struct cp_module *module =
				cp_module_registry_get(module_registry, idx);
			if (module == NULL) {
				continue;
			}
			struct agent *agent = ADDR_OF(&module->agent);
			if (agent != NULL) {
				agent->loaded_module_count -= 1;
			}
		}
	}

	registry_fini(&module_registry->registry, NULL, NULL);
}

struct cp_module *
cp_module_registry_get(
	struct cp_module_registry *module_registry, uint64_t index
) {
	struct registry_item *item =
		registry_get(&module_registry->registry, index);
	if (item == NULL) {
		return NULL;
	}
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

	if (cmp) {
		return cmp;
	}

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
	struct cp_module *new_module,
	yanet_error **err
) {
	if (registry_item_is_destroying(&new_module->config_item)) {
		yanet_error_add(
			err,
			"module '%s' is being destroyed by its owner; "
			"re-registering it is a use of a freed handle",
			new_module->name
		);
		return -1;
	}

	struct cp_module_cmp_data cmp_data = {
		.type = type,
		.name = name,
	};

	struct cp_module *old_module =
		cp_module_registry_lookup(module_registry, type, name);

	if (counter_registry_link(
		    &new_module->counter_registry,
		    (old_module != NULL) ? &old_module->counter_registry : NULL,
		    err
	    )) {
		yanet_error_add(err, "failed to link counter registry");
		return -1;
	}

	// Link each new runtime registry against the matching-tag registry on
	// the displaced module so per-rule counters survive a config rebuild
	// that leaves the layout unchanged.
	struct cp_module_counter_registry **new_runtime =
		ADDR_OF(&new_module->runtime_counter_registries);
	for (uint64_t idx = 0; idx < new_module->runtime_counter_registry_count;
	     ++idx) {
		struct cp_module_counter_registry *new_entry =
			ADDR_OF(new_runtime + idx);
		struct counter_registry *old_registry = NULL;
		if (old_module != NULL) {
			struct cp_module_counter_registry **old_runtime =
				ADDR_OF(&old_module->runtime_counter_registries
				);
			for (uint64_t old_idx = 0;
			     old_idx <
			     old_module->runtime_counter_registry_count;
			     ++old_idx) {
				struct cp_module_counter_registry *old_entry =
					ADDR_OF(old_runtime + old_idx);
				if (!strncmp(
					    old_entry->tag,
					    new_entry->tag,
					    COUNTER_NAME_LEN
				    )) {
					old_registry = &old_entry->registry;
					break;
				}
			}
		}

		if (counter_registry_link(
			    &new_entry->registry, old_registry, err
		    )) {
			yanet_error_add(
				err,
				"failed to link counter registry '%s'",
				new_entry->tag
			);
			return -1;
		}
	}

	if (registry_replace(
		    &module_registry->registry,
		    cp_module_registry_item_cmp,
		    &cmp_data,
		    &new_module->config_item,
		    NULL,
		    NULL
	    )) {
		yanet_error_add(err, "failed to replace module in registry");
		return -1;
	}

	// Mirror this upsert's generation-reference changes into each
	// affected module's own agent.
	//
	// Gaining a reference through upsert only ever happens here, and
	// losing one to a displacing upsert only ever happens here too, so
	// the per-agent live count must track both in this one place.
	struct agent *new_agent = ADDR_OF(&new_module->agent);
	if (new_agent != NULL) {
		new_agent->loaded_module_count += 1;
	}
	if (old_module != NULL) {
		struct agent *old_agent = ADDR_OF(&old_module->agent);
		if (old_agent != NULL) {
			old_agent->loaded_module_count -= 1;
		}
	}

	return 0;
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

	struct cp_module *old_module =
		cp_module_registry_lookup(module_registry, type, name);

	if (registry_replace(
		    &module_registry->registry,
		    cp_module_registry_item_cmp,
		    &cmp_data,
		    NULL,
		    NULL,
		    NULL
	    )) {
		return -1;
	}

	// Mirror the generation reference this delete drops into the removed
	// module's own agent, matching the decrement upsert performs when it
	// displaces an entry.
	if (old_module != NULL) {
		struct agent *old_agent = ADDR_OF(&old_module->agent);
		if (old_agent != NULL) {
			old_agent->loaded_module_count -= 1;
		}
	}

	return 0;
}

size_t
cp_module_registry_size(struct cp_module_registry *module_registry) {
	return module_registry->registry.capacity;
}
