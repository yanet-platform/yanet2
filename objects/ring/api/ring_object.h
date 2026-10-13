#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "lib/controlplane/config/cp_object.h"

#include "lib/errors/errors.h"

#include "common/ring.h"

#define RING_OBJECT_TYPE "ring"

struct agent;
struct cp_object;
struct memory_context;

// A named ring object in shared memory, of type "ring".
//
// It holds one ring per dataplane worker: the metadata and the data area.
// A module config can link the ring by name, and the link keeps the ring
// from being deleted.
struct ring_object {
	struct cp_object cp_object;

	// Number of per-worker rings, equal to the dataplane's worker count.
	//
	// Creation fails when the dataplane has UINT16_MAX workers or more.
	uint16_t worker_count;
	// Size of each worker's data area in bytes, fixed at creation.
	//
	// It is a power of two. It is at least the record frame size and at
	// most the allocator's largest block.
	uint32_t capacity;
	// Number of records a writer commits before it publishes them itself.
	//
	// It is the same for every worker and is fixed at creation.
	uint32_t publish_batch;

	// Raw allocation of the metadata array and its size in bytes.
	//
	// The free needs both values. The array start is rounded up to a cache
	// line, so the aligned array pointer below is not the pointer that was
	// allocated.
	void *workers_raw;
	uint64_t workers_raw_size;
	struct ring_worker *workers;
};

// Lifecycle of a ring object: new, init, fini, free.
//
// New allocates only the struct in the agent's shared memory. Init zeroes
// the struct and sets up its shared-object header. If init fails, the
// caller must free the struct. Fini frees the per-worker data areas, the
// metadata array and the header. It clears what it freed, so a second call
// does nothing. Free releases only the struct. It accepts NULL.
struct ring_object *
ring_object_new(struct agent *agent);

int
ring_object_init(
	struct ring_object *self,
	struct agent *agent,
	const char *name,
	yanet_error **err
);

void
ring_object_fini(struct ring_object *self);

void
ring_object_free(struct ring_object *self, struct agent *agent);

// Allocate, initialize and create a ring in one call.
//
// Returns the shared-object handle that the caller registers with the
// agent. On failure it frees everything it allocated and returns NULL.
// Errno then comes from the step that failed. It is EINVAL when the name is
// NULL, empty or has CP_OBJECT_NAME_LEN bytes or more, checked before
// anything is allocated. It is ENOMEM when the struct cannot be allocated, or
// the creation errno when creation fails. Initialization sets no errno, so
// errno is unspecified if init fails.
struct cp_object *
ring_object_config_new(
	struct agent *agent,
	const char *name,
	uint32_t capacity,
	uint32_t publish_batch,
	yanet_error **err
);

// Destroy the object when no live configuration generation uses it.
//
// While a live generation still uses the object, it returns -1 with errno
// EAGAIN. The caller then keeps its handle and tries again later.
int
ring_object_config_free(struct cp_object *cp_object, yanet_error **err);

// Allocate the per-worker metadata array and data areas.
//
// It creates one ring per dataplane worker. Call it once, before the object
// is published. Returns 0 on success, or -1 with errno set. EINVAL means the
// capacity is below the frame size or not a power of two, the publish batch
// is outside 1 to RING_PUBLISH_BATCH_MAX, or the dataplane has no workers.
// E2BIG means the capacity is above the allocator's largest block, or the
// dataplane has UINT16_MAX workers or more. EEXIST means the object is
// already created. ENOMEM means an allocation failed. On failure the object
// has no storage and nothing stays allocated.
int
ring_object_create(
	struct ring_object *self,
	uint32_t capacity,
	uint32_t publish_batch,
	yanet_error **err
);

// Per-worker data area size in bytes, fixed at creation.
uint32_t
ring_object_capacity(const struct cp_object *cp_object);

// Number of records a writer commits before it publishes them itself.
//
// It is fixed at creation.
uint32_t
ring_object_publish_batch(const struct cp_object *cp_object);

// Metadata of one worker's ring.
//
// It finds the entry in the aligned array, so the caller does not compute
// the entry address itself. Returns NULL when the index is past the last
// worker. A caller can list all rings by asking for index 0, 1, 2, and so
// on until it gets NULL. The layout of the returned struct depends on
// YANET_CACHE_LINE_SIZE. So only code built with the same value as this
// archive may read its fields. Other code uses ring_object_worker_view.
struct ring_worker *
ring_object_worker(const struct cp_object *cp_object, uint64_t worker_idx);

// Data area of one worker's ring, as a pointer valid in this process.
//
// Returns NULL when the index is past the last worker.
uint8_t *
ring_object_worker_data(const struct cp_object *cp_object, uint64_t worker_idx);

// What a reader needs from one worker's ring.
//
// The positions point into the published half of the metadata. A reader
// loads them atomically, with acquire order. The data pointer is valid in
// this process. The struct has no alignment attributes, so its layout does
// not depend on YANET_CACHE_LINE_SIZE.
struct ring_worker_view {
	uint64_t *write_idx;
	uint64_t *readable_idx;
	uint8_t *data;
	uint32_t size;
	uint32_t mask;
};

// Fill the reader view of one worker's ring; false past the last worker.
//
// This archive computes every address, so a caller never applies its own
// view of the metadata layout. The offset of the published positions grows
// with YANET_CACHE_LINE_SIZE. So a caller built with another value, such
// as a CGo reader, still reads the positions the writer stores to.
bool
ring_object_worker_view(
	const struct cp_object *cp_object,
	uint64_t worker_idx,
	struct ring_worker_view *view
);

// Whether a ring with this name is in the agent's published configuration.
//
// It checks the current generation under the config lock. A creator uses it
// to refuse a create that would replace a published ring of the same name
// without notice. It does not see objects that a concurrent update is still
// building and has not published yet.
bool
ring_object_exists(struct agent *agent, const char *name);

// Allocate an array of equal-size entries with an aligned start.
//
// It rounds each entry size up to the alignment. Under ASan, a red zone can
// shift any alignment above 64 bytes. So it allocates alignment - 1 extra
// bytes and rounds the start up itself. On success the whole block is
// zeroed, and the raw pointer and size outputs hold what the free needs.
// On failure it returns NULL and does not touch the raw outputs. Errno is
// EOVERFLOW when a size computation overflows, or ENOMEM when the
// allocation fails.
void *
ring_object_align_alloc(
	struct memory_context *ctx,
	uint64_t stride,
	uint64_t count,
	uint64_t alignment,
	void **raw,
	uint64_t *raw_size
);
