#pragma once

#include <stdint.h>

#define BALANCER_IC_RING_SIZE_EXP 3u
#define BALANCER_IC_RING_SIZE (1u << BALANCER_IC_RING_SIZE_EXP)
#define BALANCER_IC_RING_MASK (BALANCER_IC_RING_SIZE - 1u)

/*
 * Ring-based interval counter that stores per-timestamp deltas.
 */
struct balancer_interval_counter {
	int32_t diff[BALANCER_IC_RING_SIZE];
	uint32_t last_timestamp;
};
