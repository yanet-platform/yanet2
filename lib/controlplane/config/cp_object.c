#include "cp_object.h"

#include "common/container_of.h"
#include "lib/dataplane/config/zone.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"

#include <string.h>

int
cp_object_init(
	struct cp_object *self,
	struct agent *agent,
	const char *object_type,
	const char *name,
	yanet_error **err
) {
	memset(self, 0, sizeof(struct cp_object));

	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	if (dp_config_lookup_object(
		    dp_config, object_type, &self->dp_object_idx
	    )) {
		yanet_error_add(
			err,
			"object type '%s' not found in dataplane config",
			object_type
		);
		goto err_out;
	}

	strtcpy(self->type, object_type, sizeof(self->type));
	strtcpy(self->name, name, CP_OBJECT_NAME_LEN);

	memory_context_init_from(
		&self->memory_context, &agent->memory_context, name
	);

	registry_item_init(&self->config_item);

	SET_OFFSET_OF(&self->agent, agent);

	if (counter_registry_init(
		    &self->counter_registry, &self->memory_context, 0
	    )) {
		yanet_error_add(
			err,
			"failed to initialize counter registry for object '%s'",
			name
		);
		goto err_out;
	}

	if (counter_registry_init(
		    &self->link_counter_registry, &self->memory_context, 0
	    )) {
		yanet_error_add(
			err,
			"failed to initialize link counter registry for object "
			"'%s'",
			name
		);
		goto err_out;
	}

	// Take the creator's reference only after construction succeeds.
	//
	// A failed construction leaves nothing to unbalance. This reference
	// is never mirrored into the live-reference count: a dead creator
	// must not hold it above zero.
	registry_item_ref(&self->config_item);

	return 0;

err_out:
	cp_object_fini(self);
	return -1;
}

void
cp_object_fini(struct cp_object *self) {
	counter_registry_fini(&self->link_counter_registry);
	counter_registry_fini(&self->counter_registry);

	SET_OFFSET_OF(&self->agent, NULL);

	memory_context_fini(&self->memory_context);
}

int
cp_object_registry_init(
	struct memory_context *memory_context,
	struct cp_object_registry *new_registry,
	yanet_error **err
) {
	if (registry_init(memory_context, &new_registry->registry, 8)) {
		yanet_error_add(err, "failed to initialize object registry");
		return -1;
	}

	SET_OFFSET_OF(&new_registry->memory_context, memory_context);
	return 0;
}

int
cp_object_registry_copy(
	struct memory_context *memory_context,
	struct cp_object_registry *new_registry,
	struct cp_object_registry *old_registry,
	yanet_error **err
) {
	if (registry_copy(
		    memory_context,
		    &new_registry->registry,
		    &old_registry->registry
	    )) {
		yanet_error_add(err, "failed to copy object registry");
		return -1;
	}

	SET_OFFSET_OF(&new_registry->memory_context, memory_context);

	// Mirror each copied item's new generation reference into its own
	// agent.
	//
	// This keeps the per-agent live count tracking references from a
	// live configuration generation rather than the creator's own hold,
	// which a dead creator would otherwise never release.
	for (uint64_t idx = 0; idx < new_registry->registry.capacity; ++idx) {
		struct cp_object *object =
			cp_object_registry_get(new_registry, idx);
		if (object == NULL) {
			continue;
		}
		struct agent *agent = ADDR_OF(&object->agent);
		if (agent != NULL) {
			agent->loaded_object_count += 1;
		}
	}

	return 0;
}

void
cp_object_registry_item_free_cb(struct registry_item *item, void *data) {
	struct cp_object *object =
		container_of(item, struct cp_object, config_item);
	struct agent *agent = ADDR_OF(&object->agent);
	if (agent == NULL) {
		return;
	}

	if (ADDR_OF(&object->parked_next) != NULL) {
		return;
	}

	struct cp_object *head = ADDR_OF(&agent->parked_objects);
	SET_OFFSET_OF(&object->parked_next, (head != NULL) ? head : object);
	SET_OFFSET_OF(&agent->parked_objects, object);
	(void)data;
}

