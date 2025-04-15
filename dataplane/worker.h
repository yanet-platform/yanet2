#pragma once

#include <pthread.h>

#include <stdint.h>

#include "config.h"

#include "dataplane/pipeline/pipeline.h"

struct dataplane;
struct dataplane_numa_node;

struct dp_worker;

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
	struct dataplane_numa_node *node;
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

	struct packet_list pending;

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
