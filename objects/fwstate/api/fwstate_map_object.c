#include <errno.h>
#include <stdlib.h>
#include <string.h>

#include "fwstate_map_v4_object.h"
#include "fwstate_map_v6_object.h"

#include "common/container_of.h"
#include "common/strutils.h"
#include "controlplane/agent/agent.h"
#include "controlplane/config/cp_object.h"
#include "lib/dataplane/object/object.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwtable.h"
#include "lib/fwstate/types.h"

// Shared helpers: the v4 and v6 objects share an identical
// {cp_object, fwtable_t} layout and differ only in the fwmap_config used
// to grow the layer chain (key size and key/copy callbacks). The helpers
// below carry the family-agnostic machinery; each per-family function is
// a thin wrapper that selects its key initializer.

typedef void (*map_init_keys_fn)(fwmap_config_t *config);

// Walk a single fwmap layer chain, freeing every layer and zeroing the
// head offset.
static void
map_free_chain(fwmap_t **head_off, struct memory_context *ctx) {
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

// Free one fwtable's chains (head and stale) and zero the table.
static void
map_free_table(fwtable_t *table, struct memory_context *ctx) {
	fwtable_free_stale(table, ctx);
	map_free_chain(&table->head, ctx);
}

// Populate the fwmap_config fields common to v4 and v6 (value shape,
// hashing, sizing). The family-specific key fields are set by init_keys.
static void
map_init_common_config(
	fwmap_config_t *config,
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

static void
map_v4_init_keys(fwmap_config_t *config) {
	config->key_size = sizeof(struct fw4_state_key);
	config->key_equal_fn_id = FWMAP_KEY_EQUAL_FW4;
	config->copy_key_fn_id = FWMAP_COPY_KEY_FW4;
}

static void
map_v6_init_keys(fwmap_config_t *config) {
	config->key_size = sizeof(struct fw6_state_key);
	config->key_equal_fn_id = FWMAP_KEY_EQUAL_FW6;
	config->copy_key_fn_id = FWMAP_COPY_KEY_FW6;
}

// Append one layer built with the given key initializer. Returns 0 on
// success or -1 on error.
static int
map_insert_layer(
	fwtable_t *table,
	struct memory_context *ctx,
	map_init_keys_fn init_keys,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	if (worker_count == 0) {
		errno = EINVAL;
		return -1;
	}

	fwmap_config_t config;
	map_init_common_config(
		&config, index_size, extra_bucket_count, worker_count
	);
	init_keys(&config);

	return fwtable_insert_layer_cp(table, &config, ctx);
}

// --- IPv4 object -------------------------------------------------------------

struct fwstate_map_v4_object *
fwstate_map_v4_object_new(struct agent *agent) {
	return (struct fwstate_map_v4_object *)memory_balloc(
		&agent->memory_context, sizeof(struct fwstate_map_v4_object)
	);
}

int
fwstate_map_v4_object_init(
	struct fwstate_map_v4_object *self,
	struct agent *agent,
	const char *name,
	yanet_error **err
) {
	memset(self, 0, sizeof(struct fwstate_map_v4_object));

	return cp_object_init(
		&self->cp_object, agent, FWSTATE_MAP_V4_OBJECT_TYPE, name, err
	);
}

void
fwstate_map_v4_object_fini(struct fwstate_map_v4_object *self) {
	if (self == NULL) {
		return;
	}

	struct agent *agent = ADDR_OF(&self->cp_object.agent);
	if (agent != NULL) {
		map_free_table(&self->table, &agent->memory_context);
	}
	cp_object_fini(&self->cp_object);
}

void
fwstate_map_v4_object_free(
	struct fwstate_map_v4_object *self, struct agent *agent
) {
	if (self == NULL) {
		return;
	}
	memory_bfree(
		&agent->memory_context,
		self,
		sizeof(struct fwstate_map_v4_object)
	);
}

struct cp_object *
fwstate_map_v4_object_config_new(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct fwstate_map_v4_object *self = fwstate_map_v4_object_new(agent);
	if (self == NULL) {
		yanet_error_add(
			err, "failed to allocate fwstate-map v4 object"
		);
		return NULL;
	}

	if (fwstate_map_v4_object_init(self, agent, name, err)) {
		yanet_error_add(err, "failed to init fwstate-map v4 object");
		fwstate_map_v4_object_free(self, agent);
		return NULL;
	}

	return &self->cp_object;
}

void
fwstate_map_v4_object_config_free(struct cp_object *cp_object) {
	struct fwstate_map_v4_object *self = container_of(
		cp_object, struct fwstate_map_v4_object, cp_object
	);

	struct agent *agent = ADDR_OF(&cp_object->agent);

	fwstate_map_v4_object_fini(self);
	fwstate_map_v4_object_free(self, agent);
}

fwtable_t *
fwstate_map_v4_object_table(const struct cp_object *cp_object) {
	struct fwstate_map_v4_object *self = container_of(
		cp_object, struct fwstate_map_v4_object, cp_object
	);

	return &self->table;
}

int
fwstate_map_v4_object_insert_layer(
	struct fwstate_map_v4_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	struct agent *agent = ADDR_OF(&self->cp_object.agent);

	return map_insert_layer(
		&self->table,
		&agent->memory_context,
		map_v4_init_keys,
		index_size,
		extra_bucket_count,
		worker_count
	);
}

int
fwstate_map_v4_object_trim_stale_layers(
	struct fwstate_map_v4_object *self, uint64_t now
) {
	struct agent *agent = ADDR_OF(&self->cp_object.agent);

	return fwtable_trim_stale_cp(&self->table, &agent->memory_context, now);
}

// --- IPv6 object -------------------------------------------------------------

struct fwstate_map_v6_object *
fwstate_map_v6_object_new(struct agent *agent) {
	return (struct fwstate_map_v6_object *)memory_balloc(
		&agent->memory_context, sizeof(struct fwstate_map_v6_object)
	);
}

int
fwstate_map_v6_object_init(
	struct fwstate_map_v6_object *self,
	struct agent *agent,
	const char *name,
	yanet_error **err
) {
	memset(self, 0, sizeof(struct fwstate_map_v6_object));

	return cp_object_init(
		&self->cp_object, agent, FWSTATE_MAP_V6_OBJECT_TYPE, name, err
	);
}

void
fwstate_map_v6_object_fini(struct fwstate_map_v6_object *self) {
	if (self == NULL) {
		return;
	}

	struct agent *agent = ADDR_OF(&self->cp_object.agent);
	if (agent != NULL) {
		map_free_table(&self->table, &agent->memory_context);
	}
	cp_object_fini(&self->cp_object);
}

void
fwstate_map_v6_object_free(
	struct fwstate_map_v6_object *self, struct agent *agent
) {
	if (self == NULL) {
		return;
	}
	memory_bfree(
		&agent->memory_context,
		self,
		sizeof(struct fwstate_map_v6_object)
	);
}

struct cp_object *
fwstate_map_v6_object_config_new(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct fwstate_map_v6_object *self = fwstate_map_v6_object_new(agent);
	if (self == NULL) {
		yanet_error_add(
			err, "failed to allocate fwstate-map v6 object"
		);
		return NULL;
	}

	if (fwstate_map_v6_object_init(self, agent, name, err)) {
		yanet_error_add(err, "failed to init fwstate-map v6 object");
		fwstate_map_v6_object_free(self, agent);
		return NULL;
	}

	return &self->cp_object;
}

void
fwstate_map_v6_object_config_free(struct cp_object *cp_object) {
	struct fwstate_map_v6_object *self = container_of(
		cp_object, struct fwstate_map_v6_object, cp_object
	);

	struct agent *agent = ADDR_OF(&cp_object->agent);

	fwstate_map_v6_object_fini(self);
	fwstate_map_v6_object_free(self, agent);
}

fwtable_t *
fwstate_map_v6_object_table(const struct cp_object *cp_object) {
	struct fwstate_map_v6_object *self = container_of(
		cp_object, struct fwstate_map_v6_object, cp_object
	);

	return &self->table;
}

int
fwstate_map_v6_object_insert_layer(
	struct fwstate_map_v6_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	struct agent *agent = ADDR_OF(&self->cp_object.agent);

	return map_insert_layer(
		&self->table,
		&agent->memory_context,
		map_v6_init_keys,
		index_size,
		extra_bucket_count,
		worker_count
	);
}

int
fwstate_map_v6_object_trim_stale_layers(
	struct fwstate_map_v6_object *self, uint64_t now
) {
	struct agent *agent = ADDR_OF(&self->cp_object.agent);

	return fwtable_trim_stale_cp(&self->table, &agent->memory_context, now);
}

// --- object factories --------------------------------------------------------

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
