#include "../state/state.h"
#include "common/memory.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdalign.h>

#include "common/memory_address.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/config/zone.h"
#include "modules/balancer/state/session_table.h"
#include "vs.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_state *
balancer_state_create(struct agent *agent, size_t table_size) {
	struct memory_context *mctx = &agent->memory_context;

	// allocate balancer state
	const size_t align = alignof(struct balancer_state);
	uint8_t *memory =
		memory_balloc(mctx, sizeof(struct balancer_state) + align);
	if (memory == NULL) {
		return NULL;
	}
	uint32_t shift = (align - ((uintptr_t)memory) % align) % align;
	memory += shift;
	assert((uintptr_t)memory % align == 0);
	struct balancer_state *balancer_state = (struct balancer_state *)memory;

	// store shift to properly deallocate state
	balancer_state->memory_shift = shift;

	// get numbers of workers
	size_t workers = ADDR_OF(&agent->dp_config)->worker_count;

	// init balancer state
	int res =
		balancer_state_init(balancer_state, mctx, workers, table_size);
	if (res != 0) {
		memory_bfree(
			mctx, memory, sizeof(struct session_table) + align
		);
		return NULL;
	}

	return balancer_state;
}

void
balancer_state_destroy(struct balancer_state *state) {
	balancer_state_free(state);
	uintptr_t mem = (uintptr_t)state;
	mem -= state->memory_shift;
	memory_bfree(
		state->mctx,
		(void *)mem,
		sizeof(struct balancer_state) + alignof(struct balancer_state)
	);
}

////////////////////////////////////////////////////////////////////////////////

ssize_t
balancer_state_register_vs(
	struct balancer_state *state,
	int transport_proto,
	int network_proto,
	uint8_t *ip_address,
	uint16_t port
) {
	struct service_info *res = NULL;
	return balancer_state_find_or_insert_vs(
		state, ip_address, network_proto, port, transport_proto, &res
	);
}

ssize_t
balancer_state_register_real(
	struct balancer_state *state,
	int transport_proto,
	int vip_network_proto,
	uint8_t *vip_address,
	uint16_t port,
	int real_network_proto,
	uint8_t *ip_address
) {
	struct service_info *res = NULL;
	return balancer_state_find_or_insert_real(
		state,
		vip_address,
		vip_network_proto,
		port,
		transport_proto,
		ip_address,
		real_network_proto,
		&res
	);
}

///////////////////////////////////////////////////////////////////////////////

int
balancer_state_gc_session_table(struct balancer_state *state) {
	return session_table_free_unused(&state->session_table);
}

////////////////////////////////////////////////////////////////////////////////

size_t
balancer_state_session_table_capacity(struct balancer_state *state) {
	return session_table_capacity(&state->session_table);
}

int
balancer_state_resize_session_table(
	struct balancer_state *state, size_t new_size
) {
	return session_table_resize(&state->session_table, new_size);
}