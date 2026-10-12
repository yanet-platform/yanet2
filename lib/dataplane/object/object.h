#pragma once

#include <stdint.h>

/*
 * Dataplane-side descriptor for an object type.
 *
 * Objects take no per-packet action, so unlike modules and devices they
 * carry no packet handler; a loaded object type exists so the
 * controlplane can resolve an object configuration's type name to a
 * stable dataplane index at init time, mirroring modules and devices,
 * and so it can declare the per-context preparation below.
 *
 * The load-handler typedef is resolved by dp_load_object from a
 * new_object_<name> constructor exported by the main binary, parallel to
 * dp_load_module and dp_load_device.
 */
#define OBJECT_TYPE_LEN 80

struct object_ectx;
struct cp_object;

// Execution-context commit handler: a per-worker derivation hook,
// run once per object execution context on every assignment pass of
// a published generation.
//
// Runs inside the dataplane address space during the per-worker
// absolutization pass, which re-derives everything from the
// authoritative fields, so a repeated pass rewrites the same values
// and the hook must stay idempotent. Writes only the object's own
// private per-context buffer reached through the execution context;
// the shared object config and the context's authoritative fields
// are read-only here, and the context names its owning worker, so
// worker-indexed state is selected through it. Values that vary per
// worker belong in this hook, generation-invariant ones in a commit
// handler on the linked module configs.
typedef void (*object_commit_ectx_handler)(
	struct object_ectx *object_ectx, struct cp_object *cp_object
);

struct object {
	char name[OBJECT_TYPE_LEN];
	object_commit_ectx_handler commit_ectx_handler;
	// Size of the object's private per-context buffer, zero when the
	// object needs none. The control plane allocates a zeroed buffer
	// of this size for every object execution context it builds.
	uint64_t prepared_size;
};

typedef struct object *(*object_load_handler)();
