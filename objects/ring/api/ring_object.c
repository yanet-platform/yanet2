#include <assert.h>
#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "ring_object.h"

#include "common/container_of.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/numutils.h"
#include "common/strutils.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/object/object.h"

// Tear the object down and return its memory to the agent.
//
// It runs only on an object that no generation uses any more, after the
// guarded destroy has claimed it.
static void
ring_object_destroy(struct cp_object *cp_object) {
	struct ring_object *self =
		container_of(cp_object, struct ring_object, cp_object);

	struct agent *agent = ADDR_OF(&cp_object->agent);

	ring_object_fini(self);
	ring_object_free(self, agent);
}

struct ring_object *
ring_object_new(struct agent *agent) {
	return (struct ring_object *)memory_balloc(
		&agent->memory_context, sizeof(struct ring_object)
	);
}

int
ring_object_init(
	struct ring_object *self,
	struct agent *agent,
	const char *name,
	yanet_error **err
) {
	memset(self, 0, sizeof(struct ring_object));

	return cp_object_init(
		&self->cp_object, agent, RING_OBJECT_TYPE, name, err
	);
}

void
ring_object_fini(struct ring_object *self) {
	if (self == NULL) {
		return;
	}

	struct agent *agent = ADDR_OF(&self->cp_object.agent);
	if (agent != NULL) {
		// The frees below must run before the common teardown, because
		// that teardown zeroes the memory context they free into.
		struct memory_context *ctx = &self->cp_object.memory_context;
		struct ring_worker *workers = ADDR_OF(&self->workers);
		if (workers != NULL) {
			for (uint16_t idx = 0; idx < self->worker_count;
			     ++idx) {
				uint8_t *data =
					ADDR_OF(&workers[idx].local.data);
				memory_bfree(ctx, data, self->capacity);
			}
			void *raw = ADDR_OF(&self->workers_raw);
			memory_bfree(ctx, raw, self->workers_raw_size);
		}
	}
	// Clear the freed pointers, so a second fini frees nothing again.
	SET_OFFSET_OF(&self->workers, NULL);
	SET_OFFSET_OF(&self->workers_raw, NULL);
	self->workers_raw_size = 0;
	self->worker_count = 0;
	cp_object_fini(&self->cp_object);
}

void
ring_object_free(struct ring_object *self, struct agent *agent) {
	if (self == NULL) {
		return;
	}
	memory_bfree(&agent->memory_context, self, sizeof(struct ring_object));
}

struct cp_object *
ring_object_config_new(
	struct agent *agent,
	const char *name,
	uint32_t capacity,
	uint32_t publish_batch,
	yanet_error **err
) {
	// Refuse a name the fixed header buffer would cut.
	//
	// Two cut names with the same prefix would share a registry key, and
	// publishing the second would silently replace the first ring.
	if (name == NULL || name[0] == '\0' ||
	    strnlen(name, CP_OBJECT_NAME_LEN) >= CP_OBJECT_NAME_LEN) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_INVALID_ARGUMENT,
			"ring name must be from 1 to %d bytes",
			CP_OBJECT_NAME_LEN - 1
		);
		errno = EINVAL;
		return NULL;
	}

	struct ring_object *self = ring_object_new(agent);
	if (self == NULL) {
		yanet_error_add(err, "failed to allocate ring object");
		errno = ENOMEM;
		return NULL;
	}

	// The cleanup below may change errno. Restore the value of the step
	// that failed, so the caller gets the errno the header promises.
	if (ring_object_init(self, agent, name, err)) {
		int saved_errno = errno;
		yanet_error_add(err, "failed to init ring object");
		ring_object_free(self, agent);
		errno = saved_errno;
		return NULL;
	}

	if (ring_object_create(self, capacity, publish_batch, err)) {
		int saved_errno = errno;
		yanet_error_add(err, "failed to create ring object");
		ring_object_fini(self);
		ring_object_free(self, agent);
		errno = saved_errno;
		return NULL;
	}

	return &self->cp_object;
}

int
ring_object_config_free(struct cp_object *cp_object, yanet_error **err) {
	if (cp_object_try_destroy(cp_object, err)) {
		return -1;
	}

	ring_object_destroy(cp_object);
	return 0;
}

void *
ring_object_align_alloc(
	struct memory_context *ctx,
	uint64_t stride,
	uint64_t count,
	uint64_t alignment,
	void **raw,
	uint64_t *raw_size
) {
	// Every caller passes a constant power-of-two alignment. It never
	// comes from external input, so an assert is enough.
	assert(alignment != 0 && (alignment & (alignment - 1)) == 0);

	if (stride > UINT64_MAX - (alignment - 1)) {
		errno = EOVERFLOW;
		return NULL;
	}
	uint64_t aligned_stride = next_divisible_pow2(stride, alignment);

	uint64_t body_size = 0;
	if (count != 0) {
		if (aligned_stride > UINT64_MAX / count) {
			errno = EOVERFLOW;
			return NULL;
		}
		body_size = aligned_stride * count;
	}

	if (body_size > UINT64_MAX - (alignment - 1)) {
		errno = EOVERFLOW;
		return NULL;
	}
	uint64_t total_size = body_size + (alignment - 1);

	void *block = memory_balloc(ctx, total_size);
	if (block == NULL) {
		errno = ENOMEM;
		return NULL;
	}
	memset(block, 0, total_size);

	uintptr_t aligned_addr =
		(uintptr_t)next_divisible_pow2((uintptr_t)block, alignment);

	*raw = block;
	*raw_size = total_size;
	return (void *)aligned_addr;
}

