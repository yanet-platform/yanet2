#pragma once

#include <pthread.h>

#include <stdint.h>

#include "config.h"

#include "common/data_pipe.h"
#include "lib/dataplane/packet/packet.h"

struct dataplane;
struct dataplane_instance;

struct dp_worker;

// log2 of the per-connection SPSC data pipe capacity.
#define WORKER_TX_PIPE_SIZE 10

// Bounds head-of-line blocking by a persistently rejected packet.
//
// A worker_rx_pipe retries a rejected head instead of dropping it, so a
// packet that can never be transmitted (for example a chain exceeding the
// destination NIC's segment limit) would otherwise block the rest of the
// pipe forever. This is not a flat "drop after N rounds" budget: once the
// same head has been rejected for this many consecutive rounds, the
// consumer probes whether the NIC still accepts a different packet from
// the same batch. Only a head that keeps being rejected while other
// packets get through is dropped to let the pipe proceed. A ring that is
// stalled end to end keeps failing the probe too, so it keeps retrying
// indefinitely and never drops — except when the head sits in the ring's
// last slot before the wrap, where no probe is possible and the budget
// falls back to an unconditional drop, bounded to at most one per stall
// episode.
#define WORKER_TX_RETRY_ROUND_LIMIT 4096

struct worker_read_ctx {
	uint16_t read_size;
};

struct worker_pending_mbuf {
	struct rte_mbuf *mbuf;
	uint64_t ref_cnt;
};

// A data pipe to another worker paired with its own deferred-free ring.
//
// The producer holds an extra reference on each mbuf pushed into `pipe`
// and records it in `pending_mbufs`; the reference is released once the
// consumer's NIC tx completes. Per-pipe completion is FIFO, so the ring
// is drained head-first.
//
// The deferred-free ring is sized by the consumer's tx queue depth rather
// than a fixed multiple of the pipe capacity: see the pending_capacity
// computation in dataplane_worker_connect for the liveness invariant this
// depends on.
struct worker_tx_pipe {
	struct data_pipe pipe;
	struct worker_pending_mbuf *pending_mbufs;
	uint32_t pending_mask;
	uint64_t pending_start;
	uint64_t pending_stop;
};

struct worker_tx_connection {
	uint32_t count;
	struct worker_tx_pipe *pipes;
};

// A consumer-side rx pipe paired with retry-budget tracking for its head.
//
// A rejected batch is left in the pipe rather than freed, so the next
// nonzero tx_burst gets another chance to drain it and, on virtio, lets
// the PMD reclaim completed descriptors in the process. stall_head and
// stall_rounds bound how long a single persistently untransmittable
// packet may block the rest of the pipe behind it, per
// WORKER_TX_RETRY_ROUND_LIMIT.
struct worker_rx_pipe {
	struct data_pipe pipe;
	struct rte_mbuf *stall_head;
	uint32_t stall_rounds;
};

struct worker_write_ctx {

	uint16_t write_size;

	// pipes to send to another workers
	struct worker_tx_connection *tx_connections;

	// pipes to read from another workers
	uint32_t rx_pipe_count;
	struct worker_rx_pipe *rx_pipes;
};

struct dataplane_worker {
	struct dataplane *dataplane;
	// TODO: use it to attach worker to the local NUMA
	struct dataplane_instance *instance;
	struct dataplane_device *device;
	struct dp_worker *dp_worker;

	pthread_t thread_id;

	// FIXME port_id and device_id could be inherited from device
	uint16_t port_id;
	uint16_t queue_id;
	uint32_t device_id;

	struct rte_mempool *rx_mempool;

	struct worker_read_ctx read_ctx;
	struct worker_write_ctx write_ctx;

	struct dataplane_device_worker_config config;
};

int
dataplane_worker_init(
	struct dataplane *dataplane,
	struct dataplane_device *device,
	struct dataplane_worker *worker,
	int queue_id,
	struct dataplane_device_worker_config *config
);

int
dataplane_worker_start(struct dataplane_worker *worker);

void
dataplane_worker_stop(struct dataplane_worker *worker);
