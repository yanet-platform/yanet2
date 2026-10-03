/*
 * Tests for what the ring writer stores in shared memory: wrap, publication,
 * chunked eviction, size checks and sequence numbers.
 */

#include "common/test_assert.h"

#include "common/ring.h"

#include "lib/logging/log.h"

#include <errno.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// Build a zeroed ring of the given size and its data area.
//
// The caller frees the data area. It is NULL when the allocation fails. The
// publish batch is the largest a ring accepts. So a commit publishes by
// itself only when the batch passes the byte limit, and each test publishes
// explicitly.
static struct ring_worker
init_test_ring(uint32_t size, uint8_t **data) {
	struct ring_worker ring;
	ring_worker_init(&ring, size, RING_PUBLISH_BATCH_MAX);
	*data = calloc(1, size);
	return ring;
}

// Readable position as a reader sees it.
//
// It first checks that the published value equals the writer's own copy.
static long
published_readable(struct ring_worker *ring) {
	uint64_t published = atomic_load(&ring->published.readable_idx);
	if (published != ring->local.readable_idx) {
		LOG(ERROR,
		    "published readable position %lu differs from the "
		    "writer's %lu",
		    (unsigned long)published,
		    (unsigned long)ring->local.readable_idx);
		abort();
	}
	return (long)published;
}

// Write position as a reader sees it.
//
// It first checks that the published value equals the writer's own copy.
static long
published_write(struct ring_worker *ring) {
	uint64_t published = atomic_load(&ring->published.write_idx);
	if (published != ring->local.write_idx) {
		LOG(ERROR,
		    "published write position %lu differs from the writer's "
		    "%lu",
		    (unsigned long)published,
		    (unsigned long)ring->local.write_idx);
		abort();
	}
	return (long)published;
}

// Copy the frame at a logical position out of the ring.
//
// Like a reader, it wraps at the physical end of the ring.
static struct ring_record_frame
read_frame(const struct ring_worker *ring, const uint8_t *data, uint64_t pos) {
	uint8_t raw[sizeof(struct ring_record_frame)];
	for (size_t i = 0; i < sizeof(raw); ++i) {
		raw[i] = data[(pos + i) & ring->local.mask];
	}
	struct ring_record_frame frame;
	memcpy(&frame, raw, sizeof(frame));
	return frame;
}

