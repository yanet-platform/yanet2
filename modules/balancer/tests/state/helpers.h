#pragma once

#include "session.h"

#include "common/memory.h"

////////////////////////////////////////////////////////////////////////////////

static inline uint64_t
wyhash64(uint64_t wyhash64_x) {
	wyhash64_x += 0x60bee2bee120fc15;
	__uint128_t tmp;
	tmp = (__uint128_t)wyhash64_x * 0xa3b195354a39b70d;
	uint64_t m1 = (tmp >> 64) ^ tmp;
	tmp = (__uint128_t)m1 * 0x1b03738712fad5c9;
	uint64_t m2 = (tmp >> 64) ^ tmp;
	return m2;
}

static inline uint64_t
rng_next(uint64_t *rng) {
	return *rng = wyhash64(*rng);
}

////////////////////////////////////////////////////////////////////////////////

struct balancer_session_id *
gen_sessions(
	size_t sessions_cnt, struct memory_context *mctx, uint32_t worker_idx
);

////////////////////////////////////////////////////////////////////////////////

uint64_t
get_time_ns();