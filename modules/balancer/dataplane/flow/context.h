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

	struct packet_handler *handler;

	// state of the balancer
	struct {
		struct balancer_state *ptr;
		struct balancer_stats *stats;
	} state;

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
	} counter;

	// selected virtual service
	struct {
		struct vs_stats *ph_stats;
		struct vs_stats *state_stats;
		struct vs_info *info;
		struct vs *view;
	} vs;

	// selected real
	struct {
		struct real_stats *ph_stats;
		struct real_stats *state_stats;
		struct real_info *info;
		struct real *view;
	} real;

	// if packet was decapsulated
	bool decap;
};