// Log the writer invariant and abort if it does not hold.
#define CHECK_INVARIANT(cond)                                                  \
	do {                                                                   \
		if (!(cond)) {                                                 \
			LOG(ERROR, "ring invariant failed: %s", #cond);        \
			abort();                                               \
		}                                                              \
	} while (0)

// Walk whole records from a position until one ends at or past the bound.
//
// Returns the record boundary where the walk stopped. A length shorter
// than the frame or larger than the ring breaks the invariant.
static uint64_t
walk_frames(
	const struct ring_worker *ring,
	const uint8_t *data,
	uint64_t pos,
	uint64_t bound
) {
	while (pos < bound) {
		uint32_t len = read_frame(ring, data, pos).total_len;
		CHECK_INVARIANT(
			len >= RING_RECORD_FRAME_SIZE && len <= ring->local.size
		);
		pos += ring_align4(len);
	}
	return pos;
}

// Abort unless the writer's positions and records are consistent.
//
// The published readable position equals the private one, and the
// published write position is the batch start. The readable position, the
// eviction cursor, the batch start and the write position come in that
// order, and the occupied bytes never exceed the ring. With a data
// area, whole records also lead from the readable position through the
// cursor and the batch start to the write position. A scenario that has
// just corrupted a length passes no data area until the writer resyncs.
static void
check_invariants(const struct ring_worker *ring, const uint8_t *data) {
	const struct ring_worker_private *local = &ring->local;
	uint64_t batch_idx = local->published_write_idx;
	CHECK_INVARIANT(
		atomic_load(&ring->published.readable_idx) ==
		local->readable_idx
	);
	CHECK_INVARIANT(atomic_load(&ring->published.write_idx) == batch_idx);
	CHECK_INVARIANT(local->readable_idx <= local->evict_idx);
	CHECK_INVARIANT(local->evict_idx <= batch_idx);
	CHECK_INVARIANT(batch_idx <= local->write_idx);
	CHECK_INVARIANT(local->write_idx - local->readable_idx <= local->size);
	if (data == NULL) {
		return;
	}
	uint64_t pos = local->readable_idx;
	pos = walk_frames(ring, data, pos, local->evict_idx);
	CHECK_INVARIANT(pos == local->evict_idx);
	pos = walk_frames(ring, data, pos, batch_idx);
	CHECK_INVARIANT(pos == batch_idx);
	pos = walk_frames(ring, data, pos, local->write_idx);
	CHECK_INVARIANT(pos == local->write_idx);
}

// Publish the unpublished batch and check the writer invariants.
static void
publish_checked(struct ring_worker *ring, const uint8_t *data) {
	ring_worker_publish(ring);
	check_invariants(ring, data);
}

// A record that crosses the physical end of the ring keeps its payload.
//
// Every payload byte reads back unchanged after the wrap.
static int
run_ring_wrap_roundtrip_test() {
	const uint32_t ring_size = 32;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");

	// Start 12 bytes before the end. The 8-byte frame fits before the
	// end, and the payload wraps: [28,32) then [0,4).
	ring_worker_set_positions(&ring, ring_size - 12, ring_size - 12);
	check_invariants(&ring, data);

	const uint8_t payload[8] = {1, 2, 3, 4, 5, 6, 7, 8};
	uint32_t total_len = RING_RECORD_FRAME_SIZE + sizeof(payload);

	TEST_ASSERT_EQUAL(
		ring_worker_prepare(&ring, data, total_len),
		0,
		"prepare must succeed on an empty ring"
	);
	check_invariants(&ring, data);
	ring_worker_write(
		&ring, data, RING_RECORD_FRAME_SIZE, payload, sizeof(payload)
	);
	check_invariants(&ring, data);
	ring_worker_commit(&ring, data, total_len);
	check_invariants(&ring, data);
	publish_checked(&ring, data);

	uint8_t roundtrip[8];
	for (size_t i = 0; i < sizeof(payload); ++i) {
		uint64_t pos = (ring_size - 12 + RING_RECORD_FRAME_SIZE + i) &
			       ring.local.mask;
		roundtrip[i] = data[pos];
	}
	TEST_ASSERT_EQUAL(
		memcmp(roundtrip, payload, sizeof(payload)),
		0,
		"payload must round-trip unchanged across the physical wrap"
	);

	free(data);
	return TEST_SUCCESS;
}

// Commit one fixed-size record without publishing it.
//
// The payload repeats one fill byte, so a later read can tell which record
// is in a slot. It aborts if a step breaks a writer invariant, or if the
// ring refuses the record: every caller passes a valid size.
static void
commit_fixed_record(
	struct ring_worker *ring,
	uint8_t *data,
	uint8_t fill,
	uint32_t total_len
) {
	uint32_t payload_len = total_len - RING_RECORD_FRAME_SIZE;
	uint8_t payload[64];
	memset(payload, fill, payload_len);

	if (ring_worker_prepare(ring, data, total_len) != 0) {
		LOG(ERROR, "ring_worker_prepare(%u) failed", total_len);
		abort();
	}
	check_invariants(ring, data);
	ring_worker_write(
		ring, data, RING_RECORD_FRAME_SIZE, payload, payload_len
	);
	check_invariants(ring, data);
	ring_worker_commit(ring, data, total_len);
	check_invariants(ring, data);
}

// Commit one fixed-size record and publish it at once.
//
// This is what a producer does when it publishes after every record.
static void
write_fixed_record(
	struct ring_worker *ring,
	uint8_t *data,
	uint8_t fill,
	uint32_t total_len
) {
	commit_fixed_record(ring, data, fill, total_len);
	publish_checked(ring, data);
}

// A full ring drops whole oldest records and keeps the others intact.
//
// The readable position stops on a record boundary, also when one record
// needs the space of several. The bytes of every record that stays do not
// change.
static int
run_ring_overwrite_evicts_whole_records_test() {
	const uint32_t ring_size = 64;
	const uint32_t record_len = 16;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");

	// Fill the ring exactly with four 16-byte records, one fill byte each.
	write_fixed_record(&ring, data, 0xA0, record_len);
	write_fixed_record(&ring, data, 0xA1, record_len);
	write_fixed_record(&ring, data, 0xA2, record_len);
	write_fixed_record(&ring, data, 0xA3, record_len);
	TEST_ASSERT_EQUAL(
		published_readable(&ring), 0L, "a full ring must not evict yet"
	);

	// The fifth record needs room, so the writer evicts exactly once.
	write_fixed_record(&ring, data, 0xA4, record_len);
	TEST_ASSERT_EQUAL(
		published_readable(&ring),
		(long)record_len,
		"eviction must land on a record boundary"
	);

	// Records 1-3 are at physical [16,64). They must not change.
	uint8_t expected;
	expected = 0xA1;
	for (uint32_t i = 16 + RING_RECORD_FRAME_SIZE; i < 32; ++i) {
		TEST_ASSERT_EQUAL(
			data[i], expected, "record 1 payload must survive"
		);
	}
	expected = 0xA2;
	for (uint32_t i = 32 + RING_RECORD_FRAME_SIZE; i < 48; ++i) {
		TEST_ASSERT_EQUAL(
			data[i], expected, "record 2 payload must survive"
		);
	}
	expected = 0xA3;
	for (uint32_t i = 48 + RING_RECORD_FRAME_SIZE; i < 64; ++i) {
		TEST_ASSERT_EQUAL(
			data[i], expected, "record 3 payload must survive"
		);
	}
	// Record 4 now sits in the old slot of record 0, at [0,16).
	expected = 0xA4;
	for (uint32_t i = RING_RECORD_FRAME_SIZE; i < 16; ++i) {
		TEST_ASSERT_EQUAL(
			data[i],
			expected,
			"record 4 must occupy the evicted slot"
		);
	}

	// Records 1-4 fill [16,80). A 40-byte record needs three of them gone,
	// so the readable position stops on the boundary after record 3.
	TEST_ASSERT_EQUAL(
		ring_worker_prepare(&ring, data, 40),
		0,
		"prepare must succeed by evicting"
	);
	check_invariants(&ring, data);
	TEST_ASSERT_EQUAL(
		published_readable(&ring),
		64L,
		"eviction must stop on the first boundary that fits the record"
	);
	TEST_ASSERT_EQUAL(
		published_write(&ring), 80L, "prepare must not move write_idx"
	);

	free(data);
	return TEST_SUCCESS;
}

// A corrupt length in the oldest record drops all published records.
//
// The readable position jumps to the write position. It never stops in the
// middle of a record. A length is corrupt when it is zero, shorter than a
// frame, larger than the ring, or runs past the write position. Larger than
// the ring includes values that wrap a 32-bit integer when rounded up.
static int
run_ring_eviction_corrupt_length_catches_up_test() {
	const uint32_t ring_size = 64;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");

	write_fixed_record(&ring, data, 0xC0, 16);
	write_fixed_record(&ring, data, 0xC1, 16);
	write_fixed_record(&ring, data, 0xC2, 16);
	write_fixed_record(&ring, data, 0xC3, 16);

	struct {
		const char *name;
		uint32_t total_len;
	} cases[] = {
		{"zero length", 0},
		{"length 4", 4},
		{"length 7", 7},
		{"length past the write position", 32 + 64},
		{"length one above the ring size", 64 + 1},
		{"length whose alignment wraps", UINT32_MAX},
	};
	// Put a plausible length into the sequence number of the oldest frame.
	//
	// The probe record needs room for only one frame. If the eviction walk
	// trusted a short length, it would step 4 or 8 bytes into the record
	// and stop there, in the middle of the record.
	const uint32_t planted_len = 4;
	memcpy(data + sizeof(uint32_t), &planted_len, sizeof(planted_len));
	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
		// Go back to the full ring and corrupt the oldest frame.
		ring_worker_set_positions(&ring, ring.local.write_idx, 0);
		memcpy(data, &cases[i].total_len, sizeof(cases[i].total_len));
		check_invariants(&ring, NULL);

		TEST_ASSERT_EQUAL(
			ring_worker_prepare(
				&ring, data, RING_RECORD_FRAME_SIZE
			),
			0,
			"%s: prepare must still succeed",
			cases[i].name
		);
		check_invariants(&ring, data);
		TEST_ASSERT_EQUAL(
			published_readable(&ring),
			published_write(&ring),
			"%s: readable_idx must catch up to write_idx",
			cases[i].name
		);
	}

	free(data);
	return TEST_SUCCESS;
}

