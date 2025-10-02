#include "state1.h"

////////////////////////////////////////////////////////////////////////////////

int balancer_state_init(struct balancer_state *state, size_t workers_cnt, size_t capacity, struct memory_context *mctx) {
    SET_OFFSET_OF(&state->mctx, mctx);
    state->current_gen = 0;
    state->workers_cnt = workers_cnt;
    state->generations[0].table_capacity = capacity;
    int ret = TTLMAP_INIT(&state->generations[0].session_table, mctx, struct balancer_session_id, struct balancer_session_state, capacity);
    for (size_t i = 0; i < workers_cnt; ++i) {
        struct balancer_state_worker_local *worker_local = &state->generations[0].worker_local;
        worker_local->max_deadline_current_gen = 0;
        worker_local->max_deadline_prev_gen = 0;
        worker_local->use_prev_gen = 0;
        worker_local->active_sessions = 0;
    }
    return ret;
}