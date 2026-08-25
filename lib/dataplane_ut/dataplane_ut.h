#pragma once

#include <stddef.h>
#include <stdint.h>

#include "lib/dataplane/packet/packet.h"

struct rte_mbuf;
struct yanet_shm;
struct dataplane_ut;

// One worker's device and queue assignment, mirroring the per-worker rx/tx
// port binding the real dataplane sets up before entering its poll loop.
struct dataplane_ut_worker_spec {
	uint16_t device_id;
	uint16_t queue_id;
};

// Construction parameters for an in-process dataplane harness.
// worker_count must be >= 1.
//
// The three list pairs may be empty.
struct dataplane_ut_config {
	size_t cp_memory;
	size_t dp_memory;
	size_t worker_count;
	// Zero selects PACKET_RECIRC_LIMIT_DEFAULT.
	uint16_t packet_recirc_limit;

	const char *const *devices;
	size_t device_count;

	const char *const *modules;
	size_t module_count;

	// Directory scanned for module .so plugins. NULL or empty loads
	// only statically-linked built-ins.
	const char *plugin_dir;

	const char *const *devices_to_load;
	size_t devices_to_load_count;

	// Object type names to load via dp_load_object (resolving
	// new_object_<name> from the main binary). May be empty.
	const char *const *objects_to_load;
	size_t objects_to_load_count;

	// Optional per-worker device/queue assignment, one entry per worker.
	//
	// NULL reproduces the long-standing default: every worker is assigned
	// device id 0 with queue id equal to its index. When non-NULL, this
	// must point to exactly worker_count entries, and entry idx configures
	// worker idx.
	const struct dataplane_ut_worker_spec *workers;
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
//
// Suitable for passing to agent_attach. The handle is owned by the harness
// and lives until dataplane_ut_free; it must not be detached.
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

// Release all mbufs owned by a completed round result.
void
dataplane_ut_round_result_free(struct dataplane_ut_round_result *result);

// Return the number of mock-mempool mbufs currently owned by callers.
size_t
dataplane_ut_mempool_outstanding(struct dataplane_ut *ut);

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
// compiled with optimizations enabled and neither AddressSanitizer nor a
// Meson-selected sanitizer enabled.
//
// The Go harness warns when this returns 0 because the benchmark is not
// representative for that build.
int
dataplane_ut_build_optimized(void);

// Run rounds pipeline rounds on worker, recycling the packets in input each
// time without allocating or printing.
//
// Before the loop, every packet and its metadata are snapshotted. Each round
// rebuilds input from those snapshot pointers and calls dataplane_ut_run. The
// per-round result is discarded after newly emitted packets are reclaimed.
//
// After the loop, input is rebuilt from the snapshot so the caller's list
// holds all packets in a consistent state for freeing. The snapshot array is
// freed before returning.
//
// Mbuf geometry is snapshotted and validated before every round. When
// reset_payload is nonzero, each packet's payload bytes are also snapshotted
// and restored, so modules that rewrite headers in place (for example route
// decrementing TTL) see fresh input each round. Modules that grow, shrink, or
// re-slice packets stay out of scope and cause the run to fail.
//
// Returns 0 on success, -ENOMEM when a snapshot cannot be allocated, and
// -EINVAL when a handler changes mbuf geometry. Allocation failures leave input
// intact; every other return leaves it rebuilt from the fixed packet set.
//
// Handlers must not free or replicate the fixed packet set. Any newly emitted
// packet is reclaimed from the discarded per-round result before the next
// round, but a freed snapshot packet cannot be restored.
int
dataplane_ut_run_rounds(
	struct dataplane_ut *ut,
	size_t worker,
	struct packet_list *input,
	uint64_t rounds,
	int reset_payload
);