// The writer refuses a record smaller than the frame.
//
// It refuses it before it changes any position or any data byte.
static int
run_ring_prepare_rejects_undersize_test() {
	const uint32_t ring_size = 32;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");

	int rc = ring_worker_prepare(&ring, data, RING_RECORD_FRAME_SIZE - 1);
	check_invariants(&ring, data);
	TEST_ASSERT_EQUAL(rc, -1, "below the frame size must be rejected");
	TEST_ASSERT_EQUAL(
		errno, EINVAL, "below the frame size must set EINVAL"
	);
	TEST_ASSERT_EQUAL(
		published_write(&ring),
		0L,
		"a rejected prepare must not move write_idx"
	);
	TEST_ASSERT_EQUAL(
		published_readable(&ring),
		0L,
		"a rejected prepare must not move readable_idx"
	);

	free(data);
	return TEST_SUCCESS;
}

// The writer refuses a length above the batch limit as too big.
//
// The batch limit is the capacity minus one eviction chunk. Some huge
// lengths become small when rounded up to 4 bytes, because the 32-bit value
// wraps. The writer checks the raw length before rounding, so it refuses
// those too.
static int
run_ring_prepare_rejects_oversize_alignment_wraparound_test() {
	const uint32_t ring_size = 64;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");
	uint32_t batch_max = ring_worker_batch_max(&ring);
	TEST_ASSERT_EQUAL(
		ring_worker_prepare(&ring, data, batch_max),
		0,
		"a record of exactly the batch limit must be accepted"
	);
	check_invariants(&ring, data);

	uint32_t oversize_values[] = {batch_max + 1, 0xFFFFFFFD, 0xFFFFFFFF};
	for (size_t i = 0;
	     i < sizeof(oversize_values) / sizeof(oversize_values[0]);
	     ++i) {
		uint32_t total_len = oversize_values[i];

		int rc = ring_worker_prepare(&ring, data, total_len);
		check_invariants(&ring, data);
		TEST_ASSERT_EQUAL(
			rc,
			-1,
			"total_len %u must be rejected as oversize",
			total_len
		);
		TEST_ASSERT_EQUAL(
			errno, E2BIG, "total_len %u must set E2BIG", total_len
		);
		TEST_ASSERT_EQUAL(
			published_write(&ring),
			0L,
			"total_len %u must not move write_idx",
			total_len
		);
		TEST_ASSERT_EQUAL(
			published_readable(&ring),
			0L,
			"total_len %u must not move readable_idx",
			total_len
		);
		TEST_ASSERT_EQUAL(
			(long)ring.local.next_seqno,
			0L,
			"total_len %u must not touch next_seqno",
			total_len
		);
	}

	free(data);
	return TEST_SUCCESS;
}

