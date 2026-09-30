#pragma once

/*
 * Per-worker ring buffer of opaque records.
 *
 * Each ring has one writer, the dataplane worker. Readers may live in
 * another process. Nobody takes a lock. The writer never waits: when the
 * ring is full, it drops the oldest whole records to make room.
 *
 * Each worker has two separate allocations: the metadata and the data area.
 * The metadata puts the writer's private state and the positions readers
 * poll on different cache lines. So a reader does not slow down the
 * writer's per-record updates. Writers of neighbouring workers never share
 * a line either.
 *
 * The writer commits records one by one but publishes them in batches.
 * One store to the reader-visible line then covers a whole batch. In the
 * same way, eviction frees space in chunks, so one store and one fence
 * cover many dropped records. A commit publishes by itself when the batch
 * is full. The producer also publishes at the end of each call, so the
 * last records of a slow producer do not wait for a full batch.
 *
 * No producer uses this ring yet. Pdump capture still has its own rings.
 */

#include <assert.h>
#include <errno.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

#include "common/cache.h"
#include "common/likely.h"
#include "common/memory_address.h"
#include "common/numutils.h"

// Readers in other languages decode the record frame as little-endian, so
// the writer must be little-endian too.
_Static_assert(
	__BYTE_ORDER__ == __ORDER_LITTLE_ENDIAN__,
	"the ring wire format is little-endian"
);

// Round a record length up to a multiple of 4.
//
// Every record starts on a 4-byte boundary.
static inline uint32_t
ring_align4(uint32_t val) {
	return (uint32_t)next_divisible_pow2(val, 4);
}

// Header in front of every record's opaque payload.
//
// The length counts the header and the payload together. The writer sets
// the sequence number at commit. Each worker numbers its own records.
// Records are only 4-byte aligned. So a reader must copy the header out
// before it reads the length, and must not read it in place.
struct ring_record_frame {
	uint32_t total_len;
	uint32_t seqno;
};

_Static_assert(
	sizeof(struct ring_record_frame) == 8,
	"ring record frame must be the 8-byte wire size"
);

// Smallest valid record length: the header with an empty payload.
#define RING_RECORD_FRAME_SIZE (sizeof(struct ring_record_frame))

// Largest eviction chunk, in bytes.
//
// When a write must evict, it frees at least one chunk, or its own length
// if that is larger. So when the ring overflows all the time, one store of
// the readable position and one fence cover a whole chunk: tens of small
// records. A bigger chunk has two costs. It drops more history early. And
// each eviction reads more record headers at once, from lines a reader
// may hold.
#define RING_EVICT_CHUNK_MAX 4096u
// Divisor that limits the eviction chunk of a small ring.
//
// The chunk is at most this share of the ring capacity, so one eviction
// never drops more than this share of the history early.
#define RING_EVICT_CHUNK_SHARE 16u

// Default publish batch: how many committed records a commit collects
// before it publishes them.
//
// Each publication stores to the line that readers poll. If a reader is
// polling, the store also moves that line between CPUs. A batch pays this
// cost once for a short burst of records. A busy writer's records still
// stay only a few behind what readers see.
#define RING_PUBLISH_BATCH_DEFAULT 8u
// Largest publish batch a ring accepts.
//
// Above it, the cost of one store per batch is already very small. A
// bigger batch only makes readers wait longer for new records.
#define RING_PUBLISH_BATCH_MAX 1024u

// Writer-private half of a worker's ring metadata.
//
// The writer keeps the true positions here. Readers never load these
// positions, so the writer's per-record stores never move this line to
// another CPU. The last published write position is where the unpublished
// batch starts. The eviction cursor is the record boundary the writer has
// already walked to for the next eviction. The record count tells how many
// records the unpublished batch holds. The size, mask, eviction chunk,
// publish batch and data pointer never change after creation. A reader
// loads the size, mask and data pointer once, when it attaches.
struct ring_worker_private {
	uint64_t write_idx;
	uint64_t readable_idx;
	uint64_t published_write_idx;
	uint64_t evict_idx;
	uint8_t *data;
	uint32_t next_seqno;
	uint32_t size;
	uint32_t mask;
	uint32_t evict_chunk;
	uint32_t publish_batch;
	uint32_t batch_records;
} __attribute__((aligned(YANET_CACHE_LINE_SIZE)));

