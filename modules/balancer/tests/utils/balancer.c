#include "common/memory.h"
#include "common/memory_address.h"
#include "common/ttlmap.h"
#include <assert.h>

#include "balancer.h"

#include "clock.h"
#include "config.h"

////////////////////////////////////////////////////////////////////////////////

int
config_data_init(
	struct balancer_module_config *config,
	struct memory_context *mctx,
	struct balancer_state *state
);

////////////////////////////////////////////////////////////////////////////////

struct cp_module *
make_balancer(
	struct memory_context *mctx,
	struct balancer_session_timeouts *timeouts,
	struct balancer_state *state
) {
	struct balancer_module_config *cfg = memory_balloc(mctx, sizeof(*cfg));
	cfg->timeouts = *timeouts;
	int res = config_data_init(cfg, mctx, state);
	if (res != 0) {
		return NULL;
	}
	memory_context_init_from(
		&cfg->cp_module.memory_context, mctx, "balancer_cp"
	);
	return &cfg->cp_module;
}

////////////////////////////////////////////////////////////////////////////////

struct balancer_state *
make_balancer_state(
	struct memory_context *mctx, size_t workers, size_t reserve
) {
	size_t align = alignof(struct balancer_state);
	uint8_t *memory =
		memory_balloc(mctx, sizeof(struct balancer_state) + align);
	memory += (align - ((uintptr_t)memory) % align) % align;
	assert((uintptr_t)memory % align == 0);
	struct balancer_state *state = (struct balancer_state *)memory;
	__c11_atomic_store(&state->clock.current_time, 1, __ATOMIC_SEQ_CST);
	SET_OFFSET_OF(&state->mctx, mctx);
	int res = TTLMAP_INIT(
		&state->generations[0].session_table,
		mctx,
		struct balancer_session_id,
		struct balancer_session_state,
		reserve
	);
	if (res != 0) {
		return NULL;
	}
	state->workers_cnt = workers;
	for (size_t i = 0; i < workers; ++i) {
		worker_info_init(&state->generations[0].worker_info[i]);
	}
	return state;
}