// Readers do not see a committed record until its batch is published.
//
// One publication covers the whole batch. Sequence numbers continue across
// publications without a gap.
static int
run_ring_publish_makes_batch_visible_test() {
	const uint32_t ring_size = 256;
	const uint32_t record_len = 16;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");

	uint32_t first_seqno = ring.local.next_seqno;
	for (uint8_t i = 0; i < 3; ++i) {
		commit_fixed_record(&ring, data, i, record_len);
		TEST_ASSERT_EQUAL(
			(long)atomic_load(&ring.published.write_idx),
			0L,
			"commit %u must not publish the write position",
			i
		);
	}
	publish_checked(&ring, data);
	TEST_ASSERT_EQUAL(
		published_write(&ring),
		(long)(3 * record_len),
		"one publication must cover the whole batch"
	);

	commit_fixed_record(&ring, data, 3, record_len);
	TEST_ASSERT_EQUAL(
		(long)atomic_load(&ring.published.write_idx),
		(long)(3 * record_len),
		"the next batch must stay invisible until published"
	);
	publish_checked(&ring, data);
	TEST_ASSERT_EQUAL(
		published_write(&ring),
		(long)(4 * record_len),
		"the second publication must cover the second batch"
	);

	// The frames in both batches have consecutive sequence numbers.
	for (uint32_t i = 0; i < 4; ++i) {
		struct ring_record_frame frame;
		memcpy(&frame, data + i * record_len, sizeof(frame));
		TEST_ASSERT_EQUAL(
			(long)frame.seqno,
			(long)(first_seqno + i),
			"record %u must carry the next sequence number",
			i
		);
	}

	free(data);
	return TEST_SUCCESS;
}