void
cp_object_release(struct cp_object *self) {
	struct agent *agent = ADDR_OF(&self->agent);
	struct cp_config *cp_config =
		(agent != NULL) ? ADDR_OF(&agent->cp_config) : NULL;

	if (cp_config != NULL) {
		cp_config_lock(cp_config);
	}
	registry_item_unref(
		&self->config_item, cp_object_registry_item_free_cb, NULL
	);
	if (cp_config != NULL) {
		cp_config_unlock(cp_config);
	}
}

void
cp_object_drain_parked(
	struct agent *agent,
	const char *object_type,
	cp_object_free_handler destroy
) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	cp_config_lock(cp_config);

	// Splice the target type's entries into a local list, leaving the
	// rest linked in place for their own type's next call.
	//
	// A spliced-out tail leaves its former predecessor pointing at itself
	// as the new end of the list, so a parked entry's link is never null.
	struct cp_object *owned = NULL;
	struct cp_object *prev = NULL;
	struct cp_object *cur = ADDR_OF(&agent->parked_objects);

	while (cur != NULL) {
		struct cp_object *raw_next = ADDR_OF(&cur->parked_next);
		struct cp_object *next = (raw_next == cur) ? NULL : raw_next;

		if (!strncmp(cur->type, object_type, sizeof(cur->type))) {
			if (prev == NULL) {
				SET_OFFSET_OF(&agent->parked_objects, next);
			} else {
				SET_OFFSET_OF(
					&prev->parked_next,
					(next != NULL) ? next : prev
				);
			}
			SET_OFFSET_OF(&cur->parked_next, owned);
			owned = cur;
		} else {
			prev = cur;
		}

		cur = next;
	}

	if (owned == NULL) {
		cp_config_unlock(cp_config);
		return;
	}

	// Pin the agent's arena for the destroy loop below, still under the
	// same lock the splice above ran under, so the reclaim guard can never
	// observe the entries detached but the pin not yet set.
	agent->parked_teardown_count += 1;

	cp_config_unlock(cp_config);

	while (owned != NULL) {
		struct cp_object *next = ADDR_OF(&owned->parked_next);
		destroy(owned);
		owned = next;
	}

	cp_config_lock(cp_config);
	agent->parked_teardown_count -= 1;
	cp_config_unlock(cp_config);
}

void
cp_object_registry_fini(struct cp_object_registry *registry) {
	// Mirror each remaining item's dropped generation reference into its
	// own agent before releasing it.
	//
	// The callback this teardown invokes only parks each item at zero
	// references instead of adjusting the live count itself, so this
	// loop mirrors the drop first. The same allocation-failure guard
	// applies as elsewhere: a registry whose backing storage never
	// allocated reports a nonzero capacity with nothing to walk.
	if (ADDR_OF(&registry->registry.items) != NULL) {
		for (uint64_t idx = 0; idx < registry->registry.capacity;
		     ++idx) {
			struct cp_object *object =
				cp_object_registry_get(registry, idx);
			if (object == NULL) {
				continue;
			}
			struct agent *agent = ADDR_OF(&object->agent);
			if (agent != NULL) {
				agent->loaded_object_count -= 1;
			}
		}
	}

	registry_fini(
		&registry->registry, cp_object_registry_item_free_cb, NULL
	);
}

struct cp_object *
cp_object_registry_get(struct cp_object_registry *registry, uint64_t index) {
	struct registry_item *item = registry_get(&registry->registry, index);
	if (item == NULL) {
		return NULL;
	}
	return container_of(item, struct cp_object, config_item);
}

