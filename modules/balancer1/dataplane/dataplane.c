#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "dataplane.h"
#include "dataplane/config/zone.h"
#include "meta.h"
#include "modules/balancer1/dataplane/module.h"
#include "real.h"
#include "select.h"
#include "tunnel.h"
#include "vs.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_module {
	struct module module;
};

void
balancer_handle_packets(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
) {
	(void)dp_config;
	(void)counter_storage;

	struct balancer_module_config *config = container_of(
		cp_module, struct balancer_module_config, cp_module
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		// 1. Lookup service packet is dirrected to

		struct virtual_service *vs = vs_lookup(config, packet);
		if (vs == NULL) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		// 2. Fill packet metadata

		struct packet_metadata meta;
		int res = fill_packet_metadata(packet, &meta);
		if (res != 0) {
			// unexpected packet type
			packet_front_drop(packet_front, packet);
			continue;
		}

		// 3. Select real service packet should be forwarded

		struct real *rs = select_real(config, worker_idx, vs, &meta);
		if (rs == NULL) {
			// real lookup failed
			packet_front_drop(packet_front, packet);
			continue;
		}

		// 4. Add IP header to the packet to forward it to the selected
		// real

		res = tunnel_packet(vs->flags, rs, packet);
		if (res != 0) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		// 5. Pass packet to the next module

		packet_front_output(packet_front, packet);
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