// Reader-visible half of a worker's ring metadata.
//
// The writer only stores to these positions, with release order. It never
// loads them. So a polling reader costs the writer at most one line
// transfer per publication or eviction, not one per record.
struct ring_worker_published {
	_Atomic uint64_t write_idx;
	_Atomic uint64_t readable_idx;
} __attribute__((aligned(YANET_CACHE_LINE_SIZE)));

// Per-worker ring metadata, one per dataplane worker.
//
// The private half and the published half sit on separate cache lines.
// An unused guard line follows each half. Some CPUs prefetch lines in
// pairs. The guard lines make sure such a pair never joins a line the
// writer stores to with a line a reader polls. This holds for this worker
// and its neighbours, for any alignment of the array.
//
// Positions are logical byte offsets into the data area and only grow.
// The ring size minus one is the mask that turns a logical offset into a
// physical one. The data pointer is relative to the shared memory. Each
// process turns it into an address in its own mapping before use.
struct ring_worker {
	struct ring_worker_private local;
	uint8_t local_guard[YANET_CACHE_LINE_SIZE];
	struct ring_worker_published published;
	uint8_t published_guard[YANET_CACHE_LINE_SIZE];
} __attribute__((aligned(YANET_CACHE_LINE_SIZE)));

_Static_assert(
	sizeof(struct ring_worker_private) == YANET_CACHE_LINE_SIZE,
	"the writer-private ring metadata must fill exactly one cache line"
);
_Static_assert(
	sizeof(struct ring_worker_published) == YANET_CACHE_LINE_SIZE,
	"the published ring metadata must fill exactly one cache line"
);
_Static_assert(
	sizeof(struct ring_worker) == 4 * YANET_CACHE_LINE_SIZE,
	"ring_worker must be its two halves and their guard lines"
);
_Static_assert(
	_Alignof(struct ring_worker) == YANET_CACHE_LINE_SIZE,
	"ring_worker must be aligned to exactly one cache line"
);

// Eviction chunk for a ring of the given power-of-two capacity.
//
// The chunk is a fixed share of the capacity, with an upper cap. It is
// rounded down to a multiple of 4, the record alignment.
static inline uint32_t
ring_evict_chunk(uint32_t size) {
	uint32_t chunk = size / RING_EVICT_CHUNK_SHARE;
	if (chunk > RING_EVICT_CHUNK_MAX) {
		chunk = RING_EVICT_CHUNK_MAX;
	}
	return chunk & ~3u;
}

// Set up an empty ring with a power-of-two capacity and a publish batch.
//
// The publish batch is from 1 to RING_PUBLISH_BATCH_MAX records. The caller
// sets the data area pointer. Call it only when no writer and no reader
// runs.
static inline void
ring_worker_init(
	struct ring_worker *ring, uint32_t size, uint32_t publish_batch
) {
	memset(ring, 0, sizeof(*ring));
	ring->local.size = size;
	ring->local.mask = size - 1;
	ring->local.evict_chunk = ring_evict_chunk(size);
	ring->local.publish_batch = publish_batch;
}

// Set the write and readable positions in both halves.
//
// After the call there is no unpublished batch. Use it only for setup and
// tests, when no writer and no reader runs.
static inline void
ring_worker_set_positions(
	struct ring_worker *ring, uint64_t write_idx, uint64_t readable_idx
) {
	ring->local.write_idx = write_idx;
	ring->local.readable_idx = readable_idx;
	ring->local.published_write_idx = write_idx;
	ring->local.evict_idx = readable_idx;
	ring->local.batch_records = 0;
	atomic_store_explicit(
		&ring->published.readable_idx,
		readable_idx,
		memory_order_release
	);
	atomic_store_explicit(
		&ring->published.write_idx, write_idx, memory_order_release
	);
}

// Largest size of one batch, in bytes.
//
// A batch is all records committed since the last publication, plus the
// record being prepared. Sizes are aligned lengths. While a batch stays
// within this limit, evicting all published records frees a full chunk
// on top of the batch. So a batch never has to evict its own records. If
// a new record would go over the limit, the writer first publishes the
// batch, even a batch of few records. This is also the largest record
// the ring accepts.
static inline uint32_t
ring_worker_batch_max(const struct ring_worker *ring) {
	return ring->local.size - ring->local.evict_chunk;
}

