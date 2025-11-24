#include "state.h"
#include "common/exp_array.h"
#include "common/memory.h"
#include "common/network.h"
#include "modules/balancer/state/registry.h"
#include "modules/balancer/state/session.h"
#include "modules/balancer/state/session_table.h"
#include "session_table.h"
#include <assert.h>
#include <netinet/in.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

static int
service_state_init(
	struct service_state *state,
	struct memory_context *mctx,
	uint32_t max_timeout
) {
	state->last_packet_timestamp = 0;
	int res = interval_counter_init(
		&state->active_sessions, 0, max_timeout, mctx
	);
	if (res != 0) {
		return -1;
	}
	memset(&state->stats, 0, sizeof(state->stats));
	return 0;
}

static void
service_state_free(struct service_state *state) {
	interval_counter_free(&state->active_sessions);
}

////////////////////////////////////////////////////////////////////////////////

static int
service_registry_init(struct service_registry *registry) {
	registry->service_count = 0;
	registry->services = NULL;
	return 0;
}

static void
service_registry_free(
	struct service_registry *registry,
	struct memory_context *mctx,
	size_t workers
) {
	for (size_t i = 0; i < registry->service_count; ++i) {
		struct service_info *service = &registry->services[i];
		for (size_t w = 0; w < workers; ++w) {
			service_state_free(&service->state[w]);
		}
	}
	memory_bfree(
		mctx,
		registry->services,
		sizeof(struct service_info) * registry->service_count
	);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_state_init(
	struct balancer_state *state,
	struct memory_context *mctx,
	size_t workers,
	size_t table_size,
	uint32_t tcp_syn_ack_timeout,
	uint32_t tcp_syn_timeout,
	uint32_t tcp_fin_timeout,
	uint32_t tcp_timeout,
	uint32_t udp_timeout,
	uint32_t default_timeout
) {
	assert((uintptr_t)state % alignof(struct balancer_state) == 0);

	// memory context
	state->mctx = mctx;

	// workers
	state->workers = workers;

	// init timeouts
	state->timeouts =
		(struct sessions_timeouts){.tcp_syn_ack = tcp_syn_ack_timeout,
					   .tcp_syn = tcp_syn_timeout,
					   .tcp_fin = tcp_fin_timeout,
					   .tcp = tcp_timeout,
					   .udp = udp_timeout,
					   .default_timeout = default_timeout};

	// set max timeout
	state->max_timeout = default_timeout;
	if (tcp_syn_ack_timeout > state->max_timeout) {
		state->max_timeout = tcp_syn_ack_timeout;
	}
	if (tcp_fin_timeout > state->max_timeout) {
		state->max_timeout = tcp_fin_timeout;
	}
	if (tcp_timeout > state->max_timeout) {
		state->max_timeout = tcp_timeout;
	}
	if (udp_timeout > state->max_timeout) {
		state->max_timeout = udp_timeout;
	}

	// init session table
	int res = session_table_init(
		&state->session_table, mctx, table_size, workers
	);
	if (res != 0) {
		return -1;
	}

	// init virtual service registry
	res = service_registry_init(&state->vs_registry);
	if (res != 0) {
		return -1;
	}

	// init real registry
	res = service_registry_init(&state->real_registry);
	if (res != 0) {
		return -1;
	}

	return 0;
}

void
balancer_state_free(struct balancer_state *state) {
	session_table_free(&state->session_table);
	service_registry_free(&state->vs_registry, state->mctx, state->workers);
	service_registry_free(
		&state->real_registry, state->mctx, state->workers
	);
}

////////////////////////////////////////////////////////////////////////////////

static ssize_t
find_or_insert_into_registry(
	struct balancer_state *state,
	struct service_registry *registry,
	uint8_t *vip_address,
	int vip_proto,
	uint16_t port,
	int transport_proto,
	uint8_t *ip_address,
	int ip_proto,
	struct service_info **service_info
) {
	for (size_t i = 0; i < registry->service_count; ++i) {
		struct service_info *service = &registry->services[i];
		if (service->vip_proto != vip_proto) {
			continue;
		}
		if (memcmp(service->vip_address,
			   vip_address,
			   (vip_proto == IPPROTO_IPV6 ? NET6_LEN : NET4_LEN))) {
			continue;
		}
		if (service->ip_proto != ip_proto) {
			continue;
		}
		if (memcmp(service->ip_address,
			   ip_address,
			   (ip_proto == IPPROTO_IPV6 ? NET6_LEN : NET4_LEN))) {
			continue;
		}
		if (service->port != port ||
		    service->transport_proto != transport_proto) {
			continue;
		}
		// found
		*service_info = service;
		return i;
	}

	// extend
	struct service_info *services = memory_balloc(
		state->mctx,
		sizeof(struct service_info) * (registry->service_count + 1)
	);
	if (services == NULL) {
		return -1;
	}

	// todo: fixme
	assert((uintptr_t)services % alignof(struct service_info) == 0);
	struct service_info *services_dst = (struct service_info *)services;
	struct service_info *services_src = registry->services;
	for (size_t i = 0; i < registry->service_count; ++i) {
		struct service_info *service_dst = &services_dst[i];
		struct service_info *service_src = &services_src[i];
		memcpy(service_dst, service_src, sizeof(struct service_info));
		for (size_t w = 0; w < state->workers; ++w) {
			service_state_copy(
				&service_dst->state[w], &service_src->state[w]
			);
		}
	}

	memory_bfree(
		state->mctx,
		registry->services,
		sizeof(struct service_info) * registry->service_count
	);
	registry->services = (struct service_info *)services;
	++registry->service_count;

	size_t idx = registry->service_count - 1;
	struct service_info *service = &registry->services[idx];
	service->vip_proto = vip_proto;
	memcpy(&service->vip_address,
	       vip_address,
	       (vip_proto == IPPROTO_IPV6 ? NET6_LEN : NET4_LEN));
	service->ip_proto = ip_proto;
	service->port = port;
	service->transport_proto = transport_proto;
	memcpy(&service->ip_address,
	       ip_address,
	       (ip_proto == IPPROTO_IPV6 ? NET6_LEN : NET4_LEN));
	for (size_t worker = 0; worker < state->workers; ++worker) {
		struct service_state *service_state = &service->state[worker];
		int res = service_state_init(
			service_state, state->mctx, state->max_timeout
		);
		if (res != 0) {
			return -1;
		}
	}

	*service_info = service;
	return idx;
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
	return find_or_insert_into_registry(
		state,
		&state->vs_registry,
		ip_address,
		ip_proto,
		port,
		transport_proto,
		ip_address,
		ip_proto,
		service_info
	);
}

/// Get virtual service by index in the registry
struct service_info *
balancer_state_get_vs(struct balancer_state *state, size_t idx) {
	return &state->vs_registry.services[idx];
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
	return find_or_insert_into_registry(
		state,
		&state->real_registry,
		vip_address,
		vip_proto,
		port,
		transport_proto,
		ip_address,
		ip_proto,
		service_info
	);
}

struct service_info *
balancer_state_get_real(struct balancer_state *state, size_t idx) {
	return &state->real_registry.services[idx];
}