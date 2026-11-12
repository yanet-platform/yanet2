#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "lib/controlplane/config/cp_object.h"

#include "lib/errors/errors.h"
#include "lib/fwstate/fwstate_cursor.h"
#include "lib/fwstate/nat64state.h"
#include "lib/fwstate/stash.h"

#include "fwstate_map_object.h"
#include "lib/statemap/fwmap.h"
#include "lib/statemap/fwtable.h"

#define FWSTATE_MAP_NAT64_OBJECT_TYPE "nat64state_map"

struct agent;
struct cp_object;
struct memory_context;

// Owns the session tables of exactly one stateful NAT64 instance as an
// independent shared-memory cp_object, registered under
// ("nat64state_map", name): the lan fwtable keyed by the original IPv6
// flow and the wan fwtable keyed by the translated IPv4 flow, one layer
// chain each, plus the per-worker sync stash both share. Two instances
// never share one object, so the session keys carry no instance
// discriminator.
//
// The nat64s module config references the object via
// cp_module_link_object and resolves both tables at ectx build time.
struct fwstate_map_nat64_object {
	struct cp_object cp_object;

	fwtable_t lan;
	fwtable_t wan;

	// The object's seqlock: writers bracket every publication — layer
	// inserts and stale-layer trims of either table — with a release
	// increment on each side. A stable state is even; a reader samples
	// before the walk, rejects odd values, and closes with the fenced
	// after-walk sample: the walk is stable exactly when both samples
	// are the same even value.
	uint64_t generation;

	// Per-worker sync records, created once with the map and freed with
	// the object.
	struct fwstate_stash stash;
};

// RAII lifecycle for struct fwstate_map_nat64_object.
//
// new allocates ONLY the struct in the agent shared memory. init zeroes
// the enclosing struct and calls cp_object_init; on error callers must
// call free. fini releases field memory (free both table chains,
// cp_object_fini) and is idempotent. free deallocates ONLY the struct and
// is NULL-safe.
struct fwstate_map_nat64_object *
fwstate_map_nat64_object_new(struct agent *agent);

int
fwstate_map_nat64_object_init(
	struct fwstate_map_nat64_object *self,
	struct agent *agent,
	const char *name,
	yanet_error **err
);

void
fwstate_map_nat64_object_fini(struct fwstate_map_nat64_object *self);

void
fwstate_map_nat64_object_free(
	struct fwstate_map_nat64_object *self, struct agent *agent
);

// Registration convenience: allocate + init and return the cp_object
// pointer for agent_update_objects. On failure the object is fully
// cleaned up and NULL is returned.
struct cp_object *
fwstate_map_nat64_object_config_new(
	struct agent *agent, const char *name, yanet_error **err
);

// Destroy the object when it is dangling, per cp_object_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the object; the caller must keep its handle and retry later.
int
fwstate_map_nat64_object_config_free(
	struct cp_object *cp_object, yanet_error **err
);

// Create the map: allocate the per-worker sync stash and install the
// first layer of both tables, sized by the same parameters.
//
// Called once, before the object is published. Returns 0 on success or -1
// with errno set: EINVAL for a zero worker count or a stash size below one
// record or above the maximum, EEXIST when the map was already created,
// ENOMEM or the layer error when an allocation fails. A failure unwinds
// whatever the call allocated.
int
fwstate_map_nat64_object_create(
	struct fwstate_map_nat64_object *self,
	const struct fwstate_map_create_config *config
);

// Return the address of one side's fwtable field.
fwtable_t *
fwstate_map_nat64_object_lan_table(const struct cp_object *cp_object);
fwtable_t *
fwstate_map_nat64_object_wan_table(const struct cp_object *cp_object);

// Return the object's generation counter for the opening sample of a
// seqlock read: load with acquire, reject an odd value, walk the tables.
uint64_t
fwstate_map_nat64_object_generation(const struct cp_object *cp_object);

// Return the closing sample of a seqlock read: an acquire fence orders
// the caller's preceding table-head loads before the load, so a torn
// walk cannot validate against a stale generation. A walk is stable
// exactly when both samples are the same even value.
uint64_t
fwstate_map_nat64_object_generation_after_walk(const struct cp_object *cp_object
);

// Return the stash slot header of one worker, or NULL when the object has
// no stash. worker_idx must be below the worker count the stash was
// created with; it is not checked.
struct fwstate_stash_slot *
fwstate_map_nat64_object_stash(
	const struct cp_object *cp_object, uint16_t worker_idx
);

// Return the size in bytes of each worker's stash buffer, zero without a
// stash.
uint64_t
fwstate_map_nat64_object_stash_size(const struct cp_object *cp_object);

// Link both of the map object's tables and one worker's stash for a
// module's execution context. A NULL cp_object links neither; the stash
// links stay empty when the object has no stash or worker_idx is outside
// its workers.
void
fwstate_map_nat64_object_link(
	const struct cp_object *cp_object,
	uint64_t worker_idx,
	struct fwstate_map_link *lan,
	struct fwstate_map_link *wan
);

// Insert a new layer into both tables' chains, sized by the same
// parameters.
//
// Both layers are prepared before either head is published, so a worker
// never observes one rotated table and one not, and a failure leaves
// the object untouched. The generation counter brackets the publication
// as a seqlock: a reader that observes it change across its walk
// discards what it read.
int
fwstate_map_nat64_object_insert_layer(
	struct fwstate_map_nat64_object *self,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
);

// Unlink stale layers from both tables' chains.
//
// Returns 0 on success or -1 on error. Unlinked layers are parked in the
// fwtable stale chains; the caller frees them with
// fwstate_map_nat64_object_free_stale_layers after a config generation
// barrier has elapsed.
int
fwstate_map_nat64_object_unlink_stale_layers(
	struct fwstate_map_nat64_object *self, uint64_t now
);

// Free the layers parked by fwstate_map_nat64_object_unlink_stale_layers.
//
// Safe only after a generation barrier that every worker advanced past
// since the unlink: the barrier is what proves no reader is still
// walking the parked chains.
void
fwstate_map_nat64_object_free_stale_layers(struct fwstate_map_nat64_object *self
);

// How many layers are parked awaiting a release, both tables combined.
uint32_t
fwstate_map_nat64_object_stale_layer_count(
	const struct fwstate_map_nat64_object *self
);

// Whether a reclamation round would do anything on either table.
//
// Lets a caller skip the generation barriers reclamation needs when
// there is nothing to unlink and nothing parked.
bool
fwstate_map_nat64_object_has_reclaimable(
	const struct fwstate_map_nat64_object *self, uint64_t now
);
