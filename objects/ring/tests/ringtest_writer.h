#pragma once

/*
 * C writer helpers for the Go ringtest package.
 *
 * They run the inline writer of common/ring.h. This library is built by
 * meson with the same YANET_CACHE_LINE_SIZE as the ring object archive, so
 * a test writes to the same addresses as the dataplane. The header declares
 * struct ring_worker but does not define it, so Go code cannot read its fields.
 */

#include <stdint.h>

struct agent;
struct cp_object;
struct ring_worker;
struct ringtest_stress;

// Ring published under the name in the agent's live generation, or NULL.
struct cp_object *
ringtest_lookup_ring(struct agent *agent, const char *name);

// Metadata of one worker's ring, or NULL past the last worker.
struct ring_worker *
ringtest_worker(const struct cp_object *object, uint64_t worker_idx);

// Data area of one worker's ring, or NULL past the last worker.
uint8_t *
ringtest_worker_data(const struct cp_object *object, uint64_t worker_idx);

// Size of the ring's data area in bytes.
uint32_t
ringtest_size(const struct ring_worker *ring);

// The writer's own write position, published or not.
uint64_t
ringtest_write_idx(const struct ring_worker *ring);

// Bytes the unpublished batch can still grow before it publishes itself.
uint64_t
ringtest_batch_room(const struct ring_worker *ring);

// Set the write and readable positions in both halves of the metadata.
void
ringtest_set_positions(
	struct ring_worker *ring, uint64_t write_idx, uint64_t readable_idx
);

// Overwrite the length in the frame at a logical offset.
void
ringtest_corrupt_total_len(
	struct ring_worker *ring,
	uint8_t *data,
	uint64_t logical_offset,
	uint32_t total_len
);

// Prepare, write and commit one record.
//
// Returns the sequence number, or -1 with errno when the ring refuses the
// record. The payload may be NULL when its length is 0. The caller passes
// no pointer to receive a result, so a Go caller adds no heap allocation
// to the reader tests that count allocations.
int64_t
ringtest_commit_record(
	struct ring_worker *ring,
	uint8_t *data,
	const uint8_t *payload,
	uint32_t payload_len
);

// Publish every record committed since the last publication.
void
ringtest_publish(struct ring_worker *ring);

// Start a writer thread that commits the given number of records.
//
// With a nonzero fixed length, every payload is that many 0xAB bytes.
// Otherwise each payload is built from its seqno. Returns NULL when the
// thread cannot start.
struct ringtest_stress *
ringtest_stress_start(
	struct ring_worker *ring,
	uint8_t *data,
	uint64_t records,
	uint32_t fixed_len
);

// Whether the writer thread has finished.
int
ringtest_stress_done(struct ringtest_stress *stress);

// Number of records the writer committed.
//
// The value is final only after the writer has finished.
uint64_t
ringtest_stress_written(struct ringtest_stress *stress);

// Join the writer thread and free its state.
void
ringtest_stress_join(struct ringtest_stress *stress);

// Payload length for a seqno: 4 to 256 bytes, in whole 32-bit words.
static inline uint32_t
ringtest_stress_len(uint32_t seqno) {
	return 4 * (1 + ((seqno * 2654435761u) >> 26));
}

// Payload word k of the record stamped with seqno.
static inline uint32_t
ringtest_stress_word(uint32_t seqno, uint32_t k) {
	return seqno * 0x9E3779B1u + k;
}
