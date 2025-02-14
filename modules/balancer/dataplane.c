#include "dataplane.h"

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "dataplane/packet/encap.h"

int
balancer_handle_v4(
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
	*actions = compiler->v4_lookups.result_registry.values + range->from;
	*count = range->count;

	return 0;
}

int
balancer_handle_v6(
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
	*actions = compiler->v6_lookups.result_registry.values + range->from;
	*count = range->count;

	return 0;
}

static inline struct balancer_vs *
balancer_vs_lookup(struct balancer_module_config *config, uint32_t action) {
	return config->services + action - 1;
}

static inline struct balancer_rs *
balancer_rs_lookup(
	struct balancer_module_config *config,
	struct balancer_vs *vs,
	struct packet *packet
) {
	(void)packet;
	return config->reals + vs->real_start;
}

static int
balancer_route(
	struct balancer_module_config *config,
	struct balancer_vs *vs,
	struct balancer_rs *rs,
	struct packet *packet
) {
	if (rs->type == RS_TYPE_V4) {
		if (vs->options & VS_OPT_ENCAP) {
			return packet_ip4_encap(
				packet, rs->dst_addr, config->source_v4
			);
		}
	}

	if (rs->type == RS_TYPE_V6) {
		if (vs->options & VS_OPT_ENCAP) {
			return packet_ip6_encap(
				packet, rs->dst_addr, config->source_v6
			);
		}
	}

	return -1;
}

static void
balancer_handle_packets(
	struct module *module,
	struct module_config *config,
	struct packet_front *packet_front
) {
	(void)module;
	struct balancer_module_config *balancer_config =
		container_of(config, struct balancer_module_config, config);

	struct filter_compiler *compiler = &balancer_config->filter;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		uint32_t *actions = NULL;
		uint32_t count = 0;
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			balancer_handle_v4(compiler, packet, &actions, &count);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			balancer_handle_v6(compiler, packet, &actions, &count);
		} else {
			packet_front_drop(packet_front, packet);
			continue;
		}

		for (uint32_t idx = 0; idx < count; ++idx) {
			if (actions[idx] == 0) {
				packet_front_output(packet_front, packet);
				continue;
			}

			struct balancer_vs *vs =
				balancer_vs_lookup(balancer_config, actions[0]);

			if (vs == NULL) {
				// TODO: invalid configuration or internal error
				packet_front_drop(packet_front, packet);
				return;
			}

			struct balancer_rs *rs =
				balancer_rs_lookup(balancer_config, vs, packet);
			if (rs == NULL) {
				// real lookup failed
				packet_front_drop(packet_front, packet);
				return;
			}

			if (balancer_route(balancer_config, vs, rs, packet) !=
			    0) {
				packet_front_drop(packet_front, packet);
				continue;
			}

			packet_front_output(packet_front, packet);
		}
	}
}

