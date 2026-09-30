#pragma once

/*
 * C helpers for the Go ringtest package.
 *
 * Meson builds them with the same YANET_CACHE_LINE_SIZE as the ring object
 * archive, so they read the ring and agent layouts as the dataplane does.
 * The header leaves struct ring_worker undefined, so Go cannot read its
 * fields.
 */

#include <stdint.h>

#include "lib/errors/errors.h"

struct agent;
struct cp_object;
struct cp_object_registry;
struct ring_worker;
struct ringtest_stress;

// Exported by the ring object archive; see objects/ring/api/ring_object.h.
struct ring_worker *
ring_object_worker(const struct cp_object *cp_object, uint64_t worker_idx);

uint8_t *
ring_object_worker_data(const struct cp_object *cp_object, uint64_t worker_idx);

// Ring published under the name in the agent's live generation, or NULL.
struct cp_object *
ringtest_lookup_ring(struct agent *agent, const char *name);

// Add an extra reference to the named published ring.
//
// The reference lives in a registry of its own, like a generation that has
// not retired yet. It changes the registry under the agent's config lock,
// as production code does. Returns NULL with an error when the ring is not
// published or the reference cannot be added.
struct cp_object_registry *
ringtest_hold(struct agent *agent, const char *name, yanet_error **err);

// Drop an extra reference and free its registry.
void
ringtest_release(struct agent *agent, struct cp_object_registry *registry);

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
// Each payload is built from its seqno. Returns NULL when the thread cannot
// start.
struct ringtest_stress *
ringtest_stress_start(
	struct ring_worker *ring, uint8_t *data, uint64_t records
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
