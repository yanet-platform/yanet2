#pragma once

#include "common/network.h"

#include "lib/controlplane/config/cp_module.h"
#include "lib/filter/filter.h"

#define UNRDUP_PROTO_TCP_BIT (1u << 0)
#define UNRDUP_PROTO_UDP_BIT (1u << 1)

// The family selects which outer source applies to a peer.
struct unrdup_peer {
	struct net_addr addr;
	enum ip_family family;
};

// What a filter match names: the service that serves the matched VIP and port,
// and the transports it serves there.
struct unrdup_endpoint {
	uint64_t service_idx;
	uint32_t proto_mask;
};

struct unrdup_service {
	uint64_t peer_count;
	struct unrdup_peer *peers;
};

// An all-zero source leaves that family unconfigured and skips its peers.
//
// A filter answers with the index of the endpoint it matched, so a family that
// serves nothing keeps an endpoint count of zero and is never queried.
struct unrdup_module_config {
	struct cp_module cp_module;

	struct net source4;
	struct net source6;

	uint64_t service_count;
	struct unrdup_service *services;

	struct filter *filter4;
	struct filter *filter6;

	uint64_t endpoint4_count;
	struct unrdup_endpoint *endpoints4;

	uint64_t endpoint6_count;
	struct unrdup_endpoint *endpoints6;

	uint64_t redistributed_counter_id;
	uint64_t tunneled_received_counter_id;
	uint64_t clones_sent_counter_id;
	uint64_t clone_failed_counter_id;
	uint64_t encap_failed_counter_id;
	uint64_t peer_no_source_counter_id;
	uint64_t unserved_counter_id;
	uint64_t misaddressed_counter_id;
	uint64_t malformed_counter_id;
};
