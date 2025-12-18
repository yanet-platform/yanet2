#pragma once

#include "common/network.h"
#include "module.h"
#include "ring.h"
#include "worker.h"

#include "../state/registry.h"

////////////////////////////////////////////////////////////////////////////////

// Virtual service flags.
typedef uint8_t vs_flags_t;

// If virtual service is present in the current module config.
#define VS_PRESENT_IN_CONFIG_FLAG (1 << 7)

////////////////////////////////////////////////////////////////////////////////

// Worker local info about
// virtual service state.
struct vs_worker_local {
	// if virtual service schedule is PRR,
	// use counter to select next real for packet
	// scheduling.
	uint64_t round_robin_counter;
}; // todo: add alignment to avoid false sharing

////////////////////////////////////////////////////////////////////////////////

// Virtual service which is served by a list of reals.
struct virtual_service {
	// Index of the virtual service in the balancer
	// module state registry.
	size_t registry_idx;

	// virtual service flags
	vs_flags_t flags;

	// virtual service address (ipv4 or ipv6, depends on vs_flags)
	uint8_t address[16];

	// transport port
	uint16_t port;

	// transport proto (tcp or udp)
	uint8_t proto;

	// number of reals
	size_t real_count;

	// ring of reals which serves this virtual service
	struct ring real_ring;

	// packet source address should be from
	// allowed list for this virtual service
	struct lpm src_filter;

	// worker local state for the virtual service
	struct vs_worker_local worker_local[MAX_WORKERS_NUM];

	// id of the counter for virtual service,
	// which is related to the placement of the config
	// in controlplane topology
	uint64_t counter_id;

	// state of the virtual service,
	// which if independent of config placement in
	// controlplane topology
	struct service_state *state;

	// balancers which are IPv4 peers for service
	size_t peers_v4_count;
	struct net4_addr *peers_v4;

	// balancers which are IPv6 peers for service
	size_t peers_v6_count;
	struct net6_addr *peers_v6;
};

////////////////////////////////////////////////////////////////////////////////

// Counter for the virtual service,
// which depends on the placement of the
// module config in the controlplane topology.
static inline struct balancer_vs_stats *
vs_counter(
	struct virtual_service *vs,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(vs->counter_id, worker, storage);
	return (struct balancer_vs_stats *)counter;
}

static inline void
vs_worker_local_init(struct virtual_service *vs) {
	// todo: add workers count
	uint64_t rng = 0x12312;
	for (size_t i = 0; i < MAX_WORKERS_NUM; ++i) {
		vs->worker_local[i].round_robin_counter = rng_next(&rng);
	}
}