#pragma once

#include <rte_mbuf.h>

#include <stdint.h>

// The largest rx service weight a worker assignment may carry: the number
// of rx bursts the assignment takes in a single worker round.
enum {
	WORKER_RX_WEIGHT_MAX = 16,
};

// Whether an assignment's rx service for the round continues after a
// burst.
//
// Only a burst that filled the whole read window leaves reason to poll
// the queue again this round, so a drained queue costs no further
// polls.
static inline int
worker_rx_service_continues(uint16_t burst_size, uint16_t read_size) {
	return burst_size == read_size;
}

// Whether a received mbuf may enter the pipeline.
//
// Every packet a module sees is single-segment: the parse layer bounds
// its reads by the whole-chain length but dereferences through the head
// segment, so a chained mbuf would desynchronize bounds checks from
// pointers for every downstream consumer. The worker drops and counts a
// refused mbuf before parsing, so no module ever observes one.
static inline int
worker_rx_admits(const struct rte_mbuf *mbuf) {
	return rte_pktmbuf_is_contiguous(mbuf);
}