// Bytes the unpublished batch can still grow before the writer publishes
// it by itself.
//
// A producer may use this to size its batches. A larger record is still
// accepted: the writer first publishes the records committed so far.
static inline uint64_t
ring_worker_batch_room(const struct ring_worker *ring) {
	uint64_t pending =
		ring->local.write_idx - ring->local.published_write_idx;
	return ring_worker_batch_max(ring) - pending;
}

// Publish all records committed since the last publication.
//
// One release store of the write position does it. With no new records,
// the call does nothing. A commit calls it when the batch is full. A
// producer calls it at the end of each call or burst, so a partial batch
// reaches readers without waiting for more traffic. This worker is the
// only writer, so a plain store is enough, with no read-modify-write. The
// check uses the private copy and does not load the line readers poll.
static inline void
ring_worker_publish(struct ring_worker *ring) {
	uint64_t write_idx = ring->local.write_idx;
	if (write_idx == ring->local.published_write_idx) {
		return;
	}
	ring->local.published_write_idx = write_idx;
	ring->local.batch_records = 0;
	atomic_store_explicit(
		&ring->published.write_idx, write_idx, memory_order_release
	);
}

// Make an eviction visible to readers before the writer overwrites any
// byte of the evicted records.
//
// A release store orders only the accesses before it. On arm64 a reader
// may see the new bytes before it sees the new readable position. It then
// copies a mix of old and new bytes, its recheck after the copy passes,
// and it accepts a broken record. This fence stops that. On arm64 it costs
// one barrier per eviction chunk: `dmb ish`, or `dmb ishld` plus
// `dmb ishst` with newer GCC. The barrier waits until the position store
// reaches its line. That is a round trip if a reader holds the line. On
// x86-64 it emits no instruction. It only stops the compiler from moving
// the data stores above it.
static inline void
ring_evict_fence(void) {
	atomic_thread_fence(memory_order_release);
}

// Logical position right after the record that starts at the given
// position, or the bound if the record length is corrupt.
//
// A length is corrupt if it is smaller than the header, larger than the
// ring, or the record runs past the bound. The raw length is checked
// before alignment, so the rounding cannot overflow.
static inline uint64_t
ring_evict_next(
	const struct ring_worker *ring,
	const uint8_t *data,
	uint64_t readable_idx,
	uint64_t bound
) {
	uint32_t len;
	memcpy(&len, data + (readable_idx & ring->local.mask), sizeof(len));
	if (unlikely(
		    len < RING_RECORD_FRAME_SIZE || len > ring->local.size ||
		    readable_idx + ring_align4(len) > bound
	    )) {
		return bound;
	}
	return readable_idx + ring_align4(len);
}

// Move the eviction cursor one published record forward, if it is less
// than a chunk ahead of the readable position.
//
// The writer calls it on every write that needs no eviction. Each call
// loads one record header, and the CPU overlaps that load with the write.
// So when the ring overflows all the time, the cursor keeps up with the
// writer. The next eviction finds its chunk already walked and does not
// wait on a chain of dependent loads. It only reads headers.
static inline void
ring_worker_walk_ahead(struct ring_worker *ring, const uint8_t *data) {
	uint64_t evict_idx = ring->local.evict_idx;
	uint64_t batch_idx = ring->local.published_write_idx;
	if (evict_idx - ring->local.readable_idx < ring->local.evict_chunk &&
	    evict_idx < batch_idx) {
		ring->local.evict_idx =
			ring_evict_next(ring, data, evict_idx, batch_idx);
	}
}

