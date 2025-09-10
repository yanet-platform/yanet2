#include "dataplane.h"
#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <rte_mbuf.h>

#include "dataplane/module/module.h"

struct acl_module {
	struct module module;
};

int
acl_handle_v4(
	struct filter_compiler *compiler,
	struct packet *packet,
	uint32_t **actions,
	uint32_t *count
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	uint32_t src_net = lpm4_lookup(
		&compiler->src_net4, (uint8_t *)&ipv4_hdr->src_addr
	);
	uint32_t dst_net = lpm4_lookup(
		&compiler->dst_net4, (uint8_t *)&ipv4_hdr->dst_addr
	);

	uint32_t src_port = 0;
	uint32_t dst_port = 0;

	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_hdr = NULL;
		tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);

		src_port = value_table_get(
			&compiler->src_port4, 0, tcp_hdr->src_port
		);
		dst_port = value_table_get(
			&compiler->dst_port4, 0, tcp_hdr->dst_port
		);

	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_hdr = NULL;
		udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);

		src_port = value_table_get(
			&compiler->src_port4, 0, udp_hdr->src_port
		);
		dst_port = value_table_get(
			&compiler->dst_port4, 0, udp_hdr->dst_port
		);

	} else {
		// TODO
	}

	uint32_t net = value_table_get(
		&compiler->v4_lookups.network, src_net, dst_net
	);
	uint32_t transport = value_table_get(
		&compiler->v4_lookups.transport_port, src_port, dst_port
	);
	uint32_t result =
		value_table_get(&compiler->v4_lookups.result, net, transport);

	struct value_range *range =
		compiler->v4_lookups.result_registry.ranges + result;
	*actions = ADDR_OF(&range->values);
	*count = range->count;

	return 0;
}

int
acl_handle_v6(
	struct filter_compiler *compiler,
	struct packet *packet,
	uint32_t **actions,
	uint32_t *count
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	uint32_t src_net_hi =
		lpm8_lookup(&compiler->src_net6_hi, ipv6_hdr->src_addr);
	uint32_t src_net_lo =
		lpm8_lookup(&compiler->src_net6_lo, ipv6_hdr->src_addr + 8);
	uint32_t dst_net_hi =
		lpm8_lookup(&compiler->dst_net6_lo, ipv6_hdr->dst_addr);
	uint32_t dst_net_lo =
		lpm8_lookup(&compiler->dst_net6_lo, ipv6_hdr->dst_addr + 8);

	uint32_t src_port = 0;
	uint32_t dst_port = 0;

	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_hdr = NULL;
		tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);

		src_port = value_table_get(
			&compiler->src_port6, 0, tcp_hdr->src_port
		);
		dst_port = value_table_get(
			&compiler->dst_port6, 0, tcp_hdr->dst_port
		);

	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_hdr = NULL;
		udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);

		src_port = value_table_get(
			&compiler->src_port6, 0, udp_hdr->src_port
		);
		dst_port = value_table_get(
			&compiler->dst_port6, 0, udp_hdr->dst_port
		);

	} else {
		// TODO
	}

	uint32_t net_hi = value_table_get(
		&compiler->v6_lookups.network_hi, src_net_hi, dst_net_hi
	);
	uint32_t net_lo = value_table_get(
		&compiler->v6_lookups.network_lo, src_net_lo, dst_net_lo
	);
	uint32_t net =
		value_table_get(&compiler->v6_lookups.network, net_hi, net_lo);
	uint32_t transport = value_table_get(
		&compiler->v6_lookups.transport_port, src_port, dst_port
	);
	uint32_t result =
		value_table_get(&compiler->v6_lookups.result, net, transport);

	struct value_range *range =
		compiler->v6_lookups.result_registry.ranges + result;
	*actions = ADDR_OF(&range->values);
	*count = range->count;

	return 0;
}

static void
acl_handle_packets(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
) {
	(void)dp_config;
	(void)worker_idx;
	(void)counter_storage;
	struct acl_module_config *acl_config =
		container_of(cp_module, struct acl_module_config, cp_module);

	struct filter_compiler *compiler = &acl_config->filter;

	/*
	 * There are two major options:
	 *  - process packets one by one
	 *  - process stages ony by one
	 * For the second option we have to split v4 and v6 processing.
	 */

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		uint32_t *actions = NULL;
		uint32_t count = 0;
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			acl_handle_v4(compiler, packet, &actions, &count);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			acl_handle_v6(compiler, packet, &actions, &count);
		} else {
			packet_front_drop(packet_front, packet);
			continue;
		}

		for (uint32_t idx = 0; idx < count; ++idx) {
			if (!(actions[idx] & ACTION_NON_TERMINATE)) {
				if (actions[idx] == 1) {
					packet_front_output(
						packet_front, packet
					);
				} else if (actions[idx] == 2) {
					packet_front_drop(packet_front, packet);
				}
			}
		}
	}
}

struct module *
new_module_acl() {
	struct acl_module *module =
		(struct acl_module *)malloc(sizeof(struct acl_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(module->module.name, sizeof(module->module.name), "%s", "acl");
	module->module.handler = acl_handle_packets;

	return &module->module;
}