// A commit publishes the pending records when they fill the publish batch.
//
// It does not publish them earlier. When the producer publishes a partial
// batch itself, the count starts again from zero.
static int
run_ring_commit_publishes_full_publish_batch_test() {
	const uint32_t ring_size = 1024;
	const uint32_t record_len = 16;
	uint8_t *data = calloc(1, ring_size);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");
	struct ring_worker ring;
	ring_worker_init(&ring, ring_size, RING_PUBLISH_BATCH_DEFAULT);

	for (uint32_t batch = 1; batch <= 2; ++batch) {
		for (uint32_t i = 1; i < RING_PUBLISH_BATCH_DEFAULT; ++i) {
			commit_fixed_record(
				&ring, data, (uint8_t)i, record_len
			);
			TEST_ASSERT_EQUAL(
				(long)atomic_load(&ring.published.write_idx),
				(long)((batch - 1) *
				       RING_PUBLISH_BATCH_DEFAULT * record_len),
				"commit %u of batch %u must not publish",
				i,
				batch
			);
		}
		commit_fixed_record(&ring, data, 0xFF, record_len);
		TEST_ASSERT_EQUAL(
			published_write(&ring),
			(long)(batch * RING_PUBLISH_BATCH_DEFAULT * record_len),
			"the commit filling batch %u must publish it",
			batch
		);
	}

	// The producer publishes a partial batch. The count starts again.
	commit_fixed_record(&ring, data, 0xC0, record_len);
	publish_checked(&ring, data);
	uint64_t published = (uint64_t)published_write(&ring);
	for (uint32_t i = 1; i < RING_PUBLISH_BATCH_DEFAULT; ++i) {
		commit_fixed_record(&ring, data, 0xC1, record_len);
	}
	TEST_ASSERT_EQUAL(
		(long)atomic_load(&ring.published.write_idx),
		(long)published,
		"an explicit publication must restart the batch count"
	);
	commit_fixed_record(&ring, data, 0xC2, record_len);
	TEST_ASSERT_EQUAL(
		published_write(&ring),
		(long)(published + RING_PUBLISH_BATCH_DEFAULT * record_len),
		"a full batch after an explicit publication must publish"
	);

	free(data);
	return TEST_SUCCESS;
}

// Publishing with nothing new committed does not store anything.
//
// So an idle producer never writes to the line that readers poll.
static int
run_ring_publish_without_commit_is_noop_test() {
	const uint32_t ring_size = 64;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");

	write_fixed_record(&ring, data, 0xD0, 16);

	// No real publication stores this value, so any store replaces it.
	const uint64_t sentinel = 0xDEAD;
	atomic_store(&ring.published.write_idx, sentinel);
	ring_worker_publish(&ring);
	TEST_ASSERT_EQUAL(
		(long)atomic_load(&ring.published.write_idx),
		(long)sentinel,
		"a publication with nothing new must not store"
	);

	free(data);
	return TEST_SUCCESS;
}

// The batch room shrinks by the aligned length of each committed record.
//
// A publication gives the whole room back.
static int
run_ring_batch_room_tracks_unpublished_bytes_test() {
	const uint32_t ring_size = 1024;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");

	uint64_t room = ring_worker_batch_room(&ring);
	TEST_ASSERT_EQUAL(
		(long)room,
		(long)ring_worker_batch_max(&ring),
		"an empty batch must have the whole limit as room"
	);
	commit_fixed_record(&ring, data, 0xE0, 13);
	TEST_ASSERT_EQUAL(
		(long)ring_worker_batch_room(&ring),
		(long)(room - ring_align4(13)),
		"a commit must take its aligned length from the room"
	);
	publish_checked(&ring, data);
	TEST_ASSERT_EQUAL(
		(long)ring_worker_batch_room(&ring),
		(long)room,
		"a publication must restore the whole room"
	);

	free(data);
	return TEST_SUCCESS;
}

