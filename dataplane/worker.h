#pragma once

#include <pthread.h>

#include <stdbool.h>
#include <stdint.h>

#include "config.h"

#include "common/data_pipe.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/worker/counters.h"
#include "lib/dataplane/worker/rx_pool_sampler.h"
#include "lib/dataplane/worker/tx_stage.h"

struct dataplane;
struct dataplane_instance;

struct dp_worker;

struct worker_read_ctx {
	uint16_t read_size;

	// Batch received at the end of the previous round, parsed at the
	// start of the current one.
	//
	// The batch is polled and its coldest lines prefetched before the
	// transmit of its predecessor, so the parse runs on lines the caches
	// already hold. A non-zero count means the batch is still pending:
	// every consumer resets the count once the mbufs leave its hands.
	struct rte_mbuf *staged[WORKER_RX_BURST_SIZE];
	uint16_t staged_count;
};

// One (device, queue) pair the worker polls and transmits on, with its
// per-queue state: the staged RX batch and the private SP/SC mempool.
// The queue's ring sizing is consumed at initialization and not kept.
struct worker_assignment {
	uint32_t device_id;
	uint16_t port_id;
	uint16_t queue_id;

	struct rte_mempool *rx_mempool;

	struct worker_read_ctx read_ctx;
};

// A pipe toward this worker carrying packets for one of its queues.
//
// The pipe handle is held by value: the producer grows its own pipe
// array with realloc as the topology is wired, so a pointer into it
// would dangle, while a copy stays valid because the shared ring state
// lives behind the handle's pointers. The target pair tells the drain
// callback where to transmit: with a worker owning several devices,
// incoming remote traffic is not bound for the worker's single queue
// but for the queue the connection wired this pipe to.
struct worker_rx_pipe {
	struct data_pipe pipe;
	uint16_t port_id;
	uint16_t queue_id;
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
	struct dp_worker *dp_worker;

	pthread_t thread_id;
	// Gates the join: false unless a thread was created and not yet reaped.
	bool thread_started;

	uint32_t assignment_count;
	struct worker_assignment *assignments;

	// The assignments' pools in the same order, backing the occupancy
	// sampler below.
	struct rte_mempool **rx_pools;

	// Publishes the rx pools' combined occupancy gauge into shared
	// counter storage. Bound before the instance becomes visible; state
	// is dataplane-local on purpose (see the sampler header).
	struct worker_rx_pool_sampler rx_pool_sampler;

	// Global device id -> the worker's designated transmit assignment
	// for that device, or assignment_count when the worker does not own
	// it. Indexed by the dataplane's device count, so the
	// local-transmit decision is a bounds check plus an array read.
	//
	// When a worker owns several queues of one device, the first
	// declared assignment is the device's transmit queue; remote
	// traffic still arrives per pipe on its own target queue.
	uint32_t *owned;

	struct worker_write_ctx write_ctx;

	struct dataplane_worker_config config;
};

int
dataplane_worker_init(
	struct dataplane *dataplane,
	struct dataplane_worker *worker,
	struct dataplane_worker_config *config
);

int
dataplane_worker_start(struct dataplane_worker *worker);

void
dataplane_worker_stop(struct dataplane_worker *worker);
