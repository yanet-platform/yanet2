#include "worker.h"
#include "session.h"
#include "state.h"

////////////////////////////////////////////////////////////////////////////////

static _Atomic uint32_t iterations = 0;

void workers_prepare_globals() {
    __c11_atomic_store(&iterations, 0, __ATOMIC_SEQ_CST);
}

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t wyhash32(uint64_t wyhash64_x) {
    wyhash64_x += 0x60bee2bee120fc15;
    __uint128_t tmp;
    tmp = (__uint128_t) wyhash64_x * 0xa3b195354a39b70d;
    uint64_t m1 = (tmp >> 64) ^ tmp;
    tmp = (__uint128_t)m1 * 0x1b03738712fad5c9;
    uint64_t m2 = (tmp >> 64) ^ tmp;
    return m2 >> 32;
}

////////////////////////////////////////////////////////////////////////////////

void
run_worker(struct worker_config *config) {
    config->result->failed = 0;
    uint64_t rnd = config->worker_idx;
    for (size_t i = 0; i < config->iterations; ++i) {
        size_t modulo = 1ll << ((63 - __builtin_clzll(i + 1)) + 1);
        if (modulo > config->session_count) {
            modulo = config->session_count;
        }
        rnd = wyhash32(rnd);
        size_t idx = rnd % modulo;
        struct balancer_session_id *session_id = &config->sessions[idx];
        rnd = wyhash32(rnd);
        uint32_t timeout = config->timeout_min + (rnd % (config->timeout_max - config->timeout_min));
        uint32_t now = __c11_atomic_fetch_add(&iterations, 1, __ATOMIC_SEQ_CST);
        struct balancer_session_state *session_state;
        balancer_session_lock_t *session_lock;
        int res = balancer_get_session(config->balancer, config->worker_idx, now, timeout, session_id, &session_state, &session_lock);
        if (res == BALANCER_GET_SESSION_FAILED) {
            ++config->result->failed;
            continue;
        }
        if (res == BALANCER_SESSION_CREATED) {
            session_state->create_timestamp = now;
            session_state->real_id = 100;
        }
        session_state->last_packet_timestamp = now;
        session_state->timeout = timeout;
    }
}