// On overflow the writer drops a whole chunk of the oldest records at once.
//
// It stops on the first record boundary at least one chunk past the
// readable position. The next records go into the freed space and do not
// move the readable position.
static int
run_ring_eviction_frees_whole_chunk_test() {
	const uint32_t ring_size = 4096;
	const uint32_t record_len = 24;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");
	uint32_t chunk = ring.local.evict_chunk;
	TEST_ASSERT_EQUAL(
		(long)chunk,
		(long)(ring_size / RING_EVICT_CHUNK_SHARE),
		"a 4 KiB ring must evict a sixteenth of itself at once"
	);
	TEST_ASSERT_EQUAL(
		(long)ring_evict_chunk(1u << 20),
		(long)RING_EVICT_CHUNK_MAX,
		"a large ring's chunk must stop at the cap"
	);

	// Fill the ring with as many whole records as fit, with no eviction.
	uint32_t fill = ring_size / record_len;
	for (uint32_t i = 0; i < fill; ++i) {
		write_fixed_record(&ring, data, (uint8_t)i, record_len);
	}
	TEST_ASSERT_EQUAL(
		published_readable(&ring), 0L, "a full ring must not evict yet"
	);

	// The first record that does not fit triggers an eviction.
	//
	// The eviction stops on the first record boundary at least one chunk
	// past the readable position. This leaves at least a chunk free.
	uint64_t free_before = ring_size - ring.local.write_idx;
	write_fixed_record(&ring, data, 0xF0, record_len);
	uint64_t expected = (chunk + record_len - 1) / record_len * record_len;
	TEST_ASSERT_EQUAL(
		published_readable(&ring),
		(long)expected,
		"eviction must stop on the first boundary a chunk ahead"
	);

	// More records go into the freed chunk with no eviction until it is
	// full.
	uint32_t spare = (uint32_t)((expected + free_before) / record_len) - 1;
	for (uint32_t i = 0; i < spare; ++i) {
		write_fixed_record(&ring, data, 0xF1, record_len);
		TEST_ASSERT_EQUAL(
			published_readable(&ring),
			(long)expected,
			"record %u of the freed chunk must not evict",
			i
		);
	}
	write_fixed_record(&ring, data, 0xF2, record_len);
	long free_before_record =
		ring_size -
		(published_write(&ring) - published_readable(&ring)) +
		record_len;
	TEST_ASSERT(
		published_readable(&ring) > (long)expected &&
			free_before_record >= (long)chunk,
		"the next overflow must free another whole chunk"
	);

	free(data);
	return TEST_SUCCESS;
}

// An unpublished batch within the limit evicts only published records.
//
// Each eviction frees at least a chunk, unless it stops at the batch start.
// It always stops on a record boundary. It never goes past the batch
// start.
static int
run_ring_eviction_spares_unpublished_batch_test() {
	const uint32_t ring_size = 1024;
	const uint32_t record_len = 16;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");
	uint32_t chunk = ring.local.evict_chunk;

	for (uint32_t i = 0; i < ring_size / record_len; ++i) {
		write_fixed_record(&ring, data, (uint8_t)i, record_len);
	}
	uint64_t batch_start = ring.local.write_idx;
	uint32_t first_seqno = ring.local.next_seqno;

	// Commit the largest batch the limit allows, without publishing.
	uint32_t batch = ring_worker_batch_max(&ring) / record_len;
	long readable = published_readable(&ring);
	for (uint32_t i = 0; i < batch; ++i) {
		commit_fixed_record(&ring, data, 0xB0, record_len);
		long next = published_readable(&ring);
		TEST_ASSERT(
			next <= (long)batch_start,
			"record %u of the batch must not evict the batch "
			"itself",
			i
		);
		TEST_ASSERT_EQUAL(
			next % record_len,
			0L,
			"record %u: eviction must land on a record boundary",
			i
		);
		// Free space before this record was committed.
		long free_space = (long)ring_size -
				  (long)(ring.local.write_idx - next) +
				  record_len;
		if (next != readable) {
			TEST_ASSERT(
				free_space >= (long)chunk ||
					next == (long)batch_start,
				"record %u: an eviction must free a whole "
				"chunk",
				i
			);
		}
		readable = next;
	}
	TEST_ASSERT_EQUAL(
		(long)atomic_load(&ring.published.write_idx),
		(long)batch_start,
		"the batch must stay unpublished while it evicts"
	);

	// Every record of the batch keeps its frame. The numbers have no gap.
	for (uint32_t i = 0; i < batch; ++i) {
		struct ring_record_frame frame;
		uint64_t pos = (batch_start + i * record_len) & ring.local.mask;
		memcpy(&frame, data + pos, sizeof(frame));
		TEST_ASSERT_EQUAL(
			(long)frame.seqno,
			(long)(first_seqno + i),
			"batch record %u must survive with its sequence number",
			i
		);
	}

	free(data);
	return TEST_SUCCESS;
}

