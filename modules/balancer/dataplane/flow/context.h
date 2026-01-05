#pragma once

#include "handler/handler.h"

#include "lib/controlplane/config/econtext.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/packet/packet.h"

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
	uint32_t worker_idx;

	// packet handler
	struct packet_handler *handler;

	// state of the balancer
	struct balancer_state *balancer_state;

	// current time in seconds
	uint32_t now;

	// module counters
	struct {
		struct balancer_common_stats *common;
		struct balancer_icmp_stats *icmp_v4;
		struct balancer_icmp_stats *icmp_v6;
		struct balancer_l4_stats *l4;

		// counters storage
		struct counter_storage *storage;
	} stats;

	// selected virtual service
	struct {
		struct vs_stats *stats;
		struct vs *ptr;
	} vs;

	// selected real
	struct {
		struct real_stats *stats;
		struct real *ptr;
	} real;

	// if packet was decapsulated
	bool decap_flag;
};