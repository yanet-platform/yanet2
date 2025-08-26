#pragma once

#include <stdint.h>

#include "common/memory.h"

#define SESSION_VALUE_INVALID 0xffffffff

struct state {
	uint32_t real_id;
};

static inline void
balancer_state_init(
	struct state *state, struct memory_context *memory_context
) {
	state->real_id = SESSION_VALUE_INVALID;
	(void)memory_context;
}

static inline uint32_t
balancer_state_lookup(struct state *state, void *key) {
	(void)key;
	return state->real_id;
}

static inline int
balancer_state_touch(struct state *state, void *key, uint32_t timeout) {
	(void)state;
	(void)key;
	(void)timeout;
	return 0;
}

static inline int
balancer_state_set(
	struct state *state, void *key, uint32_t timeout, uint32_t real_id
) {
	state->real_id = real_id;
	(void)key;
	(void)timeout;
	return 0;
}

static inline void
balancer_state_free(struct state *state) {
	(void)state;
}