int
ring_object_create(
	struct ring_object *self,
	uint32_t capacity,
	uint32_t publish_batch,
	yanet_error **err
) {
	if (self->workers != NULL) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_FAILED_PRECONDITION,
			"ring object already created"
		);
		errno = EEXIST;
		return -1;
	}

	if (capacity < RING_RECORD_FRAME_SIZE ||
	    (capacity & (capacity - 1)) != 0) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_INVALID_ARGUMENT,
			"ring capacity %u must be a power of two of at "
			"least %zu bytes",
			capacity,
			RING_RECORD_FRAME_SIZE
		);
		errno = EINVAL;
		return -1;
	}
	if (publish_batch == 0 || publish_batch > RING_PUBLISH_BATCH_MAX) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_INVALID_ARGUMENT,
			"ring publish batch %u must be from 1 to %u records",
			publish_batch,
			RING_PUBLISH_BATCH_MAX
		);
		errno = EINVAL;
		return -1;
	}
	if (capacity > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_INVALID_ARGUMENT,
			"ring capacity %u exceeds the maximum block size %u",
			capacity,
			(unsigned)MEMORY_BLOCK_ALLOCATOR_MAX_SIZE
		);
		errno = E2BIG;
		return -1;
	}

	struct agent *agent = ADDR_OF(&self->cp_object.agent);
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	uint64_t dp_worker_count = dp_config->worker_count;
	if (dp_worker_count == 0) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_FAILED_PRECONDITION,
			"dataplane reports zero workers; a ring needs at least "
			"one"
		);
		errno = EINVAL;
		return -1;
	}
	if (dp_worker_count >= UINT16_MAX) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_FAILED_PRECONDITION,
			"dataplane reports %lu workers; a ring supports fewer "
			"than %u",
			(unsigned long)dp_worker_count,
			(unsigned)UINT16_MAX
		);
		errno = E2BIG;
		return -1;
	}
	uint16_t worker_count = (uint16_t)dp_worker_count;

	struct memory_context *ctx = &self->cp_object.memory_context;

	void *raw;
	uint64_t raw_size;
	struct ring_worker *workers =
		(struct ring_worker *)ring_object_align_alloc(
			ctx,
			sizeof(struct ring_worker),
			worker_count,
			YANET_CACHE_LINE_SIZE,
			&raw,
			&raw_size
		);
	if (workers == NULL) {
		int saved_errno = errno;
		yanet_error_add(
			err,
			"failed to allocate ring metadata for %u workers",
			(unsigned)worker_count
		);
		errno = saved_errno;
		return -1;
	}

	for (uint16_t idx = 0; idx < worker_count; ++idx) {
		uint8_t *data = memory_balloc(ctx, capacity);
		if (data == NULL) {
			for (uint16_t prev = 0; prev < idx; ++prev) {
				uint8_t *prior_data =
					ADDR_OF(&workers[prev].local.data);
				memory_bfree(ctx, prior_data, capacity);
			}
			memory_bfree(ctx, raw, raw_size);

			yanet_error_add(
				err,
				"failed to allocate ring data for worker "
				"%u",
				(unsigned)idx
			);
			errno = ENOMEM;
			return -1;
		}
		memset(data, 0, capacity);

		ring_worker_init(&workers[idx], capacity, publish_batch);
		SET_OFFSET_OF(&workers[idx].local.data, data);
	}

	self->worker_count = worker_count;
	self->capacity = capacity;
	self->publish_batch = publish_batch;
	self->workers_raw_size = raw_size;
	SET_OFFSET_OF(&self->workers_raw, raw);
	SET_OFFSET_OF(&self->workers, workers);

	return 0;
}

uint32_t
ring_object_capacity(const struct cp_object *cp_object) {
	const struct ring_object *self =
		container_of(cp_object, struct ring_object, cp_object);

	return self->capacity;
}

uint32_t
ring_object_publish_batch(const struct cp_object *cp_object) {
	const struct ring_object *self =
		container_of(cp_object, struct ring_object, cp_object);

	return self->publish_batch;
}

struct ring_worker *
ring_object_worker(const struct cp_object *cp_object, uint64_t worker_idx) {
	const struct ring_object *self =
		container_of(cp_object, struct ring_object, cp_object);

	if (worker_idx >= self->worker_count) {
		return NULL;
	}

	struct ring_worker *workers = ADDR_OF(&self->workers);
	return &workers[worker_idx];
}

uint8_t *
ring_object_worker_data(
	const struct cp_object *cp_object, uint64_t worker_idx
) {
	struct ring_worker *worker = ring_object_worker(cp_object, worker_idx);
	if (worker == NULL) {
		return NULL;
	}

	return ADDR_OF(&worker->local.data);
}

bool
ring_object_worker_view(
	const struct cp_object *cp_object,
	uint64_t worker_idx,
	struct ring_worker_view *view
) {
	struct ring_worker *worker = ring_object_worker(cp_object, worker_idx);
	if (worker == NULL) {
		return false;
	}

	// The view drops the atomic qualifier for callers in other languages.
	//
	// Only the writer stores to the positions. Every reader load must still
	// be atomic, with acquire order.
	view->write_idx = (uint64_t *)&worker->published.write_idx;
	view->readable_idx = (uint64_t *)&worker->published.readable_idx;
	view->data = ADDR_OF(&worker->local.data);
	view->size = worker->local.size;
	view->mask = worker->local.mask;
	return true;
}

bool
ring_object_exists(struct agent *agent, const char *name) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	cp_config_lock(cp_config);
	struct cp_config_gen *gen = ADDR_OF(&cp_config->cp_config_gen);
	bool exists =
		cp_config_gen_lookup_object(gen, RING_OBJECT_TYPE, name) !=
		NULL;
	cp_config_unlock(cp_config);

	return exists;
}

struct object *
new_object_ring() {
	struct object *object = (struct object *)malloc(sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name, RING_OBJECT_TYPE, sizeof(object->name));
	return object;
}
