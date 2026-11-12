#include <errno.h>
#include <stdlib.h>
#include <string.h>

#include "fwstate_map_nat64_object.h"
#include "fwstate_map_v4_object.h"
#include "fwstate_map_v6_object.h"

#include "common/container_of.h"
#include "common/strutils.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/object/object.h"
#include "lib/fwstate/nat64state.h"
#include "lib/fwstate/ops.h"
#include "lib/fwstate/stash.h"
#include "lib/fwstate/types.h"
#include "lib/statemap/fwtable.h"

// Shared helpers: the v4 and v6 objects share an identical
// {cp_object, fwtable_t} layout and differ only in the fwmap_config used
// to grow the layer chain (key size and key/copy callbacks). The helpers
// below carry the family-agnostic machinery; each per-family function is
// a thin wrapper that selects its key initializer.
//
// Layer memory is allocated and freed through the object's own memory
// context, so the per-object accounting attributes the fwtable stores to
// the map object rather than to the agent.

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

	config->hash_seed = 0;
	config->hash_fn_id = FWMAP_HASH_FNV1A;

	config->worker_count = worker_count;
	config->index_size = index_size;
	config->extra_bucket_count = extra_bucket_count;
	config->rand_fn_id = FWMAP_RAND_DEFAULT;
}

static void
map_v4_init_keys(fwmap_config_t *config) {
	config->value_size = sizeof(struct fw_state_value);
	config->update_value_fn_id = FWMAP_UPDATE_VALUE_FWSTATE;
	config->promote_value_fn_id = FWMAP_PROMOTE_VALUE_FWSTATE;

	config->key_size = sizeof(struct fw4_state_key);
	config->key_equal_fn_id = FWMAP_KEY_EQUAL_FW4;
	config->copy_key_fn_id = FWMAP_COPY_KEY_FW4;
}

static void
map_v6_init_keys(fwmap_config_t *config) {
	config->value_size = sizeof(struct fw_state_value);
	config->update_value_fn_id = FWMAP_UPDATE_VALUE_FWSTATE;
	config->promote_value_fn_id = FWMAP_PROMOTE_VALUE_FWSTATE;

	config->key_size = sizeof(struct fw6_state_key);
	config->key_equal_fn_id = FWMAP_KEY_EQUAL_FW6;
	config->copy_key_fn_id = FWMAP_COPY_KEY_FW6;
}

// The nat64state families key their tables with POD structs whose
// padding must not fold into key identity, so both select the semantic
// hash and equality callbacks; the value shape is shared.
static void
map_nat64_lan_init_keys(fwmap_config_t *config) {
	config->value_size = sizeof(struct nat64state_value);
	config->update_value_fn_id = FWMAP_UPDATE_VALUE_NAT64STATE;
	config->promote_value_fn_id = FWMAP_PROMOTE_VALUE_NAT64STATE;

	config->key_size = sizeof(struct nat64state_lan_key);
	config->hash_fn_id = FWMAP_HASH_NAT64STATE_LAN;
	config->key_equal_fn_id = FWMAP_KEY_EQUAL_NAT64STATE_LAN;
	config->copy_key_fn_id = FWMAP_COPY_KEY_DEFAULT;
}

