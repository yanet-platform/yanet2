#pragma once

#include <stddef.h>
#include <stdint.h>

#include <string.h>

#include "common/memory.h"
#include "common/memory_address.h"

#include "types.h"

// Decision state of one stashed sync record.
//
// ACL appends records as PENDING. The first fwstate configuration that
// reaches a record applies it to the map table and moves it to APPLIED or
// SUPPRESSED, so every later consumer only emits it or skips it.
enum fwstate_sync_record_status {
	// NOLINTBEGIN(readability-identifier-naming)
	FWSTATE_SYNC_RECORD_PENDING,
	FWSTATE_SYNC_RECORD_APPLIED,
	FWSTATE_SYNC_RECORD_SUPPRESSED,
	// NOLINTEND(readability-identifier-naming)
};

// One state-sync event captured from a packet ACL allowed.
//
// The frame is a copy, so later mutation or release of the original
// packet never changes it.
struct fwstate_sync_record {
	struct fw_state_sync_frame frame;
	uint16_t rx_device_id;
	uint16_t tx_device_id;
	uint8_t status;
};

// Records a per-worker stash holds by default.
//
// Derived from two typical 32-packet rx bursts per round, one record per
// stateful packet. The count is fwstate's own constant rather than the
// worker's burst size, so the map object does not depend on worker
// internals.
#define FWSTATE_STASH_DEFAULT_RECORDS 64

// Per-worker stash buffer size in bytes when the map create request leaves
// it zero.
#define FWSTATE_STASH_DEFAULT_SIZE                                             \
	(FWSTATE_STASH_DEFAULT_RECORDS * sizeof(struct fwstate_sync_record))

// Largest per-worker stash buffer size a map create request may ask for.
#define FWSTATE_STASH_MAX_SIZE (1024 * 1024)

// Smallest per-worker stash buffer size: one record.
#define FWSTATE_STASH_MIN_SIZE (sizeof(struct fwstate_sync_record))

// Number of records a per-worker stash buffer of size bytes holds.
static inline uint32_t
fwstate_stash_capacity(uint64_t size) {
	return (uint32_t)(size / sizeof(struct fwstate_sync_record));
}

// Per-worker stash slot header of one fwstate map object.
//
// Only the owning worker touches a slot. The map object sees the slot's
// buffer as opaque bytes; ACL and fwstate lay records out in it. The
// records of the current round are [0, count); a slot whose iteration
// differs from the worker's round counter holds stale records and is reset
// on first access. Headers and buffers are separate 64-byte aligned
// allocations, so no two workers share a cache line.
struct fwstate_stash_slot {
	uint64_t iteration;
	uint32_t count;
	// Offset pointer to this worker's stash buffer.
	void *buffer;
} __attribute__((__aligned__(64)));

// Return the absolute address of a slot's buffer as its records array.
static inline struct fwstate_sync_record *
fwstate_stash_slot_records(struct fwstate_stash_slot *slot) {
	return (struct fwstate_sync_record *)ADDR_OF(&slot->buffer);
}

// A module's direct link to one worker's stash of one map object, taken
// when the module's execution context is committed. An empty link has a
// NULL slot, and nothing is written to or read from it.
struct fwstate_stash_link {
	struct fwstate_stash_slot *slot;
	struct fwstate_sync_record *records;
	uint32_t capacity;
};

// Point a link at a slot whose buffer is size bytes, or empty it when slot
// is NULL.
//
// A commit may rerun on a context its worker already uses, so every field
// is overwritten in place with its final value and the slot goes last: a
// reader never sees a slot without its records and capacity.
static inline void
fwstate_stash_link_set(
	struct fwstate_stash_link *link,
	struct fwstate_stash_slot *slot,
	uint64_t size
) {
	if (slot == NULL) {
		link->slot = NULL;
		link->records = NULL;
		link->capacity = 0;
		return;
	}
	link->records = fwstate_stash_slot_records(slot);
	link->capacity = fwstate_stash_capacity(size);
	link->slot = slot;
}

// Drop the records of an earlier round before the slot is used.
static inline void
fwstate_stash_slot_sync_round(
	struct fwstate_stash_slot *slot, uint64_t iteration
) {
	if (slot->iteration != iteration) {
		slot->count = 0;
		slot->iteration = iteration;
	}
}

// Return the first free record of the current round in the slot's records
// array, or NULL when the slot already holds capacity records.
//
// The record is not counted until fwstate_stash_slot_commit, so a caller
// that fails to fill it leaves the slot unchanged.
static inline struct fwstate_sync_record *
fwstate_stash_slot_next(
	struct fwstate_stash_slot *slot,
	struct fwstate_sync_record *records,
	uint32_t capacity
) {
	if (slot->count >= capacity) {
		return NULL;
	}
	return &records[slot->count];
}

// Count the record returned by fwstate_stash_slot_next.
static inline void
fwstate_stash_slot_commit(struct fwstate_stash_slot *slot) {
	slot->count += 1;
}

// Stash storage owned by one fwstate map object: one slot header per
// worker, each pointing at that worker's own buffer of the stash size.
//
// The headers live in the object's memory context and are absent until the
// stash is created.
struct fwstate_stash {
	uint16_t worker_count;
	uint64_t size;
	struct fwstate_stash_slot *slots;
};

// Size of a cache-line aligned stash allocation of size bytes.
static inline size_t
fwstate_stash_alloc_size(size_t size) {
	return (size + 63) & ~(size_t)63;
}

// Allocate size zeroed bytes from ctx at a 64-byte aligned address, or
// return NULL.
//
// The block allocator aligns every block to its power-of-two size, up to
// the huge page, and red zones keep that 64-byte alignment, so rounding the
// size to the cache line is enough and no slack is taken: a buffer at the
// 1 MiB maximum stays in the 1 MiB size class.
static inline void *
fwstate_stash_zalloc(struct memory_context *ctx, size_t size) {
	size = fwstate_stash_alloc_size(size);
	void *block = memory_balloc(ctx, size);
	if (block == NULL) {
		return NULL;
	}
	if (((uintptr_t)block & 63) != 0) {
		// The allocator broke its alignment contract; refuse the
		// block rather than share a cache line.
		memory_bfree(ctx, block, size);
		return NULL;
	}
	memset(block, 0, size);
	return block;
}

// Return a block of size bytes allocated by fwstate_stash_zalloc to ctx.
static inline void
fwstate_stash_zfree(struct memory_context *ctx, void *block, size_t size) {
	memory_bfree(ctx, block, fwstate_stash_alloc_size(size));
}
