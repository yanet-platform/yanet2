#pragma once

#include "common/detail/ttlmap/bucket.h"
#include "common/memory_address.h"
#include "common/ttlmap.h"
#include "subprojects/dpdk/lib/eal/include/rte_common.h"
#include <assert.h>
#include <rte_common.h>
#include <rte_build_config.h>

////////////////////////////////////////////////////////////////////////////////

#define MAX_WORKERS_NUM 64

struct balancer_session_id {
    uint8_t protocol;
	uint8_t l3_balancing;
	uint8_t addr_type; // 4=ip4, 6=ip6.

	uint8_t ip_source[16];
	uint8_t ip_destination[16];

	uint16_t port_source;
	uint16_t port_destination;
};

struct balancer_session_state {
    uint32_t real_id; // global id of real
	uint32_t create_timestamp;
	uint32_t last_packet_timestamp;
	uint32_t timeout;
};

typedef ttlmap_lock_t balancer_session_lock_t;

////////////////////////////////////////////////////////////////////////////////

struct balancer_state_worker_local {
    uint8_t use_prev_gen; // atomic
    uint8_t __padding[63]; // NOLINT
    size_t active_sessions; // space occupied by worker
    uint32_t max_deadline_current_gen;
    uint32_t max_deadline_prev_gen;
};

struct balancer_session_table_gen {
    __rte_cache_aligned struct ttlmap session_table;
    __rte_cache_aligned struct balancer_state_worker_local worker_local[MAX_WORKERS_NUM];
    size_t table_capacity;
};

struct balancer_state {
    struct balancer_session_table_gen generations[2];
    size_t current_gen;
    size_t workers_cnt;
    struct memory_context *mctx;
};

int balancer_state_init(struct balancer_state *state, size_t workers_cnt, size_t capacity, struct memory_context *mctx);

#define BALANCER_SESSION_FOUND TTLMAP_FOUND
#define BALANCER_SESSION_CREATED TTLMAP_INSERTED
#define BALANCER_SESSION_FAILED TTLMAP_FAILED

static inline struct balancer_session_table_gen *
balancer_get_current_session_table(struct balancer_state *state) {
    return ADDR_OF(&state->generations[state->current_gen & 1]);
}

static inline struct balancer_session_table_gen *
balancer_get_previous_session_table(struct balancer_state *state) {
    return ADDR_OF(&state->generations[state->current_gen & 1 ^ 1]);
}

static inline int
balancer_get_session(struct balancer_state *state, size_t worker_idx, size_t now, size_t timeout, struct balancer_session_id *session_id, struct balancer_session_state **session_state, balancer_session_lock_t **lock) {
    struct balancer_session_table_gen *current_session_table = balancer_get_current_session_table(state);
    int ret = TTLMAP_GET(&current_session_table->session_table, session_id, session_state, lock, now, timeout);
    struct balancer_state_worker_local *worker_local = &current_session_table->worker_local[worker_idx];
    if (ret == TTLMAP_FOUND) {
        worker_local->max_deadline_current_gen = RTE_MAX(worker_local->max_deadline_current_gen, now + timeout);
        return ret;
    } else if (ret == TTLMAP_INSERTED || ret == TTLMAP_REPLACED) {
        worker_local->active_sessions += (ret == TTLMAP_INSERTED);
        if (worker_local->use_prev_gen == 1) {
            struct balacker_session_table_gen *previous_session_table = balancer_get_previous_session_table(state);
            ret = TTLMAP_LOOKUP(&previous_session_table->session_table, session_id, *session_state, now);
            if (ret == TTLMAP_FOUND) {
                return BALANCER_SESSION_FOUND;
            } else {
                return BALANCER_SESSION_CREATED;
            }
        } else {
            return BALANCER_SESSION_CREATED;
        }
    } else { // ret == TTLMAP_FAILED
        return BALANCER_SESSION_FAILED;
    }
}

////////////////////////////////////////////////////////////////////////////////

static inline int
balancer_try_free_previous_session_table(struct balancer_state *state) {
    struct balancer_session_table_gen *current_session_table = balancer_get_current_session_table(state);
    for (size_t i = 0; i < state->workers_cnt; ++i) {
        if (current_session_table->worker_local[i].use_prev_gen == 1) {
            return 0;
        }
    }
    struct balancer_session_table_gen *previous_session_table = balancer_get_previous_session_table(state);
    TTLMAP_FREE(&previous_session_table->session_table);
    return 1;
}

static inline int
balancer_extend_table_if_needed(struct balancer_state *state) {
    struct balancer_session_table_gen *current_session_table = balancer_get_current_session_table(state);
    size_t active_sessions = 0;
    for (size_t i = 0; i < state->workers_cnt; ++i) {
        struct balancer_state_worker_local *worker_local = &current_session_table->worker_local[i];
        if (worker_local->use_prev_gen == 1) {
            return 0;
        }
        active_sessions += worker_local->active_sessions;
    }
    if (active_sessions * 10 > current_session_table->table_capacity * 8) {
        struct balancer_session_table_gen *next_session_table = balancer_get_previous_session_table(state);
        int ret = TTLMAP_INIT(&next_session_table->session_table, ADDR_OF(&state->mctx), balancer_session_id, balancer_session_state, current_session_table->table_capacity * 2);
        if (ret != 0) {
            return -1;
        }
        for (size_t i = 0; i < state->workers_cnt; ++i) {
            struct balancer_state_worker_local *worker_local = &next_session_table->worker_local[i];
            worker_local->active_sessions = 0;
            worker_local->max_deadline_current_gen = 0;
            worker_local->max_deadline_prev_gen = current_session_table->worker_local[i].max_deadline_current_gen;
            worker_local->use_prev_gen = 1;
        }
        state->current_gen ^= 1;
        return 1;
    } else {
        return 0;
    }
}