// Drop whole oldest records until a chunk of space is free.
//
// Then the writer publishes the new readable position and fences it before
// any overwrite: one store and one fence for the whole chunk. The walk
// starts at the eviction cursor. Only published records are dropped, so
// the unpublished batch never drops its own records. A corrupt length
// drops all published records at once. The batch is already within its
// limit here, so dropping all published records always makes room.
static inline void
ring_worker_evict(
	struct ring_worker *ring, const uint8_t *data, uint32_t aligned_len
) {
	uint64_t write_idx = ring->local.write_idx;
	uint64_t batch_idx = ring->local.published_write_idx;
	uint64_t readable_idx = ring->local.evict_idx;
	if (readable_idx < ring->local.readable_idx) {
		readable_idx = ring->local.readable_idx;
	}

	uint32_t free_target = ring->local.evict_chunk;
	if (free_target < aligned_len) {
		free_target = aligned_len;
	}
	uint64_t keep_limit = ring->local.size - free_target;
	while (write_idx - readable_idx > keep_limit && readable_idx < batch_idx
	) {
		readable_idx =
			ring_evict_next(ring, data, readable_idx, batch_idx);
	}

	ring->local.readable_idx = readable_idx;
	ring->local.evict_idx = readable_idx;
	atomic_store_explicit(
		&ring->published.readable_idx,
		readable_idx,
		memory_order_release
	);
	ring_evict_fence();
}

// Make room for a record of the given length; 0 on success, -1 with errno
// on error.
//
// If the record does not fit, the writer drops a chunk of whole oldest
// records. No record bytes are written here. EINVAL means the length is
// smaller than the header. E2BIG means it is larger than the batch limit.
// On error no position changes. If the record would push the unpublished
// batch past the limit, the writer first publishes the batch. This happens
// before any eviction and before any byte of the new record. So a long
// batch is never refused and never drops its own records. The caller
// passes the data area already resolved, so it is not resolved per call.
static inline int
ring_worker_prepare(
	struct ring_worker *ring, uint8_t *data, uint32_t total_len
) {
	if (unlikely(total_len < RING_RECORD_FRAME_SIZE)) {
		errno = EINVAL;
		return -1;
	}
	// Check the raw length before alignment, so the rounding cannot
	// overflow.
	//
	// The limit is a multiple of 4, so it bounds the aligned length too.
	if (unlikely(total_len > ring_worker_batch_max(ring))) {
		errno = E2BIG;
		return -1;
	}
	uint32_t aligned_total_len = ring_align4(total_len);
	if (unlikely(aligned_total_len > ring_worker_batch_room(ring))) {
		ring_worker_publish(ring);
	}

	// Use only the private positions. Do not load the line a reader may be
	// polling.
	uint64_t occupied = ring->local.write_idx - ring->local.readable_idx;
	if (likely(occupied <= ring->local.size - aligned_total_len)) {
		ring_worker_walk_ahead(ring, data);
		return 0;
	}
	ring_worker_evict(ring, data, aligned_total_len);
	return 0;
}

// Copy a piece of an uncommitted record at the given offset from its start.
//
// The copy wraps around at the physical end of the ring. A record may be
// built from several pieces at growing offsets. For example, a fixed
// header first and then the payload bytes, with no extra copy. Every
// piece must stay within the length that the prepare step reserved.
static inline void
ring_worker_write(
	struct ring_worker *ring,
	uint8_t *data,
	uint64_t offset,
	const uint8_t *payload,
	uint64_t size
) {
	assert(ring->local.size >= offset + size);

	uint64_t write_idx = ring->local.write_idx;
	uint64_t written = 0;
	while (written < size) {
		uint64_t pos =
			(write_idx + offset + written) & ring->local.mask;
		uint64_t tail = ring->local.size - pos;
		uint64_t remaining = size - written;
		uint64_t chunk = remaining > tail ? tail : remaining;

		assert(chunk > 0);
		memcpy(data + pos, payload + written, chunk);
		written += chunk;
	}
}

// Write the record header and add the record to the unpublished batch.
//
// The header gets the worker's next sequence number. When the batch
// reaches the ring's publish batch size, the writer publishes it. Readers
// see the record only after the next publication. Returns the sequence
// number. It wraps from UINT32_MAX to 0.
static inline uint32_t
ring_worker_commit(
	struct ring_worker *ring, uint8_t *data, uint32_t total_len
) {
	uint32_t seqno = ring->local.next_seqno;
	ring->local.next_seqno = seqno + 1;

	struct ring_record_frame frame = {
		.total_len = total_len, .seqno = seqno
	};
	ring_worker_write(
		ring, data, 0, (const uint8_t *)&frame, sizeof(frame)
	);

	ring->local.write_idx += ring_align4(total_len);
	if (++ring->local.batch_records >= ring->local.publish_batch) {
		ring_worker_publish(ring);
	}
	return seqno;
}
