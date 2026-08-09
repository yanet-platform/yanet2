#include <errno.h>
#include <stdlib.h>
#include <string.h>

#include "fwstate_map_object.h"

#include "common/container_of.h"
#include "common/strutils.h"
#include "controlplane/agent/agent.h"
#include "controlplane/config/cp_object.h"
#include "lib/dataplane/object/object.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwtable.h"
#include "lib/fwstate/types.h"

// Helper to populate an fwmap_config_t with fwstate-specific defaults.
//
// Selects the v4 or v6 key size and key/copy callbacks based on the
// table kind, so every layer of the chain is keyed uniformly.
static void
fwstate_init_config(
	fwmap_config_t *config,
	enum fwtable_kind kind,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	if (index_size == 0) {
		index_size = 1024 * 1024;
	}
	if (extra_bucket_count == 0) {
		extra_bucket_count = 1024;
	}

	if (kind == FWTABLE_KIND_V6) {
		config->key_size = sizeof(struct fw6_state_key);
		config->key_equal_fn_id = FWMAP_KEY_EQUAL_FW6;
		config->copy_key_fn_id = FWMAP_COPY_KEY_FW6;
	} else {
		config->key_size = sizeof(struct fw4_state_key);
		config->key_equal_fn_id = FWMAP_KEY_EQUAL_FW4;
		config->copy_key_fn_id = FWMAP_COPY_KEY_FW4;
	}

	config->value_size = sizeof(struct fw_state_value);
	config->update_value_fn_id = FWMAP_UPDATE_VALUE_FWSTATE;
	config->promote_value_fn_id = FWMAP_PROMOTE_VALUE_FWSTATE;

	config->hash_seed = 0;
	config->hash_fn_id = FWMAP_HASH_FNV1A;

	config->worker_count = worker_count;
	config->index_size = index_size;
	config->extra_bucket_count = extra_bucket_count;
	config->rand_fn_id = FWMAP_RAND_DEFAULT;
}

// Walk a single fwmap layer chain, freeing every layer and zeroing the
// head offset.
static void
fwstate_map_free_chain(fwmap_t **head_off, struct memory_context *ctx) {
	if (*head_off == NULL) {
		return;
	}

	fwmap_t *node = ADDR_OF(head_off);
	while (node != NULL) {
		fwmap_t *next = (fwmap_t *)ADDR_OF(&node->next);
		fwmap_free(node, ctx);
		node = next;
	}
	*head_off = NULL;
}

void
fwstate_map_object_free_table(fwtable_t *table, struct memory_context *ctx) {
	fwtable_free_stale(table, ctx);
	fwstate_map_free_chain(&table->head, ctx);
}

enum fwtable_kind
fwstate_map_object_type_to_kind(const char *type) {
	if (strncmp(type, FWSTATE_MAP_V6_OBJECT_TYPE, OBJECT_TYPE_LEN) == 0) {
		return FWTABLE_KIND_V6;
	}
	return FWTABLE_KIND_V4;
}

struct fwstate_map_object *
fwstate_map_object_new(struct agent *agent) {
	return (struct fwstate_map_object *)memory_balloc(
		&agent->memory_context, sizeof(struct fwstate_map_object)
	);
}

int
fwstate_map_object_init(
	struct fwstate_map_object *self,
	struct agent *agent,
	const char *type,
	const char *name,
	enum fwtable_kind kind,
	yanet_error **err
) {
	memset(self, 0, sizeof(struct fwstate_map_object));
	self->kind = kind;

	if (cp_object_init(&self->cp_object, agent, type, name, err)) {
		return -1;
	}

	return 0;
}

void
fwstate_map_object_fini(struct fwstate_map_object *self) {
	if (self == NULL) {
		return;
	}

	struct agent *agent = ADDR_OF(&self->cp_object.agent);
	if (agent != NULL) {
		fwstate_map_object_free_table(
			&self->table, &agent->memory_context
		);
	}
	cp_object_fini(&self->cp_object);
}

void
fwstate_map_object_free(struct fwstate_map_object *self, struct agent *agent) {
	if (self == NULL) {
		return;
	}
	memory_bfree(
		&agent->memory_context, self, sizeof(struct fwstate_map_object)
	);
}

struct cp_object *
fwstate_map_object_config_new(
	struct agent *agent,
	const char *type,
	const char *name,
	enum fwtable_kind kind,
	yanet_error **err
) {
	struct fwstate_map_object *self = fwstate_map_object_new(agent);
	if (self == NULL) {
		yanet_error_add(err, "failed to allocate fwstate-map object");
		return NULL;
	}

	if (fwstate_map_object_init(self, agent, type, name, kind, err)) {
		yanet_error_add(err, "failed to init fwstate-map object");
		fwstate_map_object_free(self, agent);
		return NULL;
	}

	return &self->cp_object;
}

void
fwstate_map_object_config_free(struct cp_object *cp_object) {
	struct fwstate_map_object *self =
		container_of(cp_object, struct fwstate_map_object, cp_object);

	struct agent *agent = ADDR_OF(&cp_object->agent);

	fwstate_map_object_fini(self);
	fwstate_map_object_free(self, agent);
}

fwtable_t *
fwstate_map_object_table(const struct cp_object *cp_object) {
	struct fwstate_map_object *self =
		container_of(cp_object, struct fwstate_map_object, cp_object);

	return &self->table;
}

enum fwtable_kind
fwstate_map_object_kind(const struct cp_object *cp_object) {
	struct fwstate_map_object *self =
		container_of(cp_object, struct fwstate_map_object, cp_object);

	return self->kind;
}

int
fwstate_map_object_create_map(
	struct fwstate_map_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	if (worker_count == 0) {
		errno = EINVAL;
		return -1;
	}

	struct agent *agent = ADDR_OF(&self->cp_object.agent);

	fwmap_config_t fwmap_config;
	fwstate_init_config(
		&fwmap_config,
		self->kind,
		index_size,
		extra_bucket_count,
		worker_count
	);

	return fwtable_insert_layer_cp(
		&self->table, &fwmap_config, &agent->memory_context
	);
}

int
fwstate_map_object_insert_layer(
	struct fwstate_map_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	if (worker_count == 0) {
		errno = EINVAL;
		return -1;
	}

	struct agent *agent = ADDR_OF(&self->cp_object.agent);

	fwmap_config_t fwmap_config;
	fwstate_init_config(
		&fwmap_config,
		self->kind,
		index_size,
		extra_bucket_count,
		worker_count
	);

	return fwtable_insert_layer_cp(
		&self->table, &fwmap_config, &agent->memory_context
	);
}

int
fwstate_map_object_trim_stale_layers(
	struct fwstate_map_object *self, uint64_t now
) {
	struct agent *agent = ADDR_OF(&self->cp_object.agent);

	return fwtable_trim_stale_cp(&self->table, &agent->memory_context, now);
}

struct object *
new_object_fwstate_map_v4() {
	struct object *object = (struct object *)malloc(sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name, FWSTATE_MAP_V4_OBJECT_TYPE, sizeof(object->name));
	return object;
}

struct object *
new_object_fwstate_map_v6() {
	struct object *object = (struct object *)malloc(sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name, FWSTATE_MAP_V6_OBJECT_TYPE, sizeof(object->name));
	return object;
}
