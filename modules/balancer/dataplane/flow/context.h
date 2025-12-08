#pragma once

#include "lib/controlplane/config/econtext.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/packet/packet.h"

#include "module.h"
#include "modules/balancer/api/stats.h"
#include "modules/balancer/state/registry.h"
#include "real.h"
#include "vs.h"

#include "../state/state.h"

////////////////////////////////////////////////////////////////////////////////

// Context of the packet flow.
struct packet_ctx {
	// packet context belongs to
	struct packet *packet;

	// packet front which is used to
	// send or drop packets
	struct packet_front *packet_front;

	// worker which process current packet
	struct dp_worker *worker;

	// module config
	struct balancer_module_config *config;

	// state of the balancer
	struct {
		struct balancer_state *ptr;
		struct balancer_stats *stats;
	} state;

	// current time in seconds
	uint32_t now;

	// module counters
	struct {
		struct balancer_common_module_stats *common;
		struct balancer_icmp_module_stats *icmp_v4;
		struct balancer_icmp_module_stats *icmp_v6;
		struct balancer_l4_module_stats *l4;

		// counters storage
		struct counter_storage *storage;
	} counter;

	// selected virtual service
	struct {
		struct balancer_vs_stats *counter;
		struct service_state *state;
		struct virtual_service *ptr;
	} vs;

	// selected real
	struct {
		struct balancer_real_stats *counter;
		struct service_state *state;
		struct real *ptr;
	} real;

	// if packet was decapsulated
	bool decap;
};