#include "cp_object.h"

#include "common/container_of.h"
#include "dataplane/config/zone.h"

#include "controlplane/agent/agent.h"

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

	return 0;

err_out:
	cp_object_fini(self);
	return -1;
}

void
cp_object_fini(struct cp_object *self) {
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
	return 0;
}

static void
cp_object_registry_item_free_cb(struct registry_item *item, void *data) {
	(void)data;
	struct cp_object *object =
		container_of(item, struct cp_object, config_item);

	// Registry membership is not ownership: an object leaving the registry
	// is not freed here.
	//
	// The object lives in its creating agent's arena and is reclaimed
	// wholesale when the agent is reclaimed. agent_attach /
	// agent_free_unused_agents gate that reclamation on every
	// loaded_*_count reaching zero, so the agent is not torn down while any
	// of its objects still holds a reference from a live generation.
	struct agent *agent = ADDR_OF(&object->agent);
	if (agent != NULL) {
		agent->loaded_object_count -= 1;
	}
}

void
cp_object_registry_fini(struct cp_object_registry *registry) {
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

	return strncmp(object->name, (const char *)data, CP_OBJECT_NAME_LEN);
}

int
cp_object_registry_lookup_index(
	struct cp_object_registry *registry, const char *name, uint64_t *index
) {
	return registry_lookup(
		&registry->registry, cp_object_registry_item_cmp, name, index
	);
}

struct cp_object *
cp_object_registry_lookup(
	struct cp_object_registry *registry, const char *name
) {
	uint64_t index;
	if (cp_object_registry_lookup_index(registry, name, &index)) {
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
	const char *name,
	struct cp_object *new_object,
	yanet_error **err
) {
	struct cp_object *old_object =
		cp_object_registry_lookup(registry, name);

	if (counter_registry_link(
		    &new_object->counter_registry,
		    (old_object != NULL) ? &old_object->counter_registry : NULL,
		    err
	    )) {
		yanet_error_add(
			err,
			"failed to link counter registry for object '%s'",
			name
		);
		return -1;
	}

	// Count the object only on its first registry reference, mirroring the
	// 1->0 transition at which cp_object_registry_item_free_cb decrements;
	// a re-upsert of an already-referenced instance must not double-count.
	uint64_t refcnt_before = new_object->config_item.refcnt;

	if (registry_replace(
		    &registry->registry,
		    cp_object_registry_item_cmp,
		    name,
		    &new_object->config_item,
		    cp_object_registry_item_free_cb,
		    NULL
	    )) {
		yanet_error_add(err, "failed to replace object in registry");
		return -1;
	}

	struct agent *agent = ADDR_OF(&new_object->agent);
	if (agent != NULL && refcnt_before == 0) {
		agent->loaded_object_count += 1;
	}

	return 0;
}

int
cp_object_registry_delete(
	struct cp_object_registry *registry, const char *name
) {
	return registry_replace(
		&registry->registry,
		cp_object_registry_item_cmp,
		name,
		NULL,
		cp_object_registry_item_free_cb,
		NULL
	);
}
