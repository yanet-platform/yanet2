#include "dataplane.h"

#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <rte_mbuf.h>

#include "dataplane/module/module.h"

#define BATCH_SIZE 32

int
acl_handle_v4(
	struct filter_compiler *compiler,
	struct packet *packet,
	uint32_t **actions,
	uint32_t *count
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4Header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	uint32_t src_net = lpm4_lookup(
		&compiler->src_net4, (uint8_t *)&ipv4Header->src_addr
	);
	uint32_t dst_net = lpm4_lookup(
		&compiler->dst_net4, (uint8_t *)&ipv4Header->dst_addr
	);

	uint32_t src_port = 0;
	uint32_t dst_port = 0;

	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcpHeader = NULL;
		tcpHeader = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);

		src_port = value_table_get(
			&compiler->src_port4, 0, tcpHeader->src_port
		);
		dst_port = value_table_get(
			&compiler->dst_port4, 0, tcpHeader->dst_port
		);

	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udpHeader = NULL;
		udpHeader = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);

		src_port = value_table_get(
			&compiler->src_port4, 0, udpHeader->src_port
		);
		dst_port = value_table_get(
			&compiler->dst_port4, 0, udpHeader->dst_port
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
	*actions = compiler->v4_lookups.result_registry.values + range->from;
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

	struct rte_ipv6_hdr *ipv6Header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	uint32_t src_net_hi =
		lpm8_lookup(&compiler->src_net6_hi, ipv6Header->src_addr);
	uint32_t src_net_lo =
		lpm8_lookup(&compiler->src_net6_lo, ipv6Header->src_addr + 8);
	uint32_t dst_net_hi =
		lpm8_lookup(&compiler->dst_net6_lo, ipv6Header->dst_addr);
	uint32_t dst_net_lo =
		lpm8_lookup(&compiler->dst_net6_lo, ipv6Header->dst_addr + 8);

	uint32_t src_port = 0;
	uint32_t dst_port = 0;

	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcpHeader = NULL;
		tcpHeader = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);

		src_port = value_table_get(
			&compiler->src_port6, 0, tcpHeader->src_port
		);
		dst_port = value_table_get(
			&compiler->dst_port6, 0, tcpHeader->dst_port
		);

	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udpHeader = NULL;
		udpHeader = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);

		src_port = value_table_get(
			&compiler->src_port6, 0, udpHeader->src_port
		);
		dst_port = value_table_get(
			&compiler->dst_port6, 0, udpHeader->dst_port
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
	*actions = compiler->v6_lookups.result_registry.values + range->from;
	*count = range->count;

	return 0;
}

static void
acl_handle_packets(
	struct module *module,
	struct module_config *config,
	struct packet_front *packet_front
) {
	(void)module;
	struct acl_module_config *acl_config =
		container_of(config, struct acl_module_config, config);

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

static int
acl_handle_configure(
	struct module *module,
	const void *config_data,
	size_t config_data_size,
	struct module_config **new_config
) {
	(void)module;
	(void)config_data;
	(void)config_data_size;
	(void)new_config;

	struct acl_module_config *config = (struct acl_module_config *)malloc(
		sizeof(struct acl_module_config)
	);

	struct filter_action actions[10];
	actions[0].net6.src_count = 1;
	actions[0].net6.srcs = (struct net6 *)malloc(sizeof(struct net6) * 1);
	actions[0].net6.srcs[0] = (struct net6){0, 0, 0x00000000000000C0, 0};
	actions[0].net6.dst_count = 1;
	actions[0].net6.dsts = (struct net6 *)malloc(sizeof(struct net6) * 1);
	actions[0].net6.dsts[0] =
		(struct net6){0x0000000000000080, 0, 0x0000000000000080, 0};

	actions[0].net4.src_count = 1;
	actions[0].net4.srcs = (struct net4 *)malloc(sizeof(struct net4) * 1);
	actions[0].net4.srcs[0] = (struct net4){0x00000080, 0x00000080};
	actions[0].net4.dst_count = 1;
	actions[0].net4.dsts = (struct net4 *)malloc(sizeof(struct net4) * 1);
	actions[0].net4.dsts[0] = (struct net4){0x00000000, 0x00000080};

	actions[0].transport.src_count = 1;
	actions[0].transport.srcs = (struct filter_port_range *)malloc(
		sizeof(struct filter_port_range) * 1
	);
	actions[0].transport.srcs[0] = (struct filter_port_range){0, 65535};

	actions[0].transport.dst_count = 1;
	actions[0].transport.dsts = (struct filter_port_range *)malloc(
		sizeof(struct filter_port_range) * 1
	);
	actions[0].transport.dsts[0] =
		(struct filter_port_range){htobe16(0), htobe16(65535)};
	//		(struct filter_port_range){htobe16(80), htobe16(80)};

	actions[0].action = 1;

	actions[1].net6.src_count = 1;
	actions[1].net6.srcs = (struct net6 *)malloc(sizeof(struct net6) * 1);
	actions[1].net6.srcs[0] = (struct net6){0, 0, 0, 0};
	actions[1].net6.dst_count = 1;
	actions[1].net6.dsts = (struct net6 *)malloc(sizeof(struct net6) * 1);
	actions[1].net6.dsts[0] = (struct net6){0, 0, 0, 0};

	actions[1].transport.src_count = 1;
	actions[1].transport.srcs = (struct filter_port_range *)malloc(
		sizeof(struct filter_port_range) * 1
	);
	actions[1].transport.srcs[0] = (struct filter_port_range){0, 65535};

	actions[1].transport.dst_count = 1;
	actions[1].transport.dsts = (struct filter_port_range *)malloc(
		sizeof(struct filter_port_range) * 1
	);
	actions[1].transport.dsts[0] = (struct filter_port_range){0, 65535};

	actions[1].net4.src_count = 1;
	actions[1].net4.srcs = (struct net4 *)malloc(sizeof(struct net4) * 1);
	actions[1].net4.srcs[0] = (struct net4){0x00000000, 0x00000000};
	actions[1].net4.dst_count = 1;
	actions[1].net4.dsts = (struct net4 *)malloc(sizeof(struct net4) * 1);
	actions[1].net4.dsts[0] = (struct net4){0x00000000, 0x00000000};

	actions[1].action = 2;

	filter_compiler_init(&config->filter, actions, 2);

	*new_config = &config->config;

	return 0;
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
	module->module.config_handler = acl_handle_configure;

	return &module->module;
}
