#pragma once

#include <stdint.h>

#include "dataplane/pipeline/pipeline.h"

struct dataplane;

struct worker_read_ctx {

	uint16_t read_size;
};

struct worker_tx_connection {
	uint32_t count;
	struct data_pipe *pipes;
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
	struct dataplane_device *device;

	pthread_t thread_id;

	// FIXME port_id and device_id could be inherited from device
	uint16_t port_id;
	uint16_t queue_id;
	uint32_t device_id;

	struct rte_mempool *rx_mempool;

	struct worker_read_ctx read_ctx;
	struct worker_write_ctx write_ctx;
};

void
worker_exec(struct dataplane_worker *worker);

int
dataplane_worker_init(
	struct dataplane *dataplane,
	struct dataplane_device *device,
	struct dataplane_worker *worker,
	int queue_id
);