// A corrupt length met during eviction stops at the unpublished batch.
//
// The eviction resumes at the cursor that earlier writes walked ahead, so
// the corrupt frame is read by the eviction itself. Dropping all published
// records must leave the batch whole and unpublished.
static int
run_ring_corrupt_eviction_spares_unpublished_batch_test() {
	const uint32_t ring_size = 1024;
	const uint32_t record_len = 16;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");

	for (uint32_t i = 0; i < ring_size / record_len; ++i) {
		write_fixed_record(&ring, data, (uint8_t)i, record_len);
	}
	uint64_t batch_start = ring.local.write_idx;
	uint32_t first_seqno = ring.local.next_seqno;

	// The first record evicts one chunk. The next ones fit, and each
	// walks the cursor one published record ahead.
	uint32_t batch = ring.local.evict_chunk / record_len;
	for (uint32_t i = 0; i < batch; ++i) {
		commit_fixed_record(&ring, data, 0xE0, record_len);
	}
	TEST_ASSERT(
		ring.local.evict_idx > ring.local.readable_idx,
		"the cursor must be ahead of the readable position"
	);

	uint32_t corrupt_len = UINT32_MAX;
	uint64_t cursor = ring.local.evict_idx;
	memcpy(data + (cursor & ring.local.mask),
	       &corrupt_len,
	       sizeof(corrupt_len));
	check_invariants(&ring, NULL);
	commit_fixed_record(&ring, data, 0xE1, record_len);

	TEST_ASSERT_EQUAL(
		published_readable(&ring),
		(long)batch_start,
		"a corrupt length must drop published records only"
	);
	TEST_ASSERT_EQUAL(
		(long)atomic_load(&ring.published.write_idx),
		(long)batch_start,
		"the batch must stay unpublished while it evicts"
	);
	for (uint32_t i = 0; i <= batch; ++i) {
		struct ring_record_frame frame;
		uint64_t pos = (batch_start + i * record_len) & ring.local.mask;
		memcpy(&frame, data + pos, sizeof(frame));
		TEST_ASSERT_EQUAL(
			(long)frame.seqno,
			(long)(first_seqno + i),
			"batch record %u must survive with its sequence number",
			i
		);
	}

	free(data);
	return TEST_SUCCESS;
}

// The writer publishes a batch for a producer that never publishes.
//
// It does so in the prepare of the record that would take the batch past
// the limit. No record is refused. No batch evicts its own records.
// Sequence numbers have no gap. A reader sees every record up to the
// position the writer published.
static int
run_ring_full_batch_auto_publishes_test() {
	const uint32_t ring_size = 1024;
	// This length does not divide the batch limit, so a batch ends a bit
	// below the limit.
	const uint32_t record_len = 28;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");
	uint32_t batch_max = ring_worker_batch_max(&ring);
	uint32_t per_batch = batch_max / record_len;

	// Write three batch limits of data, so the ring also wraps and
	// evicts.
	uint32_t records = 3 * per_batch + 1;
	uint32_t auto_published = 0;
	for (uint32_t i = 0; i < records; ++i) {
		uint64_t write_before = ring.local.write_idx;
		uint64_t published_before =
			atomic_load(&ring.published.write_idx);
		bool overflows = write_before - published_before + record_len >
				 batch_max;
		commit_fixed_record(&ring, data, (uint8_t)i, record_len);

		uint64_t published = atomic_load(&ring.published.write_idx);
		uint64_t expected = overflows ? write_before : published_before;
		TEST_ASSERT_EQUAL(
			(long)published,
			(long)expected,
			"record %u must publish exactly when its batch is full",
			i
		);
		auto_published += overflows;
		TEST_ASSERT(
			published_readable(&ring) <= (long)published,
			"record %u must not evict an unpublished record",
			i
		);
	}
	TEST_ASSERT_EQUAL(
		(long)auto_published,
		3L,
		"each full batch must be published once"
	);

	// From the readable position to the last commit there are only whole
	// records with no gap in their numbers.
	//
	// This includes the unpublished batch.
	uint64_t readable = (uint64_t)published_readable(&ring);
	uint64_t published = atomic_load(&ring.published.write_idx);
	uint32_t seqno = read_frame(&ring, data, readable).seqno;
	uint32_t visible = 0;
	for (uint64_t pos = readable; pos < ring.local.write_idx;
	     pos += record_len) {
		struct ring_record_frame frame = read_frame(&ring, data, pos);
		TEST_ASSERT_EQUAL(
			(long)frame.total_len,
			(long)record_len,
			"the record at %lu must be whole",
			(unsigned long)pos
		);
		TEST_ASSERT_EQUAL(
			(long)frame.seqno,
			(long)seqno,
			"the record at %lu must carry the next sequence number",
			(unsigned long)pos
		);
		seqno++;
		visible += pos < published;
	}
	TEST_ASSERT_EQUAL(
		(long)seqno,
		(long)records,
		"the newest surviving record must be the last commit"
	);
	TEST_ASSERT_EQUAL(
		(long)(published - readable),
		(long)visible * record_len,
		"readers must see whole records up to the published boundary"
	);
	TEST_ASSERT_EQUAL(
		(long)(ring.local.write_idx - published),
		(long)record_len,
		"only the record after the last full batch must stay "
		"unpublished"
	);

	free(data);
	return TEST_SUCCESS;
}

