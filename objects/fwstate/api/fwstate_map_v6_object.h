#pragma once

#include <stddef.h>
#include <stdint.h>

#include "controlplane/config/cp_object.h"

#include "lib/errors/errors.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/fwstate_cursor.h"
#include "lib/fwstate/fwtable.h"

#define FWSTATE_MAP_V6_OBJECT_TYPE "fwstate_map_v6"

struct agent;
struct cp_object;
struct memory_context;

// Owns a single IPv6 fwtable plus its layer chain as an independent
// shared-memory cp_object, registered under
// ("fwstate_map_v6", name). Module configs (fwstate, acl) reference the
// table via cp_module_link_object and resolve it at ectx build time.
struct fwstate_map_v6_object {
	struct cp_object cp_object;

	fwtable_t table;
};

// RAII lifecycle for struct fwstate_map_v6_object.
//
// new allocates ONLY the struct in the agent shared memory. init zeroes
// the enclosing struct and calls cp_object_init; on error callers must
// call free. fini releases field memory (free the table chain,
// cp_object_fini) and is idempotent. free deallocates ONLY the struct and
// is NULL-safe.
struct fwstate_map_v6_object *
fwstate_map_v6_object_new(struct agent *agent);

int
fwstate_map_v6_object_init(
	struct fwstate_map_v6_object *self,
	struct agent *agent,
	const char *name,
	yanet_error **err
);

void
fwstate_map_v6_object_fini(struct fwstate_map_v6_object *self);

void
fwstate_map_v6_object_free(
	struct fwstate_map_v6_object *self, struct agent *agent
);

// Registration convenience: allocate + init and return the cp_object
// pointer for agent_update_objects. On failure the object is fully
// cleaned up and NULL is returned.
struct cp_object *
fwstate_map_v6_object_config_new(
	struct agent *agent, const char *name, yanet_error **err
);

// Free handler matching the cp_object destruction pattern.
void
fwstate_map_v6_object_config_free(struct cp_object *cp_object);

// Return the address of the object's fwtable field.
fwtable_t *
fwstate_map_v6_object_table(const struct cp_object *cp_object);

// Insert a new layer into the object's table chain.
int
fwstate_map_v6_object_insert_layer(
	struct fwstate_map_v6_object *self,
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
fwstate_map_v6_object_trim_stale_layers(
	struct fwstate_map_v6_object *self, uint64_t now
);
