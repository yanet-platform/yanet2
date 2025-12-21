#pragma once

#include "common/memory.h"
#include "registry.h"
#include "session_table.h"

////////////////////////////////////////////////////////////////////////////////

/// Persistent state of the balancer, which includes registry of virtual
/// services and reals, session table and sessions timeouts info
struct balancer_state {
	// memory context of the agent which is responsible
	// for the balancer state
	struct memory_context *mctx;

	// number of workers
	size_t workers;

	// session table
	struct session_table session_table;

	// registry of virtual services
	struct service_registry vs_registry;

	// registry or reals
	struct service_registry real_registry;

	// shift in memory which allows to deallocate state properly
	size_t memory_shift;

	// stats of the balancer state
	struct balancer_stats stats[MAX_WORKERS_NUM];
};

////////////////////////////////////////////////////////////////////////////////

int
balancer_state_init(
	struct balancer_state *state,
	struct memory_context *mctx,
	size_t workers,
	size_t table_size
);

void
balancer_state_free(struct balancer_state *state);

////////////////////////////////////////////////////////////////////////////////

/// Find or insert virtual service into registry.
/// Returns index of the found (or inserted) service and sets `service_info`.
ssize_t
balancer_state_find_or_insert_vs(
	struct balancer_state *state,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto,
	struct service_info **service_info
);

/// Get virtual service by index in the registry
struct service_info *
balancer_state_get_vs(struct balancer_state *state, size_t idx);

////////////////////////////////////////////////////////////////////////////////

/// Find or insert real into registry.
/// Returns index of the found (or inserted) real and sets `service_info`.
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
);

/// Get real service by index in the registry
struct service_info *
balancer_state_get_real(struct balancer_state *state, size_t idx);