// The sequence counter of a worker starts at 0 and counts every commit.
//
// It wraps from UINT32_MAX to 0 without a gap.
static int
run_ring_seqno_wrap_test() {
	const uint32_t ring_size = 32;
	uint8_t *data;
	struct ring_worker ring = init_test_ring(ring_size, &data);
	TEST_ASSERT_NOT_NULL(data, "failed to allocate ring data");

	uint32_t first =
		ring_worker_commit(&ring, data, RING_RECORD_FRAME_SIZE);
	check_invariants(&ring, data);
	uint32_t second =
		ring_worker_commit(&ring, data, RING_RECORD_FRAME_SIZE);
	check_invariants(&ring, data);
	TEST_ASSERT_EQUAL((long)first, 0L, "first commit must be seqno 0");
	TEST_ASSERT_EQUAL(
		(long)second, (long)first + 1, "commits must be contiguous"
	);

	ring.local.next_seqno = UINT32_MAX;

	uint32_t seqno =
		ring_worker_commit(&ring, data, RING_RECORD_FRAME_SIZE);
	check_invariants(&ring, data);
	TEST_ASSERT_EQUAL(
		(long)seqno,
		(long)UINT32_MAX,
		"the last seqno before wrap must be used"
	);

	seqno = ring_worker_commit(&ring, data, RING_RECORD_FRAME_SIZE);
	check_invariants(&ring, data);
	TEST_ASSERT_EQUAL((long)seqno, 0L, "seqno must wrap to 0 contiguously");

	free(data);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	struct test_case {
		const char *name;
		int (*func)();
	};

	struct test_case cases[] = {
		{"wrap_roundtrip", run_ring_wrap_roundtrip_test},
		{"overwrite_evicts_whole_records",
		 run_ring_overwrite_evicts_whole_records_test},
		{"eviction_corrupt_length_catches_up",
		 run_ring_eviction_corrupt_length_catches_up_test},
		{"prepare_rejects_undersize",
		 run_ring_prepare_rejects_undersize_test},
		{"prepare_rejects_oversize_alignment_wraparound",
		 run_ring_prepare_rejects_oversize_alignment_wraparound_test},
		{"publish_makes_batch_visible",
		 run_ring_publish_makes_batch_visible_test},
		{"commit_publishes_full_publish_batch",
		 run_ring_commit_publishes_full_publish_batch_test},
		{"publish_without_commit_is_noop",
		 run_ring_publish_without_commit_is_noop_test},
		{"batch_room_tracks_unpublished_bytes",
		 run_ring_batch_room_tracks_unpublished_bytes_test},
		{"eviction_frees_whole_chunk",
		 run_ring_eviction_frees_whole_chunk_test},
		{"eviction_spares_unpublished_batch",
		 run_ring_eviction_spares_unpublished_batch_test},
		{"corrupt_eviction_spares_unpublished_batch",
		 run_ring_corrupt_eviction_spares_unpublished_batch_test},
		{"full_batch_auto_publishes",
		 run_ring_full_batch_auto_publishes_test},
		{"seqno_wrap", run_ring_seqno_wrap_test},
	};

	int failed = 0;
	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
		if (cases[i].func() != TEST_SUCCESS) {
			LOG(ERROR, "%s failed", cases[i].name);
			failed = 1;
			continue;
		}
		LOG(INFO, "%s passed", cases[i].name);
	}

	return failed;
}
