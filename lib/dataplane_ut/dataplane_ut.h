#pragma once

#include <stddef.h>
#include <stdint.h>

#include "lib/dataplane/packet/packet.h"

struct rte_mbuf;
struct yanet_shm;
struct dataplane_ut;

// Construction parameters for an in-process dataplane harness.
// worker_count must be >= 1.
//
// The three list pairs may be empty.
struct dataplane_ut_config {
	size_t cp_memory;
	size_t dp_memory;
	size_t worker_count;

	const char *const *devices;
	size_t device_count;

	const char *const *modules;
	size_t module_count;

	const char *const *devices_to_load;
	size_t devices_to_load_count;
};

// Construct an in-process dataplane harness.
//
// Returns the harness handle on success, or NULL if any allocation or
// loader step fails. The caller releases it with dataplane_ut_free.
struct dataplane_ut *
dataplane_ut_new(const struct dataplane_ut_config *cfg);

// Tear down a harness previously returned by dataplane_ut_new. NULL-safe.
void
dataplane_ut_free(struct dataplane_ut *ut);

// Return the shared-memory handle backing this harness.
// Suitable for passing to agent_attach.
struct yanet_shm *
dataplane_ut_shm(struct dataplane_ut *ut);

// Install a wall-time value used by the next dataplane_ut_run call.
//
// Useful for driving time-sensitive module logic such as TTLs and NAT timeouts.
void
dataplane_ut_set_time_ns(struct dataplane_ut *ut, uint64_t ns);

// Read the currently installed wall-time value.
uint64_t
dataplane_ut_get_time_ns(struct dataplane_ut *ut);

// Allocate an mbuf from the harness mempool.
//
// Returns NULL on exhaustion. The caller frees with rte_pktmbuf_free.
struct rte_mbuf *
dataplane_ut_alloc_mbuf(struct dataplane_ut *ut);

// Result of one pipeline round. The caller owns the mbufs in both lists
// and must free them when done.
struct dataplane_ut_round_result {
	struct packet_list output;
	struct packet_list drop;
};

// Run one pipeline round on worker_idx with the given input.
//
// input is drained to empty on return; result->output and result->drop
// hold the post-pipeline packets, whose mbuf ownership transfers to the caller.
//
// The harness is single-threaded: concurrent calls on the same handle
// race on dp_worker state and the shared mempool.
void
dataplane_ut_run(
	struct dataplane_ut *ut,
	size_t worker_idx,
	struct packet_list *input,
	struct dataplane_ut_round_result *result
);

// Report whether this dataplane build is suitable for benchmarking:
// compiled with optimizations enabled and without AddressSanitizer.
//
// Benchmarks against a debug or AddressSanitizer build produce misleading
// numbers, so the Go harness warns when this returns 0.
int
dataplane_ut_build_optimized(void);

// Run rounds pipeline rounds on worker, recycling the packets in input each
// time without allocating or printing.
//
// Before the loop, every packet is popped from input and its initial
// tx_device_id and rx_device_id are recorded. Each round rebuilds input from
// those snapshot pointers, resets next/tx_device_id/rx_device_id on each
// packet, and calls dataplane_ut_run. The per-round result is discarded —
// dataplane_ut_run moves packet nodes without freeing them, so the snapshot
// pointers remain valid for every subsequent round.
//
// After the loop, input is rebuilt from the snapshot so the caller's list
// holds all packets in a consistent state for freeing. The snapshot array is
// freed before returning.
//
// Returns immediately (leaving input intact) when rounds == 0 or
// input->count == 0. Returns immediately on malloc failure.
//
// Caveat: this primitive assumes each round's handlers only forward or drop the
// fixed packet set. A handler that allocates, frees, or replicates packets per
// round breaks the recycling contract: any new mbuf emitted (for example the
// fwstate state-sync path, which calls worker_packet_alloc +
// packet_front_output) is held only by the discarded per-round result, not by
// the snapshot, so it leaks one mbuf per round. Do not benchmark configurations
// that emit or drop-free packets through this primitive.
void
dataplane_ut_run_rounds(
	struct dataplane_ut *ut,
	size_t worker,
	struct packet_list *input,
	uint64_t rounds
);
