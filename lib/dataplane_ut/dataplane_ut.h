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
