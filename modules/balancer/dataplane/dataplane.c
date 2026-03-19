#include <netinet/in.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>
#include <threads.h>

#include "flow/setup.h"
#include "flow/stats.h"

#include "common/memory_address.h"

#include "lib/dataplane/config/zone.h"

#include "dataplane.h"
#include "decap.h"

#include "handler/handler.h"
#include "icmp/handle.h"
#include "l4/handle.h"

#include "worker.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_module {
	struct module module;
};

static inline int
packet_ctx_try_decap(struct packet_ctx *ctx) {
	return try_decap(ctx);
}

void
handle_batch(size_t packets_count) {
	assert(packets_count <= batch_size);

	if (unlikely(packets_count == 0)) {
		return;
	}

	// first, handle icmp packets
	for (size_t i = 0; i < packets_count; ++i) {
		struct packet_ctx *ctx = &packet_ctxs[i];
		uint16_t packet_type = ctx->packet->transport_header.type;
		if (!ctx->processed && (packet_type == IPPROTO_ICMP ||
					packet_type == IPPROTO_ICMPV6)) {
			handle_icmp_packet(ctx);
		}
	}

	// handle TCP and UDP packets
	handle_l4_packets(packet_ctxs, packets_count);
}

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

	// setup packet ctxs and try to decap packets
	// handle packets by batches
	size_t packets_count = 0;
	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct packet_ctx *ctx = &packet_ctxs[packets_count++];
		packet_ctx_setup(
			ctx, now, dp_worker, module_ectx, handler, packet_front
		);

		// Set incoming packet
		packet_ctx_set_packet(ctx, packet);

		// Update module common stats
		packet_ctx_update_common_stats_on_incoming_packet(ctx);

		// Try decap packet if its destination
		// is from the balancer decap list.
		//
		// If packet dst is from the destination list
		// and decap failed, drop packet.
		if (packet_ctx_try_decap(ctx) != 0) {
			packet_ctx_drop_packet(ctx);
			continue;
		}

		// batch is full
		if (packets_count == batch_size) {
			// handle batch of packets
			handle_batch(packets_count);
			packets_count = 0;
		}
	}

	// if there are some unhandled packets
	// in the last batch
	handle_batch(packets_count);
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
