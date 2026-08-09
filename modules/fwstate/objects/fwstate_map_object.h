#pragma once

#include <stddef.h>
#include <stdint.h>

#include "controlplane/config/cp_object.h"

#include "lib/errors/errors.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/fwstate_cursor.h"
#include "lib/fwstate/fwtable.h"

#define FWSTATE_MAP_V4_OBJECT_TYPE "fwstate_map_v4"
#define FWSTATE_MAP_V6_OBJECT_TYPE "fwstate_map_v6"

enum fwtable_kind {
	FWTABLE_KIND_V4 = 0,
	FWTABLE_KIND_V6 = 1,
};

struct agent;
struct cp_object;
struct memory_context;

// Owns a single fwtable (v4 OR v6) plus its layer chain as an independent
// shared-memory cp_object, registered under ("fwstate_map_v4" or
// "fwstate_map_v6", name). Module configs (fwstate, acl) reference the
// table via cp_module_link_object and resolve it at ectx build time.
//
// kind selects whether the table is keyed on fw4_state_key or
// fw6_state_key, which governs the fwmap_config used to grow the layer
// chain.
struct fwstate_map_object {
	struct cp_object cp_object;

	enum fwtable_kind kind;
	fwtable_t table;
};

// RAII lifecycle for struct fwstate_map_object.
//
// new allocates ONLY the struct in the agent shared memory. init zeroes
// the enclosing struct, calls cp_object_init, and sets the kind; on error
// it calls fini. fini releases field memory (free the table chain,
// cp_object_fini) and is idempotent. free deallocates ONLY the struct and
// is NULL-safe.
struct fwstate_map_object *
fwstate_map_object_new(struct agent *agent);

int
fwstate_map_object_init(
	struct fwstate_map_object *self,
	struct agent *agent,
	const char *type,
	const char *name,
	enum fwtable_kind kind,
	yanet_error **err
);

void
fwstate_map_object_fini(struct fwstate_map_object *self);

void
fwstate_map_object_free(struct fwstate_map_object *self, struct agent *agent);

// Registration convenience: allocate + init and return the cp_object
// pointer for agent_update_objects. On failure the object is fully
// cleaned up and NULL is returned.
struct cp_object *
fwstate_map_object_config_new(
	struct agent *agent,
	const char *type,
	const char *name,
	enum fwtable_kind kind,
	yanet_error **err
);

// Free handler matching the cp_object destruction pattern.
void
fwstate_map_object_config_free(struct cp_object *cp_object);

// Return the address of the object's fwtable field.
fwtable_t *
fwstate_map_object_table(const struct cp_object *cp_object);

// Return whether the object's table is keyed for v4 or v6.
enum fwtable_kind
fwstate_map_object_kind(const struct cp_object *cp_object);

// Create the initial layer of the object's single table.
int
fwstate_map_object_create_map(
	struct fwstate_map_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
);

// Insert a new layer into the object's table chain.
int
fwstate_map_object_insert_layer(
	struct fwstate_map_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
);

// Trim stale layers from the object's table chain.
//
// Returns 0 on success or -1 on error. Trimmed layers are tracked in the
// fwtable stale chain and freed on the next trim call (giving the
// dataplane one trim cycle to quiesce), so the caller has nothing to
// free.
int
fwstate_map_object_trim_stale_layers(
	struct fwstate_map_object *self, uint64_t now
);

// Free one fwtable's chains (head and stale) and zero the table.
void
fwstate_map_object_free_table(fwtable_t *table, struct memory_context *ctx);

// Map an object type name to its fwtable_kind.
enum fwtable_kind
fwstate_map_object_type_to_kind(const char *type);