static void
map_nat64_wan_init_keys(fwmap_config_t *config) {
	config->value_size = sizeof(struct nat64state_value);
	config->update_value_fn_id = FWMAP_UPDATE_VALUE_NAT64STATE;
	config->promote_value_fn_id = FWMAP_PROMOTE_VALUE_NAT64STATE;

	config->key_size = sizeof(struct nat64state_wan_key);
	config->hash_fn_id = FWMAP_HASH_NAT64STATE_WAN;
	config->key_equal_fn_id = FWMAP_KEY_EQUAL_NAT64STATE_WAN;
	config->copy_key_fn_id = FWMAP_COPY_KEY_DEFAULT;
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

// Free every allocated buffer and the header array of a stash and forget
// it. Headers whose buffer was never allocated are skipped, so it also
// unwinds a partially created stash.
static void
map_stash_free(struct fwstate_stash *stash, struct memory_context *ctx) {
	struct fwstate_stash_slot *slots = ADDR_OF(&stash->slots);
	if (slots != NULL) {
		for (uint16_t idx = 0; idx < stash->worker_count; ++idx) {
			void *buffer = ADDR_OF(&slots[idx].buffer);
			if (buffer != NULL) {
				fwstate_stash_zfree(ctx, buffer, stash->size);
			}
		}
		fwstate_stash_zfree(
			ctx,
			slots,
			sizeof(struct fwstate_stash_slot) * stash->worker_count
		);
	}
	memset(stash, 0, sizeof(*stash));
}

// Allocate a stash of worker_count zeroed slot headers, each with its own
// zeroed buffer of size bytes, from the object's memory context. A failed
// allocation frees what was allocated and leaves the stash empty.
static int
map_stash_create(
	struct fwstate_stash *stash,
	struct memory_context *ctx,
	uint16_t worker_count,
	uint64_t size
) {
	if (size == 0) {
		size = FWSTATE_STASH_DEFAULT_SIZE;
	}
	if (worker_count == 0 || size < FWSTATE_STASH_MIN_SIZE ||
	    size > FWSTATE_STASH_MAX_SIZE) {
		errno = EINVAL;
		return -1;
	}

	// Bounded by the checks above: at most 65535 headers, so the header
	// array size does not overflow a size_t.
	struct fwstate_stash_slot *slots = fwstate_stash_zalloc(
		ctx, sizeof(struct fwstate_stash_slot) * worker_count
	);
	if (slots == NULL) {
		errno = ENOMEM;
		return -1;
	}
	stash->worker_count = worker_count;
	stash->size = size;
	SET_OFFSET_OF(&stash->slots, slots);

	for (uint16_t idx = 0; idx < worker_count; ++idx) {
		void *buffer = fwstate_stash_zalloc(ctx, size);
		if (buffer == NULL) {
			map_stash_free(stash, ctx);
			errno = ENOMEM;
			return -1;
		}
		SET_OFFSET_OF(&slots[idx].buffer, buffer);
	}
	return 0;
}

// Create a map: its stash, then its first layer. A failure unwinds
// whatever the call allocated.
static int
map_create(
	fwtable_t *table,
	struct fwstate_stash *stash,
	struct memory_context *ctx,
	map_init_keys_fn init_keys,
	const struct fwstate_map_create_config *config
) {
	if (ADDR_OF(&stash->slots) != NULL || ADDR_OF(&table->head) != NULL) {
		errno = EEXIST;
		return -1;
	}
	if (map_stash_create(
		    stash, ctx, config->worker_count, config->stash_size
	    )) {
		return -1;
	}
	if (map_insert_layer(
		    table,
		    ctx,
		    init_keys,
		    config->index_size,
		    config->extra_bucket_count,
		    config->worker_count
	    )) {
		int saved_errno = errno;
		map_stash_free(stash, ctx);
		errno = saved_errno;
		return -1;
	}
	return 0;
}

static struct fwstate_stash_slot *
map_stash_slot(const struct fwstate_stash *stash, uint16_t worker_idx) {
	struct fwstate_stash_slot *slots = ADDR_OF(&stash->slots);
	if (slots == NULL) {
		return NULL;
	}
	return slots + worker_idx;
}

static void
map_stash_link(
	const struct fwstate_stash *stash,
	uint64_t worker_idx,
	struct fwstate_stash_link *link
) {
	if (worker_idx >= stash->worker_count) {
		fwstate_stash_link_set(link, NULL, 0);
		return;
	}
	fwstate_stash_link_set(
		link, map_stash_slot(stash, (uint16_t)worker_idx), stash->size
	);
}

// --- IPv4 object -------------------------------------------------------------

// Typed destructor: tears the object down and returns its storage to the
// agent arena. Runs only on a dangling object, after
// fwstate_map_v4_object_config_free wins the try-destroy race.
static void
fwstate_map_v4_object_destroy(struct cp_object *cp_object) {
	struct fwstate_map_v4_object *self = container_of(
		cp_object, struct fwstate_map_v4_object, cp_object
	);

	struct agent *agent = ADDR_OF(&cp_object->entity.agent);

	fwstate_map_v4_object_fini(self);
	fwstate_map_v4_object_free(self, agent);
}

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

	// Creating a table is the first fwstate operation any process
	// performs; it also installs the domain's fwmap callbacks.
	fwstate_fwmap_registry_ensure();

	return cp_object_init(
		&self->cp_object, agent, FWSTATE_MAP_V4_OBJECT_TYPE, name, err
	);
}

