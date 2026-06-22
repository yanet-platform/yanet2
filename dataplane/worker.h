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
// The per-pipe deferred-free ring holds 2^(WORKER_TX_PIPE_SIZE + this)
// mbufs, sized above the pipe capacity to absorb consumer-side NIC tx
// backlog before backpressure drops further packets.
#define WORKER_TX_PIPE_PENDING_SHIFT 2

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

struct worker_write_ctx {

	uint16_t write_size;

	// pipes to send to another workers
	struct worker_tx_connection *tx_connections;

	// pipes to read from another workers
	uint32_t rx_pipe_count;
	struct data_pipe *rx_pipes;
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
