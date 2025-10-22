#pragma once

#include "common/ttlmap.h"

////////////////////////////////////////////////////////////////////////////////

#define SESSION_FOUND TTLMAP_FOUND
#define SESSION_CREATED (TTLMAP_INSERTED | TTLMAP_REPLACED)
#define SESSION_TABLE_OVERFLOW TTLMAP_FAILED

////////////////////////////////////////////////////////////////////////////////

struct worker_info {
	_Atomic uint8_t use_prev_gen; // atomic
	uint8_t pad[63];	       
	_Atomic uint32_t max_deadline_current_gen;
	_Atomic uint32_t max_deadline_prev_gen;
	_Atomic uint32_t active_sessions; // sessions created by worker
	_Atomic uint32_t density_factor;
} __rte_cache_aligned;

#define MAX_WORKERS_NUM 64

struct session_table_gen {
	struct ttlmap map;
	struct worker_info worker_info[MAX_WORKERS_NUM];
};

////////////////////////////////////////////////////////////////////////////////

struct balancer_session_table {
    struct session_table_gen generations[2];
	_Atomic uint32_t current_gen; // workers read, cp modify
	uint32_t workers_cnt;

    // relative pointer to the memory context of the
    // agent who created session table
	struct memory_context *mctx;

    // shift of &balancer_session_table in memory
    // which allows to deallocate table properly.
    uint32_t memory_shift;
};

////////////////////////////////////////////////////////////////////////////////

static inline struct session_table_gen *
session_table_current_gen(struct balancer_session_table *state) {
	uint32_t current_gen =
		atomic_load_explicit(&state->current_gen, __ATOMIC_SEQ_CST);
	return &state->generations[current_gen & 1];
}

static inline struct session_table_gen *
session_table_previous_gen(struct balancer_session_table *state) {
	uint32_t current_gen =
		atomic_load_explicit(&state->current_gen, __ATOMIC_SEQ_CST);
	return &state->generations[(current_gen & 1) ^ 1];
}