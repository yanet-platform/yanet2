#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "lib/controlplane/config/cp_object.h"

#include "lib/errors/errors.h"
#include "lib/fwstate/fwstate_cursor.h"
#include "lib/fwstate/stash.h"

#include "fwstate_map_object.h"
#include "lib/statemap/fwmap.h"
#include "lib/statemap/fwtable.h"

#define FWSTATE_MAP_V4_OBJECT_TYPE "fwstate_map_v4"

struct agent;
struct cp_object;
struct memory_context;

// Owns a single IPv4 fwtable plus its layer chain as an independent
// shared-memory cp_object, registered under
// ("fwstate_map_v4", name). Module configs (fwstate, acl) reference the
// table via cp_module_link_object and resolve it at ectx build time.
struct fwstate_map_v4_object {
	struct cp_object cp_object;

	fwtable_t table;

	// Bumped on every layer insert and stale-layer trim, so a reader
	// batching over the table can detect that the chain it is walking
	// changed under it.
	uint64_t generation;

	// Per-worker sync records ACL appends and fwstate consumes, created
	// once with the map and freed with the object.
	struct fwstate_stash stash;
};

// RAII lifecycle for struct fwstate_map_v4_object.
//
// new allocates ONLY the struct in the agent shared memory. init zeroes
// the enclosing struct and calls cp_object_init; on error callers must
// call free. fini releases field memory (free the table chain,
// cp_object_fini) and is idempotent. free deallocates ONLY the struct and
// is NULL-safe.
struct fwstate_map_v4_object *
fwstate_map_v4_object_new(struct agent *agent);

int
fwstate_map_v4_object_init(
	struct fwstate_map_v4_object *self,
	struct agent *agent,
	const char *name,
	yanet_error **err
);

void
fwstate_map_v4_object_fini(struct fwstate_map_v4_object *self);

void
fwstate_map_v4_object_free(
	struct fwstate_map_v4_object *self, struct agent *agent
);

// Registration convenience: allocate + init and return the cp_object
// pointer for agent_update_objects. On failure the object is fully
// cleaned up and NULL is returned.
struct cp_object *
fwstate_map_v4_object_config_new(
	struct agent *agent, const char *name, yanet_error **err
);

// Destroy the object when it is dangling, per cp_object_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the object; the caller must keep its handle and retry later.
int
fwstate_map_v4_object_config_free(
	struct cp_object *cp_object, yanet_error **err
);

// Return the address of the object's fwtable field.
fwtable_t *
fwstate_map_v4_object_table(const struct cp_object *cp_object);

// Return the object's generation counter, bumped on every layer insert
// and stale-layer trim.
uint64_t
fwstate_map_v4_object_generation(const struct cp_object *cp_object);

// Create the map: allocate the per-worker sync stash and install the first
// table layer.
//
// Called once, before the object is published. Returns 0 on success or -1
// with errno set: EINVAL for a zero worker count or a stash size below one
// record or above the maximum, EEXIST when the map was already created,
// ENOMEM or the layer error when an allocation fails.
// A failure leaves the object without a stash or layer.
int
fwstate_map_v4_object_create(
	struct fwstate_map_v4_object *self,
	const struct fwstate_map_create_config *config
);

// Return the stash slot header of one worker, or NULL when the object has
// no stash. worker_idx must be below the worker count the stash was
// created with; it is not checked.
struct fwstate_stash_slot *
fwstate_map_v4_object_stash(
	const struct cp_object *cp_object, uint16_t worker_idx
);

// Return the size in bytes of each worker's stash buffer, zero without a
// stash.
uint64_t
fwstate_map_v4_object_stash_size(const struct cp_object *cp_object);

// Link a map object's table and one worker's stash for a module's
// execution context. A NULL cp_object links neither; the stash stays empty
// when the object has no stash or worker_idx is outside its workers.
void
fwstate_map_v4_object_link(
	const struct cp_object *cp_object,
	uint64_t worker_idx,
	struct fwstate_map_link *link
);

// Insert a new layer into the object's table chain.
int
fwstate_map_v4_object_insert_layer(
	struct fwstate_map_v4_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
);

// Unlink stale layers from the object's table chain.
//
// Returns 0 on success or -1 on error. Unlinked layers are parked in
// the fwtable stale chain; the caller frees them with
// fwstate_map_v4_object_free_stale_layers after a config generation
// barrier has elapsed.
int
fwstate_map_v4_object_unlink_stale_layers(
	struct fwstate_map_v4_object *self, uint64_t now
);

// Free the layers parked by fwstate_map_v4_object_unlink_stale_layers.
//
// Safe only after a generation barrier that every worker advanced past
// since the unlink: the barrier is what proves no reader is still
// walking the parked chain.
void
fwstate_map_v4_object_free_stale_layers(struct fwstate_map_v4_object *self);

// How many layers are parked awaiting a release.
uint32_t
fwstate_map_v4_object_stale_layer_count(const struct fwstate_map_v4_object *self
);

// Whether a reclamation round would do anything.
//
// Lets a caller skip the generation barriers reclamation needs when
// there is nothing to unlink and nothing parked.
bool
fwstate_map_v4_object_has_reclaimable(
	const struct fwstate_map_v4_object *self, uint64_t now
);
