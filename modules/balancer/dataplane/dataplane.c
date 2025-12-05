#include <netinet/in.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "common/memory_address.h"
#include "controlplane/config/econtext.h"
#include "ctx.h"
#include "dataplane.h"
#include "dataplane/config/zone.h"
#include "decap.h"
#include "modules/balancer/dataplane/module.h"

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

static inline void
packet_ctx_try_decap(struct packet_ctx *ctx) {
	try_decap(ctx);
}

void
balancer_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct balancer_module_config *config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct balancer_module_config,
		cp_module
	);

	// TODO: FIXME
	uint32_t now = time(NULL);

	struct packet_ctx ctx;
	packet_ctx_setup(
		&ctx, now, dp_worker, module_ectx, config, packet_front
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		// set incoming packet
		packet_ctx_incoming_packet(&ctx, packet);

		// try decap packet if its destination
		// is from the balancer decap list
		packet_ctx_try_decap(&ctx);

		// handle incoming packet
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
