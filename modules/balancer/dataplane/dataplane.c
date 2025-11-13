#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "common/memory_address.h"
#include "controlplane/config/econtext.h"
#include "counters/counters.h"
#include "dataplane.h"
#include "dataplane/config/zone.h"
#include "meta.h"
#include "modules/balancer/dataplane/module.h"
#include "real.h"
#include "select.h"
#include "tunnel.h"
#include "vs.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_module {
	struct module module;
};

void
handle_packets(
	struct balancer_module_config *config,
	struct packet_front *packet_front,
	struct counter_storage *counter_storage,
	uint32_t worker_idx,
	uint32_t now
) {
	struct module_config_packets_counter *packets_counter =
		balancer_module_config_packets_counter(
			config, worker_idx, counter_storage
		);
	struct module_config_bytes_counter *bytes_counter =
		balancer_module_config_bytes_counter(
			config, worker_idx, counter_storage
		);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		// update module config counters
		packets_counter->in += 1;
		bytes_counter->in += packet->mbuf->pkt_len;

		// 1. Lookup single virtual service for which packet is
		// dirrected to

		struct virtual_service *vs = vs_lookup(config, packet);

		if (vs == NULL) { // not found virtual service
			packets_counter->select_vs_failed += 1;
			packet_front_drop(packet_front, packet);
			continue;
		}

		// 2. Fill packet metadata

		struct packet_metadata meta;
		int res = fill_packet_metadata(packet, &meta);

		if (res != 0) { // unexpected packet type
			packets_counter->invalid_packet += 1;
			packet_front_drop(packet_front, packet);
			continue;
		}

		// 3. Select real packet for which packet will be forwarded

		struct real *rs =
			select_real(config, now, worker_idx, vs, &meta);
		if (rs == NULL) { // failed to select real
			packets_counter->select_real_failed += 1;
			packet_front_drop(packet_front, packet);
			continue;
		}

		// 4. Tunnel packet to forward in to the selected real

		res = tunnel_packet(vs->flags, rs, packet);
		if (res != 0) { // failed to tunnel packet
			packets_counter->tunnel_failed += 1;
			packet_front_drop(packet_front, packet);
			continue;
		}

		// 5. Pass packet to the next module

		packet_front_output(packet_front, packet);

		// update module config counters
		packets_counter->out += 1;
		bytes_counter->out += packet->mbuf->pkt_len;
	}
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

	uint32_t worker_idx = dp_worker->idx;

	handle_packets(
		config,
		packet_front,
		ADDR_OF(&module_ectx->counter_storage),
		worker_idx,
		now
	);
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