static int
cp_object_registry_item_cmp(
	const struct registry_item *item, const void *data
) {
	const struct cp_object *object =
		container_of(item, struct cp_object, config_item);

	const struct cp_object_cmp_data *cmp_data =
		(const struct cp_object_cmp_data *)data;

	int cmp = strncmp(object->name, cmp_data->name, sizeof(object->name));
	if (cmp) {
		return cmp;
	}

	return strncmp(object->type, cmp_data->type, sizeof(object->type));
}

int
cp_object_registry_lookup_index(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name,
	uint64_t *index
) {
	struct cp_object_cmp_data cmp_data;
	strtcpy(cmp_data.type, object_type, sizeof(cmp_data.type));
	strtcpy(cmp_data.name, object_name, sizeof(cmp_data.name));

	return registry_lookup(
		&registry->registry,
		cp_object_registry_item_cmp,
		&cmp_data,
		index
	);
}

struct cp_object *
cp_object_registry_lookup(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name
) {
	uint64_t index;
	if (cp_object_registry_lookup_index(
		    registry, object_type, object_name, &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&registry->registry, index),
		struct cp_object,
		config_item
	);
}

int
cp_object_registry_upsert(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name,
	struct cp_object *new_object,
	yanet_error **err
) {
	struct cp_object *old_object =
		cp_object_registry_lookup(registry, object_type, object_name);

	if (counter_registry_link(
		    &new_object->counter_registry,
		    (old_object != NULL) ? &old_object->counter_registry : NULL,
		    err
	    )) {
		yanet_error_add(
			err,
			"failed to link counter registry for object '%s:%s'",
			object_type,
			object_name
		);
		return -1;
	}

	if (counter_registry_link(
		    &new_object->link_counter_registry,
		    (old_object != NULL) ? &old_object->link_counter_registry
					 : NULL,
		    err
	    )) {
		yanet_error_add(
			err,
			"failed to link link counter registry for object "
			"'%s:%s'",
			object_type,
			object_name
		);
		return -1;
	}

	// Mirror this upsert's generation-reference changes into each
	// affected object's own agent.
	//
	// Gaining a reference through upsert only ever happens here, and
	// losing one to a displacing upsert only ever happens here too, so
	// the per-agent live count must track both in this one place.
	struct cp_object_cmp_data cmp_data;
	strtcpy(cmp_data.type, object_type, sizeof(cmp_data.type));
	strtcpy(cmp_data.name, object_name, sizeof(cmp_data.name));

	if (registry_replace(
		    &registry->registry,
		    cp_object_registry_item_cmp,
		    &cmp_data,
		    &new_object->config_item,
		    cp_object_registry_item_free_cb,
		    NULL
	    )) {
		yanet_error_add(err, "failed to replace object in registry");
		return -1;
	}

	struct agent *new_agent = ADDR_OF(&new_object->agent);
	if (new_agent != NULL) {
		new_agent->loaded_object_count += 1;
	}
	if (old_object != NULL) {
		struct agent *old_agent = ADDR_OF(&old_object->agent);
		if (old_agent != NULL) {
			old_agent->loaded_object_count -= 1;
		}
	}

	return 0;
}

int
cp_object_registry_delete(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name
) {
	struct cp_object *old_object =
		cp_object_registry_lookup(registry, object_type, object_name);

	struct cp_object_cmp_data cmp_data;
	strtcpy(cmp_data.type, object_type, sizeof(cmp_data.type));
	strtcpy(cmp_data.name, object_name, sizeof(cmp_data.name));

	if (registry_replace(
		    &registry->registry,
		    cp_object_registry_item_cmp,
		    &cmp_data,
		    NULL,
		    cp_object_registry_item_free_cb,
		    NULL
	    )) {
		return -1;
	}

	// Mirror the generation reference this delete drops into the removed
	// object's own agent, matching the decrement upsert performs when it
	// displaces an entry.
	if (old_object != NULL) {
		struct agent *old_agent = ADDR_OF(&old_object->agent);
		if (old_agent != NULL) {
			old_agent->loaded_object_count -= 1;
		}
	}

	return 0;
}
