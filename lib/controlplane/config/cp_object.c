#include "cp_object.h"

#include "common/container_of.h"
#include "lib/dataplane/config/zone.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_entity.h"
#include "lib/controlplane/config/zone.h"

#include <stddef.h>
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

// The item-to-owner mapping for the generic registry wrapper layer,
// including its allocation-free teardown fallback.
static struct agent *
object_item_owner(struct registry_item *item) {
	struct cp_object *object =
		container_of(item, struct cp_object, config_item);
	return ADDR_OF(&object->agent);
}

static int
cp_object_registry_item_cmp(const struct registry_item *item, const void *data);
static uint64_t
cp_object_registry_key_hash(const void *data);
static uint64_t
cp_object_registry_item_hash(const struct registry_item *item);

// Counter continuity of an object upsert: both the object's own counters
// and its link counters continue from the displaced object's.
static int
cp_object_link_counters(
	struct registry_item *new_item,
	struct registry_item *old_item,
	yanet_error **err
) {
	struct cp_object *new_object =
		container_of(new_item, struct cp_object, config_item);
	struct cp_object *old_object =
		(old_item != NULL)
			? container_of(old_item, struct cp_object, config_item)
			: NULL;

	if (counter_registry_link(
		    &new_object->counter_registry,
		    (old_object != NULL) ? &old_object->counter_registry : NULL,
		    err
	    )) {
		yanet_error_add(
			err,
			"failed to link counter registry for object '%s:%s'",
			new_object->type,
			new_object->name
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
			new_object->type,
			new_object->name
		);
		return -1;
	}

	return 0;
}

static const struct cp_entity_desc cp_object_entity_desc = {
	.keys =
		{
			.cmp = cp_object_registry_item_cmp,
			.key_hash = cp_object_registry_key_hash,
			.item_hash = cp_object_registry_item_hash,
		},
	.noun = "object",
	.item_owner = object_item_owner,
	.name_offset = offsetof(struct cp_object, name),
	.name_len = CP_OBJECT_NAME_LEN,
	.type_offset = offsetof(struct cp_object, type),
	.type_len = CP_OBJECT_TYPE_LEN,
	.link_counters = cp_object_link_counters,
};

static int
cp_object_registry_item_cmp(
	const struct registry_item *item, const void *data
) {
	return cp_entity_item_cmp(&cp_object_entity_desc, item, data);
}

static uint64_t
cp_object_registry_key_hash(const void *data) {
	return cp_entity_key_hash(&cp_object_entity_desc, data);
}

static uint64_t
cp_object_registry_item_hash(const struct registry_item *item) {
	return cp_entity_item_hash(&cp_object_entity_desc, item);
}

int
cp_object_registry_init(
	struct memory_context *memory_context,
	struct cp_config *owner,
	struct cp_object_registry *new_registry,
	yanet_error **err
) {
	if (cp_entity_registry_init(
		    memory_context,
		    owner,
		    &new_registry->registry,
		    &cp_object_entity_desc,
		    err
	    )) {
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
	if (cp_entity_registry_copy(
		    memory_context,
		    &new_registry->registry,
		    &old_registry->registry,
		    &cp_object_entity_desc,
		    err
	    )) {
		return -1;
	}

	SET_OFFSET_OF(&new_registry->memory_context, memory_context);
	return 0;
}

int
cp_object_try_destroy(struct cp_object *self, yanet_error **err) {
	return cp_entity_try_destroy(
		&self->config_item, &cp_object_entity_desc, err
	);
}

void
cp_object_registry_fini(struct cp_object_registry *registry) {
	cp_entity_registry_fini(&registry->registry, &cp_object_entity_desc);
}

struct cp_object *
cp_object_registry_get(struct cp_object_registry *registry, uint64_t index) {
	struct registry_item *item = registry_get(&registry->registry, index);
	if (item == NULL) {
		return NULL;
	}
	return container_of(item, struct cp_object, config_item);
}

int
cp_object_registry_lookup_index(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name,
	uint64_t *index
) {
	struct cp_entity_key key;
	cp_entity_key_set(&key, object_type, object_name);

	return cp_entity_registry_lookup_index(
		&registry->registry, &cp_object_entity_desc, &key, index
	);
}

struct cp_object *
cp_object_registry_lookup(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name
) {
	struct cp_entity_key key;
	cp_entity_key_set(&key, object_type, object_name);

	struct registry_item *item = cp_entity_registry_lookup(
		&registry->registry, &cp_object_entity_desc, &key
	);
	if (item == NULL) {
		return NULL;
	}
	return container_of(item, struct cp_object, config_item);
}

int
cp_object_registry_upsert(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name,
	struct cp_object *new_object,
	yanet_error **err
) {
	struct cp_entity_key key;
	cp_entity_key_set(&key, object_type, object_name);

	return cp_entity_registry_upsert(
		&registry->registry,
		&cp_object_entity_desc,
		&key,
		&new_object->config_item,
		err
	);
}

int
cp_object_registry_delete(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name
) {
	struct cp_entity_key key;
	cp_entity_key_set(&key, object_type, object_name);

	return cp_entity_registry_delete(
		&registry->registry,
		&cp_object_entity_desc,
		cp_object_entity_desc.keys.cmp,
		&key
	);
}
