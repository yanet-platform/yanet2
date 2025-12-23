#include "state.h"
#include "common/memory.h"
#include "registry.h"
#include "session_table.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdalign.h>
#include <stdio.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

int
balancer_state_init(
	struct balancer_state *state,
	struct memory_context *mctx,
	size_t workers,
	size_t table_size
) {
	assert((uintptr_t)state % alignof(struct balancer_state) == 0);

	// memory context
	state->mctx = mctx;

	// workers
	state->workers = workers;

	// init session table
	int res = session_table_init(&state->session_table, mctx, table_size);
	if (res != 0) {
		return -1;
	}

	// init virtual service registry
	res = service_registry_init(&state->vs_registry, mctx);
	if (res != 0) {
		return -1;
	}

	// init real registry
	res = service_registry_init(&state->real_registry, mctx);
	if (res != 0) {
		return -1;
	}

	// setup stats
	memset(state->stats, 0, sizeof(state->stats));

	return 0;
}

void
balancer_state_free(struct balancer_state *state) {
	session_table_free(&state->session_table);
	service_registry_free(&state->vs_registry);
	service_registry_free(&state->real_registry);
}

////////////////////////////////////////////////////////////////////////////////

ssize_t
balancer_state_find_or_insert_vs(
	struct balancer_state *state,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto,
	struct service_info **service_info
) {
	return service_registry_find_or_insert_service(
		&state->vs_registry,
		ip_address,
		ip_proto,
		ip_address,
		ip_proto,
		port,
		transport_proto,
		service_info
	);
}

/// Get virtual service by index in the registry
struct service_info *
balancer_state_get_vs(struct balancer_state *state, size_t idx) {
	return service_registry_lookup(&state->vs_registry, idx);
}

////////////////////////////////////////////////////////////////////////////////

ssize_t
balancer_state_find_or_insert_real(
	struct balancer_state *state,
	uint8_t *vip_address,
	int vip_proto,
	uint16_t port,
	int transport_proto,
	uint8_t *ip_address,
	int ip_proto,
	struct service_info **service_info
) {
	return service_registry_find_or_insert_service(
		&state->real_registry,
		vip_address,
		vip_proto,
		ip_address,
		ip_proto,
		port,
		transport_proto,
		service_info
	);
}

struct service_info *
balancer_state_get_real(struct balancer_state *state, size_t idx) {
	return service_registry_lookup(&state->real_registry, idx);
}