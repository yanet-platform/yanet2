#include "state.h"

#include "api/real.h"
#include "api/vs.h"

#include "common/memory.h"
#include "controlplane/diag/diag.h"
#include "registry.h"
#include "service.h"
#include "session_table.h"
#include <assert.h>
#include <linux/if_link.h>
#include <netinet/icmp6.h>
#include <netinet/in.h>
#include <stdalign.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

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

	// init virtual service registry
	res = service_registry_init(&state->vs_registry, mctx);
	if (res != 0) {
		NEW_ERROR("failed to initialize virtual services registry");
		return -1;
	}

	// init real registry
	res = service_registry_init(&state->real_registry, mctx);
	if (res != 0) {
		NEW_ERROR("failed to initialize real registry");
		return -1;
	}

	return 0;
}

void
balancer_state_free(struct balancer_state *state) {
	session_table_free(&state->session_table);
	service_registry_free(&state->vs_registry);
	service_registry_free(&state->real_registry);
}

static void
service_id_from_vs(
	union service_identifier *service, struct vs_identifier *id
) {
	memset(service, 0, sizeof(union service_identifier));
	memcpy(&service->vs, id, sizeof(struct vs_identifier));
}

struct vs_state *
balancer_state_find_or_insert_vs(
	struct balancer_state *state, struct vs_identifier *id
) {
	union service_identifier service;
	service_id_from_vs(&service, id);

	size_t idx_output;
	struct vs_state *vs =
		(struct vs_state *)service_registry_find_or_insert_service(
			&state->vs_registry, &service, &idx_output
		);
	if (vs != NULL) {
		vs->registry_idx = idx_output;
		vs->identifier = *id;
	}
	return vs;
}

struct vs_state *
balancer_state_find_vs(struct balancer_state *state, struct vs_identifier *id) {
	union service_identifier service;
	service_id_from_vs(&service, id);

	ssize_t idx =
		service_registry_lookup_by_id(&state->vs_registry, &service);

	if (idx == -1) {
		return NULL;
	}
	return balancer_state_get_vs_by_idx(state, idx);
}

struct vs_state *
balancer_state_get_vs_by_idx(struct balancer_state *state, size_t idx) {
	return (struct vs_state *)service_registry_lookup(
		&state->vs_registry, idx
	);
}

////////////////////////////////////////////////////////////////////////////////

static void
service_id_from_real(
	union service_identifier *service, struct real_identifier *id
) {
	memset(service, 0, sizeof(union service_identifier));
	// Copy field by field to avoid uninitialized padding bytes
	service->real.vs_identifier.addr = id->vs_identifier.addr;
	service->real.vs_identifier.ip_proto = id->vs_identifier.ip_proto;
	service->real.vs_identifier.port = id->vs_identifier.port;
	service->real.vs_identifier.transport_proto =
		id->vs_identifier.transport_proto;
	service->real.relative.addr = id->relative.addr;
	service->real.relative.ip_proto = id->relative.ip_proto;
	service->real.relative.port = id->relative.port;
}

struct real_state *
balancer_state_find_or_insert_real(
	struct balancer_state *state, struct real_identifier *id
) {
	struct vs_state *vs =
		balancer_state_find_or_insert_vs(state, &id->vs_identifier);
	if (vs == NULL) {
		return NULL;
	}
	union service_identifier service;
	service_id_from_real(&service, id);
	size_t idx_output;
	struct real_state *real =
		(struct real_state *)service_registry_find_or_insert_service(
			&state->real_registry, &service, &idx_output
		);
	if (real != NULL) {
		real->registry_idx = idx_output;
		real->identifier = *id;
		real->vs_registry_idx = vs->registry_idx;
	}
	return real;
}

struct real_state *
balancer_state_find_real(
	struct balancer_state *state, struct real_identifier *id
) {
	union service_identifier service;
	service_id_from_real(&service, id);
	ssize_t idx =
		service_registry_lookup_by_id(&state->real_registry, &service);
	if (idx == -1) {
		return NULL;
	}
	return balancer_state_get_real_by_idx(state, idx);
}

struct real_state *
balancer_state_get_real_by_idx(struct balancer_state *state, size_t idx) {
	return (struct real_state *)service_registry_lookup(
		&state->real_registry, idx
	);
}

////////////////////////////////////////////////////////////////////////////////

size_t
balancer_state_reals_count(struct balancer_state *state) {
	return service_registry_size(&state->real_registry);
}

size_t
balancer_state_vs_count(struct balancer_state *state) {
	return service_registry_size(&state->vs_registry);
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
