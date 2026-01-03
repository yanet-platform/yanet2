#include <netinet/in.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "flow/setup.h"
#include "flow/stats.h"

#include "common/memory_address.h"

#include "lib/controlplane/config/econtext.h"
#include "lib/dataplane/config/zone.h"

#include "dataplane.h"
#include "decap.h"

#include "handler/handler.h"
#include "icmp/handle.h"
#include "l4/handle.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_module {
	struct module module;
};

static inline void
packet_ctx_handle(struct packet_ctx *ctx) {
	struct packet *packet = ctx->packet;
	// separately handle icmp and tcp/udp packets.
	uint16_t packet_type = packet->transport_header.type;
	if (packet_type == IPPROTO_ICMP || packet_type == IPPROTO_ICMPV6) {
		handle_icmp_packet(ctx);
	} else {
		handle_l4_packet(ctx);
	}
}

static inline int
packet_ctx_try_decap(struct packet_ctx *ctx) {
	return try_decap(ctx);
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	// Get balancer module config as container of provided cp_module.
	struct packet_handler *handler = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct packet_handler,
		cp_module
	);

	// Get current time in seconds.
	uint32_t now = dp_worker->current_time / (1000 * 1000 * 1000);

	// Setup packet context.
	struct packet_ctx ctx;
	packet_ctx_setup(
		&ctx, now, dp_worker, module_ectx, handler, packet_front
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		// Set incoming packet
		packet_ctx_set_packet(&ctx, packet);

		// Update module common stats
		packet_ctx_update_common_stats_on_incoming_packet(&ctx);

		// Try decap packet if its destination
		// is from the balancer decap list.
		//
		// If packet dst is from the destination list
		// and decap failed, drop packet.
		if (packet_ctx_try_decap(&ctx) != 0) {
			packet_ctx_drop_packet(&ctx);
			return;
		}

		// Handle incoming packet.
		// This function drop packet
		// or passes it to the next module
		// under the hood. Or crafts new ICMP packets
		// and also passes it to the next module.
		packet_ctx_handle(&ctx);
	}
}

struct module *
new_module_balancer() {
	struct balancer_module *module =
		(struct balancer_module *)malloc(sizeof(struct balancer_module)
		);

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		"balancer"
	);
	module->module.handler = balancer_handle_packets;

	return &module->module;
}