static int
balancer_handle_configure(
	struct module *module,
	const void *config_data,
	size_t config_data_size,
	struct module_config **new_config
) {
	(void)module;
	(void)config_data;
	(void)config_data_size;
	(void)new_config;

	struct balancer_module_config *config =
		(struct balancer_module_config *)malloc(
			sizeof(struct balancer_module_config)
		);

	struct filter_action actions[10];
	actions[0].net6.src_count = 1;
	actions[0].net6.srcs = (struct net6 *)malloc(sizeof(struct net6) * 1);
	actions[0].net6.srcs[0] = (struct net6){0x00, 0, 0x000000000000, 0};
	actions[0].net6.dst_count = 1;
	actions[0].net6.dsts = (struct net6 *)malloc(sizeof(struct net6) * 1);
	actions[0].net6.dsts[0] = (struct net6
	){0x00, 0x0100000000000000, 0x0, 0xffffffffffffffff};

	actions[0].net4.src_count = 0;
	actions[0].net4.dst_count = 0;

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

	actions[0].action = 1;

	actions[1].net6.src_count = 0;
	actions[1].net6.dst_count = 0;

	actions[1].net4.src_count = 1;
	actions[1].net4.srcs = (struct net4 *)malloc(sizeof(struct net4) * 1);
	actions[1].net4.srcs[0] = (struct net4){0x00000000, 0x00000000};
	actions[1].net4.dst_count = 1;
	actions[1].net4.dsts = (struct net4 *)malloc(sizeof(struct net4) * 1);
	actions[1].net4.dsts[0] = (struct net4){0x0100000a, 0xffffffff};

	actions[1].transport.src_count = 1;
	actions[1].transport.srcs = (struct filter_port_range *)malloc(
		sizeof(struct filter_port_range) * 1
	);
	actions[1].transport.srcs[0] = (struct filter_port_range){0, 65535};

	actions[1].transport.dst_count = 1;
	actions[1].transport.dsts = (struct filter_port_range *)malloc(
		sizeof(struct filter_port_range) * 1
	);
	actions[1].transport.dsts[0] =
		(struct filter_port_range){htobe16(0), htobe16(65535)};

	actions[1].action = 2;

	actions[2].net6.src_count = 1;
	actions[2].net6.srcs = (struct net6 *)malloc(sizeof(struct net6) * 1);
	actions[2].net6.srcs[0] = (struct net6){0, 0, 0, 0};
	actions[2].net6.dst_count = 1;
	actions[2].net6.dsts = (struct net6 *)malloc(sizeof(struct net6) * 1);
	actions[2].net6.dsts[0] = (struct net6){0, 0, 0, 0};

	actions[2].transport.src_count = 1;
	actions[2].transport.srcs = (struct filter_port_range *)malloc(
		sizeof(struct filter_port_range) * 1
	);
	actions[2].transport.srcs[0] = (struct filter_port_range){0, 65535};

	actions[2].transport.dst_count = 1;
	actions[2].transport.dsts = (struct filter_port_range *)malloc(
		sizeof(struct filter_port_range) * 1
	);
	actions[2].transport.dsts[0] = (struct filter_port_range){0, 65535};

	actions[2].net4.src_count = 1;
	actions[2].net4.srcs = (struct net4 *)malloc(sizeof(struct net4) * 1);
	actions[2].net4.srcs[0] = (struct net4){0x00000000, 0x00000000};
	actions[2].net4.dst_count = 1;
	actions[2].net4.dsts = (struct net4 *)malloc(sizeof(struct net4) * 1);
	actions[2].net4.dsts[0] = (struct net4){0x00000000, 0x00000000};

	actions[2].action = 0;

	filter_compiler_init(&config->filter, actions, 3);

	config->services =
		(struct balancer_vs *)malloc(sizeof(struct balancer_vs) * 2);
	config->services[0] = (struct balancer_vs){VS_OPT_ENCAP, 1, 1};
	config->services[1] = (struct balancer_vs){VS_OPT_ENCAP, 0, 1};

	config->reals =
		(struct balancer_rs *)malloc(sizeof(struct balancer_rs) * 2);

	config->reals[0] =
		(struct balancer_rs){RS_TYPE_V4, (uint8_t *)malloc(4)};
	memcpy(config->reals[0].dst_addr, (uint8_t[4]){222, 111, 33, 11}, 4);

	config->reals[1] =
		(struct balancer_rs){RS_TYPE_V6, (uint8_t *)malloc(16)};
	memcpy(config->reals[1].dst_addr,
	       (uint8_t[16]){0xaa,
			     0xbb,
			     0xcc,
			     0xdd,
			     0,
			     0,
			     0,
			     0,
			     0,
			     0,
			     0,
			     0,
			     0x01,
			     0x02,
			     0x03,
			     0x04},
	       16);

	memset(config->source_v4, 0xaa, 4);
	memset(config->source_v6, 0xbb, 16);

	*new_config = &config->config;

	return 0;
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
	module->module.config_handler = balancer_handle_configure;

	return &module->module;
}
