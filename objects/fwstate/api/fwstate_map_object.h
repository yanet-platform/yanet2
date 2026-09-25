#pragma once

#include <stdint.h>

#include "lib/fwstate/stash.h"
#include "lib/statemap/fwtable.h"

// A module's link to one linked map object for one worker: the object's
// state table and the worker's stash. A family without a linked object has
// a NULL table and an empty stash.
struct fwstate_map_link {
	fwtable_t *table;
	struct fwstate_stash_link stash;
};

// Parameters of a map object's creation: the first table layer and the
// per-worker sync stash, both sized for the dataplane's workers.
//
// The index and overflow bucket counts size the layer, zero selecting
// their defaults. The stash size is each worker's buffer in bytes, zero
// selecting the fwstate default.
struct fwstate_map_create_config {
	uint32_t index_size;
	uint32_t extra_bucket_count;
	uint16_t worker_count;
	uint64_t stash_size;
};
