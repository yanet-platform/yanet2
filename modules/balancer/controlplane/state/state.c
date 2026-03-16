#include "state.h"

#include "common/memory.h"
#include "controlplane/diag/diag.h"
#include "session_table.h"
#include <assert.h>
#include <stdalign.h>
#include <string.h>

int
balancer_state_init(
	struct balancer_state *state,
	struct memory_context *mctx,
	size_t workers,
	size_t table_size
) {
	assert((uintptr_t)state % alignof(struct balancer_state) == 0);

	// workers
	state->workers = workers;

	// init session table
	int res = session_table_init(&state->session_table, mctx, table_size);
	if (res != 0) {
		NEW_ERROR("failed to initialize session table");
		return -1;
	}

	return 0;
}

void
balancer_state_free(struct balancer_state *state) {
	session_table_free(&state->session_table);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_state_resize_session_table(
	struct balancer_state *state, size_t new_size, uint32_t now
) {
	return session_table_resize(&state->session_table, new_size, now);
}

size_t
balancer_state_session_table_capacity(struct balancer_state *state) {
	return session_table_capacity(&state->session_table);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_state_iter_session_table(
	struct balancer_state *state,
	uint32_t now,
	session_table_iter_callback cb,
	void *userdata
) {
	return session_table_iter(&state->session_table, now, cb, userdata);
}

// TODO: docs
void
balancer_state_inspect(
	struct balancer_state *state, struct state_inspect *inspect
) {
	inspect->session_table_usage =
		session_table_memory_usage(&state->session_table);
	inspect->vs_registry_usage = 0;	   // Moved to packet_handler
	inspect->reals_registry_usage = 0; // Moved to packet_handler
	inspect->total_usage = inspect->session_table_usage;
}