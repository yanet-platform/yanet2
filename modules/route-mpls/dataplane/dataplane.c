#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/memory.h"

#include "filter/query.h"

#include "lib/logging/log.h"

#include "lib/dataplane/config/zone.h"

#include "lib/dataplane/module/module.h"
#include "lib/dataplane/packet/encap.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/pipeline.h"

#include "lib/controlplane/config/econtext.h"

struct route_module {
	struct module module;
};

FILTER_QUERY_DECLARE(FILTER_IP4_DST_TAG, net4_dst);

FILTER_QUERY_DECLARE(FILTER_IP6_DST_TAG, net6_dst);

static void
route_mpls_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;

	struct module_config *module_config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct module_config,
		cp_module
	);

	struct packet *ip4_packets[packet_list_count(&packet_front->input)];
	const struct value_range
		*ip4_result[packet_list_count(&packet_front->input)];
	uint64_t ip4_idx = 0;

	struct packet *ip6_packets[packet_list_count(&packet_front->input)];
	const struct value_range
		*ip6_result[packet_list_count(&packet_front->input)];
	uint64_t ip6_idx = 0;

	for (struct packet *packet = packet_list_first(&packet_front->input);
	     packet != NULL;
	     packet = packet->next) {

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			ip4_packets[ip4_idx++] = packet;
		}

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			ip6_packets[ip6_idx++] = packet;
		}
	}

	FILTER_QUERY(
		&module_config->filter_ip4,
		FILTER_IP4_DST_TAG,
		ip4_packets,
		ip4_result,
		ip4_idx
	);

	FILTER_QUERY(
		&module_config->filter_ip6,
		FILTER_IP6_DST_TAG,
		ip6_packets,
		ip6_result,
		ip6_idx
	);

	ip4_idx = 0;
	ip6_idx = 0;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct target *target = NULL;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			if (!ip4_result[ip4_idx]->count) {
				++ip4_idx;
				continue;
			}

			target =
				ADDR_OF(ADDR_OF(&module_config->targets) +
					ADDR_OF(&ip4_result[ip4_idx]->values)[0]
				);

			++ip4_idx;
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			if (!ip6_result[ip6_idx]->count) {
				++ip6_idx;
				continue;
			}

			target =
				ADDR_OF(ADDR_OF(&module_config->targets) +
					ADDR_OF(&ip6_result[ip6_idx]->values)[0]
				);

			++ip6_idx;
		}

		if (target == NULL) {
			packet_front_output(packet_front, packet);
			continue;
		}

		if (!target->nexthop_map_size) {
			packet_front_output(packet_front, packet);
			continue;
		}

		uint64_t nexthop_idx =
			target->nexthop_map
				[packet->hash % target->nexthop_map_size];
		struct nexthop *nexthop =
			ADDR_OF(&target->nexthops) + nexthop_idx;
		uint64_t *counters = counter_get_address(
			nexthop->counter_id,
			dp_worker->idx,
			ADDR_OF(&module_ectx->counter_storage)
		);
		counters[0] += 1;
		counters[1] += packet_data_len(packet);

		if (nexthop->type == ROUTE_TYPE_NONE) {
			packet_front_output(packet_front, packet);
			continue;
		}

		if (packet_mpls_encap(packet, nexthop->mpls_label, 0, 1, 10)) {
			// FIXME update error counter
			packet_front_drop(packet_front, packet);
			continue;
		}

		// IANA-specified values
		uint16_t src_port = htobe16(0xc000 | (0x3fff & packet->hash));
		uint16_t dst_port = htobe16(6635);

		if (nexthop->type == ROUTE_TYPE_V4) {
			if (packet_encap_ip4_udp(
				    packet,
				    nexthop->ip4_tunnel.src,
				    nexthop->ip4_tunnel.dst,
				    (uint8_t *)&src_port,
				    (uint8_t *)&dst_port
			    )) {
				// FIXME update error counter
				packet_front_drop(packet_front, packet);
				continue;
			}
		} else {
			if (packet_encap_ip6_udp(
				    packet,
				    nexthop->ip6_tunnel.src,
				    nexthop->ip6_tunnel.dst,
				    (uint8_t *)&src_port,
				    (uint8_t *)&dst_port
			    )) {
				// FIXME update error counter
				packet_front_drop(packet_front, packet);
				continue;
			}
		}

		packet_front_output(packet_front, packet);
	}
}

struct module *
new_module_route_mpls() {
	struct route_module *module =
		(struct route_module *)malloc(sizeof(struct route_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		"route-mpls"
	);
	module->module.handler = route_mpls_handle_packets;

	return &module->module;
}
