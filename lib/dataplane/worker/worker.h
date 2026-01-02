#pragma once

#include <stdint.h>

#include "dataplane/time/clock.h"

struct dp_worker {
	uint64_t idx;

	uint64_t gen;

	// Allows to get current worker time.
	//
	// Currently, we init it only once
	// and dont adjust.
	// So, we have some drift, which is small but...
	// (see tsc_clock docs). It is not important
	// for now and fix should be easy, but need discuss.
	//
	// TODO: FIXME
	struct tsc_clock clock;

	// Current worker time in nanoseconds,
	// initialized on the start of the current
	// loop round.
	uint64_t current_time;

	uint64_t *iterations;

	uint64_t *rx_count;
	uint64_t *rx_size;

	uint64_t *tx_count;
	uint64_t *tx_size;

	uint64_t *remote_rx_count;
	uint64_t *remote_tx_count;

	struct rte_mempool *rx_mempool;

	uint8_t pad[24];
};

struct packet *
worker_packet_alloc(struct dp_worker *worker);

struct packet *
worker_clone_packet(struct dp_worker *dp_worker, struct packet *packet);

void
worker_packet_free(struct packet *packet);