void
fwstate_map_v4_object_fini(struct fwstate_map_v4_object *self) {
	if (self == NULL) {
		return;
	}

	struct agent *agent = ADDR_OF(&self->cp_object.entity.agent);
	if (agent != NULL) {
		// Runs before the common teardown zeroes the object's memory
		// context, which the table free must still charge.
		map_free_table(
			&self->table, &self->cp_object.entity.memory_context
		);
		map_stash_free(
			&self->stash, &self->cp_object.entity.memory_context
		);
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

int
fwstate_map_v4_object_config_free(
	struct cp_object *cp_object, yanet_error **err
) {
	if (cp_object_try_destroy(cp_object, err)) {
		return -1;
	}

	struct agent *owner = ADDR_OF(&cp_object->entity.agent);

	fwstate_map_v4_object_destroy(cp_object);
	agent_destroy_released(owner);
	return 0;
}

fwtable_t *
fwstate_map_v4_object_table(const struct cp_object *cp_object) {
	struct fwstate_map_v4_object *self = container_of(
		cp_object, struct fwstate_map_v4_object, cp_object
	);

	return &self->table;
}

uint64_t
fwstate_map_v4_object_generation(const struct cp_object *cp_object) {
	struct fwstate_map_v4_object *self = container_of(
		cp_object, struct fwstate_map_v4_object, cp_object
	);

	return self->generation;
}

int
fwstate_map_v4_object_create(
	struct fwstate_map_v4_object *self,
	const struct fwstate_map_create_config *config
) {
	int rc = map_create(
		&self->table,
		&self->stash,
		&self->cp_object.entity.memory_context,
		map_v4_init_keys,
		config
	);
	if (rc == 0) {
		self->generation += 1;
	}
	return rc;
}

struct fwstate_stash_slot *
fwstate_map_v4_object_stash(
	const struct cp_object *cp_object, uint16_t worker_idx
) {
	struct fwstate_map_v4_object *self = container_of(
		cp_object, struct fwstate_map_v4_object, cp_object
	);

	return map_stash_slot(&self->stash, worker_idx);
}

uint64_t
fwstate_map_v4_object_stash_size(const struct cp_object *cp_object) {
	struct fwstate_map_v4_object *self = container_of(
		cp_object, struct fwstate_map_v4_object, cp_object
	);

	return self->stash.size;
}

void
fwstate_map_v4_object_link(
	const struct cp_object *cp_object,
	uint64_t worker_idx,
	struct fwstate_map_link *link
) {
	if (cp_object == NULL) {
		link->table = NULL;
		fwstate_stash_link_set(&link->stash, NULL, 0);
		return;
	}
	struct fwstate_map_v4_object *self = container_of(
		cp_object, struct fwstate_map_v4_object, cp_object
	);
	link->table = &self->table;
	map_stash_link(&self->stash, worker_idx, &link->stash);
}

int
fwstate_map_v4_object_insert_layer(
	struct fwstate_map_v4_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	int rc = map_insert_layer(
		&self->table,
		&self->cp_object.entity.memory_context,
		map_v4_init_keys,
		index_size,
		extra_bucket_count,
		worker_count
	);
	if (rc == 0) {
		self->generation += 1;
	}
	return rc;
}

int
fwstate_map_v4_object_unlink_stale_layers(
	struct fwstate_map_v4_object *self, uint64_t now
) {
	int rc = fwtable_unlink_stale_cp(&self->table, now);
	if (rc == 0) {
		self->generation += 1;
	}
	return rc;
}

void
fwstate_map_v4_object_free_stale_layers(struct fwstate_map_v4_object *self) {
	fwtable_free_stale(
		&self->table, &self->cp_object.entity.memory_context
	);
}

uint32_t
fwstate_map_v4_object_stale_layer_count(const struct fwstate_map_v4_object *self
) {
	return fwtable_stale_count(&self->table);
}

bool
fwstate_map_v4_object_has_reclaimable(
	const struct fwstate_map_v4_object *self, uint64_t now
) {
	return fwtable_has_reclaimable(&self->table, now);
}

// --- IPv6 object -------------------------------------------------------------

// Typed destructor: tears the object down and returns its storage to the
// agent arena. Runs only on a dangling object, after
// fwstate_map_v6_object_config_free wins the try-destroy race.
static void
fwstate_map_v6_object_destroy(struct cp_object *cp_object) {
	struct fwstate_map_v6_object *self = container_of(
		cp_object, struct fwstate_map_v6_object, cp_object
	);

	struct agent *agent = ADDR_OF(&cp_object->entity.agent);

	fwstate_map_v6_object_fini(self);
	fwstate_map_v6_object_free(self, agent);
}

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

	fwstate_fwmap_registry_ensure();

	return cp_object_init(
		&self->cp_object, agent, FWSTATE_MAP_V6_OBJECT_TYPE, name, err
	);
}

void
fwstate_map_v6_object_fini(struct fwstate_map_v6_object *self) {
	if (self == NULL) {
		return;
	}

	struct agent *agent = ADDR_OF(&self->cp_object.entity.agent);
	if (agent != NULL) {
		// Runs before the common teardown zeroes the object's memory
		// context, which the table free must still charge.
		map_free_table(
			&self->table, &self->cp_object.entity.memory_context
		);
		map_stash_free(
			&self->stash, &self->cp_object.entity.memory_context
		);
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

int
fwstate_map_v6_object_config_free(
	struct cp_object *cp_object, yanet_error **err
) {
	if (cp_object_try_destroy(cp_object, err)) {
		return -1;
	}

	struct agent *owner = ADDR_OF(&cp_object->entity.agent);

	fwstate_map_v6_object_destroy(cp_object);
	agent_destroy_released(owner);
	return 0;
}

fwtable_t *
fwstate_map_v6_object_table(const struct cp_object *cp_object) {
	struct fwstate_map_v6_object *self = container_of(
		cp_object, struct fwstate_map_v6_object, cp_object
	);

	return &self->table;
}

uint64_t
fwstate_map_v6_object_generation(const struct cp_object *cp_object) {
	struct fwstate_map_v6_object *self = container_of(
		cp_object, struct fwstate_map_v6_object, cp_object
	);

	return self->generation;
}

int
fwstate_map_v6_object_create(
	struct fwstate_map_v6_object *self,
	const struct fwstate_map_create_config *config
) {
	int rc = map_create(
		&self->table,
		&self->stash,
		&self->cp_object.entity.memory_context,
		map_v6_init_keys,
		config
	);
	if (rc == 0) {
		self->generation += 1;
	}
	return rc;
}

struct fwstate_stash_slot *
fwstate_map_v6_object_stash(
	const struct cp_object *cp_object, uint16_t worker_idx
) {
	struct fwstate_map_v6_object *self = container_of(
		cp_object, struct fwstate_map_v6_object, cp_object
	);

	return map_stash_slot(&self->stash, worker_idx);
}

uint64_t
fwstate_map_v6_object_stash_size(const struct cp_object *cp_object) {
	struct fwstate_map_v6_object *self = container_of(
		cp_object, struct fwstate_map_v6_object, cp_object
	);

	return self->stash.size;
}

void
fwstate_map_v6_object_link(
	const struct cp_object *cp_object,
	uint64_t worker_idx,
	struct fwstate_map_link *link
) {
	if (cp_object == NULL) {
		link->table = NULL;
		fwstate_stash_link_set(&link->stash, NULL, 0);
		return;
	}
	struct fwstate_map_v6_object *self = container_of(
		cp_object, struct fwstate_map_v6_object, cp_object
	);
	link->table = &self->table;
	map_stash_link(&self->stash, worker_idx, &link->stash);
}

int
fwstate_map_v6_object_insert_layer(
	struct fwstate_map_v6_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	int rc = map_insert_layer(
		&self->table,
		&self->cp_object.entity.memory_context,
		map_v6_init_keys,
		index_size,
		extra_bucket_count,
		worker_count
	);
	if (rc == 0) {
		self->generation += 1;
	}
	return rc;
}

int
fwstate_map_v6_object_unlink_stale_layers(
	struct fwstate_map_v6_object *self, uint64_t now
) {
	int rc = fwtable_unlink_stale_cp(&self->table, now);
	if (rc == 0) {
		self->generation += 1;
	}
	return rc;
}

void
fwstate_map_v6_object_free_stale_layers(struct fwstate_map_v6_object *self) {
	fwtable_free_stale(
		&self->table, &self->cp_object.entity.memory_context
	);
}

uint32_t
fwstate_map_v6_object_stale_layer_count(const struct fwstate_map_v6_object *self
) {
	return fwtable_stale_count(&self->table);
}

bool
fwstate_map_v6_object_has_reclaimable(
	const struct fwstate_map_v6_object *self, uint64_t now
) {
	return fwtable_has_reclaimable(&self->table, now);
}

// The generation counter is the object's seqlock. Writers bracket every
// publication with a release increment on each side, so an odd value
// means a publication is in flight. Readers sample with acquire, reject
// odd values, and close the walk with the fenced after-walk sample: the
// walk is stable exactly when both samples are the same even value.
static inline void
map_nat64_generation_begin(struct fwstate_map_nat64_object *self) {
	__atomic_add_fetch(&self->generation, 1, __ATOMIC_RELEASE);
}

static inline void
map_nat64_generation_end(struct fwstate_map_nat64_object *self) {
	__atomic_add_fetch(&self->generation, 1, __ATOMIC_RELEASE);
}

// --- NAT64 object -----------------------------------------------------------

// Typed destructor: tears the object down and returns its storage to the
// agent arena. Runs only on a dangling object, after
// fwstate_map_nat64_object_config_free wins the try-destroy race.
static void
fwstate_map_nat64_object_destroy(struct cp_object *cp_object) {
	struct fwstate_map_nat64_object *self = container_of(
		cp_object, struct fwstate_map_nat64_object, cp_object
	);

	struct agent *agent = ADDR_OF(&cp_object->entity.agent);

	fwstate_map_nat64_object_fini(self);
	fwstate_map_nat64_object_free(self, agent);
}

struct fwstate_map_nat64_object *
fwstate_map_nat64_object_new(struct agent *agent) {
	return (struct fwstate_map_nat64_object *)memory_balloc(
		&agent->memory_context, sizeof(struct fwstate_map_nat64_object)
	);
}

int
fwstate_map_nat64_object_init(
	struct fwstate_map_nat64_object *self,
	struct agent *agent,
	const char *name,
	yanet_error **err
) {
	memset(self, 0, sizeof(struct fwstate_map_nat64_object));

	// Creating a table is the first fwstate operation any process
	// performs; it also installs the domain's fwmap callbacks.
	fwstate_fwmap_registry_ensure();

	return cp_object_init(
		&self->cp_object,
		agent,
		FWSTATE_MAP_NAT64_OBJECT_TYPE,
		name,
		err
	);
}

void
fwstate_map_nat64_object_fini(struct fwstate_map_nat64_object *self) {
	if (self == NULL) {
		return;
	}

	struct agent *agent = ADDR_OF(&self->cp_object.entity.agent);
	if (agent != NULL) {
		// Runs before the common teardown zeroes the object's memory
		// context, which the table frees must still charge.
		map_free_table(
			&self->lan, &self->cp_object.entity.memory_context
		);
		map_free_table(
			&self->wan, &self->cp_object.entity.memory_context
		);
		map_stash_free(
			&self->stash, &self->cp_object.entity.memory_context
		);
	}
	cp_object_fini(&self->cp_object);
}

void
fwstate_map_nat64_object_free(
	struct fwstate_map_nat64_object *self, struct agent *agent
) {
	if (self == NULL) {
		return;
	}
	memory_bfree(
		&agent->memory_context,
		self,
		sizeof(struct fwstate_map_nat64_object)
	);
}

struct cp_object *
fwstate_map_nat64_object_config_new(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct fwstate_map_nat64_object *self =
		fwstate_map_nat64_object_new(agent);
	if (self == NULL) {
		yanet_error_add(
			err, "failed to allocate nat64state map object"
		);
		return NULL;
	}

	if (fwstate_map_nat64_object_init(self, agent, name, err)) {
		yanet_error_add(err, "failed to init nat64state map object");
		fwstate_map_nat64_object_free(self, agent);
		return NULL;
	}

	return &self->cp_object;
}

int
fwstate_map_nat64_object_config_free(
	struct cp_object *cp_object, yanet_error **err
) {
	if (cp_object_try_destroy(cp_object, err)) {
		return -1;
	}

	struct agent *owner = ADDR_OF(&cp_object->entity.agent);

	fwstate_map_nat64_object_destroy(cp_object);
	agent_destroy_released(owner);
	return 0;
}

// Create the map: its shared stash, then the first layer of both tables.
// A failure unwinds whatever the call allocated.
static int
map_nat64_create(
	struct fwstate_map_nat64_object *self,
	struct memory_context *ctx,
	const struct fwstate_map_create_config *config
) {
	if (ADDR_OF(&self->stash.slots) != NULL ||
	    ADDR_OF(&self->lan.head) != NULL ||
	    ADDR_OF(&self->wan.head) != NULL) {
		errno = EEXIST;
		return -1;
	}
	if (map_stash_create(
		    &self->stash, ctx, config->worker_count, config->stash_size
	    )) {
		return -1;
	}
	if (map_insert_layer(
		    &self->lan,
		    ctx,
		    map_nat64_lan_init_keys,
		    config->index_size,
		    config->extra_bucket_count,
		    config->worker_count
	    )) {
		int saved_errno = errno;
		map_stash_free(&self->stash, ctx);
		errno = saved_errno;
		return -1;
	}
	if (map_insert_layer(
		    &self->wan,
		    ctx,
		    map_nat64_wan_init_keys,
		    config->index_size,
		    config->extra_bucket_count,
		    config->worker_count
	    )) {
		int saved_errno = errno;
		map_free_table(&self->lan, ctx);
		map_stash_free(&self->stash, ctx);
		errno = saved_errno;
		return -1;
	}
	return 0;
}

int
fwstate_map_nat64_object_create(
	struct fwstate_map_nat64_object *self,
	const struct fwstate_map_create_config *config
) {
	// No generation movement: the object is not yet published, so no
	// reader exists to bracket against, and a fresh object must rest at
	// the stable even generation zero.
	return map_nat64_create(
		self, &self->cp_object.entity.memory_context, config
	);
}

fwtable_t *
fwstate_map_nat64_object_lan_table(const struct cp_object *cp_object) {
	struct fwstate_map_nat64_object *self = container_of(
		cp_object, struct fwstate_map_nat64_object, cp_object
	);

	return &self->lan;
}

fwtable_t *
fwstate_map_nat64_object_wan_table(const struct cp_object *cp_object) {
	struct fwstate_map_nat64_object *self = container_of(
		cp_object, struct fwstate_map_nat64_object, cp_object
	);

	return &self->wan;
}

uint64_t
fwstate_map_nat64_object_generation(const struct cp_object *cp_object) {
	struct fwstate_map_nat64_object *self = container_of(
		cp_object, struct fwstate_map_nat64_object, cp_object
	);

	return __atomic_load_n(&self->generation, __ATOMIC_ACQUIRE);
}

uint64_t
fwstate_map_nat64_object_generation_after_walk(const struct cp_object *cp_object
) {
	struct fwstate_map_nat64_object *self = container_of(
		cp_object, struct fwstate_map_nat64_object, cp_object
	);

	// The acquire fence orders the caller's preceding head loads before
	// the closing sample: without it, a weakly ordered target may load
	// the new heads but still read the old generation, accepting a torn
	// snapshot as stable.
	__atomic_thread_fence(__ATOMIC_ACQUIRE);
	return __atomic_load_n(&self->generation, __ATOMIC_RELAXED);
}

struct fwstate_stash_slot *
fwstate_map_nat64_object_stash(
	const struct cp_object *cp_object, uint16_t worker_idx
) {
	struct fwstate_map_nat64_object *self = container_of(
		cp_object, struct fwstate_map_nat64_object, cp_object
	);

	return map_stash_slot(&self->stash, worker_idx);
}

uint64_t
fwstate_map_nat64_object_stash_size(const struct cp_object *cp_object) {
	struct fwstate_map_nat64_object *self = container_of(
		cp_object, struct fwstate_map_nat64_object, cp_object
	);

	return self->stash.size;
}

void
fwstate_map_nat64_object_link(
	const struct cp_object *cp_object,
	uint64_t worker_idx,
	struct fwstate_map_link *lan,
	struct fwstate_map_link *wan
) {
	if (cp_object == NULL) {
		lan->table = NULL;
		fwstate_stash_link_set(&lan->stash, NULL, 0);
		wan->table = NULL;
		fwstate_stash_link_set(&wan->stash, NULL, 0);
		return;
	}
	struct fwstate_map_nat64_object *self = container_of(
		cp_object, struct fwstate_map_nat64_object, cp_object
	);
	lan->table = &self->lan;
	wan->table = &self->wan;
	map_stash_link(&self->stash, worker_idx, &lan->stash);
	map_stash_link(&self->stash, worker_idx, &wan->stash);
}

int
fwstate_map_nat64_object_insert_layer(
	struct fwstate_map_nat64_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	// Both layers are prepared before either head is published: a worker
	// reading the live object must never see one rotated table and one
	// not, and a failed insert must leave the object exactly as it was.
	//
	// The generation counter brackets the publication as a seqlock: a
	// reader that observes the counter change across its walk discards
	// what it read, whatever mix of old and new heads it saw.
	struct memory_context *ctx = &self->cp_object.entity.memory_context;
	if (worker_count == 0) {
		errno = EINVAL;
		return -1;
	}

	fwmap_config_t config;
	map_init_common_config(
		&config, index_size, extra_bucket_count, worker_count
	);
	map_nat64_lan_init_keys(&config);
	fwmap_t *lan_layer = fwmap_new(&config, ctx);
	if (lan_layer == NULL) {
		return -1;
	}

	map_init_common_config(
		&config, index_size, extra_bucket_count, worker_count
	);
	map_nat64_wan_init_keys(&config);
	fwmap_t *wan_layer = fwmap_new(&config, ctx);
	if (wan_layer == NULL) {
		int saved_errno = errno;
		fwmap_free(lan_layer, ctx);
		errno = saved_errno;
		return -1;
	}

	map_nat64_generation_begin(self);
	fwmap_t *lan_head = ADDR_OF(&self->lan.head);
	SET_OFFSET_OF(&lan_layer->next, lan_head);
	ATOMIC_SET_OFFSET_OF(&self->lan.head, lan_layer);
	fwmap_t *wan_head = ADDR_OF(&self->wan.head);
	SET_OFFSET_OF(&wan_layer->next, wan_head);
	ATOMIC_SET_OFFSET_OF(&self->wan.head, wan_layer);
	map_nat64_generation_end(self);
	return 0;
}

int
fwstate_map_nat64_object_unlink_stale_layers(
	struct fwstate_map_nat64_object *self, uint64_t now
) {
	// The same seqlock bracket as the insert path: a worker batching
	// across both tables must detect the reclamation, and a failed
	// unlink leaves the chains exactly as they were.
	map_nat64_generation_begin(self);
	int rc = fwtable_unlink_stale_cp(&self->lan, now);
	if (rc == 0) {
		rc = fwtable_unlink_stale_cp(&self->wan, now);
	}
	// The end increment is unconditional: a partial unlink is exactly
	// the mixed state the bracket must make visible, and an untouched
	// pair costs readers nothing but one discarded walk.
	map_nat64_generation_end(self);
	return rc;
}

void
fwstate_map_nat64_object_free_stale_layers(struct fwstate_map_nat64_object *self
) {
	fwtable_free_stale(&self->lan, &self->cp_object.entity.memory_context);
	fwtable_free_stale(&self->wan, &self->cp_object.entity.memory_context);
}

uint32_t
fwstate_map_nat64_object_stale_layer_count(
	const struct fwstate_map_nat64_object *self
) {
	return fwtable_stale_count(&self->lan) +
	       fwtable_stale_count(&self->wan);
}

bool
fwstate_map_nat64_object_has_reclaimable(
	const struct fwstate_map_nat64_object *self, uint64_t now
) {
	return fwtable_has_reclaimable(&self->lan, now) ||
	       fwtable_has_reclaimable(&self->wan, now);
}

// --- object factories --------------------------------------------------------

struct object *
new_object_fwstate_map_v4() {
	struct object *object =
		(struct object *)calloc(1, sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name, FWSTATE_MAP_V4_OBJECT_TYPE, sizeof(object->name));
	return object;
}

struct object *
new_object_nat64state_map() {
	struct object *object =
		(struct object *)calloc(1, sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name,
		FWSTATE_MAP_NAT64_OBJECT_TYPE,
		sizeof(object->name));
	return object;
}

struct object *
new_object_fwstate_map_v6() {
	struct object *object =
		(struct object *)calloc(1, sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name, FWSTATE_MAP_V6_OBJECT_TYPE, sizeof(object->name));
	return object;
}
